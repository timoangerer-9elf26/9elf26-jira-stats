package web

// The Prio view's filter scaffolding (#196): the Board's (board_filter.go)
// applied to Prio rows — one URL-encoded, fragment-swapping registry whose
// controls render into the "prio-filters" chrome and whose Keep funcs narrow the
// whole-project projection. Adding a filter is purely additive: write a
// constructor returning a prioFilter and slot it into prioFilters. No route,
// handler, chrome or URL plumbing changes.
//
// It is a sibling of the Board registry rather than a generalisation of it
// because the two filter over different row types (store.BoardCard vs
// store.PrioIssue). The genuinely common pieces — the round-trip param, the
// hx-include selectors and the two-state toggle control — live in filter.go and
// are shared with the Board.

import (
	"net/url"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// prioFilter is one pluggable Prio filter, fully resolved from the request query.
type prioFilter struct {
	// Control is the name of the chrome partial rendering this filter's control;
	// Data is the view model handed to it (dispatched by the "render" func, since
	// html/template cannot take a dynamic {{template}} name).
	Control string
	Data    any
	// Params are the filter's current URL params, re-emitted as hidden inputs so a
	// change to another filter round-trips this one unchanged. A filter whose
	// current state is its default contributes none, keeping default URLs bare.
	Params []filterParam
	// Keep reports whether an issue survives this filter (true = shown).
	Keep func(store.PrioIssue) bool
}

// prioFilters is the ordered registry of Prio filters; registry order is the
// controls' left-to-right order in the chrome. The spec (#196) wants the toggles
// stacked "Non-Technical, then Not done", so the Non-Technical filter (#203)
// goes FIRST in this slice — prepended, not appended.
func prioFilters(q url.Values) []prioFilter {
	return []prioFilter{
		notDonePrioFilter(q),
	}
}

// keepPrioIssue applies the whole registry: an issue shows only if every filter
// keeps it.
func keepPrioIssue(filters []prioFilter, issue store.PrioIssue) bool {
	for _, f := range filters {
		if f.Keep != nil && !f.Keep(issue) {
			return false
		}
	}
	return true
}

const (
	notDoneParam = "not-done"
	notDoneOff   = "0"
)

// notDoneStatuses is the Not-done keep set (CONTEXT.md → Prio filters). It
// deliberately includes Triage — pre-sprint work the Board keeps off-board is
// exactly what a prioritiser needs to see — and excludes the Done set (DONE
// (This Sprint), Ready for Release, Released / Deployed) and Canceled.
// Matching is case-insensitive, like every other status-bucket test in the app
// (store.normalizeStatus), so a Jira casing quirk — "Ready to Do" for "Ready To
// Do" — cannot silently drop a ticket out of the not-done set.
var notDoneStatuses = func() map[string]bool {
	statuses := []string{"Triage", "Refinement", "Ready To Do", "In Progress", "Review / Testing"}
	set := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		set[strings.ToLower(status)] = true
	}
	return set
}()

// notDonePrioFilter is the Not-done toggle (#202), the one filter in the app
// that defaults ON: Prio's universe is ~1,400 rows dominated by released work,
// so a bare /prio opens on the ~89 not-done tickets. Because ON is the default,
// it is the OFF state that the URL encodes (not-done=0) — a bare URL means on,
// and the explicit not-done=1 an older link may carry reads as on too.
func notDonePrioFilter(q url.Values) prioFilter {
	on := q.Get(notDoneParam) != notDoneOff

	toggle := filterToggleView{
		Prefix: "prio-not-done",
		Label:  "Not done",
		On:     on,
		// Not-done defaults ON, so it is the OFF state the href must encode.
		ToggleHref:  toggleHref("/prio/results", notDoneParam, notDoneOff, on),
		IncludeAttr: filterIncludeExceptSelf(notDoneParam),
	}

	var params []filterParam
	if !on {
		params = append(params, filterParam{Name: notDoneParam, Value: notDoneOff})
	}

	keep := func(issue store.PrioIssue) bool {
		if !on {
			return true // off = every status, done and canceled included
		}
		return notDoneStatuses[strings.ToLower(issue.Status)]
	}

	return prioFilter{Control: "filter-toggle", Data: toggle, Params: params, Keep: keep}
}
