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
	"slices"
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
// controls' left-to-right order in the chrome, which since #209 reads against the
// bar's RIGHT edge: "Non-Technical, Not done, Not started, No parent".
// Non-Technical (#203) goes FIRST in this slice — prepended, not appended — and
// new filters append, as No parent (#210) did.
//
// Every filter here currently defaults ON and so encodes only its OFF state
// (`<param>=0`), No parent included since #213. That uniformity is a coincidence
// of the current set, not a rule: a filter whose ON state is the unusual one is
// an ordinary default-OFF filter taking the `<param>=1`-means-on encoding, and
// must not copy the inverted form from its neighbours.
func prioFilters(q url.Values) []prioFilter {
	return []prioFilter{
		nonTechnicalPrioFilter(q),
		notDonePrioFilter(q),
		notStartedPrioFilter(q),
		noParentPrioFilter(q),
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

// notDonePrioFilter is the Not-done toggle (#202): status is in Triage,
// Refinement, Ready To Do, In Progress or Review / Testing. It defaults ON
// because Prio's universe is ~1,400 rows dominated by released work, so a bare
// /prio must not open on them. Because ON is the default, it is the OFF state
// that the URL encodes (not-done=0) — a bare URL means on, and the explicit
// not-done=1 an older link may carry reads as on too.
//
// Since #209 it is a no-op at the defaults: Not-started is a strict subset of
// this set and also defaults ON. That is deliberate, not redundancy to "fix" —
// Not-started off + Not-done on is the second gear, all open work with the
// released rows still hidden.
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

// nonTechnicalParam is the URL/query key carrying the Non-Technical toggle state.
// Since #209 it defaults ON and so encodes the OFF state (non-technical=0),
// exactly like not-done: the toggle is on iff the param is anything but "0", which
// means the explicit non-technical=1 an older bookmark may carry still reads as on.
//
// technicalLabel is the canonical stored Jira label, matched exactly (capital T,
// whole label) — "non-technical" is the ABSENCE of it, not the presence of the
// sibling `Product` label (ADR 0009).
const (
	nonTechnicalParam = "non-technical"
	nonTechnicalOff   = "0"
	technicalLabel    = "Technical"
)

// nonTechnicalPrioFilter hides tickets carrying the exact `Technical` label.
// It is default ON (#209) and so uses the INVERTED encoding Not-done uses: only
// the off state is encoded, as `non-technical=0`; any other value — including
// the `non-technical=1` that the default-OFF version (#203) used to emit —
// leaves it on, so old bookmarks still resolve to a lit toggle.
func nonTechnicalPrioFilter(q url.Values) prioFilter {
	on := q.Get(nonTechnicalParam) != nonTechnicalOff

	toggle := filterToggleView{
		Prefix:      "prio-non-technical",
		Label:       "Non-Technical",
		On:          on,
		ToggleHref:  toggleHref("/prio/results", nonTechnicalParam, nonTechnicalOff, on),
		IncludeAttr: filterIncludeExceptSelf(nonTechnicalParam),
	}

	var params []filterParam
	if !on {
		params = append(params, filterParam{Name: nonTechnicalParam, Value: nonTechnicalOff})
	}

	keep := func(issue store.PrioIssue) bool {
		if !on {
			return true // off = every issue, technical included
		}
		return !slices.Contains(issue.Labels, technicalLabel)
	}

	return prioFilter{Control: "filter-toggle", Data: toggle, Params: params, Keep: keep}
}

const (
	notStartedParam = "not-started"
	notStartedOff   = "0"
)

// notStartedStatuses is the not-yet-started slice of the workflow: a strict
// subset of notDoneStatuses, stopping short of In Progress. Matched
// case-insensitively, like every other status bucket in the app.
var notStartedStatuses = func() map[string]bool {
	statuses := []string{"Triage", "Refinement", "Ready To Do"}
	set := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		set[strings.ToLower(status)] = true
	}
	return set
}()

// notStartedPrioFilter narrows to work nobody has picked up yet (#209). It is a
// plain independent toggle ANDed with the rest, deliberately NOT merged with
// Not-done into a three-state status control. The consequence is accepted: at
// the defaults (both on) Not-done is a no-op, since Not-started is a strict
// subset of it. Not-done earns its keep as the second gear — Not-started off +
// Not-done on = all open work, with the Released / Deployed bulk still hidden.
func notStartedPrioFilter(q url.Values) prioFilter {
	on := q.Get(notStartedParam) != notStartedOff

	toggle := filterToggleView{
		Prefix:      "prio-not-started",
		Label:       "Not started",
		On:          on,
		ToggleHref:  toggleHref("/prio/results", notStartedParam, notStartedOff, on),
		IncludeAttr: filterIncludeExceptSelf(notStartedParam),
	}

	var params []filterParam
	if !on {
		params = append(params, filterParam{Name: notStartedParam, Value: notStartedOff})
	}

	keep := func(issue store.PrioIssue) bool {
		if !on {
			return true // off = contributes nothing
		}
		return notStartedStatuses[strings.ToLower(issue.Status)]
	}

	return prioFilter{Control: "filter-toggle", Data: toggle, Params: params, Keep: keep}
}

// noParentParam is the URL/query key carrying the No-parent toggle state. Like
// the three filters above it defaults ON (#213), so only its OFF state is in the
// URL, as `no-parent=0`; any other value — including the `no-parent=1` old
// bookmarks carry — leaves it on.
const (
	noParentParam = "no-parent"
	noParentOff   = "0"
)

// noParentPrioFilter narrows to the top of the issue tree: tickets with no parent
// at all (#210). In DCAI that is every Epic plus the unparented Tasks, Bugs and
// Stories nobody has filed under one.
//
// The rule is LITERALLY "parent key is empty" — deliberately NOT `Type == "Epic"
// || no parent`. The two select the same rows today (no DCAI Epic currently has a
// parent), so the Epic clause would buy nothing now and would lie later: the day
// an Epic is parented under an Initiative it is genuinely no longer
// top-of-tree and should drop out of this filter.
//
// It defaults ON (#213): parented tickets are already prioritised by whatever
// they hang under, so the default slice is the top of the tree — the work nothing
// else is ordering. Off widens back out to the parented majority of the project.
func noParentPrioFilter(q url.Values) prioFilter {
	on := q.Get(noParentParam) != noParentOff

	toggle := filterToggleView{
		Prefix: "prio-no-parent",
		Label:  "No parent",
		On:     on,
		// Default ON, so it is the OFF state the href encodes.
		ToggleHref:  toggleHref("/prio/results", noParentParam, noParentOff, on),
		IncludeAttr: filterIncludeExceptSelf(noParentParam),
	}

	var params []filterParam
	if !on {
		params = append(params, filterParam{Name: noParentParam, Value: noParentOff})
	}

	keep := func(issue store.PrioIssue) bool {
		if !on {
			return true // off = every issue, parented work included
		}
		return issue.ParentKey == ""
	}

	return prioFilter{Control: "filter-toggle", Data: toggle, Params: params, Keep: keep}
}
