package web_test

// Integration tests for the Prio view's filter registry over the HTTP seam.
//
// Every control opens on the narrowed, prioritisable slice — Planned,
// non-technical, top-of-tree work — rather than the raw ~1,400-row project. A
// test that wants a wider universe must say so in the URL; prioEveryFilterOff is
// that URL suffix. The two pills default ON and encode only their off state
// (`<param>=0`); the status select (#214) defaults to Planned and encodes only
// the other three categories (`status=doing|done|all`).

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// prioStatusAndLabelFiltersOff widens the status select to All and turns the
// label filter off, but leaves No parent at its default, for cases where the
// parent rule is the one under test.
const prioStatusAndLabelFiltersOff = "status=all&non-technical=0"

// prioEveryFilterOff widens every Prio filter, i.e. "show me the whole project".
const prioEveryFilterOff = prioStatusAndLabelFiltersOff + "&no-parent=0"

func prioStatusFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Bug", Summary: "Triaged", Status: "Triage", StatusCategory: "To Do", Priority: "Highest"},
		{Key: "DCAI-2", Type: "Story", Summary: "Refined", Status: "Refinement", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-3", Type: "Task", Summary: "Queued", Status: "Ready To Do", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-4", Type: "Task", Summary: "Underway", Status: "In Progress", StatusCategory: "In Progress", Priority: "Medium"},
		{Key: "DCAI-5", Type: "Task", Summary: "In review", Status: "Review / Testing", StatusCategory: "In Progress", Priority: "Low"},
		{Key: "DCAI-6", Type: "Task", Summary: "Done this sprint", Status: "DONE (This Sprint)", StatusCategory: "Done", Priority: "Medium"},
		{Key: "DCAI-7", Type: "Task", Summary: "Awaiting release", Status: "Ready for Release", StatusCategory: "Done", Priority: "Medium"},
		{Key: "DCAI-8", Type: "Story", Summary: "Shipped", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Lowest"},
		{Key: "DCAI-9", Type: "Task", Summary: "Abandoned", Status: "Canceled", StatusCategory: "Done", Priority: "Low"},
	}}
}

// The status fixture's keys grouped by the Prio-local status categories (#214).
// Canceled (DCAI-9) belongs to no category and surfaces only under All — note
// that live Jira files it in status_category "Done", which is exactly why the
// categories are explicit status sets in our code rather than derived from it.
var (
	prioPlannedKeys  = []string{"DCAI-1", "DCAI-2", "DCAI-3"}
	prioDoingKeys    = []string{"DCAI-4", "DCAI-5"}
	prioDoneKeys     = []string{"DCAI-6", "DCAI-7", "DCAI-8"}
	prioCanceledKeys = []string{"DCAI-9"}
)

func prioAllKeys() []string {
	all := append([]string{}, prioPlannedKeys...)
	all = append(all, prioDoingKeys...)
	all = append(all, prioDoneKeys...)
	return append(all, prioCanceledKeys...)
}

// prioKeysExcept is every status-fixture key bar the groups given — the "absent"
// side of a category assertion, so a row that quietly joins two categories fails.
func prioKeysExcept(present ...[]string) []string {
	keep := map[string]bool{}
	for _, group := range present {
		for _, key := range group {
			keep[key] = true
		}
	}
	var absent []string
	for _, key := range prioAllKeys() {
		if !keep[key] {
			absent = append(absent, key)
		}
	}
	return absent
}

func assertPrioRows(t *testing.T, body string, want, absent []string) {
	t.Helper()
	for _, key := range want {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("prio missing row %s\n%s", key, body)
		}
	}
	for _, key := range absent {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("prio should have hidden row %s\n%s", key, body)
		}
	}
}

// assertToggle checks one toggle's rendered state: pressed or not, and the URL
// it flips to. Reading the toggle's own opening tag keeps the assertion from
// accidentally matching a sibling toggle's identical href.
func assertToggle(t *testing.T, body, prefix string, wantOn bool, wantHref string) {
	t.Helper()
	tag := openingTag(body, `data-testid="`+prefix+`-toggle"`)
	if tag == "" {
		t.Fatalf("no %s toggle rendered\n%s", prefix, body)
	}
	if got := strings.Contains(tag, `aria-pressed="true"`); got != wantOn {
		t.Errorf("%s pressed=%v, want %v: %s", prefix, got, wantOn, tag)
	}
	if !strings.Contains(tag, `hx-get="`+wantHref+`"`) {
		t.Errorf("%s should flip to %q: %s", prefix, wantHref, tag)
	}
}

// assertStatusSelect checks the status select renders with exactly the chosen
// category marked selected — a native <select>, so the selection is an attribute
// on one <option>, not aria-pressed on the control.
func assertStatusSelect(t *testing.T, body, want string) {
	t.Helper()
	tag := openingTag(body, `data-testid="prio-status-select"`)
	if tag == "" {
		t.Fatalf("no status select rendered\n%s", body)
	}
	// Read the options out of the select itself, so unrelated markup elsewhere on
	// the page can neither satisfy nor break the assertion.
	start := strings.Index(body, tag)
	end := strings.Index(body[start:], "</select>")
	if end < 0 {
		t.Fatalf("status select is never closed\n%s", body)
	}
	options := body[start : start+end]
	if !strings.Contains(options, `<option value="`+want+`" selected>`) {
		t.Errorf("status select should have %q selected\n%s", want, options)
	}
	if n := strings.Count(options, ` selected>`); n != 1 {
		t.Errorf("status select marked %d options selected, want 1\n%s", n, options)
	}
}

// --- Status select (#214) ----------------------------------------------------

func TestPrioStatusSelectRendersEveryCategoryDefaultingToPlanned(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	body := get(t, app.URL+"/prio")

	for _, want := range []string{
		`data-testid="prio-filters"`,
		`data-testid="prio-status"`,
		`data-testid="prio-status-select"`,
		`<option value="planned" selected>Planned</option>`,
		`<option value="doing">Doing</option>`,
		`<option value="done">Done</option>`,
		`<option value="all">All</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prio filter chrome missing %q\n%s", want, body)
		}
	}
	assertStatusSelect(t, body, "planned")
	if !strings.Contains(body, `hx-target="#prio-panel"`) {
		t.Errorf("prio filter form should target #prio-panel\n%s", body)
	}
	// The select GETs the results path like the pills do, carrying its own value
	// as the requesting element and hx-including only the OTHER filters.
	tag := openingTag(body, `data-testid="prio-status-select"`)
	if !strings.Contains(tag, `hx-get="/prio/results"`) {
		t.Errorf("the status select should GET the results path: %s", tag)
	}
	if !strings.Contains(tag, `hx-include="[data-filterparam]:not([name='status'])"`) {
		t.Errorf("the status select should hx-include only the other filters: %s", tag)
	}
	if !strings.Contains(tag, `name="status"`) {
		t.Errorf("the status select should submit as ?status=: %s", tag)
	}
}

// The two toggles the select replaced are gone from the bar entirely.
func TestPrioStatusSelectReplacesTheNotDoneAndNotStartedToggles(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, path := range []string{"/prio", "/prio/results?" + prioEveryFilterOff} {
		body := get(t, app.URL+path)
		for _, gone := range []string{
			"Not done", "Not started",
			`data-testid="prio-not-done"`, `data-testid="prio-not-started"`,
			`name="not-done"`, `name="not-started"`,
		} {
			if strings.Contains(body, gone) {
				t.Errorf("%s still renders the retired %q control\n%s", path, gone, body)
			}
		}
	}
}

func TestPrioStatusPlannedIsTheDefaultCategory(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	// Bare, explicit, and unrecognised all resolve to Planned.
	for _, path := range []string{
		"/prio",
		"/prio/results",
		"/prio?status=planned",
		"/prio?status=not-done",
		"/prio?status=",
	} {
		body := get(t, app.URL+path)
		assertPrioRows(t, body, prioPlannedKeys, prioKeysExcept(prioPlannedKeys))
		if n := strings.Count(body, `data-testid="prio-row"`); n != len(prioPlannedKeys) {
			t.Errorf("%s rendered %d rows, want %d", path, n, len(prioPlannedKeys))
		}
		assertStatusSelect(t, body, "planned")
	}
}

// Each category is exactly its own status set: Doing is the two in-flight
// statuses, Done the three finished ones, All the whole nine including Canceled.
func TestPrioStatusCategoriesSelectTheirOwnStatusSets(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, tc := range []struct {
		category string
		want     []string
	}{
		{"planned", prioPlannedKeys},
		{"doing", prioDoingKeys},
		{"done", prioDoneKeys},
		{"all", prioAllKeys()},
	} {
		t.Run(tc.category, func(t *testing.T) {
			body := get(t, app.URL+"/prio?status="+tc.category)
			assertPrioRows(t, body, tc.want, prioKeysExcept(tc.want))
			if n := strings.Count(body, `data-testid="prio-row"`); n != len(tc.want) {
				t.Errorf("status=%s rendered %d rows, want %d", tc.category, n, len(tc.want))
			}
			assertStatusSelect(t, body, tc.category)
		})
	}
}

// Canceled has no category of its own: it appears under All and nowhere else.
func TestPrioStatusCanceledOnlyAppearsUnderAll(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, category := range []string{"planned", "doing", "done"} {
		if body := get(t, app.URL+"/prio?status="+category); strings.Contains(body, `data-key="DCAI-9"`) {
			t.Errorf("status=%s should not show the Canceled ticket\n%s", category, body)
		}
	}
	all := get(t, app.URL+"/prio?status=all")
	assertPrioRows(t, all, prioCanceledKeys, nil)
	for _, want := range []string{"Canceled", "Released / Deployed", "DONE (This Sprint)", "Ready for Release"} {
		if !strings.Contains(all, want) {
			t.Errorf("status=all should reveal %q\n%s", want, all)
		}
	}
}

// Triage sits in Planned on this view — an untriaged ticket is exactly the
// unprioritised work a prioritiser wants — even though the sprint rollups treat
// it as pre-sprint.
func TestPrioStatusPlannedIncludesTriage(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	assertPrioRows(t, get(t, app.URL+"/prio"), []string{"DCAI-1"}, nil)
}

func TestPrioStatusMatchesStatusCaseInsensitively(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Odd casing", Status: "Ready to Do", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-2", Type: "Task", Summary: "Started", Status: "in progress", StatusCategory: "In Progress", Priority: "Medium"},
		{Key: "DCAI-3", Type: "Task", Summary: "Odd done casing", Status: "released / deployed", StatusCategory: "Done", Priority: "Medium"},
	}})

	assertPrioRows(t, get(t, app.URL+"/prio"), []string{"DCAI-1"}, []string{"DCAI-2", "DCAI-3"})
	assertPrioRows(t, get(t, app.URL+"/prio?status=doing"), []string{"DCAI-2"}, []string{"DCAI-1", "DCAI-3"})
	assertPrioRows(t, get(t, app.URL+"/prio?status=done"), []string{"DCAI-3"}, []string{"DCAI-1", "DCAI-2"})
}

// The non-default category rides back out as a hidden param, exactly like a
// pill's off state, so toggling a pill preserves the chosen category.
func TestPrioStatusRoundTripsWithThePills(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	bare := get(t, app.URL+"/prio")
	if strings.Contains(bare, `data-filterparam name="status"`) {
		t.Errorf("the default Planned category should emit no param\n%s", bare)
	}

	doing := get(t, app.URL+"/prio?status=doing")
	if !strings.Contains(doing, `<input type="hidden" data-filterparam name="status" value="doing">`) {
		t.Errorf("a non-default category should round-trip as a filter param\n%s", doing)
	}
	// The pills' hrefs never carry the category — it rides along via hx-include.
	assertToggle(t, doing, "prio-non-technical", true, "/prio/results?non-technical=0")
	assertToggle(t, doing, "prio-no-parent", true, "/prio/results?no-parent=0")

	// And a pill's off state does not disturb the category.
	both := get(t, app.URL+"/prio?status=all&non-technical=0")
	assertStatusSelect(t, both, "all")
	if !strings.Contains(both, `<input type="hidden" data-filterparam name="status" value="all">`) {
		t.Errorf("the category should survive alongside an off pill\n%s", both)
	}
	if !strings.Contains(both, `<input type="hidden" data-filterparam name="non-technical" value="0">`) {
		t.Errorf("the off pill should round-trip alongside the category\n%s", both)
	}
	// The select is not itself one of the round-tripped params: its live value
	// rides along because it is the element issuing the request.
	// (Its hx-include selector mentions [data-filterparam]; strip that before
	// looking for the marker attribute itself.)
	tag := strings.ReplaceAll(openingTag(both, `data-testid="prio-status-select"`), "[data-filterparam]", "")
	if strings.Contains(tag, "data-filterparam") {
		t.Errorf("the select should not double as a round-tripped param\n%s", both)
	}
}

func TestPrioNoMatchState(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-8", Type: "Story", Summary: "Shipped", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Lowest"},
	}})

	body := get(t, app.URL+"/prio")
	if !strings.Contains(body, `data-testid="prio-no-match"`) {
		t.Errorf("prio missing the no-match state\n%s", body)
	}
	if strings.Contains(body, `data-testid="prio-row"`) {
		t.Errorf("prio rendered rows when nothing matches\n%s", body)
	}
	off := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)
	if !strings.Contains(off, `data-key="DCAI-8"`) {
		t.Errorf("every filter off should show the released row\n%s", off)
	}
}

func TestPrioResultsServesAFragment(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	fragment := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)

	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Errorf("/prio/results returned a full page, want a fragment\n%s", fragment)
	}
	assertPrioRows(t, fragment, prioAllKeys(), nil)
	if n := strings.Count(fragment, `data-testid="prio-row"`); n != 9 {
		t.Errorf("every filter off rendered %d rows, want 9", n)
	}
}

// --- Non-Technical (#203, default flipped by #209) ---------------------------

func prioLabelFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Technical, in flight", Status: "In Progress", StatusCategory: "In Progress", Priority: "High", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", Summary: "Product, planned", Status: "Triage", StatusCategory: "To Do", Priority: "High", Labels: []string{"Product"}},
		{Key: "DCAI-3", Type: "Task", Summary: "Technical, done", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Low", Labels: []string{"Frontend", "Technical"}},
		{Key: "DCAI-4", Type: "Task", Summary: "Unlabelled, done", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Low"},
	}}
}

func TestPrioNonTechnicalToggleRendersDefaultOn(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio")

	for _, want := range []string{
		`data-testid="prio-non-technical"`,
		`data-testid="prio-non-technical-toggle"`,
		"Non-Technical",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prio filter chrome missing %q\n%s", want, body)
		}
	}
	assertToggle(t, body, "prio-non-technical", true, "/prio/results?non-technical=0")
	if strings.Contains(body, `name="non-technical"`) {
		t.Errorf("the default-on Non-Technical filter should emit no param\n%s", body)
	}
	if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='non-technical'])"`) {
		t.Errorf("Non-Technical toggle should hx-include only the other filters\n%s", body)
	}
}

// The registry order reads left-to-right against the right edge of the bar, the
// status select first (#214).
func TestPrioControlsRenderInRegistryOrder(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio")

	assertOrder(t, body,
		`data-testid="prio-status"`,
		`data-testid="prio-non-technical"`,
		`data-testid="prio-no-parent"`,
	)
	if !strings.Contains(openingTag(body, `data-testid="prio-filters"`), "justify-end") {
		t.Errorf("the filter controls should sit against the right edge\n%s", body)
	}
	if strings.Contains(openingTag(body, `data-testid="prio-filters"`), "w-fit") {
		t.Errorf("the filter card itself should stay full-width\n%s", body)
	}
}

// Default on: no Technical-labelled ticket survives, whatever the other filters
// are doing.
func TestPrioNonTechnicalOnByDefaultHidesTechnicalTickets(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())

	for _, path := range []string{
		"/prio",
		"/prio/results?status=all",
		// An old #203-era bookmark still resolves to ON.
		"/prio/results?status=all&non-technical=1",
	} {
		body := get(t, app.URL+path)
		if strings.Contains(body, `data-key="DCAI-1"`) || strings.Contains(body, `data-key="DCAI-3"`) {
			t.Errorf("%s should hide the Technical-labelled tickets\n%s", path, body)
		}
		assertToggle(t, body, "prio-non-technical", true, "/prio/results?non-technical=0")
	}

	// With the status select widened, the surviving non-technical work shows.
	assertPrioRows(t, get(t, app.URL+"/prio/results?status=all"),
		[]string{"DCAI-2", "DCAI-4"}, []string{"DCAI-1", "DCAI-3"})
}

func TestPrioNonTechnicalOffShowsTechnicalTickets(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	fragment := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)

	assertPrioRows(t, fragment, []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil)
	assertToggle(t, fragment, "prio-non-technical", false, "/prio/results")
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="non-technical" value="0">`) {
		t.Errorf("the off state should be re-emitted as a filter param\n%s", fragment)
	}
}

func TestPrioNonTechnicalMatchesTheExactLabel(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Exact", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", Summary: "Lowercased", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"technical"}},
		{Key: "DCAI-3", Type: "Task", Summary: "Prefixed", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"Technical-Debt"}},
	}})

	body := get(t, app.URL+"/prio")
	assertPrioRows(t, body, []string{"DCAI-2", "DCAI-3"}, []string{"DCAI-1"})
}

// --- No parent (#210, default flipped by #213) -------------------------------

// prioParentFixture pairs parented and unparented tickets, including the case the
// "is an Epic" shortcut would get wrong: an Epic that itself has a parent. Every
// row is Triage and unlabelled, so the status select and the label pill keep all
// four and No parent is the filter under test.
func prioParentFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Top-level epic", Status: "Triage", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-2", Type: "Task", Summary: "Child of the epic", Status: "Triage", StatusCategory: "To Do", Priority: "High", ParentKey: "DCAI-1"},
		{Key: "DCAI-3", Type: "Bug", Summary: "Unfiled bug", Status: "Triage", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-4", Type: "Epic", Summary: "Epic under an initiative", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", ParentKey: "DCAI-99"},
	}}
}

func TestPrioNoParentToggleRendersDefaultOn(t *testing.T) {
	app := newTestApp(t, prioParentFixture())
	body := get(t, app.URL+"/prio")

	for _, want := range []string{
		`data-testid="prio-no-parent"`,
		`data-testid="prio-no-parent-toggle"`,
		"No parent",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prio filter chrome missing %q\n%s", want, body)
		}
	}
	// Default ON (#213), so — like the other pill — the href encodes the OFF
	// state and the default emits no param at all.
	assertToggle(t, body, "prio-no-parent", true, "/prio/results?no-parent=0")
	if strings.Contains(body, `name="no-parent"`) {
		t.Errorf("the default-on No parent filter should emit no param\n%s", body)
	}
	if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='no-parent'])"`) {
		t.Errorf("No parent toggle should hx-include only the other filters\n%s", body)
	}
}

// On (the default), only rows with an empty parent survive — the rule is "has no
// parent", not "is an Epic": the child Task goes, and so does the Epic filed
// under DCAI-99. A bare /prio is the on state.
func TestPrioNoParentOnKeepsOnlyUnparentedTickets(t *testing.T) {
	app := newTestApp(t, prioParentFixture())

	// A bare path, and an old #210-era bookmark, both resolve to ON.
	for _, path := range []string{"/prio", "/prio/results", "/prio/results?no-parent=1"} {
		assertPrioRows(t, get(t, app.URL+path), []string{"DCAI-1", "DCAI-3"}, []string{"DCAI-2", "DCAI-4"})
	}
}

// Off it contributes nothing: parented and unparented rows alike, and the off
// state rides back out as `no-parent=0`.
func TestPrioNoParentOffShowsParentedTickets(t *testing.T) {
	app := newTestApp(t, prioParentFixture())

	for _, path := range []string{"/prio?no-parent=0", "/prio/results?no-parent=0", "/prio/results?" + prioEveryFilterOff} {
		fragment := get(t, app.URL+path)
		assertPrioRows(t, fragment, []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil)
		assertToggle(t, fragment, "prio-no-parent", false, "/prio/results")
		if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="no-parent" value="0">`) {
			t.Errorf("%s: the off state should be re-emitted as a filter param\n%s", path, fragment)
		}
	}
}

// It is ANDed with the rest, and its param round-trips both ways.
func TestPrioNoParentCombinesWithTheOtherFilters(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Planned epic", Status: "Triage", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-2", Type: "Epic", Summary: "Shipped epic", Status: "Released / Deployed", StatusCategory: "Done", Priority: "High"},
		{Key: "DCAI-3", Type: "Task", Summary: "Technical, unparented", Status: "Triage", StatusCategory: "To Do", Priority: "High", Labels: []string{"Technical"}},
		{Key: "DCAI-4", Type: "Task", Summary: "Planned child", Status: "Triage", StatusCategory: "To Do", Priority: "High", ParentKey: "DCAI-1"},
	}})

	for _, tc := range []struct {
		name   string
		query  string
		want   []string
		absent []string
	}{
		{"at the defaults it hides done, technical and parented work alike", "", []string{"DCAI-1"}, []string{"DCAI-2", "DCAI-3", "DCAI-4"}},
		{"with non-technical off the technical unparented task returns", "?non-technical=0", []string{"DCAI-1", "DCAI-3"}, []string{"DCAI-2", "DCAI-4"}},
		{"with every other filter widened only the parent rule applies", "?" + prioStatusAndLabelFiltersOff, []string{"DCAI-1", "DCAI-2", "DCAI-3"}, []string{"DCAI-4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+"/prio"+tc.query), tc.want, tc.absent)
		})
	}

	// Off alongside the defaults: it round-trips and the others stay bare.
	off := get(t, app.URL+"/prio?no-parent=0")
	if !strings.Contains(off, `<input type="hidden" data-filterparam name="no-parent" value="0">`) {
		t.Errorf("no-parent off should round-trip alone\n%s", off)
	}
	for _, param := range []string{"status", "non-technical"} {
		if strings.Contains(off, `data-filterparam name="`+param+`"`) {
			t.Errorf("%s is at its default and should emit no param\n%s", param, off)
		}
	}

	// And the other controls' hrefs never carry it — it rides along via hx-include.
	assertToggle(t, off, "prio-non-technical", true, "/prio/results?non-technical=0")
	assertStatusSelect(t, off, "planned")
}

// --- The registry as a whole -------------------------------------------------

func TestPrioFiltersCombineIndependently(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())

	for _, tc := range []struct {
		name   string
		query  string
		want   []string
		absent []string
	}{
		{"defaults: Planned, non-technical, top-of-tree", "", []string{"DCAI-2"}, []string{"DCAI-1", "DCAI-3", "DCAI-4"}},
		{"doing", "?status=doing", nil, []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}},
		{"doing + non-technical off", "?status=doing&non-technical=0", []string{"DCAI-1"}, []string{"DCAI-2", "DCAI-3", "DCAI-4"}},
		{"done", "?status=done", []string{"DCAI-4"}, []string{"DCAI-1", "DCAI-2", "DCAI-3"}},
		{"all", "?status=all", []string{"DCAI-2", "DCAI-4"}, []string{"DCAI-1", "DCAI-3"}},
		{"everything widened", "?" + prioEveryFilterOff, []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+"/prio"+tc.query), tc.want, tc.absent)
		})
	}
}

// Every control re-emits the OTHER filters' params, so flipping one preserves the
// rest across the HTMX swap.
func TestPrioControlsPreserveTheOtherFilters(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

	if !strings.Contains(body, `<input type="hidden" data-filterparam name="status" value="all">`) {
		t.Errorf("the chosen category should round-trip as a param\n%s", body)
	}
	for _, param := range []string{"status", "non-technical", "no-parent"} {
		if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='`+param+`'])"`) {
			t.Errorf("the %s control should include every other filter's param\n%s", param, body)
		}
	}
	for _, param := range []string{"non-technical", "no-parent"} {
		if !strings.Contains(body, `<input type="hidden" data-filterparam name="`+param+`" value="0">`) {
			t.Errorf("%s off should round-trip as a param\n%s", param, body)
		}
	}

	// One filter off, the others at their defaults: the off one still round-trips.
	single := get(t, app.URL+"/prio?non-technical=0")
	if !strings.Contains(single, `<input type="hidden" data-filterparam name="non-technical" value="0">`) {
		t.Errorf("non-technical off should round-trip alone\n%s", single)
	}
	for _, param := range []string{"status", "no-parent"} {
		if strings.Contains(single, `data-filterparam name="`+param+`"`) {
			t.Errorf("%s is at its default and should emit no param\n%s", param, single)
		}
	}
}
