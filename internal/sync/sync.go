// Package sync orchestrates pulling Jira issues (via a jira.Client) into the
// store. The walking skeleton implements a one-shot sync; incremental and
// background sync land in later tickets.
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

// Once fetches every issue from the client and writes it into the store,
// stamping each snapshot with a single shared synced_at timestamp.
func Once(ctx context.Context, client jira.Client, store Store) error {
	issues, err := client.FetchIssues(ctx)
	if err != nil {
		return fmt.Errorf("fetch issues: %w", err)
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, iss := range issues {
		if err := store.SaveIssue(iss, syncedAt); err != nil {
			return fmt.Errorf("save issue %s: %w", iss.Key, err)
		}
	}
	return nil
}
