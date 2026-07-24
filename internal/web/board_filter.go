package web

import (
	"html/template"
	"net/url"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// Board filter scaffolding (#157).
//
// The Board's filter chrome, the /board/results fragment, the HTMX panel swap
// and the URL round-trip are all filter-AGNOSTIC: boardView ranges over a
// registry of boardFilter values, renders each filter's Control partial with its
// Data (via the "render" template func), hides any card failing a Keep predicate
// (columns are never removed), and re-emits every filter's Params as hidden
// round-trip inputs. Adding a filter (#158 no-estimate, #159 active-in-24h) is
// therefore purely additive: implement a constructor returning a boardFilter and
// append it to boardFilters, plus a Control partial. No route, handler, fragment
// or URL plumbing changes.

// boardFilterParam is one URL param a filter contributes to round-tripping: a
// name/value re-emitted as a hidden <input data-filterparam> inside the filter
// form, so a change to ANY control preserves every other filter's state. The
// [data-filterparam] marker lets a control hx-include the sibling filters
// generically (by attribute), never by hard-coding their param names.
type boardFilterParam struct {
	Name  string
	Value string
}

// boardFilter is one pluggable Board filter, fully resolved from the request
// query. See the package-level scaffolding note above.
type boardFilter struct {
	// Control is the name of the chrome partial that renders this filter's control;
	// Data is the view model handed to it (dispatched by the "render" func, since
	// html/template cannot take a dynamic {{template}} name).
	Control string
	Data    any
	// Params are the filter's current URL params, re-emitted as hidden inputs so a
	// change to another filter round-trips this one unchanged.
	Params []boardFilterParam
	// Keep reports whether a card survives this filter (true = shown). Filtering
	// only ever hides cards; the column set is fixed.
	Keep func(store.BoardCard) bool
}

// boardFilters is the ordered registry of Board filters. To add a filter, append
// its constructor here — the plumbing needs no other change. Each constructor may
// query the store, so the slice is built with an error.
func (s *Server) boardFilters(q url.Values) ([]boardFilter, error) {
	assignee, err := s.assigneeBoardFilter(q)
	if err != nil {
		return nil, err
	}
	noEstimate, err := s.noEstimateBoardFilter(q)
	if err != nil {
		return nil, err
	}
	active24h, err := s.active24hBoardFilter(q)
	if err != nil {
		return nil, err
	}
	return []boardFilter{
		assignee,
		noEstimate,
		active24h,
	}, nil
}

// filterIncludeExceptSelf builds the hx-include attribute a self-encoding control
// carries so a swap preserves EVERY other filter but not its own param: the
// control already encodes its own resulting state in its toggle URL, so
// re-including its own hidden inputs would double-count. Selecting
// [data-filterparam] except the control's own param does this generically (by
// attribute), so a newly added filter is preserved without touching this control.
func filterIncludeExceptSelf(param string) template.HTMLAttr {
	return template.HTMLAttr(`hx-include="[data-filterparam]:not([name='` + param + `'])"`)
}

// keepCard reports whether a card survives ALL filters (logical AND), so
// composing filters simply intersects their predicates.
func keepCard(filters []boardFilter, card store.BoardCard) bool {
	for _, f := range filters {
		if f.Keep != nil && !f.Keep(card) {
			return false
		}
	}
	return true
}

// assigneeChip is one toggle in an assignee avatar bar: a named active-sprint
// assignee or the trailing Unassigned sentinel. It is the shared shape both the
// Daily and Board bars render (see the "assignee-bar" partial). Value is the
// ?assignee= param it carries; Assignee/AvatarURL/Initials feed the shared
// card-avatar partial (the Unassigned chip leaves Assignee empty for the neutral
// circle); Selected marks it pressed; ToggleHref is the results URL that flips
// just this chip while preserving the rest of the selection.
type assigneeChip struct {
	Value      string
	Name       string
	Assignee   string
	AvatarURL  string
	Initials   string
	Selected   bool
	ToggleHref string
}

// assigneeBarView is the model for the shared "assignee-bar" partial. Prefix is
// the data-testid stem ("daily-assignee" / "board-assignee"); ClearHref is the
// bare results path Clear points at; IncludeAttr is the full hx-include attribute
// the chips and Clear carry to preserve the OTHER controls' state on a swap.
type assigneeBarView struct {
	Prefix      string
	Chips       []assigneeChip
	AnySelected bool
	ClearHref   string
	IncludeAttr template.HTMLAttr
}

// assigneeBoardFilter builds the Board's assignee filter from the query: the
// multi-select avatar bar (identical control/semantics to Daily), a Keep
// predicate that hides cards whose current assignee is not in the selection, and
// the repeated ?assignee= params for round-tripping. A fresh load (no assignee
// param) selects nothing, which means "all assignees" — every card shows.
func (s *Server) assigneeBoardFilter(q url.Values) (boardFilter, error) {
	// The selection is the deduped set of repeated ?assignee= params (a display
	// name or the Unassigned sentinel each). Zero selected = all (the default).
	selected := dedupeAssignees(q["assignee"])
	selectedSet := make(map[string]bool, len(selected))
	for _, v := range selected {
		selectedSet[v] = true
	}

	assignees, err := s.rollups.ActiveSprintAssignees()
	if err != nil {
		return boardFilter{}, err
	}

	bar := assigneeBarView{
		Prefix:      "board-assignee",
		AnySelected: len(selected) > 0,
		ClearHref:   "/board/results",
		// The chips already encode the full resulting ?assignee= set in their own
		// URL, so they must NOT re-include the sibling assignee hidden inputs (which
		// would double-count) — but they must preserve EVERY other filter.
		IncludeAttr: filterIncludeExceptSelf("assignee"),
	}
	// One chip per active-sprint assignee (alphabetical, from the store), then the
	// trailing Unassigned sentinel chip. Each ToggleHref flips just that chip.
	for _, a := range assignees {
		bar.Chips = append(bar.Chips, assigneeChip{
			Value: a.Name, Name: a.Name, Assignee: a.Name,
			AvatarURL: a.AvatarURL, Initials: avatarInitials(a.Name),
			Selected:   selectedSet[a.Name],
			ToggleHref: assigneeToggleHref("/board/results", selected, a.Name, selectedSet[a.Name]),
		})
	}
	bar.Chips = append(bar.Chips, assigneeChip{
		Value: dailyAssigneeUnassigned, Name: "Unassigned", Assignee: "",
		Selected:   selectedSet[dailyAssigneeUnassigned],
		ToggleHref: assigneeToggleHref("/board/results", selected, dailyAssigneeUnassigned, selectedSet[dailyAssigneeUnassigned]),
	})

	params := make([]boardFilterParam, 0, len(selected))
	for _, v := range selected {
		params = append(params, boardFilterParam{Name: "assignee", Value: v})
	}

	keep := func(card store.BoardCard) bool {
		if len(selectedSet) == 0 {
			return true // no selection = all assignees
		}
		if card.Assignee == "" {
			return selectedSet[dailyAssigneeUnassigned]
		}
		return selectedSet[card.Assignee]
	}

	return boardFilter{Control: "assignee-bar", Data: bar, Params: params, Keep: keep}, nil
}

// noEstimateParam is the URL/query key carrying the no-estimate toggle state.
// The toggle is on iff this param equals noEstimateOn.
const (
	noEstimateParam = "no-estimate"
	noEstimateOn    = "1"
)

// noEstimateToggleView is the model for the compact "No estimates" toggle
// control (#158): a single server-driven toggle that lenses the Board onto
// unsized cards (a data-quality view for finding unestimated tickets). On is the
// current state; ToggleHref is the results URL that flips it; IncludeAttr
// preserves every OTHER filter on the swap (it replaces only its own param, like
// the assignee bar). Prefix is the data-testid stem.
type noEstimateToggleView struct {
	Prefix      string
	On          bool
	ToggleHref  string
	IncludeAttr template.HTMLAttr
}

// noEstimateBoardFilter builds the Board's no-estimate toggle from the query: the
// compact control, a Keep predicate that (when on) hides any card carrying an
// estimate, and the round-trip param. Default off (no param) shows every card, so
// it composes with the assignee filter as a plain intersection via keepCard.
func (s *Server) noEstimateBoardFilter(q url.Values) (boardFilter, error) {
	on := q.Get(noEstimateParam) == noEstimateOn

	toggle := noEstimateToggleView{
		Prefix:     "board-no-estimate",
		On:         on,
		ToggleHref: noEstimateToggleHref("/board/results", on),
		// The toggle encodes its own resulting state in ToggleHref, so it must NOT
		// re-include its own hidden param (which would fight the href), but it MUST
		// preserve every other filter.
		IncludeAttr: filterIncludeExceptSelf(noEstimateParam),
	}

	var params []boardFilterParam
	if on {
		params = append(params, boardFilterParam{Name: noEstimateParam, Value: noEstimateOn})
	}

	keep := func(card store.BoardCard) bool {
		if !on {
			return true // off = show all cards
		}
		return card.Size == "" // on = only cards with no estimate
	}

	return boardFilter{Control: "no-estimate-toggle", Data: toggle, Params: params, Keep: keep}, nil
}

// noEstimateToggleHref returns the results URL (rooted at basePath) that flips the
// toggle: turning it on adds ?no-estimate=1, turning it off drops back to the bare
// path. Only its own param is encoded; other filters ride along via hx-include.
func noEstimateToggleHref(basePath string, on bool) string {
	if on {
		return basePath // toggling an on filter off yields the bare path
	}
	return basePath + "?" + noEstimateParam + "=" + noEstimateOn
}

// active24hParam is the URL/query key carrying the active-in-24h toggle state.
// The toggle is on iff this param equals active24hOn.
const (
	active24hParam  = "active-24h"
	active24hOn     = "1"
	active24hWindow = 24 * time.Hour
)

// active24hToggleView is the model for the compact "Active in last 24h" toggle
// control (#159): a single server-driven toggle that lenses the Board onto cards
// active in the rolling [now − 24h, now) window. On is the current state;
// ToggleHref is the results URL that flips it; IncludeAttr preserves every OTHER
// filter on the swap (it replaces only its own param, like the assignee and
// no-estimate controls). Prefix is the data-testid stem.
type active24hToggleView struct {
	Prefix      string
	On          bool
	ToggleHref  string
	IncludeAttr template.HTMLAttr
}

// active24hBoardFilter builds the Board's active-in-24h toggle from the query: the
// compact control, a Keep predicate that (when on) hides any card whose
// latest-activity instant falls outside the rolling [now − 24h, now) window, and
// the round-trip param. Default off (no param) shows every card, so it composes
// with the assignee and no-estimate filters as a plain intersection via keepCard.
//
// "Active" reuses the Daily rule end to end: each card's LatestActivity is already
// the latest non-intra-Done status change (or its creation instant) computed by
// the shared store primitive, so a card whose only 24h activity was intra-Done
// housekeeping has an earlier LatestActivity and is hidden, and a card created in
// the window shows. Because the window always ends at now, "active in the window"
// is exactly "LatestActivity in [now − 24h, now)". now is the server clock (s.now)
// so a pinned test clock makes the window deterministic.
func (s *Server) active24hBoardFilter(q url.Values) (boardFilter, error) {
	on := q.Get(active24hParam) == active24hOn

	toggle := active24hToggleView{
		Prefix:     "board-active-24h",
		On:         on,
		ToggleHref: active24hToggleHref("/board/results", on),
		// The toggle encodes its own resulting state in ToggleHref, so it must NOT
		// re-include its own hidden param (which would fight the href), but it MUST
		// preserve every other filter.
		IncludeAttr: filterIncludeExceptSelf(active24hParam),
	}

	var params []boardFilterParam
	if on {
		params = append(params, boardFilterParam{Name: active24hParam, Value: active24hOn})
	}

	now := s.now()
	from := now.Add(-active24hWindow)
	keep := func(card store.BoardCard) bool {
		if !on {
			return true // off = show all cards
		}
		// Active iff the card's latest activity is in the rolling [now − 24h, now)
		// window. A zero instant (no status change and no creation instant) is never
		// active. The upper bound is half-open to mirror every other window in the app.
		a := card.LatestActivity
		return !a.IsZero() && !a.Before(from) && a.Before(now)
	}

	return boardFilter{Control: "active-24h-toggle", Data: toggle, Params: params, Keep: keep}, nil
}

// active24hToggleHref returns the results URL (rooted at basePath) that flips the
// toggle: turning it on adds ?active-24h=1, turning it off drops back to the bare
// path. Only its own param is encoded; other filters ride along via hx-include.
func active24hToggleHref(basePath string, on bool) string {
	if on {
		return basePath // toggling an on filter off yields the bare path
	}
	return basePath + "?" + active24hParam + "=" + active24hOn
}

// assigneeToggleHref builds the results URL (rooted at basePath) that flips one
// chip against the current selection: removing it when already selected, adding
// it otherwise. Only the resulting ?assignee= set is encoded (other filters ride
// along via hx-include); deselecting the last chip yields the bare path ("all").
// dailyToggleHref delegates here so both bars share one toggle rule.
func assigneeToggleHref(basePath string, current []string, value string, selected bool) string {
	var next []string
	if selected {
		for _, v := range current {
			if v != value {
				next = append(next, v)
			}
		}
	} else {
		next = append(append(next, current...), value)
	}
	if len(next) == 0 {
		return basePath
	}
	vals := url.Values{}
	for _, v := range next {
		vals.Add("assignee", v)
	}
	return basePath + "?" + vals.Encode()
}
