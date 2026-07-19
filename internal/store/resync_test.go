package store

// Unit test for Reset — the projection wipe that backs the full-resync button
// (#52). Reset must empty every projection table AND clear the last_sync
// bookkeeping, so a subsequent sync cycle sees a cold store and re-backfills
// from scratch.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestResetClearsProjectionAndLastSync(t *testing.T) {
	st := openTempStore(t)

	// Seed every projection table plus the last_sync meta row.
	iss := jira.Issue{
		Key: "DCAI-1", Type: "Story", Summary: "Seed", Status: "In Progress",
		StatusCategory: "In Progress", Size: "M", ActiveSprint: "KW29",
		Changelog: []jira.ChangelogEntry{
			{ID: "t1", Field: "status", From: "Ready To Do", To: "In Progress",
				Timestamp: time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)},
		},
		SprintChanges: []jira.SprintMembershipChange{
			{EntryID: "s1", SprintID: 29, SprintName: "KW29", Entered: true,
				Timestamp: time.Date(2026, 7, 13, 7, 0, 0, 0, time.UTC)},
		},
	}
	if err := st.SaveIssue(iss, "2026-07-13T10:00:00Z"); err != nil {
		t.Fatalf("save issue: %v", err)
	}
	if err := st.SaveSprint(jira.Sprint{ID: 29, Name: "KW29", State: "active"}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}
	if err := st.SetLastSync(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("set last sync: %v", err)
	}
	// A prior full resync stamp must SURVIVE Reset (a full resync is Reset then
	// re-backfill then record last_full_resync — clearing it here would lose it).
	fullResyncAt := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	if err := st.SetLastFullResync(fullResyncAt); err != nil {
		t.Fatalf("set last full resync: %v", err)
	}

	if err := st.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	for _, table := range []string{"issue", "status_transition", "sprint_membership_transition", "sprint"} {
		if got := countRows(t, st, "SELECT COUNT(*) FROM "+table); got != 0 {
			t.Errorf("%s not cleared after Reset: %d rows remain", table, got)
		}
	}
	if _, ok, err := st.LastSync(); err != nil {
		t.Fatalf("last sync after reset: %v", err)
	} else if ok {
		t.Errorf("last_sync still present after Reset; want cleared")
	}
	if got, ok, err := st.LastFullResync(); err != nil {
		t.Fatalf("last full resync after reset: %v", err)
	} else if !ok || !got.Equal(fullResyncAt) {
		t.Errorf("last_full_resync = (%v, ok=%v) after Reset; want it preserved as %v", got, ok, fullResyncAt)
	}
}
