package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// dailyTimeFormat renders a card's latest in-window activity instant in the
// display timezone as the compact "20.7. 19:10" the board uses.
const dailyTimeFormat = "2.1. 15:04"

// dailyTitleFormat renders a preset button's concrete date for its hover title,
// e.g. "Fri 17 Jul" — since "Yesterday" can actually resolve to Friday.
const dailyTitleFormat = "Mon 2 Jan"

// dailyInputFormat is the value layout of an HTML datetime-local input (minute
// granularity, no timezone), used to parse and render the custom From/Until.
const dailyInputFormat = "2006-01-02T15:04"

// dailyAssigneeUnassigned is the URL-safe sentinel value the Unassigned avatar
// chip carries in an ?assignee= param. It maps to store.UnassignedAssignee (a
// NUL-prefixed value that cannot travel in a URL) when the filter reaches the
// store. Zero assignee params means "all"; a named chip carries its display name
// verbatim.
const dailyAssigneeUnassigned = "__unassigned__"

// The three working-day preset keys. Each spans one whole calendar day
// [00:00, next 00:00). Yesterday and day-before-yesterday walk back over
// weekends to the most recent working days; Today is literal (disabled on a
// weekend). See CONTEXT.md → Daily view and docs/adr/0003.
const (
	dailyPresetToday     = "today"
	dailyPresetYesterday = "yesterday"
	dailyPresetDayBefore = "day-before-yesterday"
)

// dailyPresetLast24h is the rolling preset key: a [now − 24h, now) window,
// distinct from the calendar-day presets above. It is never weekend-adjusted
// and never disabled — it always resolves relative to the current instant, and
// sits rightmost after Today. See docs/adr/0003 (amended).
const dailyPresetLast24h = "last-24h"

// dailyLast24hWindow is the rolling window the Last 24h preset spans.
const dailyLast24hWindow = 24 * time.Hour

// dailyCardView is one card on the Daily board: the display fields, resolved
// Jira link (empty when unconfigured), the latest in-window activity timestamp
// ("20.7. 19:10"), and the origin badge fields. Origin names where the card came
// from into its column ("↳ from <OriginFrom>"), coloured by Kind; a card created
// in the window reads "✦ created here" (CreatedHere), with a kind colour only
// when it also moved. Moves is shown as "· N moves" when > 1.
type dailyCardView struct {
	Key     string
	Summary string
	// Assignee is the raw display name ("" when unassigned), AvatarURL the public
	// Jira avatar image URL ("" when none) and Initials the computed fallback — the
	// trio the shared card-avatar partial renders (image → initials → empty circle),
	// exactly as the Board does.
	Assignee  string
	AvatarURL string
	Initials  string
	Size      string // "S"/"M"/"L" or "no estimate"
	// RawSize is the ticket's stored T-shirt label ("S"/"M"/"L" or "" for
	// no-estimate), carried alongside Size so the editable pill knows the current
	// selection and the value to revert to. Only consumed when Editable.
	RawSize string
	// Editable makes the estimate pill an interactive write-back control reusing
	// POST /board/estimate (#115, docs/adr/0005), the same behaviour as the Board.
	// The Daily board sets it true; the Sprint drill-down (a different partial)
	// stays read-only, so editability never leaks in through a shared define.
	Editable    bool
	Type        string
	Href        string
	LatestAt    string
	OriginFrom  string // window-start status (shown when not CreatedHere)
	Moves       int
	Kind        string // "finished" | "advanced" | "pulled-back" | "" (created, unmoved)
	CreatedHere bool
}

// dailyColumnView is one column of the Daily board: its display name, its cards
// (already recency-sorted), and whether it is the Canceled column (rendered only
// when it holds a card, and styled distinctly).
type dailyColumnView struct {
	Name     string
	Canceled bool
	Cards    []dailyCardView
}

// dailyBoardOrder is the fixed left→right set of workflow columns the Daily board
// always renders, even empty (Done collapses the whole done set). The Canceled
// column is appended after these, only when it holds at least one card.
var dailyBoardOrder = []string{"Refinement", "Ready To Do", "In Progress", "Review / Testing", store.DailyColumnDone}

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

// dailyView is the model for the Daily board page and its panel fragment.
// HasSprint is false when no active sprint is known (drives the no-sprint empty
// state). Columns are the board's columns (the five workflow columns always
// present, plus Canceled when non-empty). On an invalid custom range Columns is
// nil and RangeError is set, so the template shows the inline error and no board.
type dailyView struct {
	SprintName string
	HasSprint  bool
	Assignees  []assigneeChip
	// Selected is the current assignee selection as the values to preserve across a
	// preset/range change (the hidden ?assignee= inputs). AnySelected enables the
	// Clear affordance; false is also the "zero selected = all" default.
	Selected    []string
	AnySelected bool
	Presets     []dailyPresetView
	CustomFrom  string
	CustomTo    string
	RangeError  string
	Columns     []dailyColumnView
}

// handleDaily renders the full standalone Daily page.
func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	s.renderDaily(w, r, "daily.html")
}

// handleDailyResults renders the complete Daily panel (the HTMX swap target):
// its single sticky chrome region plus results, so controls, headings and cards
// re-render together after a range or assignee change.
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
	// The selection is the set of repeated ?assignee= params (a display name or the
	// unassigned sentinel each). Zero selected means "all" — the default on a fresh
	// load and what Clear produces. Deduped, preserving first-seen order.
	selected := dedupeAssignees(q["assignee"])
	selectedSet := make(map[string]bool, len(selected))
	for _, v := range selected {
		selectedSet[v] = true
	}

	sprint, hasSprint, err := s.rollups.ActiveSprintWindow()
	if err != nil {
		return dailyView{}, err
	}
	view := dailyView{HasSprint: hasSprint, Selected: selected, AnySelected: len(selected) > 0}
	if hasSprint {
		view.SprintName = sprint.Name
	}
	rng := s.dailyRangeSelection(q, s.now())
	view.Presets = rng.presets
	view.CustomFrom = rng.customFrom
	view.CustomTo = rng.customTo
	view.RangeError = rng.errMsg

	assignees, err := s.rollups.ActiveSprintAssignees()
	if err != nil {
		return dailyView{}, err
	}
	// One chip per active-sprint assignee (alphabetical, from the store), then the
	// trailing Unassigned chip. Each chip's ToggleHref flips just that chip against
	// the current selection.
	for _, a := range assignees {
		view.Assignees = append(view.Assignees, assigneeChip{
			Value: a.Name, Name: a.Name, Assignee: a.Name,
			AvatarURL: a.AvatarURL, Initials: avatarInitials(a.Name),
			Selected:   selectedSet[a.Name],
			ToggleHref: assigneeToggleHref("/daily/results", selected, a.Name, selectedSet[a.Name]),
		})
	}
	view.Assignees = append(view.Assignees, assigneeChip{
		Value: dailyAssigneeUnassigned, Name: "Unassigned", Assignee: "",
		Selected:   selectedSet[dailyAssigneeUnassigned],
		ToggleHref: assigneeToggleHref("/daily/results", selected, dailyAssigneeUnassigned, selectedSet[dailyAssigneeUnassigned]),
	})

	// With no active sprint there is nothing to query; the template shows the
	// friendly no-sprint state.
	if !hasSprint {
		return view, nil
	}

	// An invalid custom range renders no results, no silent fallback: the inline
	// error is shown and the board stays empty (no columns at all).
	if rng.errMsg != "" {
		return view, nil
	}

	from, to := rng.from, rng.to
	cards, err := s.rollups.DailyBoard(dailyStoreAssignees(selected), from, to)
	if err != nil {
		return dailyView{}, err
	}
	view.Columns = s.dailyBoard(cards)
	return view, nil
}

// dedupeAssignees drops empty and duplicate values from the repeated ?assignee=
// params, preserving first-seen order so the selection (and the hidden inputs
// that round-trip it) stays stable across swaps.
func dedupeAssignees(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// dailyBoard groups the store's recency-sorted board cards into the fixed
// workflow columns (Refinement → Done, always rendered even empty), appending
// the Canceled column last only when it holds a card. Each store card is mapped
// to its display card, resolving the Jira link, the compact timestamp, the
// origin badge fields and the movement-kind colour.
func (s *Server) dailyBoard(cards []store.DailyBoardCard) []dailyColumnView {
	cols := make([]dailyColumnView, len(dailyBoardOrder))
	index := make(map[string]int, len(dailyBoardOrder))
	for i, name := range dailyBoardOrder {
		cols[i] = dailyColumnView{Name: name}
		index[name] = i
	}
	var canceled dailyColumnView
	canceled = dailyColumnView{Name: store.DailyColumnCanceled, Canceled: true}

	for _, c := range cards {
		card := dailyCardView{
			Key:         c.Key,
			Summary:     c.Summary,
			Assignee:    c.Assignee,
			AvatarURL:   c.AssigneeAvatarURL,
			Initials:    avatarInitials(c.Assignee),
			Size:        sizeDisplay(c.Size),
			RawSize:     c.Size,
			Editable:    true, // the Daily board is an editable estimate surface (#115)
			Type:        c.Type,
			Href:        s.jiraIssueURL(c.Key),
			LatestAt:    c.LatestActivity.In(s.loc).Format(dailyTimeFormat),
			Moves:       c.Moves,
			CreatedHere: c.CreatedInWindow,
		}
		if !c.CreatedInWindow {
			card.OriginFrom = statusDisplay(c.StartStatus)
		}
		// A created-but-unmoved card carries no movement kind (neutral highlight);
		// anything that moved is coloured by its net-movement bucket.
		if c.Moves > 0 {
			card.Kind = dailyMovementKind(c.Movement)
		}
		if c.Column == store.DailyColumnCanceled {
			canceled.Cards = append(canceled.Cards, card)
			continue
		}
		cols[index[c.Column]].Cards = append(cols[index[c.Column]].Cards, card)
	}
	if len(canceled.Cards) > 0 {
		cols = append(cols, canceled)
	}
	return cols
}

// dailyMovementKind maps a net-movement bucket to the CSS kind suffix the origin
// badge is coloured by (finished=emerald, advanced=sky, pulled-back=amber).
func dailyMovementKind(m store.DailyMovement) string {
	switch m {
	case store.MovementFinished:
		return "finished"
	case store.MovementPulledBack:
		return "pulled-back"
	default:
		return "advanced"
	}
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
		if selectedKey == dailyPresetLast24h {
			// Rolling window: relative to the current instant, never snapped to a
			// calendar day and never weekend-adjusted.
			res.to = now
			res.from = now.Add(-dailyLast24hWindow)
		} else {
			day := days[selectedKey]
			res.from = day
			res.to = day.AddDate(0, 0, 1)
		}
		res.customFrom = res.from.Format(dailyInputFormat)
		res.customTo = res.to.Format(dailyInputFormat)
	}

	// The Yesterday button reads "Yesterday" only when the preset resolves to the
	// actual calendar yesterday; when it walks back over a weekend (Mon/Sun) it
	// reads the full weekday name of the day it maps to (e.g. "Friday"), matching
	// how the day-before button is labelled. See docs/adr/0003 (amended by #95).
	yesterdayLabel := "Yesterday"
	if !yesterdayStart.Equal(todayStart.AddDate(0, 0, -1)) {
		yesterdayLabel = yesterdayStart.Format("Monday")
	}

	// Build the three preset buttons in chronological display order: the
	// weekday-named day-before button first, then Yesterday, then Today. The
	// day-before button is labelled with its full weekday name; each carries its
	// concrete date as a hover title.
	res.presets = []dailyPresetView{
		{
			Key: dailyPresetDayBefore, Label: dayBeforeStart.Format("Monday"),
			Title:    dayBeforeStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetDayBefore,
		},
		{
			Key: dailyPresetYesterday, Label: yesterdayLabel,
			Title:    yesterdayStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetYesterday,
		},
		{
			Key: dailyPresetToday, Label: "Today",
			Title:    todayStart.Format(dailyTitleFormat),
			Selected: selectedKey == dailyPresetToday,
			Disabled: todayDisabled,
		},
		// Rightmost: the rolling [now − 24h, now) preset. Never disabled and never
		// weekend-adjusted — it sits after Today but is not a calendar day.
		{
			Key: dailyPresetLast24h, Label: "Last 24h",
			Title:    "Rolling window: the last 24 hours up to now",
			Selected: selectedKey == dailyPresetLast24h,
		},
	}
	return res
}

// normalizeDailyPreset resolves a requested preset key to a concrete selection.
// The rolling Last 24h key passes through unchanged (never weekend-adjusted).
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
	case dailyPresetLast24h:
		return dailyPresetLast24h
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

// dailyStoreAssignees maps the selected chip values to the store's union filter:
// the unassigned sentinel becomes store.UnassignedAssignee, every other value is
// an exact name match, and an empty selection stays empty (all assignees).
func dailyStoreAssignees(selected []string) []string {
	if len(selected) == 0 {
		return nil
	}
	out := make([]string, len(selected))
	for i, v := range selected {
		if v == dailyAssigneeUnassigned {
			out[i] = store.UnassignedAssignee
		} else {
			out[i] = v
		}
	}
	return out
}

// statusDisplay renders a transition's source status, labelling a missing from
// (a first transition with no recorded source) as "(none)".
func statusDisplay(status string) string {
	if status == "" {
		return "(none)"
	}
	return status
}
