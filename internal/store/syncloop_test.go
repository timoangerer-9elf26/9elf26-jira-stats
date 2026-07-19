package store

// Integration test for the incremental background sync loop.
//
// Seam: a controllable jira.FakeClient feeds a real sync.Syncer writing into a
// temp SQLite store. Assertions are on the resulting SQLite projection
// (snapshots + transition log) via the shared helpers, never on private sync
// internals.
//
// Covers the ticket's required cases:
//   - first cycle on an empty DB backfills the full project
//   - a second cycle picks up a newly-changed issue via the incremental query
//   - existing transitions are NOT duplicated (dedup by changelog entry id)
//   - last_sync is persisted and the incremental query is bounded by it
//   - a failed cycle returns an error without crashing, and the loop stops
//     cleanly on context cancellation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func TestSyncerBackfillsThenIncrementallyPicksUpChanges(t *testing.T) {
	fake := &jira.FakeClient{
		Issues: []jira.Issue{
			{
				Key: "DCAI-1", Type: "Story", Summary: "Shell", Status: "In Progress",
				StatusCategory: "In Progress", Size: "L",
				Changelog: []jira.ChangelogEntry{
					{ID: "t1", Field: "status", From: "Ready to Do", To: "In Progress",
						Timestamp: time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)},
				},
			},
			{
				Key: "DCAI-2", Type: "Task", Summary: "Schema", Status: "Ready to Do",
				StatusCategory: "To Do", Size: "M",
				Changelog: []jira.ChangelogEntry{
					{ID: "t2", Field: "status", From: "Refinement", To: "Ready to Do",
						Timestamp: time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)},
				},
			},
		},
	}

	st := openTempStore(t)
	syncer := sync.NewSyncer(fake, st, 2*time.Minute)

	// --- Cycle 1: empty DB -> full backfill ---
	if err := syncer.Cycle(context.Background()); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	assertEq(t, "issues after backfill", countRows(t, st, "SELECT COUNT(*) FROM issue"), 2)
	assertEq(t, "transitions after backfill", countRows(t, st, "SELECT COUNT(*) FROM status_transition"), 2)
	if len(fake.SinceCalls) != 0 {
		t.Fatalf("backfill must not issue an incremental query, got %d", len(fake.SinceCalls))
	}
	if _, ok, err := st.LastSync(); err != nil || !ok {
		t.Fatalf("last_sync not persisted after backfill (ok=%v err=%v)", ok, err)
	}
	// The cold-start backfill is NOT a full resync: it must not set last_full_resync
	// (that stamp stays "never" until a user-triggered full resync).
	if _, ok, err := st.LastFullResync(); err != nil {
		t.Fatalf("last_full_resync read after backfill: %v", err)
	} else if ok {
		t.Errorf("cold-start backfill set last_full_resync; want it left unset")
	}

	// --- A newly-changed issue: DCAI-2 advances to In Progress, adding a new
	// changelog entry. The incremental fetch also re-reports the existing t2
	// transition, which must NOT be duplicated. ---
	fake.Updated = []jira.Issue{
		{
			Key: "DCAI-2", Type: "Task", Summary: "Schema", Status: "In Progress",
			StatusCategory: "In Progress", Size: "M",
			Changelog: []jira.ChangelogEntry{
				{ID: "t2", Field: "status", From: "Refinement", To: "Ready to Do",
					Timestamp: time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)},
				{ID: "t3", Field: "status", From: "Ready to Do", To: "In Progress",
					Timestamp: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)},
			},
		},
	}

	// --- Cycle 2: non-empty DB -> incremental ---
	if err := syncer.Cycle(context.Background()); err != nil {
		t.Fatalf("second cycle: %v", err)
	}

	assertEq(t, "issues after incremental", countRows(t, st, "SELECT COUNT(*) FROM issue"), 2)
	assertEq(t, "transitions after incremental", countRows(t, st, "SELECT COUNT(*) FROM status_transition"), 3)
	assertEq(t, "DCAI-2 transitions (no dup)",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-2'"), 2)
	assertEq(t, "DCAI-2 refreshed status", readIssue(t, st, "DCAI-2").status, "In Progress")

	if len(fake.SinceCalls) != 1 {
		t.Fatalf("expected exactly one incremental query, got %d", len(fake.SinceCalls))
	}
	// The incremental bound is last_sync minus the overlap window, so it sits in
	// the recent past, never in the future.
	if since := fake.SinceCalls[0]; since.IsZero() || since.After(time.Now()) {
		t.Fatalf("incremental bound %v is not a sane recent past", since)
	}
}

func TestSyncerCycleIsResilientAndLoopStopsOnCancel(t *testing.T) {
	fake := &jira.FakeClient{Err: errors.New("jira unavailable")}
	st := openTempStore(t)
	syncer := sync.NewSyncer(fake, st, 2*time.Minute)

	// A failed cycle surfaces the error rather than crashing.
	if err := syncer.Cycle(context.Background()); err == nil {
		t.Fatal("expected error from failing cycle, got nil")
	}
	assertEq(t, "no issues after failed cycle", countRows(t, st, "SELECT COUNT(*) FROM issue"), 0)

	// The loop must swallow the error, never panic, and return promptly once the
	// context is cancelled (without waiting for the interval to elapse).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		syncer.Run(ctx, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	assertEq(t, "no issues after failed loop", countRows(t, st, "SELECT COUNT(*) FROM issue"), 0)
}
