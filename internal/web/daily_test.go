package web_test

// Integration tests for the "Daily" standup view over the HTTP seam.
//
// Fixtures are active-sprint tickets whose `status` transitions fall inside or
// outside the two windows ("Last 24h" and "Since yesterday"), across assignees
// (incl. Unassigned). A fixed clock pins "now" so both windows resolve
// deterministically. Tests drive the real handlers and assert on rendered HTML.

import (
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// dailyIssue builds an active-sprint (unless active=false) Task/Bug/Story with a
// single status transition at `at`.
func dailyIssue(key, typ, assignee string, active bool, from, to string, at time.Time) jira.Issue {
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: to,
		StatusCategory: "In Progress", Size: "M", Assignee: assignee,
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: from, To: to, Timestamp: at},
		},
	}
	if active {
		iss.ActiveSprint = "KW29"
	}
	return iss
}

// dailyFixture pins the transitions relative to now = Thu 2026-07-16 10:00 Berlin.
//
//	Last 24h        = [2026-07-15 10:00, 2026-07-16 10:00)
//	Since yesterday = [2026-07-15 00:00, 2026-07-16 10:00)
func dailyFixture(t *testing.T) (*jira.FakeClient, time.Time) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		// Inside both windows.
		dailyIssue("DCAI-1", "Story", "alice", true, "In Progress", "Review / Testing", at(16, 8)),
		dailyIssue("DCAI-2", "Task", "bob", true, "Refinement", "Ready to Do", at(15, 11)),
		dailyIssue("DCAI-4", "Bug", "", true, "Ready to Do", "In Progress", at(16, 9)),
		// Only inside "Since yesterday" (before the rolling 24h start of 15 10:00).
		dailyIssue("DCAI-3", "Story", "alice", true, "Refinement", "Ready to Do", at(15, 6)),
		// Outside both windows.
		dailyIssue("DCAI-5", "Story", "alice", true, "Ready to Do", "In Progress", at(13, 9)),
		// Excluded by scope: recent change but not in the active sprint.
		dailyIssue("DCAI-6", "Story", "carol", false, "Ready to Do", "In Progress", at(16, 9)),
		// Excluded types.
		dailyIssue("DCAI-7", "Epic", "alice", true, "Ready to Do", "In Progress", at(16, 9)),
		dailyIssue("DCAI-8", "Sub-task", "alice", true, "Ready to Do", "In Progress", at(16, 9)),
	}}, now
}

func TestDailyAllAssigneesLast24h(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily") // default: All + Last 24h

	for _, key := range []string{"DCAI-1", "DCAI-2", "DCAI-4"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Last 24h/All should include %s\n%s", key, body)
		}
	}
	for _, key := range []string{"DCAI-3", "DCAI-5", "DCAI-6", "DCAI-7", "DCAI-8"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Last 24h/All should NOT include %s", key)
		}
	}
	// From → To change and its Berlin timestamp render.
	if !strings.Contains(body, "In Progress → Review / Testing") {
		t.Errorf("DCAI-1 change (From → To) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "08:00") {
		t.Errorf("DCAI-1 change timestamp (Berlin) not rendered:\n%s", body)
	}
	// Cards sorted most-recent-first: DCAI-4 (09:00) before DCAI-1 (08:00) before
	// DCAI-2 (15 Jul 11:00).
	assertOrder(t, body, `data-key="DCAI-4"`, `data-key="DCAI-1"`, `data-key="DCAI-2"`)
}

func TestDailySpecificAssignee(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=alice&window=last-24h")

	if !strings.Contains(body, `data-key="DCAI-1"`) {
		t.Errorf("alice/Last 24h should include DCAI-1\n%s", body)
	}
	for _, key := range []string{"DCAI-2", "DCAI-3", "DCAI-4"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("alice/Last 24h should NOT include %s", key)
		}
	}
}

func TestDailySinceYesterdayWidensSet(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=all&window=since-yesterday")

	// DCAI-3 (15 Jul 06:00) is only inside "Since yesterday".
	for _, key := range []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Since yesterday/All should include %s\n%s", key, body)
		}
	}
	if strings.Contains(body, `data-key="DCAI-5"`) {
		t.Errorf("Since yesterday should still exclude the out-of-window DCAI-5")
	}
}

func TestDailyUnassigned(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=unassigned&window=last-24h")

	if !strings.Contains(body, `data-key="DCAI-4"`) {
		t.Errorf("Unassigned/Last 24h should include DCAI-4\n%s", body)
	}
	for _, key := range []string{"DCAI-1", "DCAI-2"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Unassigned/Last 24h should NOT include assigned %s", key)
		}
	}
}

func TestDailyEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	// One active-sprint ticket, but its only change is well out of both windows.
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		dailyIssue("DCAI-1", "Story", "alice", true, "Ready to Do", "In Progress",
			time.Date(2026, time.July, 1, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/daily/results?assignee=all&window=last-24h")
	if !strings.Contains(body, `data-testid="daily-empty"`) {
		t.Errorf("expected friendly empty state when nothing matches:\n%s", body)
	}
	if strings.Contains(body, `data-key="DCAI-1"`) {
		t.Errorf("out-of-window ticket must not render")
	}
}

func TestDailyNoActiveSprintEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		dailyIssue("DCAI-1", "Story", "alice", false, "Ready to Do", "In Progress",
			time.Date(2026, time.July, 16, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-testid="daily-no-sprint"`) {
		t.Errorf("expected friendly no-active-sprint state:\n%s", body)
	}
}

// TestDailyPanelKeepsSelectionAfterSwap is the regression guard (cf. the
// Completed picker): the swapped panel must re-render with the selected assignee
// and window marked, not reset to the defaults.
func TestDailyPanelKeepsSelectionAfterSwap(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=bob&window=since-yesterday")

	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Fatalf("results endpoint must return a partial, got full document:\n%s", body)
	}
	// The controls are part of the swapped fragment.
	if !strings.Contains(body, `data-testid="daily-assignee"`) {
		t.Fatalf("results fragment must include the controls so they re-render:\n%s", body)
	}
	// The selected assignee stays selected.
	if !strings.Contains(body, `value="bob" selected`) {
		t.Errorf("bob should stay selected in the assignee dropdown:\n%s", body)
	}
	// The selected window stays checked; the other does not.
	if !windowChecked(body, "since-yesterday") {
		t.Errorf("since-yesterday window should stay selected:\n%s", body)
	}
	if windowChecked(body, "last-24h") {
		t.Errorf("last-24h window should NOT be selected after choosing since-yesterday")
	}
}

// windowChecked reports whether the window radio for the given key is checked.
func windowChecked(html, key string) bool {
	marker := `data-testid="daily-window:` + key + `"`
	start := strings.Index(html, marker)
	if start == -1 {
		return false
	}
	end := strings.Index(html[start:], ">")
	if end == -1 {
		return false
	}
	return strings.Contains(html[start:start+end], " checked")
}

func TestDailyNavActive(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-nav="daily" aria-current="page"`) {
		t.Errorf("/daily should mark the Daily nav item active:\n%s", body)
	}
	if n := strings.Count(body, `aria-current="page"`); n != 1 {
		t.Errorf("/daily: expected exactly one active nav item, got %d", n)
	}
}
