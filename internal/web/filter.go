package web

// Filter scaffolding shared by the Board (#157, board_filter.go) and the Prio
// view (#202, prio_filter.go). Both registries encode filter state in the URL,
// re-emit every filter's params as hidden [data-filterparam] inputs, and swap
// their panel fragment over HTMX; only the row type each filters over differs.
// This file owns the pieces that are genuinely common: the round-trip param,
// the hx-include selectors, and the two-state toggle control (view model + its
// href rule), plus the single-choice select control (#214). Their partials are
// "filter-toggle" and "filter-select".

import "html/template"

// filterParam is one URL param a filter contributes to round-tripping: a
// name/value re-emitted as a hidden <input data-filterparam> inside the filter
// form, so a change to ANY control preserves every other filter's state. The
// [data-filterparam] marker lets a control hx-include the sibling filters
// generically (by attribute), never by hard-coding their param names.
type filterParam struct {
	Name  string
	Value string
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

// filterIncludeAll includes EVERY filter param, the caller's own included. The
// toggle filters cannot use it — their href already encodes the state that
// results from flipping them, so re-sending their current param would fight the
// href (hence filterIncludeExceptSelf). A text box is the opposite case: its
// value lives in the control itself, so the request must carry it (#193).
const filterIncludeAll template.HTMLAttr = `hx-include="[data-filterparam]"`

// filterToggleView is the model of a two-state filter pill (the "filter-toggle"
// partial): a server-driven control that hx-GETs ToggleHref — the results URL
// encoding the state that RESULTS from flipping it — and swaps the caller's
// panel, so the control re-renders pressed or not. On is the current state;
// IncludeAttr preserves every OTHER filter on the swap (it replaces only its own
// param). Prefix is the data-testid stem, Label the visible text.
type filterToggleView struct {
	Prefix      string
	Label       string
	On          bool
	ToggleHref  string
	IncludeAttr template.HTMLAttr
}

// filterSelectView is the model of a single-choice filter <select> (the
// "filter-select" partial, #214): a server-driven control that hx-GETs
// ResultsHref on change and swaps the caller's panel. Exactly one Option is
// Selected. Prefix is the data-testid stem, Label the visible text, Param the
// query key it submits under.
//
// It is the third control shape here, and sits between the other two on where its
// value lives. A pill encodes the state it flips TO in its href. The Board's
// search box carries its value in the input AND marks itself data-filterparam, so
// the term is round-tripped by the control itself. The select is the element
// issuing the request, so the value rides along automatically — but it is NOT
// marked data-filterparam, because a select always has a value and so would make
// every sibling's request carry the default. Dropping the default is a decision
// only the server can take, so the owning filter re-emits a non-default value as
// a hidden param instead. Hence IncludeAttr is the "all but self" selector, as
// for a pill, and not filterIncludeAll.
type filterSelectView struct {
	Prefix      string
	Label       string
	Param       string
	Options     []filterSelectOption
	ResultsHref string
	IncludeAttr template.HTMLAttr
}

// filterSelectOption is one choice of a filterSelectView: the URL value, the
// visible label, and whether it is the current one.
type filterSelectOption struct {
	Value    string
	Label    string
	Selected bool
}

// toggleHref returns the results URL (rooted at basePath) that flips a two-state
// toggle. A toggle's DEFAULT state is the bare path; only the non-default state
// is encoded, so URLs stay short and a bare /board or /prio means "every filter
// at its default". Pass encode = whether the state the flip lands in is the
// non-default one: for a default-off filter that is !on (flipping off→on must
// encode), for a default-on filter it is on (flipping on→off must).
// Only the toggle's own param is encoded; other filters ride along via
// hx-include.
func toggleHref(basePath, param, value string, encode bool) string {
	if encode {
		return basePath + "?" + param + "=" + value
	}
	return basePath
}
