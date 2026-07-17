package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// UnassignedAssignee is the sentinel passed to DailyStatusChanges to select only
// tickets with no assignee. It is distinct from "" (which means "all
// assignees") and cannot collide with a real Jira display name.
const UnassignedAssignee = "\x00unassigned"

// DailyStatusChange is one in-window status transition of a ticket: the crossing
// From one status To another at a recorded instant (stored UTC).
type DailyStatusChange struct {
	From           string // source status ("" when the changelog recorded no from)
	To             string // destination status
	TransitionedAt time.Time
}

// DailyTicket is an active-sprint work item that changed status within the
// queried window: its display fields plus every in-window status change (oldest
// first).
type DailyTicket struct {
	Key      string
	Summary  string
	Assignee string // "" for an unassigned ticket
	Size     string // 'S'/'M'/'L', or "" for no estimate
	Type     string // Task, Bug or Story
	Changes  []DailyStatusChange
}

// DailyMovement is the net-movement bucket a moved ticket falls into over the
// Daily window — the summary layer of the Daily digest. Every moved ticket maps
// to exactly one value.
type DailyMovement int

const (
	// MovementAdvanced is net forward in the workflow but not into Done, including
	// net-zero churn (moved out and back to the same status).
	MovementAdvanced DailyMovement = iota
	// MovementFinished crossed into the Done set within the window (the same
	// crossing the Weekly view counts as Finished).
	MovementFinished
	// MovementPulledBack is net backward in the workflow, including a move to
	// Canceled.
	MovementPulledBack
)

// StartStatus is the ticket's status at the window start: the first in-window
// change's source. Panics on a ticket with no changes — DailyStatusChanges only
// returns moved tickets, so callers always have at least one change.
func (t DailyTicket) StartStatus() string { return t.Changes[0].From }

// EndStatus is the ticket's status at the window end: the last in-window
// change's destination.
func (t DailyTicket) EndStatus() string { return t.Changes[len(t.Changes)-1].To }

// Movement classifies the ticket's net movement over the window into exactly one
// bucket. Finished takes priority: any in-window crossing INTO the Done set
// (mirroring the Weekly view's completion crossing) buckets Finished regardless
// of intermediate hops. Otherwise the net position from the window start
// (StartStatus) to the window end (EndStatus) decides — a move ending in
// Canceled, or one net backward in the DCAI workflow, is Pulled back; net
// forward and net-zero churn are Advanced. Canceled is special-cased because it
// sorts last in the workflow order yet a move into it is an abandonment, not a
// step forward.
func (t DailyTicket) Movement() DailyMovement {
	for _, c := range t.Changes {
		if !isDoneStatus(c.From) && isDoneStatus(c.To) {
			return MovementFinished
		}
	}
	start, end := t.StartStatus(), t.EndStatus()
	if normalizeStatus(end) == normalizeStatus("Canceled") || workflowLess(end, start) {
		return MovementPulledBack
	}
	return MovementAdvanced
}

// DailyStatusChanges returns the active-sprint work items (Task/Bug/Story; Epics
// and Sub-tasks excluded, consistent with the rollups) that had one or more
// `status` transitions in [from, to), each carrying its in-window changes.
//
// The assignee argument selects the scope: "" means all assignees;
// UnassignedAssignee means only tickets with no assignee; any other value is an
// exact display-name match (the ticket's CURRENT assignee). Tickets are ordered
// by their most recent in-window transition first; within a ticket the changes
// are oldest-first. from/to are absolute instants; a change is included when its
// stored UTC instant is >= from and < to, mirroring CompletedInRange.
func (s *Store) DailyStatusChanges(assignee string, from, to time.Time) ([]DailyTicket, error) {
	query := `
		SELECT i.key, i.summary, i.assignee, i.size, i.type,
		       t.from_status, t.to_status, t.transitioned_at
		FROM issue i
		JOIN status_transition t ON t.issue_key = i.key
		WHERE i.active_sprint IS NOT NULL
		  AND i.type IN (` + rollupTypes + `)
		  AND t.field = 'status'
		  AND t.transitioned_at >= ? AND t.transitioned_at < ?`
	args := []any{from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)}

	switch assignee {
	case "":
		// All assignees — no additional predicate.
	case UnassignedAssignee:
		query += ` AND (i.assignee IS NULL OR i.assignee = '')`
	default:
		query += ` AND i.assignee = ?`
		args = append(args, assignee)
	}
	query += ` ORDER BY t.transitioned_at`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("daily status changes: %w", err)
	}
	defer rows.Close()

	// Group the ascending-by-time rows into one ticket per key, preserving arrival
	// order so each ticket's changes stay oldest-first.
	byKey := map[string]*DailyTicket{}
	var order []string
	for rows.Next() {
		var key, summary, typ, toStatus, atStr string
		var assigneeCol, size, fromStatus sql.NullString
		if err := rows.Scan(&key, &summary, &assigneeCol, &size, &typ, &fromStatus, &toStatus, &atStr); err != nil {
			return nil, fmt.Errorf("scan daily row: %w", err)
		}
		at, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			return nil, fmt.Errorf("parse transitioned_at %q: %w", atStr, err)
		}
		ticket, seen := byKey[key]
		if !seen {
			ticket = &DailyTicket{
				Key: key, Summary: summary, Assignee: assigneeCol.String,
				Size: size.String, Type: typ,
			}
			byKey[key] = ticket
			order = append(order, key)
		}
		ticket.Changes = append(ticket.Changes, DailyStatusChange{
			From: fromStatus.String, To: toStatus, TransitionedAt: at,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily rows: %w", err)
	}

	tickets := make([]DailyTicket, 0, len(order))
	for _, key := range order {
		tickets = append(tickets, *byKey[key])
	}
	// Most recent in-window transition first; each ticket's last change is its
	// latest, since the rows arrived time-ascending.
	sort.SliceStable(tickets, func(i, j int) bool {
		return latestChange(tickets[i]).After(latestChange(tickets[j]))
	})
	return tickets, nil
}

// latestChange returns the instant of a ticket's most recent in-window change.
func latestChange(t DailyTicket) time.Time {
	return t.Changes[len(t.Changes)-1].TransitionedAt
}

// ActiveSprintAssignees returns the distinct, non-empty assignees of active-
// sprint work items (Task/Bug/Story), sorted alphabetically — the named options
// for the Daily view's assignee dropdown. Unassigned tickets are represented by
// the caller's separate "Unassigned" option, not here.
func (s *Store) ActiveSprintAssignees() ([]string, error) {
	const query = `
		SELECT DISTINCT assignee
		FROM issue
		WHERE active_sprint IS NOT NULL
		  AND type IN (` + rollupTypes + `)
		  AND assignee IS NOT NULL AND assignee != ''
		ORDER BY assignee`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("active sprint assignees: %w", err)
	}
	defer rows.Close()

	var assignees []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("scan assignee: %w", err)
		}
		assignees = append(assignees, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assignees: %w", err)
	}
	return assignees, nil
}
