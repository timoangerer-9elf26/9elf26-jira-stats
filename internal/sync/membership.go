package sync

import (
	"context"
	"fmt"
	"log"
	"time"
)

const membershipCheckInterval = 15 * time.Minute

type projectMembershipClient interface {
	FetchProjectIssueKeys(context.Context) ([]string, error)
	IssueInProject(context.Context, string) (bool, error)
}

// The caller holds the projection lock across listing, verification and removal.
func (s *Syncer) reconcileProjectIssues(ctx context.Context, now time.Time) error {
	client, ok := s.client.(projectMembershipClient)
	if !ok || now.Before(s.nextMembershipCheck) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	local, err := s.store.IssueKeys(ctx)
	if err != nil {
		return fmt.Errorf("list projected keys: %w", err)
	}
	remote, err := client.FetchProjectIssueKeys(ctx)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(remote))
	for _, key := range remote {
		present[key] = true
	}

	var retired []string
	for _, key := range local {
		if present[key] {
			continue
		}
		// The search index can lag. Only evict a key after the direct
		// issue lookup confirms deletion, a rename or a move.
		active, err := client.IssueInProject(ctx, key)
		if err != nil {
			return fmt.Errorf("verify Jira membership for %s: %w", key, err)
		}
		if !active {
			retired = append(retired, key)
		}
	}
	if len(retired) > 0 {
		if err := s.store.RemoveIssues(ctx, retired); err != nil {
			return fmt.Errorf("prune retired Jira keys: %w", err)
		}
		log.Printf("sync: project membership removed %d stale issues", len(retired))
	}
	s.nextMembershipCheck = now.Add(membershipCheckInterval)
	return nil
}
