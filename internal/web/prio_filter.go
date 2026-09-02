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
// bar's RIGHT edge: "Status, Non-Technical, No parent". The status select (#214)
// goes FIRST in this slice — it is the bar's only non-pill control, and the one
// that decides which slice of the workflow the table is about.
//
// The registry is control-agnostic: a filter names the chrome partial rendering
// it, so the select ("filter-select") sits in the same list as the pills
// ("filter-toggle") with no special-casing in the chrome or the handler.
//
// The two pills default ON and so encode only their OFF state (`<param>=0`), No
// parent included since #213. That is not a rule for the registry: a filter
// whose ON state is the unusual one is an ordinary default-OFF filter taking the
// `<param>=1`-means-on encoding, and must not copy the inverted form from its
// neighbours. The status select follows the same spirit in its own idiom — only
// its non-default categories reach the URL.
func prioFilters(q url.Values) []prioFilter {
	return []prioFilter{
		statusPrioFilter(q),
		nonTechnicalPrioFilter(q),
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

// The status select (#214): the Prio view's status control is one <select> over
// four categories, not a set of overlapping toggles. It replaced the Not done and
// Not started pills, whose overlap (Not started was a strict subset of Not done)
// meant no combination could express "only work in flight" or "only finished
// work" — see docs/adr/0011, which supersedes that part of ADR 0009.
//
// The categories are an EXPLICIT status set here in our code, deliberately NOT
// derived from Jira's status_category: live Jira files Canceled under category
// "Done" and Triage under "To Do", so a category-derived map would sweep Canceled
// into Done and be wrong (CONTEXT.md → Ticket status buckets).
//
// This is a second, Prio-LOCAL partition. It is not the project-wide sprint
// buckets (Triage / Open ticket / Finished / Canceled) and must not be conflated
// with them: here Triage sits inside Planned, because on a prioritisation surface
// an untriaged ticket is exactly the unprioritised work you want to see, while
// the sprint rollups rightly treat Triage as pre-sprint and exclude it.
const (
	statusParam = "status"
	// statusPlanned is the default category, so it is the one value the URL never
	// carries: a bare /prio means Planned, and so does any unrecognised value.
	statusPlanned = "planned"
)

// prioStatusCategory is one option of the status select: the URL value, the label
// on the option, and the statuses it keeps. A nil Statuses means "every status",
// which is what makes All the only category Canceled surfaces under.
type prioStatusCategory struct {
	Value    string
	Label    string
	Statuses []string
}

// prioStatusCategories is the category map, in the select's option order. Note
// Ready for Release is a DONE state despite the name (CONTEXT.md), and Canceled
// belongs to no category at all — it appears only under All.
var prioStatusCategories = []prioStatusCategory{
	{statusPlanned, "Planned", []string{"Triage", "Refinement", "Ready To Do"}},
	{"doing", "Doing", []string{"In Progress", "Review / Testing"}},
	{"done", "Done", []string{"DONE (This Sprint)", "Ready for Release", "Released / Deployed"}},
	{"all", "All", nil},
}

// prioStatusSets is each category's keep set, lower-cased. Matching is
// case-insensitive like every other status bucket in the app
// (store.normalizeStatus), so a Jira casing quirk — "Ready to Do" for "Ready To
// Do" — cannot silently drop a ticket out of its category. A nil set is All.
var prioStatusSets = func() map[string]map[string]bool {
	sets := make(map[string]map[string]bool, len(prioStatusCategories))
	for _, category := range prioStatusCategories {
		if category.Statuses == nil {
			sets[category.Value] = nil
			continue
		}
		set := make(map[string]bool, len(category.Statuses))
		for _, status := range category.Statuses {
			set[strings.ToLower(status)] = true
		}
		sets[category.Value] = set
	}
	return sets
}()

// prioStatusCategoryFor resolves a URL value to a category, falling back to
// Planned for anything unrecognised — including the retired not-done/not-started
// params an old bookmark may still carry, which are simply inert now.
func prioStatusCategoryFor(value string) prioStatusCategory {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, category := range prioStatusCategories {
		if category.Value == value {
			return category
		}
	}
	return prioStatusCategories[0]
}

// statusPrioFilter narrows the table to one category of the workflow (#214).
//
// It round-trips like the pills but in its own idiom: the <select> carries
// name="status" and is the element issuing the request, so its value rides along
// automatically and it hx-includes only the OTHER filters. It is therefore NOT
// itself marked data-filterparam; instead a non-default category is re-emitted as
// a hidden param, so toggling a pill preserves the chosen category.
func statusPrioFilter(q url.Values) prioFilter {
	selected := prioStatusCategoryFor(q.Get(statusParam))

	options := make([]filterSelectOption, 0, len(prioStatusCategories))
	for _, category := range prioStatusCategories {
		options = append(options, filterSelectOption{
			Value:    category.Value,
			Label:    category.Label,
			Selected: category.Value == selected.Value,
		})
	}
	view := filterSelectView{
		Prefix:      "prio-status",
		Label:       "Status",
		Param:       statusParam,
		Options:     options,
		ResultsHref: "/prio/results",
		IncludeAttr: filterIncludeExceptSelf(statusParam),
	}

	var params []filterParam
	if selected.Value != statusPlanned {
		params = append(params, filterParam{Name: statusParam, Value: selected.Value})
	}

	keep := func(issue store.PrioIssue) bool {
		set := prioStatusSets[selected.Value]
		if set == nil {
			return true // All: every status, Canceled included
		}
		return set[strings.ToLower(issue.Status)]
	}

	return prioFilter{Control: "filter-select", Data: view, Params: params, Keep: keep}
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
