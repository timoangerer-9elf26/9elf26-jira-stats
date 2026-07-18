package store

// Projection-level tests for synthetic sprint-membership entries (#55): a ticket
// created directly into a sprint has its Sprint field set at creation and so
// carries NO "Sprint" changelog item — nothing enters it into
// sprint_membership_transition. The store must synthesize an entry (at the
// issue's created instant) for the active sprint it currently belongs to, so
// membership history stops undercounting created-into-sprint tickets, while a
// normally moved-in ticket keeps its real changelog-derived entry untouched.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestCreatedIntoSprintSynthesizesMembershipFromCreated(t *testing.T) {
	st := openTempStore(t)

	const sprintID = 30
	sprintStart := time.Date(2026, time.July, 20, 7, 0, 0, 0, time.UTC)
	created := time.Date(2026, time.July, 21, 9, 0, 0, 0, time.UTC)

	if err := st.SaveSprint(jira.Sprint{ID: sprintID, Name: "KW30", State: "active", ActivatedAt: sprintStart}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Created directly into KW30: current active sprint is KW30, but there is no
	// Sprint changelog item, so SprintChanges is empty.
	created2 := jira.Issue{
		Key: "DCAI-CREATED", Type: "Task", Summary: "born in the sprint", Status: "In Progress",
		StatusCategory: "In Progress", Size: "M",
		ActiveSprint: "KW30", ActiveSprintID: sprintID, CreatedAt: created,
	}
	if err := st.SaveIssue(created2, "2026-07-21T10:00:00Z"); err != nil {
		t.Fatalf("save created-into-sprint issue: %v", err)
	}

	// It is reconstructable as a member from its created instant onward…
	members := membersAt(t, st, sprintID, created)
	if !contains(members, "DCAI-CREATED") {
		t.Errorf("created-into-sprint ticket missing from membership at its created instant: %v", members)
	}
	// …but not before it existed.
	if before := membersAt(t, st, sprintID, created.Add(-time.Hour)); contains(before, "DCAI-CREATED") {
		t.Errorf("created-into-sprint ticket is a member before it was created: %v", before)
	}
	// The synthetic entry instant is the issue's created time.
	if at, ok, err := st.SprintEntry(sprintID, "DCAI-CREATED"); err != nil || !ok {
		t.Fatalf("no sprint entry recorded (ok=%v err=%v)", ok, err)
	} else if !at.Equal(created) {
		t.Errorf("synthetic entry instant = %v, want created %v", at, created)
	}

	// Re-sync is idempotent: exactly one membership row, no duplicate.
	if err := st.SaveIssue(created2, "2026-07-21T11:00:00Z"); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition WHERE issue_key='DCAI-CREATED'"); n != 1 {
		t.Errorf("expected exactly one membership row after re-sync, got %d", n)
	}
}

func TestNormallyMovedInTicketKeepsRealEntry(t *testing.T) {
	st := openTempStore(t)

	const sprintID = 30
	sprintStart := time.Date(2026, time.July, 20, 7, 0, 0, 0, time.UTC)
	created := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	movedIn := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)

	if err := st.SaveSprint(jira.Sprint{ID: sprintID, Name: "KW30", State: "active", ActivatedAt: sprintStart}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Moved into KW30 via a real Sprint changelog item after creation. The store
	// must NOT synthesize a second (created-time) entry for the same sprint.
	moved := jira.Issue{
		Key: "DCAI-MOVED", Type: "Task", Summary: "pulled in", Status: "In Progress",
		StatusCategory: "In Progress", Size: "S",
		ActiveSprint: "KW30", ActiveSprintID: sprintID, CreatedAt: created,
		SprintChanges: []jira.SprintMembershipChange{
			{EntryID: "real1", SprintID: sprintID, SprintName: "KW30", Entered: true, Timestamp: movedIn},
		},
	}
	if err := st.SaveIssue(moved, "2026-07-22T10:00:00Z"); err != nil {
		t.Fatalf("save moved-in issue: %v", err)
	}

	if n := countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition WHERE issue_key='DCAI-MOVED'"); n != 1 {
		t.Errorf("expected exactly one (real) membership row, got %d (synthetic duplicated a real entry?)", n)
	}
	// The entry instant is the real move-in, not the earlier created time.
	if at, ok, err := st.SprintEntry(sprintID, "DCAI-MOVED"); err != nil || !ok {
		t.Fatalf("no sprint entry recorded (ok=%v err=%v)", ok, err)
	} else if !at.Equal(movedIn) {
		t.Errorf("entry instant = %v, want the real move-in %v (synthetic must not override it)", at, movedIn)
	}
}

// TestCreatedIntoSprintClosesMembershipGap is AC3: for a sprint containing
// created-into-sprint tickets, the reconstructed membership-history count equals
// the snapshot count (no gap). Two tickets are created directly into KW30 (no
// changelog) and one is moved in normally; all three currently belong to KW30, so
// membership reconstructed at now must return all three — matching the snapshot.
func TestCreatedIntoSprintClosesMembershipGap(t *testing.T) {
	st := openTempStore(t)

	const sprintID = 30
	start := time.Date(2026, time.July, 20, 7, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if err := st.SaveSprint(jira.Sprint{ID: sprintID, Name: "KW30", State: "active", ActivatedAt: start}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	save := func(iss jira.Issue) {
		t.Helper()
		if err := st.SaveIssue(iss, "2026-07-23T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", iss.Key, err)
		}
	}
	// Two created directly into KW30 (no Sprint changelog item)…
	save(jira.Issue{Key: "DCAI-1733", Type: "Task", Summary: "born", Status: "In Progress",
		StatusCategory: "In Progress", ActiveSprint: "KW30", ActiveSprintID: sprintID,
		CreatedAt: time.Date(2026, time.July, 21, 9, 0, 0, 0, time.UTC)})
	save(jira.Issue{Key: "DCAI-1734", Type: "Task", Summary: "born too", Status: "In Progress",
		StatusCategory: "In Progress", ActiveSprint: "KW30", ActiveSprintID: sprintID,
		CreatedAt: time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)})
	// …and one moved in via a real changelog entry.
	save(jira.Issue{Key: "DCAI-100", Type: "Task", Summary: "pulled in", Status: "In Progress",
		StatusCategory: "In Progress", ActiveSprint: "KW30", ActiveSprintID: sprintID,
		CreatedAt:     time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC),
		SprintChanges: []jira.SprintMembershipChange{{EntryID: "r1", SprintID: sprintID, SprintName: "KW30", Entered: true, Timestamp: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}}})

	snapshot := countRows(t, st, "SELECT COUNT(*) FROM issue WHERE active_sprint = 'KW30'")
	history := len(membersAt(t, st, sprintID, now))
	if history != snapshot {
		t.Errorf("membership-history count %d != snapshot count %d (gap remains)", history, snapshot)
	}
	if snapshot != 3 {
		t.Fatalf("fixture snapshot count = %d, want 3", snapshot)
	}
}

// membersAt returns the sprint members at an instant, via the public rollup.
func membersAt(t *testing.T, st *Store, sprintID int, at time.Time) []string {
	t.Helper()
	keys, err := st.IssuesInSprintAt(sprintID, at)
	if err != nil {
		t.Fatalf("IssuesInSprintAt: %v", err)
	}
	return keys
}
