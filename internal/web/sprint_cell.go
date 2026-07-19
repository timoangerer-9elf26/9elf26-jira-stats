package web

import (
	"net/http"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// sprintCellView is the model for the Sprint cell drill-down page (#79): the
// tickets behind one cohort × outcome cell of the Sprint table, rendered as a
// flat list of Board-style cards. CohortLabel/OutcomeLabel name the cell in the
// heading (e.g. "Started with · Open"); Count is the ticket count (matches the
// cell's "N tickets"); SprintName/WindowLabel echo the Sprint view's context.
// Cards carry the same fields as the Board plus a status pill, pre-sorted by
// workflow order so same-status cards group in the flat list.
type sprintCellView struct {
	CohortLabel  string
	OutcomeLabel string
	Count        int
	SprintName   string
	WindowLabel  string
	Cards        []boardCard
}

// cohortSelector maps a row render-key to its store selector and display label.
func cohortSelector(rowKey string) (store.SprintCohortSel, string, bool) {
	switch rowKey {
	case "started":
		return store.CohortStartedWith, "Started with", true
	case "added":
		return store.CohortAdded, "Added", true
	case "total":
		return store.CohortTotal, "Total", true
	}
	return 0, "", false
}

// outcomeSelector maps a column render-key to its store selector and display
// label. The labels match the Sprint table's outcome column headers.
func outcomeSelector(colKey string) (store.SprintOutcomeSel, string, bool) {
	switch colKey {
	case "open":
		return store.OutcomeOpen, "Open", true
	case "finished":
		return store.OutcomeFinished, "Finished", true
	case "removed":
		return store.OutcomeRemoved, "Removed", true
	case "total":
		return store.OutcomeTotal, "Total", true
	}
	return 0, "", false
}

// handleSprintCell renders the drill-down page for one Sprint cell: the tickets
// behind the cohort × outcome named by the ?row= and ?col= query params, as a
// flat list of cards. Unknown keys 404. With no active sprint it redirects back
// to the Sprint view (there is no cell to drill into).
func (s *Server) handleSprintCell(w http.ResponseWriter, r *http.Request) {
	cohort, cohortLabel, okRow := cohortSelector(r.URL.Query().Get("row"))
	outcome, outcomeLabel, okCol := outcomeSelector(r.URL.Query().Get("col"))
	if !okRow || !okCol {
		http.NotFound(w, r)
		return
	}

	sprint, hasSprint, err := s.rollups.ActiveSprintWindow()
	if err != nil {
		s.renderError(w)
		return
	}
	if !hasSprint {
		http.Redirect(w, r, "/sprint", http.StatusFound)
		return
	}

	from, to := s.sprintWindow(sprint)
	issues, err := s.rollups.SprintCellIssues(sprint.ID, from, to, cohort, outcome)
	if err != nil {
		s.renderError(w)
		return
	}

	view := sprintCellView{
		CohortLabel:  cohortLabel,
		OutcomeLabel: outcomeLabel,
		Count:        len(issues),
		SprintName:   sprint.Name,
		WindowLabel:  rangeLabel(from, to),
		Cards:        make([]boardCard, 0, len(issues)),
	}
	for _, is := range issues {
		view.Cards = append(view.Cards, boardCard{
			Key:          is.Key,
			Summary:      is.Summary,
			Size:         sizeDisplay(is.Size),
			Type:         is.Type,
			Href:         s.jiraIssueURL(is.Key),
			Assignee:     is.Assignee,
			AvatarURL:    is.AssigneeAvatarURL,
			Initials:     avatarInitials(is.Assignee),
			EpicName:     is.EpicName,
			EpicColorHex: epicPillColor(is.EpicColor),
			Status:       is.Status,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "sprint-cell.html", view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}
