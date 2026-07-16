package store

// Integration test for sprint entities: a jira.FakeClient carrying an ACTIVE
// sprint (activated, not yet completed) and a COMPLETED sprint (both lifecycle
// instants set) is synced through the real sync.Backfill into a temp store, and
// the projection is asserted directly.
//
// Covers the ticket's required cases:
//   - both sprints persist as first-class entities
//   - the active sprint's activation instant is readable and drives the window
//   - the completed sprint exposes its completion instant
//   - re-sync upserts (no duplicate rows)

import (
	"context"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func TestSyncPersistsSprintEntitiesWithLifecycleInstants(t *testing.T) {
	kw28Activated := time.Date(2026, time.July, 6, 7, 0, 0, 0, time.UTC)
	kw28Completed := time.Date(2026, time.July, 13, 6, 30, 0, 0, time.UTC)
	kw29Activated := time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)

	fake := &jira.FakeClient{
		Sprints: []jira.Sprint{
			{ID: 28, Name: "KW28", State: "closed", ActivatedAt: kw28Activated, CompletedAt: kw28Completed},
			{ID: 29, Name: "KW29", State: "active", ActivatedAt: kw29Activated},
		},
	}
	st := openTempStore(t)
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Both sprints persist.
	if got := countRows(t, st, "SELECT COUNT(*) FROM sprint"); got != 2 {
		t.Fatalf("sprint rows = %d, want 2", got)
	}

	// The active sprint's activation instant is readable and drives the window.
	active, ok, err := st.ActiveSprintWindow()
	if err != nil || !ok {
		t.Fatalf("ActiveSprintWindow ok=%v err=%v", ok, err)
	}
	assertEq(t, "active sprint name", active.Name, "KW29")
	assertEq(t, "active sprint activation", active.Activated.UTC().Format(time.RFC3339), "2026-07-13T07:00:00Z")

	// The completed sprint exposes its completion instant; the active one has none.
	byName := map[string]Sprint{}
	sprints, err := st.Sprints()
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	for _, sp := range sprints {
		byName[sp.Name] = sp
	}
	assertEq(t, "KW28 state", byName["KW28"].State, "closed")
	assertEq(t, "KW28 completion", byName["KW28"].CompletedAt.UTC().Format(time.RFC3339), "2026-07-13T06:30:00Z")
	assertEq(t, "KW28 activation", byName["KW28"].ActivatedAt.UTC().Format(time.RFC3339), "2026-07-06T07:00:00Z")
	if !byName["KW29"].CompletedAt.IsZero() {
		t.Fatalf("active KW29 must have no completion instant, got %v", byName["KW29"].CompletedAt)
	}

	// Re-sync upserts rather than duplicating.
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("re-backfill: %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM sprint"); got != 2 {
		t.Fatalf("after re-sync sprint rows = %d, want 2 (upsert failed)", got)
	}
}
