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
		{Key: "DCAI-1", Type: "Epic", Summary: "Big theme", Status: "In Progress", StatusCategory: "In Progress", Priority: "Medium"},
		{Key: "DCAI-2", Type: "Story", Summary: "Refine the widget", Status: "Refinement", StatusCategory: "To Do", Sprint: "KW29", ActiveSprint: "KW29", Priority: "Highest", Labels: []string{"needs-design"}},
		{Key: "DCAI-3", Type: "Task", Summary: "Wire the gadget", Status: "In Progress", StatusCategory: "In Progress", Sprint: "KW29", ActiveSprint: "KW29", Priority: "Low", Labels: []string{"Technical", "Product"}},
		{Key: "DCAI-4", Type: "Bug", Summary: "Fix the sprocket", Status: "Triage", StatusCategory: "To Do", Priority: "Highest", Labels: []string{"Technical"}},
		{Key: "DCAI-5", Type: "Story", Summary: "Old shipped work", Status: "Released / Deployed", StatusCategory: "Done", Sprint: "KW28", Priority: "Lowest"},
		{Key: "DCAI-6", Type: "Task", Summary: "Patch the flange", Status: "Ready To Do", StatusCategory: "To Do", Priority: "High"},
		{Key: "DCAI-7", Type: "Task", Summary: "Priority-less oddity", Status: "Triage", StatusCategory: "To Do"},
	}}
}

// Every column-content assertion below drives the view with every filter turned
// off (#202, #203, #209), so the whole fixture — released work, In Progress work
// and Technical-labelled work included — renders. The defaults narrow to the
// not-started, non-technical slice, which is not what these tests are about.
func TestPrioListsEveryIssueWithTypeNameStatus(t *testing.T) {
	app := newTestApp(t, prioFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

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
	if n := strings.Count(body, `data-testid="prio-row"`); n != 7 {
		t.Errorf("prio rendered %d rows, want 7", n)
	}

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
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

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
	fragment := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)

	if strings.Contains(fragment, "<!DOCTYPE html>") {
		t.Errorf("/prio/results returned a full page, want a fragment\n%s", fragment)
	}
	if n := strings.Count(fragment, `data-testid="prio-row"`); n != 7 {
		t.Errorf("/prio/results rendered %d rows, want 7", n)
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

func TestPrioRendersPriorityColumnWithIconAndName(t *testing.T) {
	app := newTestApp(t, prioFixture())
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

	assertOrder(t, body,
		`data-testid="prio-col-name"`,
		`data-testid="prio-col-priority"`,
		`data-testid="prio-col-status"`,
	)

	wants := []string{
		`data-testid="prio:DCAI-2:priority-name">Highest<`,
		`data-testid="prio:DCAI-6:priority-name">High<`,
		`data-testid="prio:DCAI-1:priority-name">Medium<`,
		`data-testid="prio:DCAI-3:priority-name">Low<`,
		`data-testid="prio:DCAI-5:priority-name">Lowest<`,
		`data-testid="prio:DCAI-2:priority-icon"`,
		`data-testid="prio:DCAI-5:priority-icon"`,
		// The icon's colour rides in an inline style; html/template blanks an
		// unsafe CSS value to #ZgotmplZ, so assert the hex survives.
		`style="stroke:#E11D48"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("prio missing %q\n%s", w, body)
		}
	}

	// An issue with no priority (should not occur in DCAI) renders no icon.
	if strings.Contains(body, `data-testid="prio:DCAI-7:priority-icon"`) {
		t.Errorf("prio drew a severity icon for an issue with no priority\n%s", body)
	}
}

func TestPrioSortsHighestToLowestTiesByKey(t *testing.T) {
	app := newTestApp(t, prioFixture())

	for _, path := range []string{"/prio?" + prioEveryFilterOff, "/prio/results?" + prioEveryFilterOff} {
		body := get(t, app.URL+path)
		assertOrder(t, body,
			`data-key="DCAI-2"`, // Highest
			`data-key="DCAI-4"`, // Highest, tie broken by key
			`data-key="DCAI-6"`, // High
			`data-key="DCAI-1"`, // Medium
			`data-key="DCAI-3"`, // Low
			`data-key="DCAI-5"`, // Lowest
			`data-key="DCAI-7"`, // no priority sorts last
		)
	}
}

func TestPrioRendersLabelsColumnAsGreyPills(t *testing.T) {
	app := newTestApp(t, prioFixture())
	body := get(t, app.URL+"/prio?"+prioEveryFilterOff)

	assertOrder(t, body,
		`data-testid="prio-col-status"`,
		`data-testid="prio-col-labels"`,
	)

	wants := []string{
		// Every label of an issue renders, in Jira's order, including Technical.
		`data-testid="prio:DCAI-3:label:Technical">Technical<`,
		`data-testid="prio:DCAI-3:label:Product">Product<`,
		`data-testid="prio:DCAI-4:label:Technical">Technical<`,
		`data-testid="prio:DCAI-2:label:needs-design">needs-design<`,
		// Grey pill styling: grey fill, grey text.
		`bg-slate-100`,
		`text-slate-600`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("prio missing %q\n%s", w, body)
		}
	}

	assertOrder(t, body,
		`data-testid="prio:DCAI-3:label:Technical"`,
		`data-testid="prio:DCAI-3:label:Product"`,
	)

	// An issue with no labels renders an empty cell — the cell exists, but holds
	// no pills.
	cell := labelsCell(t, body, "DCAI-1")
	if strings.Contains(cell, "prio:DCAI-1:label:") {
		t.Errorf("prio rendered a label pill for an unlabelled issue: %q", cell)
	}
	if strings.TrimSpace(cell) != "" {
		t.Errorf("prio labels cell for an unlabelled issue is not empty: %q", cell)
	}
}

// labelsCell returns the inner HTML of one row's Labels cell, so a test can
// assert on what the cell does NOT contain.
func labelsCell(t *testing.T, body, key string) string {
	t.Helper()
	open := `data-testid="prio:` + key + `:labels">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("no labels cell for %s\n%s", key, body)
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</td>")
	if j < 0 {
		t.Fatalf("unterminated labels cell for %s", key)
	}
	return rest[:j]
}

func TestPrioResultsFragmentCarriesLabels(t *testing.T) {
	app := newTestApp(t, prioFixture())
	fragment := get(t, app.URL+"/prio/results?"+prioEveryFilterOff)

	if !strings.Contains(fragment, `data-testid="prio:DCAI-4:label:Technical">Technical<`) {
		t.Errorf("/prio/results missing label pills\n%s", fragment)
	}
}
