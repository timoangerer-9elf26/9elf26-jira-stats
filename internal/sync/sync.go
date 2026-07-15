// Package sync orchestrates pulling Jira issues (via a jira.Client) into the
// store. It offers a full-project backfill; incremental and background sync
// land in later tickets.
package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// Store is the subset of the persistence layer the sync engine writes to.
type Store interface {
	SaveIssue(iss jira.Issue, syncedAt string) error
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
