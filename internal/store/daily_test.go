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

// TestDailyTicketMovement pins the net-movement bucketing (the Daily digest
// seam): each moved ticket lands in exactly one of Finished / Advanced / Pulled
// back, computed from its in-window changes alone.
func TestDailyTicketMovement(t *testing.T) {
	at := time.Date(2026, time.July, 16, 9, 0, 0, 0, time.UTC)
	// mk builds a moved ticket from a sequence of (from, to) status pairs.
	mk := func(pairs ...[2]string) DailyTicket {
		var ch []DailyStatusChange
		for _, p := range pairs {
			ch = append(ch, DailyStatusChange{From: p[0], To: p[1], TransitionedAt: at})
		}
		return DailyTicket{Changes: ch}
	}
	tests := []struct {
		name string
		t    DailyTicket
		want DailyMovement
	}{
		{"net forward is advanced", mk([2]string{"Ready To Do", "In Progress"}), MovementAdvanced},
		{"crossing into done is finished", mk([2]string{"Review / Testing", "DONE (This Sprint)"}), MovementFinished},
		{"finished regardless of intermediate hops", mk(
			[2]string{"Ready To Do", "In Progress"},
			[2]string{"In Progress", "DONE (This Sprint)"},
			[2]string{"DONE (This Sprint)", "Review / Testing"},
		), MovementFinished},
		{"a move between two done statuses is not a crossing", mk(
			[2]string{"DONE (This Sprint)", "Released / Deployed"},
		), MovementAdvanced},
		{"net backward is pulled back", mk([2]string{"Review / Testing", "In Progress"}), MovementPulledBack},
		{"a move to canceled is pulled back", mk([2]string{"In Progress", "Canceled"}), MovementPulledBack},
		{"net-zero churn is advanced", mk(
			[2]string{"Ready To Do", "In Progress"},
			[2]string{"In Progress", "Ready To Do"},
		), MovementAdvanced},
		// Jira casing quirk ("Ready to Do") must not change the bucket.
		{"casing quirks still classify", mk([2]string{"Refinement", "Ready to Do"}), MovementAdvanced},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.t.Movement(); got != tc.want {
				t.Errorf("Movement() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDailyStatusChangesDropsIntraDoneChanges pins issue #98: on the Daily view
// a status transition whose from AND to are both in the done set is dropped
// before the movement/net/appearance computation, so buckets, net From⟶To and
// whether a ticket appears all derive from the remaining changes.
func TestDailyStatusChangesDropsIntraDoneChanges(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	at := func(hour, min int) time.Time { return time.Date(2026, time.July, 15, hour, min, 0, 0, loc) }
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 0, 0, 0, 0, loc)

	// In Progress → DONE → Released: the DONE→Released hop is intra-done and is
	// dropped; the net is In Progress ⟶ DONE (This Sprint), bucketed Finished.
	saveDaily(t, st, "DCAI-1", "Story", "alice", true,
		xition{"t1a", "In Progress", "DONE (This Sprint)", at(9, 0)},
		xition{"t1b", "DONE (This Sprint)", "Released / Deployed", at(10, 0)},
	)
	// Only intra-done moves (DONE → Ready for Release → Released): every change is
	// dropped, so the ticket disappears from Daily entirely.
	saveDaily(t, st, "DCAI-2", "Task", "alice", true,
		xition{"t2a", "DONE (This Sprint)", "Ready for Release", at(9, 0)},
		xition{"t2b", "Ready for Release", "Released / Deployed", at(10, 0)},
	)
	// Reopen: a done → non-done move is kept (shown as a pull-back).
	saveDaily(t, st, "DCAI-3", "Bug", "alice", true,
		xition{"t3", "Ready for Release", "In Progress", at(11, 0)},
	)
	// Finish crossing: non-done → done is kept and stays Finished.
	saveDaily(t, st, "DCAI-4", "Story", "alice", true,
		xition{"t4", "Review / Testing", "DONE (This Sprint)", at(12, 0)},
	)

	got, err := st.DailyStatusChanges("", from, to)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}

	byKey := map[string]DailyTicket{}
	for _, tk := range got {
		byKey[tk.Key] = tk
	}

	// DCAI-2 vanished — its only moves were inside the done set.
	if _, ok := byKey["DCAI-2"]; ok {
		t.Errorf("DCAI-2 (only intra-done moves) must not appear on Daily")
	}
	if len(got) != 3 {
		t.Fatalf("want 3 tickets (DCAI-1/3/4), got %d: %+v", len(got), got)
	}

	// DCAI-1: intra-done hop dropped, net In Progress ⟶ DONE (This Sprint), Finished.
	one := byKey["DCAI-1"]
	if len(one.Changes) != 1 {
		t.Fatalf("DCAI-1: want 1 remaining change, got %d: %+v", len(one.Changes), one.Changes)
	}
	if one.StartStatus() != "In Progress" || one.EndStatus() != "DONE (This Sprint)" {
		t.Errorf("DCAI-1 net: want In Progress ⟶ DONE (This Sprint), got %s ⟶ %s", one.StartStatus(), one.EndStatus())
	}
	if one.Movement() != MovementFinished {
		t.Errorf("DCAI-1 movement: want Finished, got %v", one.Movement())
	}

	// DCAI-3: reopen kept, bucketed Pulled back.
	if three := byKey["DCAI-3"]; three.Movement() != MovementPulledBack {
		t.Errorf("DCAI-3 movement: want Pulled back, got %v", three.Movement())
	}

	// DCAI-4: finish crossing kept, Finished.
	if four := byKey["DCAI-4"]; four.Movement() != MovementFinished {
		t.Errorf("DCAI-4 movement: want Finished, got %v", four.Movement())
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
