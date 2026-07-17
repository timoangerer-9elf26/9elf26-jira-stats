package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// The two Weekly window modes. "work-week" is the default. Both scope membership
// to the active sprint; the mode changes only the time window (see CONTEXT.md
// "Week window" and docs/adr/0002).
const (
	weeklyWindowWorkWeek   = "work-week"
	weeklyWindowLiveSprint = "live-sprint"
)

// defaultWeeklyWindow is applied when no (or an unknown) window is requested.
const defaultWeeklyWindow = weeklyWindowWorkWeek

// weeklyWindowDef is one selectable window mode in the Weekly selector.
type weeklyWindowDef struct {
	Key   string
	Label string
}

// weeklyWindowDefs are the Weekly window modes, in display order.
var weeklyWindowDefs = []weeklyWindowDef{
	{weeklyWindowWorkWeek, "Work week"},
	{weeklyWindowLiveSprint, "Live sprint"},
}

// weeklyWindowOption is one window-mode control, with Selected reflecting the
// current choice so the picked mode stays highlighted after a swap.
type weeklyWindowOption struct {
	Key      string
	Label    string
	Selected bool
}

// weeklyView is the model for the Weekly page and its panel fragment. HasSprint
// is false when no active sprint is recorded (drives the no-sprint empty state,
// same treatment as the Board view); Empty is true when neither Started-with nor
// Added has any ticket, so the results show a friendly note instead of a table of
// zeros.
//
// Rows carries the three category rows in display order (Started with, Added,
// Total). Each row is a tickets+points headline with the S/M/L split and its
// finished figure (finished-from-started, finished-from-added and the finished
// total respectively).
type weeklyView struct {
	Windows     []weeklyWindowOption
	WindowLabel string // human-readable resolved window, e.g. "13 Jul – 17 Jul 2026"
	SprintName  string
	HasSprint   bool
	Rows        []weeklyCategoryRow
	Empty       bool
}

// weeklyCategoryRow is one row of the Weekly table: a category's size tally as a
// tickets+points headline with the S/M/L/no-estimate split, plus the finished
// figure attributed to that category. Key is the testid/render prefix
// ("started", "added", "total").
type weeklyCategoryRow struct {
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

// weeklyRow builds one category row from its tally and the finished tally
// attributed to it.
func weeklyRow(label, key string, tally, finished store.SizeTally) weeklyCategoryRow {
	return weeklyCategoryRow{
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

// handleWeekly renders the full standalone Weekly page.
func (s *Server) handleWeekly(w http.ResponseWriter, r *http.Request) {
	s.renderWeekly(w, r, "weekly.html")
}

// handleWeeklyResults renders just the panel fragment (the HTMX swap target), so
// the selected window mode re-renders highlighted — not only the numbers. This
// whole-panel swap is the fix from #10 (a preset click that updated the results
// but left the wrong control highlighted); the mode selector lives inside the
// swapped panel for exactly that reason.
func (s *Server) handleWeeklyResults(w http.ResponseWriter, r *http.Request) {
	s.renderWeekly(w, r, "weekly-panel")
}

func (s *Server) renderWeekly(w http.ResponseWriter, r *http.Request, name string) {
	view, err := s.weeklyView(r.URL.Query())
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// weeklyView resolves the request query into the page model: the window-mode
// controls (with the current selection marked) plus the active sprint's
// finished-this-week tally over the resolved window. With no active sprint it
// returns early with HasSprint=false, so the template shows the friendly
// no-sprint state rather than a row of zeros.
func (s *Server) weeklyView(q url.Values) (weeklyView, error) {
	windowKey := weeklyWindowKey(q.Get("window"))

	sprint, hasSprint, err := s.rollups.ActiveSprintWindow()
	if err != nil {
		return weeklyView{}, err
	}
	view := weeklyView{HasSprint: hasSprint}
	for _, def := range weeklyWindowDefs {
		view.Windows = append(view.Windows, weeklyWindowOption{
			Key: def.Key, Label: def.Label, Selected: def.Key == windowKey,
		})
	}
	if !hasSprint {
		return view, nil
	}
	view.SprintName = sprint.Name

	from, to := s.weeklyWindow(windowKey, sprint)
	view.WindowLabel = rangeLabel(from, to)
	cats, err := s.rollups.WeeklyCategoriesInWindow(sprint.ID, from, to)
	if err != nil {
		return weeklyView{}, err
	}
	view.Rows = []weeklyCategoryRow{
		weeklyRow("Started with", "started", cats.StartedWith, cats.FinishedFromStarted),
		weeklyRow("Added during the week", "added", cats.Added, cats.FinishedFromAdded),
		weeklyRow("Total", "total", cats.Total, cats.FinishedTotal),
	}
	// Empty when nothing was started with and nothing was added — a table of zeros
	// is not worth showing (same friendly-note philosophy as the skeleton).
	view.Empty = cats.Total == store.SizeTally{}
	return view, nil
}

// weeklyWindow resolves a window-mode key to its absolute [from, to) bounds,
// computed in the display timezone. Work week is a fixed clock window —
// Monday 00:00 → Saturday 00:00 (Friday end-of-day), Europe/Berlin — for the
// week containing now, so the weekend is excluded. Live sprint runs from the
// active sprint's activation instant (its startDate; Jira Cloud exposes no
// dedicated activation field, see jira.Sprint / docs/adr/0002) to now. A
// live-sprint request with no recorded activation falls back to the work-week
// window so the view still renders a sensible span.
func (s *Server) weeklyWindow(windowKey string, sprint store.ActiveSprint) (from, to time.Time) {
	now := s.now().In(s.loc)
	if windowKey == weeklyWindowLiveSprint && !sprint.Activated.IsZero() {
		return sprint.Activated.In(s.loc), now
	}
	monday := weekStart(now, s.loc)
	return monday, monday.AddDate(0, 0, 5) // Mon 00:00 + 5 days = Sat 00:00
}

// weeklyWindowKey normalizes a requested window mode, defaulting to Work week.
func weeklyWindowKey(v string) string {
	if v == weeklyWindowLiveSprint {
		return weeklyWindowLiveSprint
	}
	return defaultWeeklyWindow
}

// weekStart returns Monday 00:00 in loc for the ISO week containing t. Shared
// with the Velocity view's per-week bucketing.
func weekStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	// Go weekdays are Sunday=0..Saturday=6; ISO weeks start Monday.
	offset := (int(t.Weekday()) + 6) % 7
	y, m, d := t.Date()
	return time.Date(y, m, d-offset, 0, 0, 0, 0, loc)
}

// rangeLabel formats [from, to) inclusively for humans, e.g.
// "13 Jul – 17 Jul 2026" (the exclusive upper bound is shown as its last day).
func rangeLabel(from, to time.Time) string {
	lastDay := to.AddDate(0, 0, -1)
	return from.Format("2 Jan") + " – " + lastDay.Format("2 Jan 2006")
}
