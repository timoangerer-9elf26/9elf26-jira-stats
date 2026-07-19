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
// Columns carries the outcome columns (Open, Finished, Removed, Total) in display
// order with their help tooltips; Rows carries the cohort rows (Started with,
// Added, Total), each a slice of Cells in the same column order. Each cell is a
// size tally rendered as a tickets·points headline with the S/M/L/no-estimate
// split — the same styling as the old single tickets·points cell.
type sprintView struct {
	WindowLabel string // human-readable resolved window, e.g. "13 Jul – 15 Jul 2026"
	SprintName  string
	HasSprint   bool
	Columns     []sprintColumn
	Rows        []sprintCohortRow
	Empty       bool
}

// sprintColumn is one outcome column header: its render key (open/finished/
// removed/total), display label and the help-tooltip copy shown on the `?`.
type sprintColumn struct {
	Key     string
	Label   string
	Tooltip string
}

// sprintCohortRow is one row of the Sprint table: a cohort's label plus its four
// outcome cells in column order. Key is the testid/render prefix ("started",
// "added", "total"); Tooltip is the help copy on the row label's `?` (empty for
// the Total row, which needs no explanation).
type sprintCohortRow struct {
	Label   string
	Key     string
	Tooltip string
	Cells   []sprintCell
}

// sprintCell is one cohort × outcome cell: a size tally as a tickets+points
// headline with the S/M/L/no-estimate split. RowKey/ColKey form the testid
// (sprint-cell:<row>:<col>...).
type sprintCell struct {
	RowKey     string
	ColKey     string
	Tickets    int
	Points     int
	S          int
	M          int
	L          int
	NoEstimate int
}

// sprintColumns is the fixed outcome-column order and the agreed help copy. Total
// = Open + Finished + Removed.
var sprintColumns = []sprintColumn{
	{Key: "open", Label: "Open", Tooltip: "Still in the sprint and not finished — the remainder after Finished and Removed."},
	{Key: "finished", Label: "Finished", Tooltip: "Crossed into Done within the sprint window (sprint start to now)."},
	{Key: "removed", Label: "Removed", Tooltip: "Not finished and either cancelled or no longer in the sprint. For Added, only cancellation counts — reprioritised-out adds are dropped."},
	{Key: "total", Label: "Total", Tooltip: "Open + Finished + Removed."},
}

const (
	startedTooltip = "Members at the end of the one-hour grace window (sprint start + 1h), regardless of status — the capacity baseline."
	addedTooltip   = "First joined the sprint after the grace window — scope creep. Reprioritised out again and it drops entirely; only cancellation counts as Removed."
)

// tickets is the ticket count of a size tally (S + M + L + no-estimate).
func tickets(t store.SizeTally) int { return t.S + t.M + t.L + t.NoEstimate }

// cohortRow builds one cohort row: its label, tooltip and its four outcome cells
// in the fixed column order.
func cohortRow(label, key, tooltip string, c store.SprintCohort) sprintCohortRow {
	return sprintCohortRow{
		Label:   label,
		Key:     key,
		Tooltip: tooltip,
		Cells: []sprintCell{
			sprintCellOf(key, "open", c.Open),
			sprintCellOf(key, "finished", c.Finished),
			sprintCellOf(key, "removed", c.Removed),
			sprintCellOf(key, "total", c.Total),
		},
	}
}

// sprintCellOf projects one outcome tally into a render cell.
func sprintCellOf(rowKey, colKey string, t store.SizeTally) sprintCell {
	return sprintCell{
		RowKey:     rowKey,
		ColKey:     colKey,
		Tickets:    tickets(t),
		Points:     t.Points,
		S:          t.S,
		M:          t.M,
		L:          t.L,
		NoEstimate: t.NoEstimate,
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
	view.Columns = sprintColumns
	view.Rows = []sprintCohortRow{
		cohortRow("Started with", "started", startedTooltip, cats.StartedWith),
		cohortRow("Added", "added", addedTooltip, cats.Added),
		cohortRow("Total", "total", "", cats.Total),
	}
	// Empty when the grand total is zero across every column — a table of zeros is
	// not worth showing (same friendly-note philosophy as the skeleton).
	view.Empty = cats.Total.Total == store.SizeTally{}
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
