package web_test

// Integration tests for the Prio view's filter registry over the HTTP seam.
//
// After #209 the three status/label toggles default ON, so a bare /prio shows
// the narrowed, prioritisable slice — not-started, non-technical work — rather
// than the raw ~1,400-row project. A test that wants a wider universe must say so
// in the URL; prioEveryFilterOff is that URL suffix. No parent (#210) is the
// exception: it defaults OFF, contributes nothing at a bare /prio, and so is
// deliberately absent from that const — a test turns it ON with `no-parent=1`.
//
// Note the accepted overlap: Not-started (Triage / Refinement / Ready To Do) is
// a strict subset of Not-done, so at the defaults Not-done changes nothing. The
// Not-done cases below therefore drive it with `not-started=0` — that is the
// state in which Not-done is the filter under test.

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// prioEveryFilterOff turns off every default-on Prio filter, i.e. "show me the
// whole project".
const prioEveryFilterOff = "not-done=0&not-started=0&non-technical=0"

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

var (
	// The not-yet-started slice: what a bare /prio shows of the status fixture.
	prioNotStartedKeys = []string{"DCAI-1", "DCAI-2", "DCAI-3"}
	// Open but already picked up — inside Not-done, outside Not-started.
	prioStartedKeys = []string{"DCAI-4", "DCAI-5"}
	prioNotDoneKeys = append(append([]string{}, prioNotStartedKeys...), prioStartedKeys...)
	prioDoneKeys    = []string{"DCAI-6", "DCAI-7", "DCAI-8", "DCAI-9"}
)

func prioAllKeys() []string {
	return append(append([]string{}, prioNotDoneKeys...), prioDoneKeys...)
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

func TestPrioNotDoneToggleRendersDefaultOn(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	body := get(t, app.URL+"/prio")

	for _, want := range []string{
		`data-testid="prio-filters"`,
		`data-testid="prio-not-done"`,
		`data-testid="prio-not-done-toggle"`,
		"Not done",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prio filter chrome missing %q\n%s", want, body)
		}
	}
	assertToggle(t, body, "prio-not-done", true, "/prio/results?not-done=0")
	if !strings.Contains(body, `hx-target="#prio-panel"`) {
		t.Errorf("prio filter form should target #prio-panel\n%s", body)
	}
}

// With Not-started off, Not-done is the status filter doing the work: it keeps
// every open ticket and hides the done set.
func TestPrioNotDoneOnHidesDoneAndCanceled(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, path := range []string{
		"/prio?not-started=0",
		"/prio/results?not-started=0",
		"/prio/results?not-started=0&not-done=1",
	} {
		body := get(t, app.URL+path)
		assertPrioRows(t, body, prioNotDoneKeys, prioDoneKeys)
		if n := strings.Count(body, `data-testid="prio-row"`); n != len(prioNotDoneKeys) {
			t.Errorf("%s rendered %d rows, want %d", path, n, len(prioNotDoneKeys))
		}
	}
}

func TestPrioNotDoneOffShowsEveryStatus(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	fragment := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)

	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Errorf("/prio/results returned a full page, want a fragment\n%s", fragment)
	}
	assertPrioRows(t, fragment, prioAllKeys(), nil)
	if n := strings.Count(fragment, `data-testid="prio-row"`); n != 9 {
		t.Errorf("every filter off rendered %d rows, want 9", n)
	}
	for _, want := range []string{"Released / Deployed", "Canceled", "DONE (This Sprint)", "Ready for Release"} {
		if !strings.Contains(fragment, want) {
			t.Errorf("not-done off should reveal %q\n%s", want, fragment)
		}
	}
	assertToggle(t, fragment, "prio-not-done", false, "/prio/results")
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="not-done" value="0">`) {
		t.Errorf("off state should be re-emitted as a filter param\n%s", fragment)
	}
	if !strings.Contains(fragment, `hx-include="[data-filterparam]:not([name='not-done'])"`) {
		t.Errorf("Not-done toggle should hx-include only the other filters\n%s", fragment)
	}
}

func TestPrioNotDoneMatchesStatusCaseInsensitively(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Odd casing", Status: "Ready to Do", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-2", Type: "Task", Summary: "Odd done casing", Status: "released / deployed", StatusCategory: "Done", Priority: "Medium"},
	}})

	body := get(t, app.URL+"/prio?not-started=0")
	assertPrioRows(t, body, []string{"DCAI-1"}, []string{"DCAI-2"})
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

// --- Not started (#209) ------------------------------------------------------

func TestPrioNotStartedToggleRendersDefaultOn(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	body := get(t, app.URL+"/prio")

	for _, want := range []string{
		`data-testid="prio-not-started"`,
		`data-testid="prio-not-started-toggle"`,
		"Not started",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prio filter chrome missing %q\n%s", want, body)
		}
	}
	assertToggle(t, body, "prio-not-started", true, "/prio/results?not-started=0")
	if strings.Contains(body, `name="not-started"`) {
		t.Errorf("the default-on Not-started filter should emit no param\n%s", body)
	}
	if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='not-started'])"`) {
		t.Errorf("Not-started toggle should hx-include only the other filters\n%s", body)
	}
}

func TestPrioNotStartedOnByDefaultHidesStartedWork(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, path := range []string{"/prio", "/prio/results", "/prio/results?not-started=1"} {
		body := get(t, app.URL+path)
		assertPrioRows(t, body, prioNotStartedKeys, append(append([]string{}, prioStartedKeys...), prioDoneKeys...))
		if n := strings.Count(body, `data-testid="prio-row"`); n != len(prioNotStartedKeys) {
			t.Errorf("%s rendered %d rows, want %d", path, n, len(prioNotStartedKeys))
		}
	}
}

// Off, Not-started contributes nothing: In Progress and Review / Testing come
// back, subject to the filters still on (here Not-done, so the done set stays
// hidden).
func TestPrioNotStartedOffRestoresStartedWork(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	fragment := get(t, app.URL+"/prio/results?not-started=0")

	assertPrioRows(t, fragment, prioNotDoneKeys, prioDoneKeys)
	for _, want := range []string{"In Progress", "Review / Testing"} {
		if !strings.Contains(fragment, `:status">`+want+`<`) {
			t.Errorf("not-started off should reveal %q\n%s", want, fragment)
		}
	}
	assertToggle(t, fragment, "prio-not-started", false, "/prio/results")
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="not-started" value="0">`) {
		t.Errorf("off state should be re-emitted as a filter param\n%s", fragment)
	}
}

func TestPrioNotStartedMatchesStatusCaseInsensitively(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Odd casing", Status: "ready to do", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-2", Type: "Task", Summary: "Started", Status: "in progress", StatusCategory: "In Progress", Priority: "Medium"},
	}})

	assertPrioRows(t, get(t, app.URL+"/prio"), []string{"DCAI-1"}, []string{"DCAI-2"})
}

// --- Non-Technical (#203, default flipped by #209) ---------------------------

func prioLabelFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Technical, not done", Status: "In Progress", StatusCategory: "In Progress", Priority: "High", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", Summary: "Product, not done", Status: "Triage", StatusCategory: "To Do", Priority: "High", Labels: []string{"Product"}},
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

// The registry order reads left-to-right against the right edge of the bar.
func TestPrioTogglesRenderInRegistryOrder(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio")

	assertOrder(t, body,
		`data-testid="prio-non-technical"`,
		`data-testid="prio-not-done"`,
		`data-testid="prio-not-started"`,
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
		"/prio/results?not-done=0&not-started=0",
		// An old #203-era bookmark still resolves to ON.
		"/prio/results?not-done=0&not-started=0&non-technical=1",
	} {
		body := get(t, app.URL+path)
		if strings.Contains(body, `data-key="DCAI-1"`) || strings.Contains(body, `data-key="DCAI-3"`) {
			t.Errorf("%s should hide the Technical-labelled tickets\n%s", path, body)
		}
		assertToggle(t, body, "prio-non-technical", true, "/prio/results?non-technical=0")
	}

	// With only the status filters off, the surviving non-technical work shows.
	assertPrioRows(t, get(t, app.URL+"/prio/results?not-done=0&not-started=0"),
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

// prioParentFixture pairs parented and unparented tickets, including the case the
// "is an Epic" shortcut would get wrong: an Epic that itself has a parent. Every
// row is Triage and unlabelled, so the three default-on filters keep all four and
// No parent is the filter under test.
func prioParentFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Top-level epic", Status: "Triage", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-2", Type: "Task", Summary: "Child of the epic", Status: "Triage", StatusCategory: "To Do", Priority: "High", ParentKey: "DCAI-1"},
		{Key: "DCAI-3", Type: "Bug", Summary: "Unfiled bug", Status: "Triage", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-4", Type: "Epic", Summary: "Epic under an initiative", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", ParentKey: "DCAI-99"},
	}}
}

func TestPrioNoParentToggleRendersDefaultOff(t *testing.T) {
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
	// Default OFF, so the href encodes the ON state — the opposite of its
	// default-on neighbours.
	assertToggle(t, body, "prio-no-parent", false, "/prio/results?no-parent=1")
	if strings.Contains(body, `name="no-parent"`) {
		t.Errorf("the default-off No parent filter should emit no param\n%s", body)
	}
}

// Off (the default) it contributes nothing: parented and unparented rows alike.
func TestPrioNoParentOffShowsParentedTickets(t *testing.T) {
	app := newTestApp(t, prioParentFixture())

	for _, path := range []string{"/prio", "/prio/results?no-parent=0", "/prio/results?" + prioEveryFilterOff} {
		assertPrioRows(t, get(t, app.URL+path), []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil)
	}
}

// On, only rows with an empty parent survive — the rule is "has no parent", not
// "is an Epic": the child Task goes, and so does the Epic filed under DCAI-99.
func TestPrioNoParentOnKeepsOnlyUnparentedTickets(t *testing.T) {
	app := newTestApp(t, prioParentFixture())
	fragment := get(t, app.URL+"/prio/results?no-parent=1")

	assertPrioRows(t, fragment, []string{"DCAI-1", "DCAI-3"}, []string{"DCAI-2", "DCAI-4"})
	assertToggle(t, fragment, "prio-no-parent", true, "/prio/results")
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="no-parent" value="1">`) {
		t.Errorf("the on state should be re-emitted as a filter param\n%s", fragment)
	}
	if !strings.Contains(fragment, `hx-include="[data-filterparam]:not([name='no-parent'])"`) {
		t.Errorf("No parent toggle should hx-include only the other filters\n%s", fragment)
	}
}

// It is ANDed with the rest, and its param round-trips both ways.
func TestPrioNoParentCombinesWithTheOtherFilters(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Open epic", Status: "Triage", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-2", Type: "Epic", Summary: "Shipped epic", Status: "Released / Deployed", StatusCategory: "Done", Priority: "High"},
		{Key: "DCAI-3", Type: "Task", Summary: "Technical, unparented", Status: "Triage", StatusCategory: "To Do", Priority: "High", Labels: []string{"Technical"}},
		{Key: "DCAI-4", Type: "Task", Summary: "Open child", Status: "Triage", StatusCategory: "To Do", Priority: "High", ParentKey: "DCAI-1"},
	}})

	for _, tc := range []struct {
		name   string
		query  string
		want   []string
		absent []string
	}{
		{"no-parent alone still hides done and technical work", "?no-parent=1", []string{"DCAI-1"}, []string{"DCAI-2", "DCAI-3", "DCAI-4"}},
		{"with non-technical off the technical unparented task returns", "?no-parent=1&non-technical=0", []string{"DCAI-1", "DCAI-3"}, []string{"DCAI-2", "DCAI-4"}},
		{"with every other filter off only the parent rule applies", "?no-parent=1&" + prioEveryFilterOff, []string{"DCAI-1", "DCAI-2", "DCAI-3"}, []string{"DCAI-4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+"/prio"+tc.query), tc.want, tc.absent)
		})
	}

	// On alongside the defaults: it round-trips and the others stay bare.
	on := get(t, app.URL+"/prio?no-parent=1")
	if !strings.Contains(on, `<input type="hidden" data-filterparam name="no-parent" value="1">`) {
		t.Errorf("no-parent on should round-trip alone\n%s", on)
	}
	for _, param := range []string{"not-done", "not-started", "non-technical"} {
		if strings.Contains(on, `name="`+param+`"`) {
			t.Errorf("%s is at its default and should emit no param\n%s", param, on)
		}
	}

	// And the other toggles' hrefs never carry it — it rides along via hx-include.
	assertToggle(t, on, "prio-not-done", true, "/prio/results?not-done=0")
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
		{"defaults: all three on", "", []string{"DCAI-2"}, []string{"DCAI-1", "DCAI-3", "DCAI-4"}},
		{"not-started off", "?not-started=0", []string{"DCAI-2"}, []string{"DCAI-1", "DCAI-3", "DCAI-4"}},
		{"not-started + non-technical off", "?not-started=0&non-technical=0", []string{"DCAI-1", "DCAI-2"}, []string{"DCAI-3", "DCAI-4"}},
		{"status filters off", "?not-done=0&not-started=0", []string{"DCAI-2", "DCAI-4"}, []string{"DCAI-1", "DCAI-3"}},
		{"all off", "?" + prioEveryFilterOff, []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+"/prio"+tc.query), tc.want, tc.absent)
		})
	}
}

// Every toggle re-emits the OTHER filters' params, so flipping one preserves the
// rest across the HTMX swap.
func TestPrioToggleHrefsPreserveTheOtherFilters(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

	for _, param := range []string{"not-done", "not-started", "non-technical"} {
		if !strings.Contains(body, `<input type="hidden" data-filterparam name="`+param+`" value="0">`) {
			t.Errorf("%s off should round-trip as a param\n%s", param, body)
		}
		if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='`+param+`'])"`) {
			t.Errorf("the %s toggle should include every other filter's param\n%s", param, body)
		}
	}

	// One filter off, the others at their defaults: the off one still round-trips.
	single := get(t, app.URL+"/prio?not-started=0")
	if !strings.Contains(single, `<input type="hidden" data-filterparam name="not-started" value="0">`) {
		t.Errorf("not-started off should round-trip alone\n%s", single)
	}
	for _, param := range []string{"not-done", "non-technical"} {
		if strings.Contains(single, `name="`+param+`"`) {
			t.Errorf("%s is at its default and should emit no param\n%s", param, single)
		}
	}
}
