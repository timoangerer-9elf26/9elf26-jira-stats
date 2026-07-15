package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// dateInputFormat is the value format of an HTML <input type="date"> and of the
// custom-range query params (YYYY-MM-DD, interpreted in the display timezone).
const dateInputFormat = "2006-01-02"

// preset is one selectable date-range shortcut in the picker.
type preset struct {
	Key   string // query value, e.g. "this-week"
	Label string // human label, e.g. "This week"
}

// completedPresets are the Completed-view date-range shortcuts, in display
// order. "active-sprint" is the default.
var completedPresets = []preset{
	{"this-week", "This week"},
	{"last-week", "Last week"},
	{"active-sprint", "Active sprint"},
	{"last-2-weeks", "Last 2 weeks"},
}

// defaultPreset is applied when no (or an unknown) preset is requested.
const defaultPreset = "active-sprint"

// dateRange is a resolved [From, To) window plus the presentation state the
// picker needs to reflect the current selection.
type dateRange struct {
	From, To time.Time
	Preset   string // the active preset key ("custom" for a custom range)
	FromDate string // From as YYYY-MM-DD, for the custom <input> and echo
	ToDate   string // To as YYYY-MM-DD (exclusive boundary date)
	Label    string // human-readable range, e.g. "13 Jul – 19 Jul 2026"
}

// completedView is the model for the Completed page and its results fragment.
// Empty is true when nothing completed in the resolved range, so the results
// show a friendly note instead of a row of zeros.
type completedView struct {
	Tally   store.SizeTally
	Range   dateRange
	Presets []preset
	Empty   bool
}

// handleCompleted renders the full standalone Completed page.
func (s *Server) handleCompleted(w http.ResponseWriter, r *http.Request) {
	s.renderCompleted(w, r, "completed.html")
}

// handleCompletedResults renders just the results fragment (the HTMX swap
// target) for the requested range.
func (s *Server) handleCompletedResults(w http.ResponseWriter, r *http.Request) {
	s.renderCompleted(w, r, "completed-results")
}

func (s *Server) renderCompleted(w http.ResponseWriter, r *http.Request, name string) {
	rng := s.resolveRange(r.URL.Query())
	tally, err := s.rollups.CompletedInRange(rng.From, rng.To)
	if err != nil {
		s.renderError(w)
		return
	}
	view := completedView{Tally: tally, Range: rng, Presets: completedPresets, Empty: tally == store.SizeTally{}}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// resolveRange turns the request query into a concrete [From, To) window. All
// week maths run in the display timezone with Monday-start ISO weeks.
//
// "Active sprint": the store records only a sprint label per issue, not sprint
// start/end dates or which sprint is active, so the real sprint window cannot be
// derived from stored data. As a pragmatic, clearly-documented stand-in we use
// the current Berlin ISO week (a one-week sprint being the team's norm). A later
// ticket that persists sprint dates can replace just this branch.
func (s *Server) resolveRange(q url.Values) dateRange {
	now := s.now().In(s.loc)
	thisMonday := weekStart(now, s.loc)
	nextMonday := thisMonday.AddDate(0, 0, 7)
	lastMonday := thisMonday.AddDate(0, 0, -7)

	switch key := presetKey(q.Get("preset")); key {
	case "last-week":
		return s.makeRange(lastMonday, thisMonday, key)
	case "last-2-weeks":
		return s.makeRange(lastMonday, nextMonday, key)
	case "custom":
		return s.customRange(q.Get("from"), q.Get("to"), thisMonday, nextMonday)
	case "this-week", "active-sprint":
		return s.makeRange(thisMonday, nextMonday, key)
	default:
		return s.makeRange(thisMonday, nextMonday, defaultPreset)
	}
}

// presetKey normalizes a requested preset, defaulting when empty.
func presetKey(v string) string {
	if v == "" {
		return defaultPreset
	}
	return v
}

// customRange parses the from/to date inputs; a missing or malformed bound
// falls back to the current week so the view still renders.
func (s *Server) customRange(fromStr, toStr string, fallbackFrom, fallbackTo time.Time) dateRange {
	from := s.parseDate(fromStr, fallbackFrom)
	to := s.parseDate(toStr, fallbackTo)
	return s.makeRange(from, to, "custom")
}

func (s *Server) parseDate(v string, fallback time.Time) time.Time {
	t, err := time.ParseInLocation(dateInputFormat, v, s.loc)
	if err != nil {
		return fallback
	}
	return t
}

func (s *Server) makeRange(from, to time.Time, key string) dateRange {
	return dateRange{
		From:     from,
		To:       to,
		Preset:   key,
		FromDate: from.Format(dateInputFormat),
		ToDate:   to.Format(dateInputFormat),
		Label:    rangeLabel(from, to),
	}
}

// weekStart returns Monday 00:00 in loc for the ISO week containing t.
func weekStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	// Go weekdays are Sunday=0..Saturday=6; ISO weeks start Monday.
	offset := (int(t.Weekday()) + 6) % 7
	y, m, d := t.Date()
	return time.Date(y, m, d-offset, 0, 0, 0, 0, loc)
}

// rangeLabel formats [from, to) inclusively for humans, e.g.
// "13 Jul – 19 Jul 2026" (the exclusive upper bound is shown as its last day).
func rangeLabel(from, to time.Time) string {
	lastDay := to.AddDate(0, 0, -1)
	return from.Format("2 Jan") + " – " + lastDay.Format("2 Jan 2006")
}
