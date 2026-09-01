package web_test

// Integration tests for the Prio view over the HTTP seam: a flat table of EVERY
// issue in the projection (any sprint, any status) with Type · Name · Status
// columns, ordered by issue key, reachable at /prio with a swappable fragment at
// /prio/results, and the shared nav reordered to Board · Prio · Sprint ·
// Velocity · Daily.

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// prioFixture spans the whole projection: active-sprint work, an older sprint,
// a sprintless Triage ticket, an Epic and a released ticket — everything the
// sprint-scoped views drop must still show up as a Prio row.
func prioFixture() *jira.FakeClient {
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Big theme", Status: "In Progress", StatusCategory: "In Progress"},
		{Key: "DCAI-2", Type: "Story", Summary: "Refine the widget", Status: "Refinement", StatusCategory: "To Do", Sprint: "KW29", ActiveSprint: "KW29"},
		{Key: "DCAI-3", Type: "Task", Summary: "Wire the gadget", Status: "In Progress", StatusCategory: "In Progress", Sprint: "KW29", ActiveSprint: "KW29"},
		{Key: "DCAI-4", Type: "Bug", Summary: "Fix the sprocket", Status: "Triage", StatusCategory: "To Do"},
		{Key: "DCAI-5", Type: "Story", Summary: "Old shipped work", Status: "Released / Deployed", StatusCategory: "Done", Sprint: "KW28"},
	}}
}

func TestPrioListsEveryIssueWithTypeNameStatus(t *testing.T) {
	app := newTestApp(t, prioFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/prio")

	// The three columns render, in order.
	assertOrder(t, body,
		`data-testid="prio-col-type"`,
		`data-testid="prio-col-name"`,
		`data-testid="prio-col-status"`,
	)

	// Every issue in the projection has a row — including the Epic, the Triage
	// ticket that never entered a sprint, and the released work from an older
	// sprint, none of which the sprint-scoped views show.
	wants := []string{
		`data-testid="prio:DCAI-1:type">Epic<`,
		`data-testid="prio:DCAI-2:type">Story<`,
		`data-testid="prio:DCAI-3:type">Task<`,
		`data-testid="prio:DCAI-4:type">Bug<`,
		"Big theme", "Refine the widget", "Wire the gadget", "Fix the sprocket", "Old shipped work",
		`data-testid="prio:DCAI-1:status">In Progress<`,
		`data-testid="prio:DCAI-4:status">Triage<`,
		`data-testid="prio:DCAI-5:status">Released / Deployed<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("prio missing %q\n%s", w, body)
		}
	}
	if n := strings.Count(body, `data-testid="prio-row"`); n != 5 {
		t.Errorf("prio rendered %d rows, want 5", n)
	}

	// Rows are ordered by issue key.
	assertOrder(t, body,
		`data-key="DCAI-1"`,
		`data-key="DCAI-2"`,
		`data-key="DCAI-3"`,
		`data-key="DCAI-4"`,
		`data-key="DCAI-5"`,
	)

	// The Epic badge is its own variant, not the neutral fallback.
	if !strings.Contains(body, `bg-violet-100 text-violet-700`) {
		t.Errorf("prio missing the Epic type-badge variant\n%s", body)
	}

	// The name links to the Jira issue in a new tab.
	link := `href="https://9elf26.atlassian.net/browse/DCAI-3" target="_blank" rel="noopener"`
	if !strings.Contains(body, link) {
		t.Errorf("prio row missing Jira link %q\n%s", link, body)
	}
}

// Without a configured Jira base URL the name renders as plain text rather than
// a broken link, matching the board card.
func TestPrioNameUnlinkedWithoutJiraBaseURL(t *testing.T) {
	app := newTestApp(t, prioFixture())
	body := get(t, app.URL+"/prio")

	if strings.Contains(body, "/browse/DCAI-3") {
		t.Errorf("prio linked a name with no Jira base URL configured\n%s", body)
	}
	if !strings.Contains(body, `data-testid="prio:DCAI-3:name"`) {
		t.Errorf("prio missing the name cell\n%s", body)
	}
	if !strings.Contains(body, "Wire the gadget") {
		t.Errorf("prio missing the unlinked name text\n%s", body)
	}
}

// /prio/results serves the swappable fragment: the same rows, no document shell.
func TestPrioResultsServesTheFragment(t *testing.T) {
	app := newTestApp(t, prioFixture())
	fragment := get(t, app.URL+"/prio/results")

	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Errorf("/prio/results returned a full page, want a fragment\n%s", fragment)
	}
	if n := strings.Count(fragment, `data-testid="prio-row"`); n != 5 {
		t.Errorf("/prio/results rendered %d rows, want 5", n)
	}
	for _, want := range []string{"Big theme", "Fix the sprocket", `data-testid="prio:DCAI-5:status">Released / Deployed<`} {
		if !strings.Contains(fragment, want) {
			t.Errorf("/prio/results missing %q\n%s", want, fragment)
		}
	}
}

// The shared nav gains Prio in the order Board · Prio · Sprint · Velocity ·
// Daily, and /prio marks itself current.
func TestPrioNavOrderAndActiveTab(t *testing.T) {
	app := newTestApp(t, prioFixture())
	body := get(t, app.URL+"/prio")

	assertOrder(t, body,
		`data-nav="board"`,
		`data-nav="prio"`,
		`data-nav="sprint"`,
		`data-nav="velocity"`,
		`data-nav="daily"`,
	)
	if !strings.Contains(body, `href="/prio"`) {
		t.Errorf("nav missing the Prio link\n%s", body)
	}
	if !strings.Contains(body, `data-nav="prio" aria-current="page"`) {
		t.Errorf("/prio: Prio tab not marked active\n%s", body)
	}
}

// An unsynced projection renders the friendly empty state, not a headerless
// table or a 500.
func TestPrioEmptyStateBeforeFirstSync(t *testing.T) {
	body := get(t, newEmptyTestApp(t).URL+"/prio")

	if !strings.Contains(body, `data-testid="prio-empty"`) {
		t.Errorf("prio missing the empty state\n%s", body)
	}
	if strings.Contains(body, `data-testid="prio-row"`) {
		t.Errorf("prio rendered rows against an empty projection\n%s", body)
	}
}
