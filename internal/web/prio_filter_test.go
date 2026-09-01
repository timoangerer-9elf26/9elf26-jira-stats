package web_test

// Integration tests for the Prio view's two filters, both two-state toggles
// built on the Board's URL-encoded, fragment-swapping filter scaffolding.
// Not-done (#202) is default ON: on it keeps the not-done statuses (Triage
// included), off it reveals the Done set and Canceled too. Non-Technical (#203)
// is the mirror image, default OFF: off it shows every ticket, on it hides the
// ones carrying the exact `Technical` label. The two are independent, so the
// last tests here drive them in combination.

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// prioStatusFixture spans every status bucket the Not-done filter cares about:
// the five not-done statuses, the three Done-set ones, and Canceled.
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
	prioNotDoneKeys = []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4", "DCAI-5"}
	prioDoneKeys    = []string{"DCAI-6", "DCAI-7", "DCAI-8", "DCAI-9"}
)

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

// AC1: the Not-done toggle renders on the Prio filter chrome, default ON.
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
	if !strings.Contains(openingTag(body, `data-testid="prio-not-done-toggle"`), `aria-pressed="true"`) {
		t.Errorf("the Not-done toggle should render pressed on a fresh /prio\n%s", body)
	}
	// The filter form swaps the Prio panel, not the whole page.
	if !strings.Contains(body, `hx-target="#prio-panel"`) {
		t.Errorf("prio filter form should target #prio-panel\n%s", body)
	}
	// On (the default) the toggle's href encodes the state flipping it yields.
	if !strings.Contains(body, `hx-get="/prio/results?not-done=0"`) {
		t.Errorf("pressed Not-done toggle should link to the off state\n%s", body)
	}
}

// AC2: on, only the five not-done statuses show (Triage included).
func TestPrioNotDoneOnByDefaultHidesDoneAndCanceled(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())

	for _, path := range []string{"/prio", "/prio/results", "/prio/results?not-done=1"} {
		body := get(t, app.URL+path)
		assertPrioRows(t, body, prioNotDoneKeys, prioDoneKeys)
		if n := strings.Count(body, `data-testid="prio-row"`); n != len(prioNotDoneKeys) {
			t.Errorf("%s rendered %d rows, want %d", path, n, len(prioNotDoneKeys))
		}
	}
}

// AC3 + AC4: toggling off reveals the Done set and Canceled, and the selection
// round-trips through the URL — the fragment carries the flipped toggle back.
func TestPrioNotDoneOffShowsEveryStatus(t *testing.T) {
	app := newTestApp(t, prioStatusFixture())
	fragment := get(t, app.URL+"/prio/results?not-done=0")

	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Errorf("/prio/results returned a full page, want a fragment\n%s", fragment)
	}
	assertPrioRows(t, fragment, append(append([]string{}, prioNotDoneKeys...), prioDoneKeys...), nil)
	if n := strings.Count(fragment, `data-testid="prio-row"`); n != 9 {
		t.Errorf("/prio/results?not-done=0 rendered %d rows, want 9", n)
	}
	for _, want := range []string{"Released / Deployed", "Canceled", "DONE (This Sprint)", "Ready for Release"} {
		if !strings.Contains(fragment, want) {
			t.Errorf("not-done off should reveal %q\n%s", want, fragment)
		}
	}
	// The off state re-renders unpressed and offers the way back on.
	if strings.Contains(openingTag(fragment, `data-testid="prio-not-done-toggle"`), "aria-pressed") {
		t.Errorf("Not-done toggle should render unpressed when off\n%s", fragment)
	}
	if !strings.Contains(fragment, `hx-get="/prio/results"`) {
		t.Errorf("unpressed Not-done toggle should link back to the default on state\n%s", fragment)
	}
	// Off is the non-default, so it is the state carried as a filter param so
	// sibling filters round-trip it.
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="not-done" value="0">`) {
		t.Errorf("off state should be re-emitted as a filter param\n%s", fragment)
	}
	// A toggle never re-includes its own param (its href already carries it).
	if !strings.Contains(fragment, `hx-include="[data-filterparam]:not([name='not-done'])"`) {
		t.Errorf("Not-done toggle should hx-include only the other filters\n%s", fragment)
	}
}

// A Jira casing quirk ("Ready to Do" for "Ready To Do") must not silently drop a
// ticket out of the not-done set: every status bucket in the app matches
// case-insensitively (store.normalizeStatus).
func TestPrioNotDoneMatchesStatusCaseInsensitively(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Odd casing", Status: "Ready to Do", StatusCategory: "To Do", Priority: "Medium"},
		{Key: "DCAI-2", Type: "Task", Summary: "Odd done casing", Status: "released / deployed", StatusCategory: "Done", Priority: "Medium"},
	}})

	body := get(t, app.URL+"/prio")
	assertPrioRows(t, body, []string{"DCAI-1"}, []string{"DCAI-2"})
}

// AC5: a filter combination that matches nothing says so instead of rendering
// an empty table.
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
	// Turning the filter off brings the row back.
	off := get(t, app.URL+"/prio/results?not-done=0")
	if !strings.Contains(off, `data-key="DCAI-8"`) {
		t.Errorf("not-done off should show the released row\n%s", off)
	}
}

// prioLabelFixture pairs technical-ness with done-ness so the two toggles can be
// exercised independently and in combination. DCAI-1/-2 are not done, -3/-4 are
// done; the odd ones carry the exact `Technical` label, the even ones do not.
func prioLabelFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Technical, not done", Status: "In Progress", StatusCategory: "In Progress", Priority: "High", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", Summary: "Product, not done", Status: "Triage", StatusCategory: "To Do", Priority: "High", Labels: []string{"Product"}},
		{Key: "DCAI-3", Type: "Task", Summary: "Technical, done", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Low", Labels: []string{"Frontend", "Technical"}},
		{Key: "DCAI-4", Type: "Task", Summary: "Unlabelled, done", Status: "Released / Deployed", StatusCategory: "Done", Priority: "Low"},
	}}
}

// AC1: the Non-Technical toggle renders on the Prio filter chrome, default OFF,
// its state URL-encoded.
func TestPrioNonTechnicalToggleRendersDefaultOff(t *testing.T) {
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
	if strings.Contains(openingTag(body, `data-testid="prio-non-technical-toggle"`), "aria-pressed") {
		t.Errorf("the Non-Technical toggle should render unpressed on a fresh /prio\n%s", body)
	}
	if !strings.Contains(body, `hx-get="/prio/results?non-technical=1"`) {
		t.Errorf("unpressed Non-Technical toggle should link to the on state\n%s", body)
	}
	if strings.Contains(body, `name="non-technical"`) {
		t.Errorf("the default-off Non-Technical filter should emit no param\n%s", body)
	}
	if !strings.Contains(body, `hx-include="[data-filterparam]:not([name='non-technical'])"`) {
		t.Errorf("Non-Technical toggle should hx-include only the other filters\n%s", body)
	}
}

// The spec (#196) wants the toggles stacked "Non-Technical, then Not done",
// which the prioFilters registry order decides.
func TestPrioNonTechnicalStacksLeftOfNotDone(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio")

	nonTechnical := strings.Index(body, `data-testid="prio-non-technical"`)
	notDone := strings.Index(body, `data-testid="prio-not-done"`)
	if nonTechnical < 0 || notDone < 0 {
		t.Fatalf("prio chrome is missing a toggle (non-technical=%d, not-done=%d)\n%s", nonTechnical, notDone, body)
	}
	if nonTechnical > notDone {
		t.Errorf("toggles should stack Non-Technical then Not done\n%s", body)
	}
}

// AC3: off, every ticket shows regardless of the `Technical` label — whether the
// param is absent or explicitly off.
func TestPrioNonTechnicalOffShowsTechnicalTickets(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())

	for _, tc := range []struct {
		name string
		path string
	}{
		{"param absent", "/prio/results?not-done=0"},
		{"param explicitly off", "/prio/results?not-done=0&non-technical=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+tc.path), []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil)
		})
	}
}

// AC2 + AC5: on, `Technical`-labelled tickets are hidden and the rest (including
// unlabelled ones) remain, and the swapped fragment carries the flipped toggle
// and its round-trip param back.
func TestPrioNonTechnicalOnHidesTechnicalTickets(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	fragment := get(t, app.URL+"/prio/results?not-done=0&non-technical=1")

	// Only the `Technical`-labelled rows go; unlabelled rows stay.
	assertPrioRows(t, fragment, []string{"DCAI-2", "DCAI-4"}, []string{"DCAI-1", "DCAI-3"})
	if !strings.Contains(openingTag(fragment, `data-testid="prio-non-technical-toggle"`), `aria-pressed="true"`) {
		t.Errorf("Non-Technical toggle should render pressed when on\n%s", fragment)
	}
	if !strings.Contains(fragment, `hx-get="/prio/results"`) {
		t.Errorf("pressed Non-Technical toggle should link back to the default off state\n%s", fragment)
	}
	if !strings.Contains(fragment, `<input type="hidden" data-filterparam name="non-technical" value="1">`) {
		t.Errorf("the on state should be re-emitted as a filter param\n%s", fragment)
	}
}

// "Technical" is matched as an exact whole label (capital T, the canonical
// stored value): neither a differently-cased nor a prefixed label counts.
func TestPrioNonTechnicalMatchesTheExactLabel(t *testing.T) {
	app := newTestApp(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Summary: "Exact", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", Summary: "Lowercased", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"technical"}},
		{Key: "DCAI-3", Type: "Task", Summary: "Prefixed", Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Labels: []string{"Technical-Debt"}},
	}})

	body := get(t, app.URL+"/prio?non-technical=1")
	assertPrioRows(t, body, []string{"DCAI-2", "DCAI-3"}, []string{"DCAI-1"})
}

// AC4: the two filters combine as a plain intersection — all four quadrants.
func TestPrioFiltersCombineIndependently(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())

	for _, tc := range []struct {
		name   string
		query  string
		want   []string
		absent []string
	}{
		{"defaults: not-done on, non-technical off", "", []string{"DCAI-1", "DCAI-2"}, []string{"DCAI-3", "DCAI-4"}},
		{"both on", "?non-technical=1", []string{"DCAI-2"}, []string{"DCAI-1", "DCAI-3", "DCAI-4"}},
		{"both off", "?not-done=0", []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"}, nil},
		{"non-technical alone", "?not-done=0&non-technical=1", []string{"DCAI-2", "DCAI-4"}, []string{"DCAI-1", "DCAI-3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPrioRows(t, get(t, app.URL+"/prio"+tc.query), tc.want, tc.absent)
		})
	}
}

// AC4: each toggle's href must carry the OTHER filter's non-default state so a click
// round-trips both selections through the URL.
func TestPrioToggleHrefsPreserveTheOtherFilter(t *testing.T) {
	app := newTestApp(t, prioLabelFixture())
	body := get(t, app.URL+"/prio?not-done=0&non-technical=1")

	if !strings.Contains(body, `<input type="hidden" data-filterparam name="not-done" value="0">`) {
		t.Errorf("not-done off should round-trip as a param\n%s", body)
	}
	if !strings.Contains(body, `<input type="hidden" data-filterparam name="non-technical" value="1">`) {
		t.Errorf("non-technical on should round-trip as a param\n%s", body)
	}
}
