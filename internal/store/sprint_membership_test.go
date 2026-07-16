package store

// Integration test for sprint-membership history: a jira.FakeClient carrying
// Sprint-field changelog changes is synced through the real sync.Backfill into a
// temp store, and membership at past instants is reconstructed the same way
// status history is.
//
// Covers the ticket's required cases:
//   - a ticket PRESENT FROM THE START (entered the active sprint at/before its
//     activation instant) is a member at activation
//   - a ticket ADDED MID-SPRINT (entered after activation) is NOT a member at
//     activation, but is a member afterwards
//   - the entry instant is derivable (SprintEntry)
//   - membership replays "left" transitions (a ticket that entered then left is
//     not a member afterwards)
//   - backfill populates history retroactively; re-sync dedups (no dup rows)
//   - the active sprint's id is reconciled onto the window (ActiveSprintWindow)

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func TestSyncReconstructsSprintMembershipAtInstant(t *testing.T) {
	activation := time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)
	beforeActivation := time.Date(2026, time.July, 13, 6, 0, 0, 0, time.UTC)
	midSprintEntry := time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)
	leftInstant := time.Date(2026, time.July, 14, 8, 0, 0, 0, time.UTC)
	afterAll := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)

	const sprintID = 29

	fake := &jira.FakeClient{
		Sprints: []jira.Sprint{
			{ID: sprintID, Name: "KW29", State: "active", ActivatedAt: activation},
		},
		Issues: []jira.Issue{
			{
				Key: "DCAI-100", Type: "Story", Summary: "present from the start",
				Status: "In Progress", StatusCategory: "In Progress", ActiveSprint: "KW29",
				SprintChanges: []jira.SprintMembershipChange{
					{EntryID: "e100", SprintID: sprintID, SprintName: "KW29", Entered: true, Timestamp: beforeActivation},
				},
			},
			{
				Key: "DCAI-200", Type: "Task", Summary: "added mid-sprint",
				Status: "Ready To Do", StatusCategory: "To Do", ActiveSprint: "KW29",
				SprintChanges: []jira.SprintMembershipChange{
					{EntryID: "e200", SprintID: sprintID, SprintName: "KW29", Entered: true, Timestamp: midSprintEntry},
				},
			},
			{
				Key: "DCAI-300", Type: "Bug", Summary: "entered then left",
				Status: "Refinement", StatusCategory: "To Do",
				SprintChanges: []jira.SprintMembershipChange{
					{EntryID: "e300a", SprintID: sprintID, SprintName: "KW29", Entered: true, Timestamp: beforeActivation},
					{EntryID: "e300b", SprintID: sprintID, SprintName: "KW29", Entered: false, Timestamp: leftInstant},
				},
			},
		},
	}

	st := openTempStore(t)
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Membership rows persisted retroactively from the changelog: 1 + 1 + 2 = 4.
	if got := countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition"); got != 4 {
		t.Fatalf("membership rows = %d, want 4", got)
	}

	// The active sprint's id is reconciled onto the window (name was the only key
	// on the per-issue snapshot; the entity's id keys the history).
	win, ok, err := st.ActiveSprintWindow()
	if err != nil || !ok {
		t.Fatalf("ActiveSprintWindow ok=%v err=%v", ok, err)
	}
	if win.ID != sprintID {
		t.Fatalf("active sprint id = %d, want %d", win.ID, sprintID)
	}

	// At activation: present-from-start and entered-then-left (still in) are
	// members; added-mid is NOT yet.
	assertMembers(t, st, sprintID, activation, []string{"DCAI-100", "DCAI-300"})

	// After the added-mid entry and after DCAI-300 left: present-from-start and
	// added-mid are members; entered-then-left is gone.
	assertMembers(t, st, sprintID, afterAll, []string{"DCAI-100", "DCAI-200"})

	// The entry instant is derivable.
	assertEntry(t, st, sprintID, "DCAI-100", beforeActivation)
	assertEntry(t, st, sprintID, "DCAI-200", midSprintEntry)

	// A never-member issue has no entry.
	if _, ok, err := st.SprintEntry(sprintID, "DCAI-999"); err != nil || ok {
		t.Fatalf("SprintEntry for never-member: ok=%v err=%v, want ok=false", ok, err)
	}

	// Started-with vs Added (what the Weekly view derives): started-with =
	// members at activation; added = members later that were not members at
	// activation.
	startedWith, err := st.IssuesInSprintAt(sprintID, activation)
	if err != nil {
		t.Fatalf("IssuesInSprintAt: %v", err)
	}
	if got := contains(startedWith, "DCAI-200"); got {
		t.Fatalf("DCAI-200 (added mid-sprint) must not be in the started-with set")
	}
	if got := contains(startedWith, "DCAI-100"); !got {
		t.Fatalf("DCAI-100 (present from start) must be in the started-with set")
	}

	// Re-sync dedups: no duplicate membership rows.
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("re-backfill: %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition"); got != 4 {
		t.Fatalf("after re-sync membership rows = %d, want 4 (dedup failed)", got)
	}
}

func assertMembers(t *testing.T, st *Store, sprintID int, at time.Time, want []string) {
	t.Helper()
	got, err := st.IssuesInSprintAt(sprintID, at)
	if err != nil {
		t.Fatalf("IssuesInSprintAt(%d, %v): %v", sprintID, at, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("members at %v = %v, want %v", at, got, want)
	}
}

func assertEntry(t *testing.T, st *Store, sprintID int, key string, want time.Time) {
	t.Helper()
	got, ok, err := st.SprintEntry(sprintID, key)
	if err != nil || !ok {
		t.Fatalf("SprintEntry(%d, %s) ok=%v err=%v", sprintID, key, ok, err)
	}
	if !got.UTC().Equal(want) {
		t.Fatalf("SprintEntry(%d, %s) = %v, want %v", sprintID, key, got.UTC(), want)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
