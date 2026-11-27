package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PrioIssue is one row of the Prio view's whole-project projection: the fields
// the Prio view reads, independent of sprint and status. Not all of them are
// displayed — ParentKey backs the No-parent filter and has no column (#210). Unlike every other read on
// this store, the Prio read is NOT scoped to the active sprint (CONTEXT.md →
// Prio view), so a row here may be backlog, Triage or long-since released work.
type PrioIssue struct {
	Key     string
	Type    string // Epic, Task, Bug, Story, … (whatever Jira reported)
	Summary string
	Status  string // the issue's current workflow status, verbatim
	// Priority is the Jira level (Highest / High / Medium / Low / Lowest); empty
	// for a row synced before priority joined the projection.
	Priority string
	// Labels are the issue's Jira labels, in the order Jira reported them; empty
	// when the issue carries none (or was synced before labels joined the
	// projection). They are split back out of the space-delimited `labels`
	// column, so each stays a whole string a caller can match exactly.
	Labels []string
	// ParentKey is the key of the issue's parent (in DCAI usually an Epic), empty
	// when the issue is top-of-tree — which is every Epic today, plus any
	// unparented Task, Bug or Story. Unlike priority and labels it needed no
	// resync of its own to join the projection: `parent_key` has been written on
	// every issue save since migration 00008, so any row synced since then already
	// carries it; it was simply not selected here (#210).
	ParentKey  string
	CreatedAt  time.Time
	ResolvedAt time.Time // Jira resolutiondate, zero when unresolved
}

const prioIssueColumns = `
	key, type, summary, status, COALESCE(priority, ''), COALESCE(labels, ''),
	COALESCE(parent_key, ''), created_at, resolved_at`

func scanPrioIssue(row interface{ Scan(...any) error }, extra ...any) (PrioIssue, error) {
	var issue PrioIssue
	var labels string
	var created, resolved sql.NullString
	if err := row.Scan(append([]any{
		&issue.Key, &issue.Type, &issue.Summary, &issue.Status,
		&issue.Priority, &labels, &issue.ParentKey, &created, &resolved,
	}, extra...)...); err != nil {
		return PrioIssue{}, err
	}
	var err error
	if issue.CreatedAt, err = parseNullableInstant(created); err != nil {
		return PrioIssue{}, err
	}
	if issue.ResolvedAt, err = parseNullableInstant(resolved); err != nil {
		return PrioIssue{}, err
	}
	issue.Labels = strings.Fields(labels)
	return issue, nil
}

// PrioIssues returns EVERY issue in the projection as a Prio row, ordered by
// issue key. It applies no sprint, type or status filter on purpose: the Prio
// view's universe is the whole DCAI project, and the narrowing it does (the Prio
// filters) lives in the web layer, mirroring how the Board filters over
// ActiveSprintBoard rather than pushing filter logic into the store.
func (s *Store) PrioIssues() ([]PrioIssue, error) {
	const query = `SELECT ` + prioIssueColumns + ` FROM issue ORDER BY key`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("prio issues: %w", err)
	}
	defer rows.Close()

	var issues []PrioIssue
	for rows.Next() {
		issue, err := scanPrioIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("scan prio issue: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prio issues: %w", err)
	}
	return issues, nil
}
