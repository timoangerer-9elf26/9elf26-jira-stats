package web_test

// Integration tests for the Sprint cell drill-down (#79) over the HTTP seam:
// each non-zero cell's "N tickets" text links to /sprint/cell?row=&col=, which
// lists exactly those tickets as Board-style cards (with a status pill) in a
// flat, workflow-sorted list. Zero cells are not linked; unknown cells 404.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// drillFixtureApp builds the DCAI-1..4 fixture (same shape as
// TestSprintTableCoversAllCategories): DCAI-1 started+finished (M),
// DCAI-2 started+open (L), DCAI-3 added+open (S), DCAI-4 added+finished (M).
func drillFixtureApp(t *testing.T) *testApp {
	t.Helper()
	loc := berlin(t)
	now := time.Date(2026, time.July, 17, 18, 0, 0, 0, loc)
	beforeStart := time.Date(2026, time.July, 12, 0, 0, 0, 0, loc)
	afterStart := time.Date(2026, time.July, 14, 10, 0, 0, 0, loc)
	fri := time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)

	return newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			sprintIssue("DCAI-1", "M", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("s1a", "Ready To Do", "In Progress", beforeStart),
					sprintStatus("s1b", "In Progress", "DONE (This Sprint)", fri),
				},
				[]jira.SprintMembershipChange{enteredSprintID("m1", 29, "KW29", beforeStart)}),
			sprintIssue("DCAI-2", "L", "In Progress", "KW29",
				[]jira.ChangelogEntry{sprintStatus("s2a", "Ready To Do", "In Progress", beforeStart)},
				[]jira.SprintMembershipChange{enteredSprintID("m2", 29, "KW29", beforeStart)}),
			sprintIssue("DCAI-3", "S", "Ready To Do", "KW29",
				nil,
				[]jira.SprintMembershipChange{enteredSprintID("m3", 29, "KW29", afterStart)}),
			sprintIssue("DCAI-4", "M", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("s4a", "Ready To Do", "In Progress", afterStart),
					sprintStatus("s4b", "In Progress", "DONE (This Sprint)", fri),
				},
				[]jira.SprintMembershipChange{enteredSprintID("m4", 29, "KW29", afterStart)}),
		},
	}, now)
}

// TestSprintCellNonZeroCellsLinkZeroCellsDoNot asserts a non-zero cell's tickets
// text is a link to the drill page, while a zero cell (started:removed) is not.
func TestSprintCellNonZeroCellsLinkZeroCellsDoNot(t *testing.T) {
	app := drillFixtureApp(t)
	body := get(t, app.URL+"/sprint/results")

	for _, want := range []string{
		`data-testid="sprint-cell:started:total:link"`,
		`href="/sprint/cell?row=started&amp;col=total"`,
		`data-testid="sprint-cell:total:total:link"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("non-zero cell must link; missing %q\n%s", want, body)
		}
	}
	// started:removed has 0 tickets in this fixture → no link.
	if strings.Contains(body, `data-testid="sprint-cell:started:removed:link"`) {
		t.Errorf("zero cell must not be linked (started:removed)\n%s", body)
	}
}

// TestSprintCellPageListsExactTicketsAsCards drills into the Started-with Total
// cell and asserts it lists exactly DCAI-1 and DCAI-2 as Board-style cards, each
// with a status pill, plus the heading, count and back link.
func TestSprintCellPageListsExactTicketsAsCards(t *testing.T) {
	app := drillFixtureApp(t)
	body := get(t, app.URL+"/sprint/cell?row=started&col=total")

	for _, want := range []string{
		`<!DOCTYPE`,
		`data-testid="sprint-cell-back"`,
		`href="/sprint"`,
		`data-testid="sprint-cell-heading"`,
		`Started with`,
		`data-testid="sprint-cell-count">2<`,
		`data-testid="board-card" data-key="DCAI-1"`,
		`data-testid="board-card" data-key="DCAI-2"`,
		// Status pill per card.
		`data-testid="card:DCAI-1:status">DONE (This Sprint)<`,
		`data-testid="card:DCAI-2:status">In Progress<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("drill page missing %q\n%s", want, body)
		}
	}
	// The cell has only these two tickets — the Added-only ones must not appear.
	for _, absent := range []string{`data-key="DCAI-3"`, `data-key="DCAI-4"`} {
		if strings.Contains(body, absent) {
			t.Errorf("drill page leaked a ticket outside the cell: %q", absent)
		}
	}
	// A flat list, not the Board's status columns.
	if strings.Contains(body, `data-testid="board-column"`) {
		t.Errorf("drill page must be a flat list, not status columns")
	}
}

// TestSprintCellFlatListSortedByWorkflow asserts the Total×Total drill lists all
// four tickets sorted by workflow order (open statuses before Done), so same-
// status cards group.
func TestSprintCellFlatListSortedByWorkflow(t *testing.T) {
	app := drillFixtureApp(t)
	body := get(t, app.URL+"/sprint/cell?row=total&col=total")

	if !strings.Contains(body, `data-testid="sprint-cell-count">4<`) {
		t.Errorf("Total×Total drill must list all 4 tickets\n%s", body)
	}
	// Workflow order: Ready To Do (DCAI-3) < In Progress (DCAI-2) < DONE (DCAI-1, DCAI-4).
	order := []string{`data-key="DCAI-3"`, `data-key="DCAI-2"`, `data-key="DCAI-1"`, `data-key="DCAI-4"`}
	prev := -1
	for _, k := range order {
		i := strings.Index(body, k)
		if i < 0 {
			t.Fatalf("missing %q in drill list\n%s", k, body)
		}
		if i < prev {
			t.Errorf("drill list not sorted by workflow order at %q", k)
		}
		prev = i
	}
}

// TestSprintCellUnknownCell404s asserts an unknown row/col yields 404.
func TestSprintCellUnknownCell404s(t *testing.T) {
	app := drillFixtureApp(t)
	for _, url := range []string{
		app.URL + "/sprint/cell?row=bogus&col=open",
		app.URL + "/sprint/cell?row=started&col=bogus",
		app.URL + "/sprint/cell",
	} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", url, resp.StatusCode)
		}
	}
}
