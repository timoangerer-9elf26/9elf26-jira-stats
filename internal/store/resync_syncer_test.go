package store

// Integration test for the full-resync trigger on the sync engine (#52).
//
// Seam: a real sync.Syncer over a real temp store, driven by a gated fake
// client so the background resync can be held mid-flight. Assertions are on the
// observable engine state (Resyncing) and the resulting SQLite projection,
// never on private sync internals.

import (
	"context"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

// gatedClient is a jira.Client whose FetchIssues blocks until released, so a
// test can hold a resync mid-flight and observe the in-progress state. entered
// is signalled once FetchIssues is reached; the call returns Issues after
// release is closed.
type gatedClient struct {
	Issues  []jira.Issue
	Sprints []jira.Sprint
	entered chan struct{}
	release chan struct{}
}

func newGatedClient(issues []jira.Issue, sprints []jira.Sprint) *gatedClient {
	return &gatedClient{
		Issues:  issues,
		Sprints: sprints,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (c *gatedClient) FetchIssues(ctx context.Context) ([]jira.Issue, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.release
	return c.Issues, nil
}

func (c *gatedClient) FetchIssuesUpdatedSince(context.Context, time.Time) ([]jira.Issue, error) {
	return nil, nil
}

func (c *gatedClient) FetchSprints(context.Context) ([]jira.Sprint, error) {
	return c.Sprints, nil
}

func TestTriggerResyncRebuildsProjectionAndGuardsOverlap(t *testing.T) {
	st := openTempStore(t)

	// Pre-seed a stale issue that is NOT in the fake's set, so a correct resync
	// (clear + re-backfill) must drop it.
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-STALE", Type: "Task", Summary: "gone next sync", Status: "In Progress",
		StatusCategory: "In Progress",
	}, "2026-07-01T00:00:00Z"); err != nil {
		t.Fatalf("seed stale issue: %v", err)
	}

	fresh := []jira.Issue{
		{Key: "DCAI-1", Type: "Story", Summary: "Fresh", Status: "In Progress",
			StatusCategory: "In Progress", Size: "M", ActiveSprint: "KW29"},
	}
	client := newGatedClient(fresh, []jira.Sprint{{ID: 29, Name: "KW29", State: "active"}})
	syncer := sync.NewSyncer(client, st, 2*time.Minute)

	if !syncer.TriggerResync(context.Background()) {
		t.Fatal("first TriggerResync returned false; want true (started)")
	}

	// Wait until the resync is mid-flight (inside FetchIssues).
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("resync never reached FetchIssues")
	}

	if !syncer.Resyncing() {
		t.Error("Resyncing() = false while a resync is in flight; want true")
	}
	// A second trigger while one is running is a safe no-op.
	if syncer.TriggerResync(context.Background()) {
		t.Error("second TriggerResync returned true; want false (already running)")
	}

	close(client.release)

	// Wait for completion.
	deadline := time.Now().Add(3 * time.Second)
	for syncer.Resyncing() {
		if time.Now().After(deadline) {
			t.Fatal("resync did not finish after release")
		}
		time.Sleep(5 * time.Millisecond)
	}

	assertEq(t, "stale issue dropped", countRows(t, st, "SELECT COUNT(*) FROM issue WHERE key='DCAI-STALE'"), 0)
	assertEq(t, "fresh issue backfilled", countRows(t, st, "SELECT COUNT(*) FROM issue WHERE key='DCAI-1'"), 1)
	assertEq(t, "sprint backfilled", countRows(t, st, "SELECT COUNT(*) FROM sprint"), 1)
	if _, ok, err := st.LastSync(); err != nil || !ok {
		t.Fatalf("last_sync not recorded after resync (ok=%v err=%v)", ok, err)
	}
}
