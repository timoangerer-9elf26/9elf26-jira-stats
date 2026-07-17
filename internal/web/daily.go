package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// dailyTimeFormat renders an in-window transition instant in the display
// timezone, e.g. "16 Jul 08:00".
const dailyTimeFormat = "2 Jan 15:04"

// The Daily assignee-filter query values. "" is treated as "all". These are the
// dropdown option values; a specific name is passed through verbatim.
const (
	dailyAssigneeAll        = "all"
	dailyAssigneeUnassigned = "unassigned"
)

// The two Daily window keys. "last-24h" is the default.
const (
	dailyWindowLast24h        = "last-24h"
	dailyWindowSinceYesterday = "since-yesterday"
)

// dailyWindowDef is one selectable window in the Daily controls.
type dailyWindowDef struct {
	Key   string
	Label string
}

// dailyWindowDefs are the Daily window options, in display order.
var dailyWindowDefs = []dailyWindowDef{
	{dailyWindowLast24h, "Last 24h"},
	{dailyWindowSinceYesterday, "Since yesterday"},
}

// dailyChangeView is one in-window status change on a card: "From → To" at a
// display-timezone timestamp.
type dailyChangeView struct {
	From string
	To   string
	At   string
}

// dailyCardView is one ticket on the Daily view: its display fields, resolved
// Jira link (empty when unconfigured), and its in-window status changes.
type dailyCardView struct {
	Key      string
	Summary  string
	Assignee string // "Unassigned" for a ticket with no assignee
	Size     string // "S"/"M"/"L" or "no estimate"
	Type     string
	Href     string
	Changes  []dailyChangeView
}

// dailyAssigneeOption is one entry of the assignee dropdown (All, Unassigned, or
// a named assignee), with Selected reflecting the current filter.
type dailyAssigneeOption struct {
	Value    string
	Label    string
	Selected bool
}

// dailyWindowView is one window control, with Selected reflecting the current
// choice.
type dailyWindowView struct {
	Key      string
	Label    string
	Selected bool
}

// dailyDigestTicketView is one ticket in a digest bucket: its key, resolved Jira
// link (empty when unconfigured), and its net movement across the window
// rendered as From ⟶ To.
type dailyDigestTicketView struct {
	Key  string
	Href string
	From string
	To   string
}

// dailyDigestBucketView is one net-movement bucket of the digest: a stable Key
// for testids/styling, a display Label, its ticket count, and the tickets that
// landed in it (in the granular log's recency order).
type dailyDigestBucketView struct {
	Key     string
	Label   string
	Count   int
	Tickets []dailyDigestTicketView
}

// dailyDigestView is the summary layer above the granular log: a one-line
// Headline (e.g. "moved 5 — 2 finished, 2 advanced, 1 pulled back") plus the
// non-empty buckets in Finished → Advanced → Pulled back order. Present is false
// when the selection moved nothing, so the template omits the whole section.
type dailyDigestView struct {
	Present  bool
	Headline string
	Buckets  []dailyDigestBucketView
}

// dailyView is the model for the Daily page and its panel fragment. HasSprint is
// false when no active sprint is known (drives the no-sprint empty state); Empty
// is true when the selection has no in-window status changes.
type dailyView struct {
	SprintName string
	HasSprint  bool
	Assignees  []dailyAssigneeOption
	Windows    []dailyWindowView
	Digest     dailyDigestView
	Cards      []dailyCardView
	Empty      bool
}

// handleDaily renders the full standalone Daily page.
func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	s.renderDaily(w, r, "daily.html")
}

// handleDailyResults renders just the controls+results panel (the HTMX swap
// target), so the selected assignee and window re-render to match the choice —
// not only the results (cf. the Completed picker fix).
func (s *Server) handleDailyResults(w http.ResponseWriter, r *http.Request) {
	s.renderDaily(w, r, "daily-panel")
}

func (s *Server) renderDaily(w http.ResponseWriter, r *http.Request, name string) {
	view, err := s.dailyView(r.URL.Query())
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// dailyView resolves the request query into the page model: the assignee and
// window controls (with the current selection marked) plus the matching cards.
func (s *Server) dailyView(q url.Values) (dailyView, error) {
	windowKey := dailyWindowKey(q.Get("window"))
	assigneeParam := q.Get("assignee")
	if assigneeParam == "" {
		// No explicit choice: default to the configured "me" (a display name), or
		// "All" when me is unconfigured. An explicit choice (incl. "all") overrides.
		assigneeParam = s.defaultAssignee()
	}

	sprint, hasSprint, err := s.rollups.ActiveSprintWindow()
	if err != nil {
		return dailyView{}, err
	}
	view := dailyView{HasSprint: hasSprint}
	if hasSprint {
		view.SprintName = sprint.Name
	}
	for _, wdef := range dailyWindowDefs {
		view.Windows = append(view.Windows, dailyWindowView{
			Key: wdef.Key, Label: wdef.Label, Selected: wdef.Key == windowKey,
		})
	}

	names, err := s.rollups.ActiveSprintAssignees()
	if err != nil {
		return dailyView{}, err
	}
	view.Assignees = append(view.Assignees,
		dailyAssigneeOption{Value: dailyAssigneeAll, Label: "All", Selected: assigneeParam == dailyAssigneeAll},
		dailyAssigneeOption{Value: dailyAssigneeUnassigned, Label: "Unassigned", Selected: assigneeParam == dailyAssigneeUnassigned},
	)
	// Tracks whether the resolved assignee already appears as one of the options
	// emitted above (distinct from each option's own Selected flag).
	represented := assigneeParam == dailyAssigneeAll || assigneeParam == dailyAssigneeUnassigned
	for _, name := range names {
		match := assigneeParam == name
		represented = represented || match
		view.Assignees = append(view.Assignees, dailyAssigneeOption{
			Value: name, Label: name, Selected: match,
		})
	}
	// The filter resolved to a named assignee not on the active sprint (e.g. a
	// configured "me" who has no sprint work). Surface them as a selected option
	// so the dropdown reflects the actual scope rather than silently showing All.
	if !represented {
		view.Assignees = append(view.Assignees, dailyAssigneeOption{
			Value: assigneeParam, Label: assigneeParam, Selected: true,
		})
	}

	// With no active sprint there is nothing to query; the template shows the
	// friendly no-sprint state.
	if !hasSprint {
		return view, nil
	}

	from, to := s.dailyRange(windowKey, s.now())
	tickets, err := s.rollups.DailyStatusChanges(dailyStoreAssignee(assigneeParam), from, to)
	if err != nil {
		return dailyView{}, err
	}
	for _, tk := range tickets {
		card := dailyCardView{
			Key:      tk.Key,
			Summary:  tk.Summary,
			Assignee: assigneeDisplay(tk.Assignee),
			Size:     sizeDisplay(tk.Size),
			Type:     tk.Type,
			Href:     s.jiraIssueURL(tk.Key),
		}
		for _, c := range tk.Changes {
			card.Changes = append(card.Changes, dailyChangeView{
				From: statusDisplay(c.From),
				To:   c.To,
				At:   c.TransitionedAt.In(s.loc).Format(dailyTimeFormat),
			})
		}
		view.Cards = append(view.Cards, card)
	}
	view.Digest = s.dailyDigest(tickets)
	view.Empty = len(view.Cards) == 0
	return view, nil
}

// dailyDigest summarises the moved tickets into the digest's net-movement
// buckets. It buckets each ticket by its Movement, keeps the granular log's
// recency order within each bucket, drops empty buckets, and builds the headline
// (e.g. "moved 5 — 2 finished, 2 advanced, 1 pulled back") from the same counts.
// Returns a zero (Present=false) digest when nothing moved, so the template
// omits the whole section.
func (s *Server) dailyDigest(tickets []store.DailyTicket) dailyDigestView {
	if len(tickets) == 0 {
		return dailyDigestView{}
	}
	// Display order: Finished, Advanced, Pulled back.
	finished := dailyDigestBucketView{Key: "finished", Label: "Finished"}
	advanced := dailyDigestBucketView{Key: "advanced", Label: "Advanced"}
	pulledBack := dailyDigestBucketView{Key: "pulled-back", Label: "Pulled back"}
	for _, tk := range tickets {
		entry := dailyDigestTicketView{
			Key:  tk.Key,
			Href: s.jiraIssueURL(tk.Key),
			From: statusDisplay(tk.StartStatus()),
			To:   tk.EndStatus(),
		}
		switch tk.Movement() {
		case store.MovementFinished:
			finished.Tickets = append(finished.Tickets, entry)
		case store.MovementPulledBack:
			pulledBack.Tickets = append(pulledBack.Tickets, entry)
		default:
			advanced.Tickets = append(advanced.Tickets, entry)
		}
	}
	var buckets []dailyDigestBucketView
	var parts []string
	for _, b := range []dailyDigestBucketView{finished, advanced, pulledBack} {
		b.Count = len(b.Tickets)
		if b.Count == 0 {
			continue
		}
		buckets = append(buckets, b)
		parts = append(parts, fmt.Sprintf("%d %s", b.Count, strings.ToLower(b.Label)))
	}
	return dailyDigestView{
		Present:  true,
		Headline: fmt.Sprintf("moved %d — %s", len(tickets), strings.Join(parts, ", ")),
		Buckets:  buckets,
	}
}

// defaultAssignee is the Daily assignee filter when the request carries no
// explicit choice: the configured "me" display name, or "all" when me is unset.
func (s *Server) defaultAssignee() string {
	if s.me != "" {
		return s.me
	}
	return dailyAssigneeAll
}

// dailyRange resolves a window key to its absolute [from, to) bounds, computed
// in the display timezone. "Last 24h" rolls back 24h from now; "Since yesterday"
// runs from 00:00 of the last *working* day to now — walking back over weekends
// so a Monday morning (and Sat/Sun) reaches Friday, not the prior calendar day.
func (s *Server) dailyRange(windowKey string, now time.Time) (from, to time.Time) {
	now = now.In(s.loc)
	switch windowKey {
	case dailyWindowSinceYesterday:
		y, m, d := now.Date()
		startToday := time.Date(y, m, d, 0, 0, 0, 0, s.loc)
		from = startToday.AddDate(0, 0, -1)
		for isWeekend(from.Weekday()) {
			from = from.AddDate(0, 0, -1)
		}
		return from, now
	default: // last-24h
		return now.Add(-24 * time.Hour), now
	}
}

// isWeekend reports whether a weekday falls on the weekend (Sat/Sun), which the
// "Since yesterday" window skips when reaching back to the last working day.
func isWeekend(d time.Weekday) bool {
	return d == time.Saturday || d == time.Sunday
}

// dailyWindowKey normalizes a requested window, defaulting to Last 24h.
func dailyWindowKey(v string) string {
	if v == dailyWindowSinceYesterday {
		return dailyWindowSinceYesterday
	}
	return dailyWindowLast24h
}

// dailyStoreAssignee maps a dropdown value to the store filter argument: "all"
// (or empty) means all assignees (""), "unassigned" the no-assignee sentinel,
// and any other value an exact name match.
func dailyStoreAssignee(param string) string {
	switch param {
	case "", dailyAssigneeAll:
		return ""
	case dailyAssigneeUnassigned:
		return store.UnassignedAssignee
	default:
		return param
	}
}

// assigneeDisplay renders a ticket's assignee for a card, labelling an empty
// assignee as "Unassigned".
func assigneeDisplay(assignee string) string {
	if assignee == "" {
		return "Unassigned"
	}
	return assignee
}

// statusDisplay renders a transition's source status, labelling a missing from
// (a first transition with no recorded source) as "(none)".
func statusDisplay(status string) string {
	if status == "" {
		return "(none)"
	}
	return status
}
