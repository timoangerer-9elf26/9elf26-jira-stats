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
	// AssigneeAvatarURL is the current assignee's public Jira avatar image URL
	// ("" when unassigned or no avatar captured) — the same field the Board fetches
	// so the Daily card avatar renders identically.
	AssigneeAvatarURL string
	Size              string // 'S'/'M'/'L', or "" for no estimate
	Type              string // Task, Bug or Story
	Changes           []DailyStatusChange
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
	// crossing the Sprint view counts as Finished).
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
// (mirroring the Sprint view's completion crossing) buckets Finished regardless
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
		SELECT i.key, i.summary, i.assignee, i.assignee_avatar_url, i.size, i.type,
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
		var assigneeCol, avatarCol, size, fromStatus sql.NullString
		if err := rows.Scan(&key, &summary, &assigneeCol, &avatarCol, &size, &typ, &fromStatus, &toStatus, &atStr); err != nil {
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
				AssigneeAvatarURL: avatarCol.String,
				Size:              size.String, Type: typ,
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
		tk := *byKey[key]
		tk.Changes = dropIntraDoneChanges(tk.Changes)
		// A ticket whose only in-window moves were inside the done set has no
		// remaining changes, so it drops off the Daily view entirely (see #98).
		if len(tk.Changes) == 0 {
			continue
		}
		tickets = append(tickets, tk)
	}
	// Most recent in-window transition first; each ticket's last change is its
	// latest, since the rows arrived time-ascending.
	sort.SliceStable(tickets, func(i, j int) bool {
		return latestChange(tickets[i]).After(latestChange(tickets[j]))
	})
	return tickets, nil
}

// dropIntraDoneChanges removes the in-window status transitions that happen
// ENTIRELY inside the done set — both from and to are done statuses (e.g. DONE
// (This Sprint) → Ready for Release, Ready for Release → Released / Deployed).
// This is a Daily-view-only rule (#98): such hops are workflow noise, not a
// movement worth surfacing. Finish crossings (non-done → done) and reopens
// (done → non-done) are kept, so the surviving changes still drive each ticket's
// net From⟶To, its movement bucket, and whether it appears at all. It reuses
// isDoneStatus (the authoritative done set) and does NOT alter the global bucket
// used by the Sprint view and Velocity.
func dropIntraDoneChanges(changes []DailyStatusChange) []DailyStatusChange {
	kept := changes[:0:0]
	for _, c := range changes {
		if isDoneStatus(c.From) && isDoneStatus(c.To) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// latestChange returns the instant of a ticket's most recent in-window change.
func latestChange(t DailyTicket) time.Time {
	return t.Changes[len(t.Changes)-1].TransitionedAt
}

// DailyBoardCard is one ticket on the Daily board (issue #112): an active-sprint
// Task/Bug/Story that was created in the window OR moved in it, resolved to the
// facts a board card renders. Placement is by EndStatus (status at the window
// END); the origin badge and movement kind derive from the in-window changes.
type DailyBoardCard struct {
	Key      string
	Summary  string
	Assignee string // "" for an unassigned ticket
	// AssigneeAvatarURL is the current assignee's public Jira avatar image URL
	// ("" when unassigned or no avatar captured) — sourced directly from the Daily
	// query so the card avatar matches the Board's without a secondary fetch.
	AssigneeAvatarURL string
	Size              string // 'S'/'M'/'L', or "" for no estimate
	Type              string // Task, Bug or Story
	// Column is the collapsed Daily-board column the card belongs to, decided by
	// its status at the window END (see dailyBoardColumn): the four open statuses
	// map to themselves, the whole done set collapses to "Done", Canceled to
	// "Canceled", and anything else falls into "Refinement" so a card is never
	// dropped.
	Column string
	// StartStatus is the ticket's status at the window START — the origin the badge
	// names ("↳ from <StartStatus>"). "" when the ticket was created in the window
	// (no prior status); CreatedInWindow is then set and the card reads
	// "✦ created here".
	StartStatus string
	// Moves is the number of surviving in-window status changes (after the #98
	// intra-Done drop); 0 for a created-but-unmoved ticket.
	Moves int
	// Movement is the net-movement kind (Finished/Advanced/Pulled back), meaningful
	// only when Moves > 0 (a created-but-unmoved card carries no kind colour).
	Movement DailyMovement
	// CreatedInWindow is set when the ticket's created_at falls in the window; it
	// drives the "✦ created here" highlight.
	CreatedInWindow bool
	// LatestActivity is the instant of the most recent in-window activity — the
	// last surviving move, or the creation instant for a created-but-unmoved
	// ticket. Drives the card timestamp and the within-column sort.
	LatestActivity time.Time
}

// DailyBoard returns the Daily board's cards for [from, to): active-sprint
// Task/Bug/Story that were created in the window OR had an in-window status
// change (the same population the digest used, plus created-but-unmoved
// tickets), filtered by assignee ("" = all, UnassignedAssignee = unassigned,
// else an exact current-assignee match). Each card is placed by its status at
// the window END, reconstructed at that instant via statusAtSubquery (so a
// historical window is a snapshot and a ticket moved after the window still
// shows in its window-end column); a created-but-unmoved ticket lands in its
// creation status. The #98 intra-Done drop is preserved (a ticket whose only
// in-window moves are inside the done set is absent). Cards are returned sorted
// by most-recent in-window activity first.
func (s *Store) DailyBoard(assignee string, from, to time.Time) ([]DailyBoardCard, error) {
	moved, err := s.DailyStatusChanges(assignee, from, to)
	if err != nil {
		return nil, err
	}
	created, err := s.dailyCreatedInSprint(assignee, from, to)
	if err != nil {
		return nil, err
	}
	statusAt, err := s.statusAtInstant(to)
	if err != nil {
		return nil, err
	}

	byKey := map[string]*DailyBoardCard{}
	var order []string
	get := func(key string) (*DailyBoardCard, bool) {
		if c, ok := byKey[key]; ok {
			return c, true
		}
		c := &DailyBoardCard{Key: key}
		byKey[key] = c
		order = append(order, key)
		return c, false
	}

	// endStatus reconstructs the window-end status for placement: the latest
	// transition at or before `to` (statusAtSubquery), falling back to the given
	// current status when the ticket has no such transition (e.g. a created ticket
	// whose creation recorded no status change).
	endStatus := func(key, fallback string) string {
		if st, ok := statusAt[key]; ok {
			return st
		}
		return fallback
	}

	for _, tk := range moved {
		c, _ := get(tk.Key)
		c.Summary, c.Assignee, c.Size, c.Type = tk.Summary, tk.Assignee, tk.Size, tk.Type
		c.AssigneeAvatarURL = tk.AssigneeAvatarURL
		c.StartStatus = tk.StartStatus()
		c.Moves = len(tk.Changes)
		c.Movement = tk.Movement()
		c.LatestActivity = latestChange(tk)
		c.Column = dailyBoardColumn(endStatus(tk.Key, tk.EndStatus()))
	}
	for _, ct := range created {
		c, existed := get(ct.Key)
		c.CreatedInWindow = true
		if !existed {
			// Created in the window but never moved in it: place it in its creation
			// status, timestamp/sort on the creation instant, carry no movement kind.
			c.Summary, c.Assignee, c.Size, c.Type = ct.summary, ct.assignee, ct.size, ct.typ
			c.AssigneeAvatarURL = ct.assigneeAvatarURL
			c.LatestActivity = ct.createdAt
			c.Column = dailyBoardColumn(endStatus(ct.Key, ct.status))
		}
	}

	cards := make([]DailyBoardCard, 0, len(order))
	for _, key := range order {
		cards = append(cards, *byKey[key])
	}
	// Most-recent in-window activity first (stable, so equal instants keep the
	// moved-then-created arrival order).
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].LatestActivity.After(cards[j].LatestActivity)
	})
	return cards, nil
}

// dailyCreatedRow is an active-sprint work item created within the Daily window
// (used to place created-but-unmoved tickets on the board); status is its
// CURRENT status, the window-end fallback when the ticket has no reconstructable
// status transition.
type dailyCreatedRow struct {
	Key               string
	summary           string
	assignee          string
	assigneeAvatarURL string
	size              string
	typ               string
	status            string
	createdAt         time.Time
}

// dailyCreatedInSprint returns the active-sprint Task/Bug/Story created within
// [from, to), filtered by CURRENT assignee the same way DailyStatusChanges is
// ("" = all, UnassignedAssignee = unassigned, else an exact match). Unlike the
// removed "tickets I created" list this IS sprint-scoped and keyed on assignee
// (not creator): it is the created-in-window arm of the board population.
func (s *Store) dailyCreatedInSprint(assignee string, from, to time.Time) ([]dailyCreatedRow, error) {
	query := `
		SELECT key, summary, assignee, assignee_avatar_url, size, type, status, created_at
		FROM issue
		WHERE active_sprint IS NOT NULL
		  AND type IN (` + rollupTypes + `)
		  AND created_at IS NOT NULL
		  AND created_at >= ? AND created_at < ?`
	args := []any{from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)}

	switch assignee {
	case "":
		// All assignees — no additional predicate.
	case UnassignedAssignee:
		query += ` AND (assignee IS NULL OR assignee = '')`
	default:
		query += ` AND assignee = ?`
		args = append(args, assignee)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("daily created in sprint: %w", err)
	}
	defer rows.Close()

	var out []dailyCreatedRow
	for rows.Next() {
		var r dailyCreatedRow
		var createdStr string
		var assigneeCol, avatarCol, size sql.NullString
		if err := rows.Scan(&r.Key, &r.summary, &assigneeCol, &avatarCol, &size, &r.typ, &r.status, &createdStr); err != nil {
			return nil, fmt.Errorf("scan daily created row: %w", err)
		}
		r.assignee, r.assigneeAvatarURL, r.size = assigneeCol.String, avatarCol.String, size.String
		if r.createdAt, err = time.Parse(time.RFC3339, createdStr); err != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", createdStr, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily created rows: %w", err)
	}
	return out, nil
}

// statusAtInstant reconstructs every issue's status at instant `at` (the
// to_status of its latest `status` transition at or before `at`), returning a
// key→status map. It reuses statusAtSubquery — the same machinery the Sprint
// view uses for status-at-window-start — so the Daily board's window-end
// placement never drifts from it.
func (s *Store) statusAtInstant(at time.Time) (map[string]string, error) {
	rows, err := s.db.Query(statusAtSubquery, at.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("status at instant: %w", err)
	}
	defer rows.Close()

	m := map[string]string{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			return nil, fmt.Errorf("scan status-at row: %w", err)
		}
		m[key] = status
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate status-at rows: %w", err)
	}
	return m, nil
}

// dailyBoardColumn collapses a workflow status into one of the Daily board's
// columns: the four open statuses map to their canonical names, the whole done
// set collapses to "Done", Canceled to "Canceled", and anything else
// (Triage/none/an unknown status) falls into "Refinement" (the leftmost column)
// so a card is never dropped. Matching is case-insensitive (normalizeStatus), so
// a Jira casing quirk like "Ready to Do" still lands in "Ready To Do".
func dailyBoardColumn(status string) string {
	switch {
	case isDoneStatus(status):
		return DailyColumnDone
	case normalizeStatus(status) == normalizeStatus("Canceled"):
		return DailyColumnCanceled
	}
	switch normalizeStatus(status) {
	case normalizeStatus("Ready To Do"):
		return "Ready To Do"
	case normalizeStatus("In Progress"):
		return "In Progress"
	case normalizeStatus("Review / Testing"):
		return "Review / Testing"
	default: // Refinement, Triage, none, or an unknown status
		return "Refinement"
	}
}

// The two collapsed Daily-board columns whose names are not a single workflow
// status: Done folds the whole done set, Canceled is the rightmost column
// rendered only when non-empty. The four open columns use their status names
// verbatim.
const (
	DailyColumnDone     = "Done"
	DailyColumnCanceled = "Canceled"
)

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
