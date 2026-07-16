package store

// Projection-level tests for the "Daily" rollup: active-sprint Task/Bug/Story
// tickets that had one or more status transitions within a [from, to) window,
// each carrying its in-window status changes, filtered by assignee.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// saveDaily saves an active-sprint issue with the given assignee plus its status
// transitions (all field='status').
func saveDaily(t *testing.T, st *Store, key, typ, assignee string, activeSprint bool, xs ...xition) {
	t.Helper()
	cl := make([]jira.ChangelogEntry, len(xs))
	for i, x := range xs {
		cl[i] = jira.ChangelogEntry{ID: x.id, Field: "status", From: x.from, To: x.to, Timestamp: x.at}
	}
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: "In Progress",
		StatusCategory: "In Progress", Size: "M", Assignee: assignee, Changelog: cl,
	}
	if activeSprint {
		iss.ActiveSprint = "KW29"
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

func TestDailyStatusChangesFiltersWindowAndScope(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 12, 0, 0, 0, loc)

	// In window, alice: two transitions.
	saveDaily(t, st, "DCAI-1", "Story", "alice", true,
		xition{"t1a", "Ready to Do", "In Progress", at(15, 9)},
		xition{"t1b", "In Progress", "Review / Testing", at(16, 8)},
	)
	// In window, bob.
	saveDaily(t, st, "DCAI-2", "Task", "bob", true,
		xition{"t2", "Refinement", "Ready to Do", at(15, 14)})
	// Out of window (before from).
	saveDaily(t, st, "DCAI-3", "Bug", "alice", true,
		xition{"t3", "Ready to Do", "In Progress", at(10, 9)})
	// Non-active-sprint, in window — excluded.
	saveDaily(t, st, "DCAI-4", "Story", "alice", false,
		xition{"t4", "Ready to Do", "In Progress", at(15, 10)})
	// Excluded types (Epic, Sub-task) even in window + active sprint.
	saveDaily(t, st, "DCAI-5", "Epic", "alice", true,
		xition{"t5", "Ready to Do", "In Progress", at(15, 11)})
	saveDaily(t, st, "DCAI-6", "Sub-task", "alice", true,
		xition{"t6", "Ready to Do", "In Progress", at(15, 11)})

	// All assignees.
	got, err := st.DailyStatusChanges("", from, to)
	if err != nil {
		t.Fatalf("daily all: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("all: want 2 tickets, got %d: %+v", len(got), got)
	}
	// Sorted most-recent-first: DCAI-1's latest change is 16 Jul 08:00, DCAI-2 is
	// 15 Jul 14:00.
	if got[0].Key != "DCAI-1" || got[1].Key != "DCAI-2" {
		t.Fatalf("all: wrong order/keys: %+v", got)
	}
	if len(got[0].Changes) != 2 {
		t.Fatalf("DCAI-1 should carry both in-window changes, got %d", len(got[0].Changes))
	}
	if got[0].Changes[0].From != "Ready to Do" || got[0].Changes[0].To != "In Progress" {
		t.Errorf("DCAI-1 first change wrong: %+v", got[0].Changes[0])
	}
	if got[0].Changes[1].To != "Review / Testing" {
		t.Errorf("DCAI-1 second change wrong: %+v", got[0].Changes[1])
	}

	// Specific assignee.
	gotAlice, err := st.DailyStatusChanges("alice", from, to)
	if err != nil {
		t.Fatalf("daily alice: %v", err)
	}
	if len(gotAlice) != 1 || gotAlice[0].Key != "DCAI-1" {
		t.Fatalf("alice: want only DCAI-1, got %+v", gotAlice)
	}
}

func TestDailyStatusChangesUnassigned(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 12, 0, 0, 0, loc)
	at := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)

	saveDaily(t, st, "DCAI-1", "Story", "alice", true, xition{"t1", "Ready to Do", "In Progress", at})
	saveDaily(t, st, "DCAI-2", "Task", "", true, xition{"t2", "Ready to Do", "In Progress", at})

	got, err := st.DailyStatusChanges(UnassignedAssignee, from, to)
	if err != nil {
		t.Fatalf("daily unassigned: %v", err)
	}
	if len(got) != 1 || got[0].Key != "DCAI-2" {
		t.Fatalf("unassigned: want only DCAI-2, got %+v", got)
	}
}

func TestActiveSprintAssigneesDistinctAndScoped(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	at := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)

	saveDaily(t, st, "DCAI-1", "Story", "alice", true, xition{"t1", "Ready to Do", "In Progress", at})
	saveDaily(t, st, "DCAI-2", "Task", "bob", true, xition{"t2", "Ready to Do", "In Progress", at})
	saveDaily(t, st, "DCAI-3", "Bug", "alice", true, xition{"t3", "Ready to Do", "In Progress", at})
	// Unassigned active-sprint ticket — must NOT appear as a named assignee.
	saveDaily(t, st, "DCAI-4", "Story", "", true, xition{"t4", "Ready to Do", "In Progress", at})
	// Non-active-sprint assignee — excluded.
	saveDaily(t, st, "DCAI-5", "Story", "carol", false, xition{"t5", "Ready to Do", "In Progress", at})
	// Excluded type.
	saveDaily(t, st, "DCAI-6", "Epic", "dave", true, xition{"t6", "Ready to Do", "In Progress", at})

	got, err := st.ActiveSprintAssignees()
	if err != nil {
		t.Fatalf("assignees: %v", err)
	}
	want := []string{"alice", "bob"}
	if len(got) != len(want) {
		t.Fatalf("assignees: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("assignees: want %v, got %v", want, got)
		}
	}
}
