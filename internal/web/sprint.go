package web

import (
	"net/http"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// sprintView is the model for the Sprint page and its results fragment.
// HasSprint is false when no active sprint is recorded (drives the no-sprint
// empty state, same treatment as the Board view); Empty is true when neither
// Started-with nor Added has any ticket, so the results show a friendly note
// instead of a table of zeros.
//
// The view always centers on the current active sprint over the window
// [sprint start, now) — there is no window selector (see #53). WindowLabel is the
// human-readable resolved window; SprintName is the active sprint's name, both
// shown in the header.
//
// Rows carries the three category rows in display order (Started with, Added,
// Total). Each row is a tickets+points headline with the S/M/L split and its
// finished figure (finished-from-started, finished-from-added and the finished
// total respectively).
type sprintView struct {
	WindowLabel string // human-readable resolved window, e.g. "13 Jul – 15 Jul 2026"
	SprintName  string
	HasSprint   bool
	Rows        []sprintCategoryRow
	Empty       bool
}

// sprintCategoryRow is one row of the Sprint table: a category's size tally as a
// tickets+points headline with the S/M/L/no-estimate split, plus the finished
// figure attributed to that category. Key is the testid/render prefix
// ("started", "added", "total").
type sprintCategoryRow struct {
	Label           string
	Key             string
	Tickets         int
	Points          int
	S               int
	M               int
	L               int
	NoEstimate      int
	FinishedTickets int
	FinishedPoints  int
}

// tickets is the ticket count of a size tally (S + M + L + no-estimate).
func tickets(t store.SizeTally) int { return t.S + t.M + t.L + t.NoEstimate }

// sprintRow builds one category row from its tally and the finished tally
// attributed to it.
func sprintRow(label, key string, tally, finished store.SizeTally) sprintCategoryRow {
	return sprintCategoryRow{
		Label:           label,
		Key:             key,
		Tickets:         tickets(tally),
		Points:          tally.Points,
		S:               tally.S,
		M:               tally.M,
		L:               tally.L,
		NoEstimate:      tally.NoEstimate,
		FinishedTickets: tickets(finished),
		FinishedPoints:  finished.Points,
	}
}

// handleSprint renders the full standalone Sprint page.
func (s *Server) handleSprint(w http.ResponseWriter, r *http.Request) {
	s.renderSprint(w, "sprint.html")
}

// handleSprintResults renders just the results fragment (the HTMX swap target),
// so the panel can refresh on its own without reloading the whole page.
func (s *Server) handleSprintResults(w http.ResponseWriter, r *http.Request) {
	s.renderSprint(w, "sprint-panel")
}

func (s *Server) renderSprint(w http.ResponseWriter, name string) {
	view, err := s.sprintView()
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// sprintView resolves the active sprint into the page model: the Started-with /
// Added / Finished breakdown over the sprint window [sprint start, now). With no
// active sprint it returns early with HasSprint=false, so the template shows the
// friendly no-sprint state rather than a row of zeros.
func (s *Server) sprintView() (sprintView, error) {
	sprint, hasSprint, err := s.rollups.ActiveSprintWindow()
	if err != nil {
		return sprintView{}, err
	}
	view := sprintView{HasSprint: hasSprint}
	if !hasSprint {
		return view, nil
	}
	view.SprintName = sprint.Name

	from, to := s.sprintWindow(sprint)
	view.WindowLabel = rangeLabel(from, to)
	cats, err := s.rollups.SprintCategoriesInWindow(sprint.ID, from, to)
	if err != nil {
		return sprintView{}, err
	}
	view.Rows = []sprintCategoryRow{
		sprintRow("Started with", "started", cats.StartedWith, cats.FinishedFromStarted),
		sprintRow("Added", "added", cats.Added, cats.FinishedFromAdded),
		sprintRow("Total", "total", cats.Total, cats.FinishedTotal),
	}
	// Empty when nothing was started with and nothing was added — a table of zeros
	// is not worth showing (same friendly-note philosophy as the skeleton).
	view.Empty = cats.Total == store.SizeTally{}
	return view, nil
}

// sprintWindow is the Sprint view's window: from the active sprint's activation
// instant (its start, anchored on Jira's startDate — see jira.Sprint /
// docs/adr/0002) to now, both in the display timezone. Anchoring Started-with /
// Added on the sprint's own start — not a calendar week — is the fix from #53:
// a ticket carried over into the sprint at rollover is a member at the start
// instant and so lands in Started-with, not Added.
func (s *Server) sprintWindow(sprint store.ActiveSprint) (from, to time.Time) {
	return sprint.Activated.In(s.loc), s.now().In(s.loc)
}

// rangeLabel formats [from, to) inclusively for humans, e.g.
// "13 Jul – 15 Jul 2026" (the exclusive upper bound is shown as its last day).
func rangeLabel(from, to time.Time) string {
	lastDay := to.AddDate(0, 0, -1)
	return from.Format("2 Jan") + " – " + lastDay.Format("2 Jan 2006")
}
