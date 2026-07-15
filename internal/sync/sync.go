// Package sync orchestrates pulling Jira issues (via a jira.Client) into the
// store. It offers a full-project backfill, an incremental sync of only changed
// issues, and a resilient background loop that keeps SQLite current cheaply.
package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// Store is the subset of the persistence layer the sync engine writes to and
// reads bookkeeping from.
type Store interface {
	SaveIssue(iss jira.Issue, syncedAt string) error
	IssueCount() (int, error)
	LastSync() (t time.Time, ok bool, err error)
	SetLastSync(t time.Time) error
}

// Backfill walks the whole project through the client and persists every
// issue: the current snapshot plus all status- and Estimated-Time-change
// transitions. Transitions are deduped by changelog entry id in the store, so a
// re-run inserts no duplicates. Returns the number of issues persisted. Each
// snapshot is stamped with a single shared synced_at timestamp.
func Backfill(ctx context.Context, client jira.Client, store Store) (int, error) {
	issues, err := client.FetchIssues(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch issues: %w", err)
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, iss := range issues {
		if err := store.SaveIssue(iss, syncedAt); err != nil {
			return 0, fmt.Errorf("save issue %s: %w", iss.Key, err)
		}
	}
	return len(issues), nil
}

// Once runs a single full-project backfill, discarding the issue count. It is
// the entry point used by the web integration harness.
func Once(ctx context.Context, client jira.Client, store Store) error {
	_, err := Backfill(ctx, client, store)
	return err
}

// incremental refreshes only issues changed since the given bound: it re-fetches
// their snapshots and appends any new changelog transitions (deduped in the
// store by changelog entry id).
func incremental(ctx context.Context, client jira.Client, store Store, since time.Time) error {
	issues, err := client.FetchIssuesUpdatedSince(ctx, since)
	if err != nil {
		return fmt.Errorf("fetch updated issues: %w", err)
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, iss := range issues {
		if err := store.SaveIssue(iss, syncedAt); err != nil {
			return fmt.Errorf("save issue %s: %w", iss.Key, err)
		}
	}
	return nil
}

// Syncer keeps the store current by running sync cycles: a full backfill when
// the store is empty, then cheap incremental syncs bounded by the last sync
// time (less an overlap window that absorbs clock skew between the app and
// Jira, so no change is missed at the boundary).
type Syncer struct {
	client  jira.Client
	store   Store
	overlap time.Duration
}

// NewSyncer builds a Syncer. overlap is the window subtracted from the last
// sync time when bounding the incremental query (e.g. 2 minutes).
func NewSyncer(client jira.Client, store Store, overlap time.Duration) *Syncer {
	return &Syncer{client: client, store: store, overlap: overlap}
}

// Cycle runs one sync cycle: a full backfill if the store is empty, otherwise
// an incremental sync of issues changed since the last sync (minus the overlap
// window). On success it records the cycle's start time as the new last_sync,
// so the next incremental picks up from here. Cycles are idempotent — the store
// dedups transitions — so a retried cycle inserts no duplicates.
func (s *Syncer) Cycle(ctx context.Context) error {
	startedAt := time.Now().UTC()

	count, err := s.store.IssueCount()
	if err != nil {
		return fmt.Errorf("issue count: %w", err)
	}

	if count == 0 {
		n, err := Backfill(ctx, s.client, s.store)
		if err != nil {
			return err
		}
		log.Printf("sync: backfilled %d issues", n)
	} else {
		since := time.Time{}
		if last, ok, err := s.store.LastSync(); err != nil {
			return err
		} else if ok {
			since = last.Add(-s.overlap)
		}
		if err := incremental(ctx, s.client, s.store, since); err != nil {
			return err
		}
	}

	if err := s.store.SetLastSync(startedAt); err != nil {
		return fmt.Errorf("record last_sync: %w", err)
	}
	return nil
}

// Run drives Cycle immediately and then on every interval tick until ctx is
// cancelled. A failed cycle is logged and retried on the next tick; the loop
// never crashes the server and always returns cleanly on cancellation.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := s.Cycle(ctx); err != nil {
			log.Printf("sync: cycle failed, retrying in %s: %v", interval, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
