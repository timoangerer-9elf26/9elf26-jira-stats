package web

import (
	"net/http"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// boardCard is one card on the sprint board: the display fields plus a resolved
// Jira link. Href is empty when no Jira base URL is configured, so the template
// renders the card without a broken link.
type boardCard struct {
	Key     string
	Summary string
	Size    string // "S"/"M"/"L" or "no estimate"
	Type    string // Task, Bug or Story
	Href    string // "<base>/browse/<KEY>", or "" when unconfigured
}

// boardColumn is one workflow-status column and its cards.
type boardColumn struct {
	Status string
	Cards  []boardCard
}

// boardView is the /board page model: the active sprint's columns plus its name
// (empty when no active sprint is known, driving the friendly empty state).
type boardView struct {
	Columns    []boardColumn
	SprintName string
	HasSprint  bool
}

// handleBoard renders the sprint Kanban board for the active sprint.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	view, err := s.boardView()
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "board.html", view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// boardView projects the store board into the page model, resolving each card's
// Jira link and the active-sprint name shown in the heading.
func (s *Server) boardView() (boardView, error) {
	board, err := s.rollups.ActiveSprintBoard()
	if err != nil {
		return boardView{}, err
	}

	view := boardView{Columns: make([]boardColumn, 0, len(board.Columns))}
	for _, col := range board.Columns {
		cards := make([]boardCard, 0, len(col.Cards))
		for _, c := range col.Cards {
			cards = append(cards, boardCard{
				Key:     c.Key,
				Summary: c.Summary,
				Size:    sizeDisplay(c.Size),
				Type:    c.Type,
				Href:    s.jiraIssueURL(c.Key),
			})
		}
		view.Columns = append(view.Columns, boardColumn{Status: col.Status, Cards: cards})
	}

	switch sprint, ok, err := s.rollups.ActiveSprintWindow(); {
	case err != nil:
		return boardView{}, err
	case ok:
		view.SprintName = sprint.Name
		view.HasSprint = true
	}
	return view, nil
}

// sizeDisplay renders a stored T-shirt label for a card: the letter as-is, or
// "no estimate" for an unsized issue.
func sizeDisplay(size string) string {
	if size == "" {
		return "no estimate"
	}
	return size
}

// jiraIssueURL builds the Jira detail link for an issue key, or "" when no base
// URL is configured (so the card degrades to no link rather than a broken one).
func (s *Server) jiraIssueURL(key string) string {
	if s.jiraBaseURL == "" {
		return ""
	}
	return s.jiraBaseURL + "/browse/" + key
}

// compile-time check that the concrete store satisfies the read side.
var _ Rollups = (*store.Store)(nil)
