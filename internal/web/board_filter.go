package web

import (
	"html/template"
	"net/url"

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
	return []boardFilter{
		assignee,
		// #158: s.noEstimateBoardFilter(q)
		// #159: s.active24hBoardFilter(q)
	}, nil
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
		// would double-count) — but they must preserve EVERY other filter. Selecting
		// all filter params except assignee does exactly that, generically, so a new
		// filter is picked up without touching this control.
		IncludeAttr: template.HTMLAttr(`hx-include="[data-filterparam]:not([name='assignee'])"`),
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
