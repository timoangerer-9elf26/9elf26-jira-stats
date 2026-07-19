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

// dailyTitleFormat renders a preset button's concrete date for its hover title,
// e.g. "Fri 17 Jul" — since "Yesterday" can actually resolve to Friday.
const dailyTitleFormat = "Mon 2 Jan"

// dailyInputFormat is the value layout of an HTML datetime-local input (minute
// granularity, no timezone), used to parse and render the custom From/Until.
const dailyInputFormat = "2006-01-02T15:04"

// The Daily assignee-filter query values. "" is treated as "all". These are the
// dropdown option values; a specific name is passed through verbatim.
const (
	dailyAssigneeAll        = "all"
	dailyAssigneeUnassigned = "unassigned"
)

// The three working-day preset keys. Each spans one whole calendar day
// [00:00, next 00:00). Yesterday and day-before-yesterday walk back over
// weekends to the most recent working days; Today is literal (disabled on a
// weekend). See CONTEXT.md → Daily view and docs/adr/0003.
const (
	dailyPresetToday     = "today"
	dailyPresetYesterday = "yesterday"
	dailyPresetDayBefore = "day-before-yesterday"
)

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

// dailyPresetView is one working-day preset button: a stable Key for the URL and
// testids, a display Label (the day-before button's is its full weekday name), a
// Title showing the concrete date it maps to (hover disambiguation), plus
// Selected (highlighted) and Disabled (Today on a weekend) flags.
type dailyPresetView struct {
	Key      string
	Label    string
	Title    string
	Selected bool
	Disabled bool
}

// dailyRangeResult is the resolved outcome of the Daily range controls: the
// preset buttons (with the active one marked), the custom From/Until input
// values (always pre-filled with the resolved bounds so editing one keeps the
// other), the absolute [from, to) window, and an inline errMsg. When errMsg is
// non-empty the range is invalid: from/to stay zero and no results are rendered.
type dailyRangeResult struct {
	presets    []dailyPresetView
	customFrom string
	customTo   string
	from       time.Time
	to         time.Time
	errMsg     string
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
// Headline (e.g. "moved 5 — 2 finished, 2 advanced, 1 pulled back, created 2")
// plus the non-empty movement buckets in Finished → Advanced → Pulled back
// order. Present is false only when the selection moved nothing AND created
// nothing, so the template omits the whole section.
type dailyDigestView struct {
	Present  bool
	Headline string
	Buckets  []dailyDigestBucketView
}

// dailyCreatedTicketView is one ticket in the "tickets I created" section: its
// display fields, resolved Jira link (empty when unconfigured), and the creation
// instant rendered in the display timezone.
type dailyCreatedTicketView struct {
	Key     string
	Summary string
	Type    string
	Size    string // "S"/"M"/"L" or "no estimate"
	Href    string
	At      string
}

// dailyCreatedView is the "tickets I created" section: the tickets the selection
// authored within the window (NOT sprint-scoped, unlike the movement digest) and
// their count. Empty is true when the selection created nothing in the window.
type dailyCreatedView struct {
	Count   int
	Tickets []dailyCreatedTicketView
	Empty   bool
}

// dailyView is the model for the Daily page and its panel fragment. HasSprint is
// false when no active sprint is known (drives the no-sprint empty state); Empty
// is true when the selection has no in-window status changes.
type dailyView struct {
	SprintName string
	HasSprint  bool
	Assignees  []dailyAssigneeOption
	Presets    []dailyPresetView
	CustomFrom string
	CustomTo   string
	RangeError string
	Digest     dailyDigestView
	Created    dailyCreatedView
	Cards      []dailyCardView
	Empty      bool
}

// handleDaily renders the full standalone Daily page.
func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	s.renderDaily(w, r, "daily.html")
}

// handleDailyResults renders just the controls+results panel (the HTMX swap
// target), so the selected assignee and range controls re-render to match the
// choice — not only the results (cf. the Completed picker fix).
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
// range controls (with the current selection marked) plus the matching cards.
func (s *Server) dailyView(q url.Values) (dailyView, error) {
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
	rng := s.dailyRangeSelection(q, s.now())
	view.Presets = rng.presets
	view.CustomFrom = rng.customFrom
	view.CustomTo = rng.customTo
	view.RangeError = rng.errMsg

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

	// An invalid custom range renders no results, no silent fallback: the inline
	// error is shown and the digest / created / cards stay empty.
	if rng.errMsg != "" {
		view.Empty = true
		return view, nil
	}

	from, to := rng.from, rng.to
	tickets, err := s.rollups.DailyStatusChanges(dailyStoreAssignee(assigneeParam), from, to)
	if err != nil {
		return dailyView{}, err
	}
	// "Tickets I created" is pinned to the configured "me", NOT the selected
	// assignee: it is a personal "what I authored" panel (see the issue AC and
	// docs/adr/0003), so it stays on me even while the dropdown re-scopes the
	// movement digest to a teammate. Same window as the rest of Daily, but NOT
	// sprint-scoped: a ticket you authored counts whether or not it landed in the
	// sprint. With no me configured the section is simply empty (no crash,
	// consistent with #46's fallback) rather than listing everyone's tickets.
	var created []store.CreatedTicket
	if s.me != "" {
		created, err = s.rollups.IssuesCreatedInRange(s.me, from, to)
		if err != nil {
			return dailyView{}, err
		}
	}
	view.Created = s.dailyCreated(created)
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
	view.Digest = s.dailyDigest(tickets, view.Created.Count)
	view.Empty = len(view.Cards) == 0
	return view, nil
}

// dailyCreated builds the "tickets I created" section from the created tickets,
// keeping the store's most-recent-first order. Empty is set when nothing was
// created so the template can show a friendly empty state.
func (s *Server) dailyCreated(created []store.CreatedTicket) dailyCreatedView {
	view := dailyCreatedView{Count: len(created), Empty: len(created) == 0}
	for _, tk := range created {
		view.Tickets = append(view.Tickets, dailyCreatedTicketView{
			Key:     tk.Key,
			Summary: tk.Summary,
			Type:    tk.Type,
			Size:    sizeDisplay(tk.Size),
			Href:    s.jiraIssueURL(tk.Key),
			At:      tk.CreatedAt.In(s.loc).Format(dailyTimeFormat),
		})
	}
	return view
}

// dailyDigest summarises the window into the digest: the moved tickets bucketed
// by net-movement (Finished / Advanced / Pulled back) plus the count of tickets
// the selection created. It keeps the granular log's recency order within each
// bucket, drops empty buckets, and builds the headline from the same counts
// (e.g. "moved 5 — 2 finished, 2 advanced, 1 pulled back, created 2"). The
// created count feeds the headline alongside the movement count but the bucket
// grid stays movement-only (the created tickets get their own section). Returns a
// zero (Present=false) digest only when nothing moved AND nothing was created, so
// the template omits the whole section.
func (s *Server) dailyDigest(tickets []store.DailyTicket, createdCount int) dailyDigestView {
	if len(tickets) == 0 && createdCount == 0 {
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
	if len(tickets) > 0 {
		var movementParts []string
		for _, b := range []dailyDigestBucketView{finished, advanced, pulledBack} {
			b.Count = len(b.Tickets)
			if b.Count == 0 {
				continue
			}
			buckets = append(buckets, b)
			movementParts = append(movementParts, fmt.Sprintf("%d %s", b.Count, strings.ToLower(b.Label)))
		}
		parts = append(parts, fmt.Sprintf("moved %d — %s", len(tickets), strings.Join(movementParts, ", ")))
	}
	if createdCount > 0 {
		parts = append(parts, fmt.Sprintf("created %d", createdCount))
	}
	return dailyDigestView{
		Present:  true,
		Headline: strings.Join(parts, ", "),
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

// dailyRangeSelection resolves the Daily range controls from the request query,
// computed in the display timezone. Precedence: a custom range (from/to present
// with no preset param) is honoured verbatim, parsed and validated — an invalid
// range yields an inline error and no window. Otherwise a working-day preset
// drives the range; an absent/unknown preset falls back to the default (Today,
// or Yesterday when Today is disabled on a weekend). The preset buttons are
// always built (labels, concrete-date titles, disabled/selected flags) so the
// controls render identically in either mode.
func (s *Server) dailyRangeSelection(q url.Values, now time.Time) dailyRangeResult {
	now = now.In(s.loc)
	y, m, d := now.Date()
	todayStart := time.Date(y, m, d, 0, 0, 0, 0, s.loc)
	yesterdayStart := mostRecentWorkingDayBefore(todayStart)
	dayBeforeStart := mostRecentWorkingDayBefore(yesterdayStart)
	todayDisabled := isWeekend(now.Weekday())

	days := map[string]time.Time{
		dailyPresetToday:     todayStart,
		dailyPresetYesterday: yesterdayStart,
		dailyPresetDayBefore: dayBeforeStart,
	}

	var res dailyRangeResult
	selectedKey := ""

	presetParam := q.Get("preset")
	fromParam := q.Get("from")
	toParam := q.Get("to")

	// Custom mode only when no preset is requested and a From/Until was supplied.
	if presetParam == "" && (fromParam != "" || toParam != "") {
		from, to, errMsg := parseDailyRange(fromParam, toParam, s.loc)
		if errMsg != "" {
			// Echo the raw inputs so the invalid values stay visible to correct.
			res.customFrom = fromParam
			res.customTo = toParam
			res.errMsg = errMsg
		} else {
			res.from, res.to = from, to
			res.customFrom = from.Format(dailyInputFormat)
			res.customTo = to.Format(dailyInputFormat)
		}
	} else {
		selectedKey = normalizeDailyPreset(presetParam, todayDisabled)
		day := days[selectedKey]
		res.from = day
		res.to = day.AddDate(0, 0, 1)
		res.customFrom = res.from.Format(dailyInputFormat)
		res.customTo = res.to.Format(dailyInputFormat)
	}

	// Build the three preset buttons in display order. The day-before button is
	// labelled with its full weekday name; each carries its concrete date as a
	// hover title.
	res.presets = []dailyPresetView{
		{
			Key: dailyPresetToday, Label: "Today",
			Title:    todayStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetToday,
			Disabled: todayDisabled,
		},
		{
			Key: dailyPresetYesterday, Label: "Yesterday",
			Title:    yesterdayStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetYesterday,
		},
		{
			Key: dailyPresetDayBefore, Label: dayBeforeStart.Format("Monday"),
			Title:    dayBeforeStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetDayBefore,
		},
	}
	return res
}

// normalizeDailyPreset resolves a requested preset key to a concrete selection.
// An absent or unrecognised key defaults to Today; because Today is disabled on
// a weekend, a Today selection (default or explicit) then falls back to
// Yesterday — the most recent enabled preset — so a disabled Today is never the
// active selection.
func normalizeDailyPreset(param string, todayDisabled bool) string {
	switch param {
	case dailyPresetYesterday:
		return dailyPresetYesterday
	case dailyPresetDayBefore:
		return dailyPresetDayBefore
	default: // "today", "" or unknown
		if todayDisabled {
			return dailyPresetYesterday
		}
		return dailyPresetToday
	}
}

// parseDailyRange parses and validates a custom From/Until pair in the display
// timezone. Both must be present and parseable (datetime-local, or an ISO
// variant with seconds/offset), and From must be strictly before Until;
// otherwise it returns an inline error message and zero times (no fallback).
func parseDailyRange(fromParam, toParam string, loc *time.Location) (from, to time.Time, errMsg string) {
	from, okFrom := parseDailyInstant(fromParam, loc)
	to, okTo := parseDailyInstant(toParam, loc)
	if !okFrom || !okTo {
		return time.Time{}, time.Time{}, "Enter both a valid From and Until date-time."
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, "From must be before Until."
	}
	return from, to, ""
}

// parseDailyInstant parses one custom range endpoint, accepting the
// datetime-local input layout plus the common ISO variants the URL may carry.
func parseDailyInstant(v string, loc *time.Location) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{dailyInputFormat, "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// mostRecentWorkingDayBefore returns the midnight of the most recent working day
// strictly before dayStart, walking back over weekends (Sat/Sun) so the day
// before a Monday is the preceding Friday.
func mostRecentWorkingDayBefore(dayStart time.Time) time.Time {
	d := dayStart.AddDate(0, 0, -1)
	for isWeekend(d.Weekday()) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// isWeekend reports whether a weekday falls on the weekend (Sat/Sun), which the
// Yesterday / day-before presets skip when reaching back to a working day, and
// which disables the literal Today preset.
func isWeekend(d time.Weekday) bool {
	return d == time.Saturday || d == time.Sunday
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
