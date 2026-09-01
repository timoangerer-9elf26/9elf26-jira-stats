package web_test

// Integration tests for the Prio view's Not-done filter (#202): a two-state
// toggle, default ON, built on the Board's URL-encoded, fragment-swapping
// filter scaffolding. On it keeps the not-done statuses (Triage included); off
// it reveals the Done set and Canceled too.

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
