package web

import (
	"net/http"
	"strings"

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
	// Assignee is the assignee's display name ("" when unassigned); AvatarURL is
	// their public Jira avatar image URL ("" when none). Initials is the computed
	// fallback shown when there is an assignee but no image. The template renders
	// the image, else the initials, else a neutral empty circle when unassigned.
	Assignee  string
	AvatarURL string
	Initials  string
	// EpicName is the parent epic's name shown as a pill under the title ("" when
	// the card has no parent epic, so no pill renders); EpicColorHex is the pill's
	// background hex resolved from the epic's Jira Issue color (purple by default).
	EpicName     string
	EpicColorHex string
	// Status is the ticket's current workflow status, rendered as a status pill on
	// the card. It is set only by the Sprint cell drill-down (#79); the Board leaves
	// it "" so no status pill renders there.
	Status string
	// Editable makes the estimate pill an interactive write-back control (#108,
	// docs/adr/0005). It is set ONLY by the Board view; the same card on the Sprint
	// drill-down leaves it false, so editability does not leak in through the shared
	// board-card partial. When false the pill is a read-only display chip.
	Editable bool
	// RawSize is the ticket's stored T-shirt label ("S"/"M"/"L" or "" for
	// no-estimate), carried alongside the display string (Size) so the editable
	// pill knows the current selection and the value to revert to. It is only
	// consumed when Editable.
	RawSize string
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
				Key:          c.Key,
				Summary:      c.Summary,
				Size:         sizeDisplay(c.Size),
				RawSize:      c.Size,
				Editable:     true, // the Board is the sole surface the estimate is editable on
				Type:         c.Type,
				Href:         s.jiraIssueURL(c.Key),
				Assignee:     c.Assignee,
				AvatarURL:    c.AssigneeAvatarURL,
				Initials:     avatarInitials(c.Assignee),
				EpicName:     c.EpicName,
				EpicColorHex: epicPillColor(c.EpicColor),
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

// avatarInitials computes the initials shown when an assignee has no avatar
// image: the first letter of the first name plus the first letter of the last
// name, a single-word name's first two letters, and "" for an unassigned issue
// (whose card renders a neutral empty circle). Output is upper-cased; leading,
// trailing and repeated whitespace is ignored.
func avatarInitials(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return ""
	case 1:
		r := []rune(parts[0])
		if len(r) == 1 {
			return strings.ToUpper(string(r))
		}
		return strings.ToUpper(string(r[:2]))
	default:
		first := []rune(parts[0])
		last := []rune(parts[len(parts)-1])
		return strings.ToUpper(string(first[0]) + string(last[0]))
	}
}

// epicColorHex maps a Jira "Issue color" (customfield_10017) value to the pill
// background hex the Board card renders inline. DCAI's epics use the base colours
// and their dark_ variants; anything unset or unrecognised falls back to purple,
// Jira's own default epic colour. Keys are lower-cased before lookup. The hexes
// are medium/dark tones chosen so white pill text stays legible.
var epicColorHex = map[string]string{
	"purple":      "#6554C0",
	"dark_purple": "#403294",
	"blue":        "#2684FF",
	"dark_blue":   "#0747A6",
	"green":       "#36B37E",
	"dark_green":  "#006644",
	"teal":        "#00A3BF",
	"dark_teal":   "#008DA6",
	"yellow":      "#B7791F",
	"dark_yellow": "#946C00",
	"orange":      "#D9730D",
	"dark_orange": "#B65C02",
	"grey":        "#6B778C",
	"dark_grey":   "#42526E",
}

// epicPillColor resolves an epic's Jira Issue color to its pill background hex,
// defaulting to purple when the colour is unset or unrecognised.
func epicPillColor(jiraColor string) string {
	if hex, ok := epicColorHex[strings.ToLower(jiraColor)]; ok {
		return hex
	}
	return epicColorHex["purple"]
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
