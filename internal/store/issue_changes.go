package store

import (
	"fmt"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// IssueChangesSince reads the project's status, priority and resolution changes at or
// after since, grouped by Jira issue key. Each issue's changes run oldest
// first and include earlier Done transitions even if the issue has reopened.
func (s *Store) IssueChangesSince(since time.Time) (map[string][]jira.ChangelogEntry, error) {
	rows, err := s.db.Query(`
		SELECT issue_key, changelog_entry_id, field, COALESCE(from_status, ''), COALESCE(to_status, ''),
		       from_id, to_id, author_account_id, author_display_name, transitioned_at, subsecond_ns
		FROM status_transition
		WHERE field IN ('status', 'priority', 'resolution') AND (transitioned_at, subsecond_ns) >= (?, ?)
		ORDER BY transitioned_at, subsecond_ns, changelog_entry_id`,
		since.UTC().Format(time.RFC3339), since.Nanosecond())
	if err != nil {
		return nil, fmt.Errorf("read issue changes: %w", err)
	}
	defer rows.Close()

	changes := make(map[string][]jira.ChangelogEntry)
	for rows.Next() {
		var change jira.ChangelogEntry
		var key, instant string
		var nanoseconds time.Duration
		if err := rows.Scan(&key, &change.ID, &change.Field, &change.From, &change.To,
			&change.FromID, &change.ToID, &change.Author.AccountID, &change.Author.DisplayName,
			&instant, &nanoseconds); err != nil {
			return nil, fmt.Errorf("scan issue change: %w", err)
		}
		at, err := time.Parse(time.RFC3339, instant)
		if err != nil {
			return nil, fmt.Errorf("parse time of issue change %s: %w", change.ID, err)
		}
		change.Timestamp = at.Add(nanoseconds)
		changes[key] = append(changes[key], change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issue changes: %w", err)
	}
	return changes, nil
}

// LatestIssueStatusChanges reports the last status change per issue, including
// changes that predate the meeting cutoff.
func (s *Store) LatestIssueStatusChanges() (map[string]time.Time, error) {
	rows, err := s.db.Query(`
		SELECT issue_key, transitioned_at, subsecond_ns
		FROM (
			SELECT issue_key, transitioned_at, subsecond_ns,
			       ROW_NUMBER() OVER (PARTITION BY issue_key ORDER BY transitioned_at DESC, subsecond_ns DESC) AS position
			FROM status_transition WHERE field = 'status'
		) WHERE position = 1`)
	if err != nil {
		return nil, fmt.Errorf("read latest status changes: %w", err)
	}
	defer rows.Close()

	changes := make(map[string]time.Time)
	for rows.Next() {
		var key, instant string
		var nanoseconds time.Duration
		if err := rows.Scan(&key, &instant, &nanoseconds); err != nil {
			return nil, fmt.Errorf("scan latest status change: %w", err)
		}
		at, err := time.Parse(time.RFC3339, instant)
		if err != nil {
			return nil, fmt.Errorf("parse latest status change for %s: %w", key, err)
		}
		changes[key] = at.Add(nanoseconds)
	}
	return changes, rows.Err()
}
