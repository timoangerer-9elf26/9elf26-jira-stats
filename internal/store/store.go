// Package store is the SQLite persistence layer: schema (via embedded goose
// migrations), issue-snapshot and status-transition writes, and rollup queries.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at dsnPath in WAL mode
// with a busy timeout, applies all embedded migrations, and returns a ready
// Store. Access is serialized through a single connection per the SQLite
// single-writer constraint.
func Open(dsnPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dsnPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite allows only one writer; serialize all access through one connection
	// to avoid "database is locked" under concurrent writes.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// SaveIssue upserts an issue snapshot and appends any not-yet-seen status
// transitions (deduped by changelog entry id). syncedAt is the RFC3339 UTC
// timestamp stamped onto the snapshot.
func (s *Store) SaveIssue(iss jira.Issue, syncedAt string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var size any
	if iss.Size != "" {
		size = iss.Size
	}
	var activeSprint any
	if iss.ActiveSprint != "" {
		activeSprint = iss.ActiveSprint
	}
	if _, err := tx.Exec(
		`INSERT INTO issue (key, type, summary, status, status_category, size, sprint, active_sprint, assignee, synced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     type=excluded.type, summary=excluded.summary, status=excluded.status,
		     status_category=excluded.status_category, size=excluded.size,
		     sprint=excluded.sprint, active_sprint=excluded.active_sprint,
		     assignee=excluded.assignee, synced_at=excluded.synced_at`,
		iss.Key, iss.Type, iss.Summary, iss.Status, iss.StatusCategory, size, iss.Sprint, activeSprint, iss.Assignee, syncedAt,
	); err != nil {
		return fmt.Errorf("upsert issue %s: %w", iss.Key, err)
	}

	// Capture the active sprint window in meta from any issue that carries it.
	// All issues on a board share the same active sprint, so these writes are
	// idempotent across the synced set; only active-sprint issues touch it, so a
	// closed/future/no-sprint issue never clobbers a known window.
	if iss.ActiveSprint != "" {
		for _, kv := range [][2]string{
			{activeSprintNameKey, iss.ActiveSprint},
			{activeSprintStartKey, iss.ActiveSprintStart.UTC().Format(time.RFC3339)},
			{activeSprintEndKey, iss.ActiveSprintEnd.UTC().Format(time.RFC3339)},
		} {
			if _, err := tx.Exec(
				`INSERT INTO meta (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				kv[0], kv[1],
			); err != nil {
				return fmt.Errorf("set %s: %w", kv[0], err)
			}
		}
	}

	for _, e := range iss.Changelog {
		if _, err := tx.Exec(
			`INSERT INTO status_transition
			     (changelog_entry_id, issue_key, field, from_status, to_status, transitioned_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(changelog_entry_id) DO NOTHING`,
			e.ID, iss.Key, e.Field, e.From, e.To, e.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00"),
		); err != nil {
			return fmt.Errorf("insert transition %s: %w", e.ID, err)
		}
	}

	return tx.Commit()
}

// lastSyncKey is the meta row holding the RFC3339 UTC timestamp of the most
// recent successful sync cycle.
const lastSyncKey = "last_sync"

// The active-sprint window captured during sync: its name and RFC3339 UTC
// start/end instants. Read back via ActiveSprintWindow to scope the "Now" and
// "Completed active sprint" views to the real sprint.
const (
	activeSprintNameKey  = "active_sprint_name"
	activeSprintStartKey = "active_sprint_start"
	activeSprintEndKey   = "active_sprint_end"
)

// ActiveSprint is the active sprint window recorded during sync: the sprint
// name and its [Start, End) bounds (zero when the boundary was unknown).
type ActiveSprint struct {
	Name  string
	Start time.Time
	End   time.Time
}

// ActiveSprintWindow returns the active sprint window seen during sync. ok is
// false when no active sprint has been recorded (a fresh DB, or no synced issue
// belonged to an active sprint). Missing/blank start or end instants read back
// as the zero time.
func (s *Store) ActiveSprintWindow() (ActiveSprint, bool, error) {
	name, ok, err := s.readMeta(activeSprintNameKey)
	if err != nil || !ok {
		return ActiveSprint{}, false, err
	}
	sprint := ActiveSprint{Name: name}
	if sprint.Start, err = s.readMetaTime(activeSprintStartKey); err != nil {
		return ActiveSprint{}, false, err
	}
	if sprint.End, err = s.readMetaTime(activeSprintEndKey); err != nil {
		return ActiveSprint{}, false, err
	}
	return sprint, true, nil
}

// readMeta reads a single meta value; ok is false when the key is absent.
func (s *Store) readMeta(key string) (value string, ok bool, err error) {
	switch err = s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read meta %s: %w", key, err)
	}
	return value, true, nil
}

// readMetaTime reads a meta value as an RFC3339 instant, yielding the zero time
// when the key is absent or blank.
func (s *Store) readMetaTime(key string) (time.Time, error) {
	v, ok, err := s.readMeta(key)
	if err != nil || !ok || v == "" {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse meta %s %q: %w", key, v, err)
	}
	return t, nil
}

// IssueCount returns the number of issue snapshots currently stored. The sync
// loop uses it to decide between an initial full backfill and an incremental
// sync.
func (s *Store) IssueCount() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issue`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count issues: %w", err)
	}
	return n, nil
}

// LastSync returns the timestamp of the last recorded sync. ok is false when no
// sync has been recorded yet (a fresh database).
func (s *Store) LastSync() (t time.Time, ok bool, err error) {
	var v string
	switch err = s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, lastSyncKey).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read last_sync: %w", err)
	}
	t, err = time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse last_sync %q: %w", v, err)
	}
	return t, true, nil
}

// SetLastSync records the timestamp of a completed sync cycle (stored UTC).
func (s *Store) SetLastSync(t time.Time) error {
	if _, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		lastSyncKey, t.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("set last_sync: %w", err)
	}
	return nil
}

// SizeTally counts work items by estimate bucket (S/M/L or no-estimate) and
// sums their points (S=1, M=2, L=3; no-estimate contributes 0).
type SizeTally struct {
	S          int
	M          int
	L          int
	NoEstimate int
	Points     int
}

// sizeTallyColumns is the shared SELECT list that projects a set of issue rows
// into a SizeTally: the S/M/L/no-estimate counts and the S=1/M=2/L=3 points sum.
// Both the open-board and completed rollups reuse it so the points arithmetic
// lives in exactly one place. COALESCE keeps it safe for an ungrouped aggregate
// over an empty set (SUM would otherwise return NULL). Scan with scanTally, in
// this exact column order.
const sizeTallyColumns = `
		COALESCE(SUM(CASE size WHEN 'S' THEN 1 ELSE 0 END), 0) AS s,
		COALESCE(SUM(CASE size WHEN 'M' THEN 1 ELSE 0 END), 0) AS m,
		COALESCE(SUM(CASE size WHEN 'L' THEN 1 ELSE 0 END), 0) AS l,
		COALESCE(SUM(CASE WHEN size IS NULL THEN 1 ELSE 0 END), 0) AS no_estimate,
		COALESCE(SUM(CASE size WHEN 'S' THEN 1 WHEN 'M' THEN 2 WHEN 'L' THEN 3 ELSE 0 END), 0) AS points`

// scanTally reads the sizeTallyColumns projection from a row.
func scanTally(row interface{ Scan(...any) error }) (SizeTally, error) {
	var t SizeTally
	err := row.Scan(&t.S, &t.M, &t.L, &t.NoEstimate, &t.Points)
	return t, err
}

// rollupTypes are the issue types counted in every rollup (hierarchy level 0);
// Epics and Sub-tasks are stored but excluded.
const rollupTypes = `'Task', 'Bug', 'Story'`

// doneStatuses are the workflow statuses in Jira's "Done" category for DCAI. A
// completion is a transition crossing from a non-Done status into one of these;
// a move BETWEEN them is within-category and is not a new completion. Kept as a
// small constant here — a later ticket may make the Done set configurable.
var doneStatuses = []string{"DONE (This Sprint)", "Released / Deployed"}

// StatusColumn is the tally of open work in a single workflow status — one
// column of the "Now" board.
type StatusColumn struct {
	Status string
	SizeTally
}

// OpenBoard is the "Now" view projection: open work tallied per open workflow
// status (ordered by the workflow, unknown statuses last) plus a grand total
// across every open status.
type OpenBoard struct {
	Columns []StatusColumn
	Total   SizeTally
}

// workflowOrder is the DCAI workflow left-to-right. Open status columns render
// in this order; a status not listed here (e.g. one newly added in Jira) sorts
// after the known ones, alphabetically.
var workflowOrder = []string{
	"Refinement",
	"Ready to Do",
	"In Progress",
	"Review / Testing",
	"DONE (This Sprint)",
	"Released / Deployed",
}

// workflowRank maps each known workflow status to its left-to-right position,
// derived once from workflowOrder so the ordering lives in exactly one place.
// Keys are normalized (see normalizeStatus) so Jira casing quirks — e.g.
// "Ready To Do" vs the constant's "Ready to Do" — still match their position.
var workflowRank = func() map[string]int {
	m := make(map[string]int, len(workflowOrder))
	for i, status := range workflowOrder {
		m[normalizeStatus(status)] = i
	}
	return m
}()

// normalizeStatus folds a workflow status to a canonical form for rank lookup,
// so matching against workflowOrder ignores casing differences in the Jira
// status strings.
func normalizeStatus(status string) string {
	return strings.ToLower(status)
}

// workflowLess reports whether status a precedes status b in the DCAI workflow.
// Known statuses sort in workflow order ahead of any unknown status (e.g. one
// newly added in Jira); unknown statuses sort alphabetically among themselves.
// Matching against the workflow is case-insensitive so Jira casing quirks map
// to the intended position. It is the single ordering rule shared by every
// per-status projection.
func workflowLess(a, b string) bool {
	ra, aKnown := workflowRank[normalizeStatus(a)]
	rb, bKnown := workflowRank[normalizeStatus(b)]
	switch {
	case aKnown && bKnown:
		return ra < rb
	case aKnown != bKnown:
		return aKnown // known statuses before unknown ones
	default:
		return a < b
	}
}

// OpenByStatus tallies open work items in the ACTIVE sprint per workflow
// status. Open = current status not in the Done category, restricted to the
// rollup issue types Task/Bug/Story (Epics and Sub-tasks are stored but
// excluded) and to issues in the active sprint (active_sprint IS NOT NULL);
// whole-project open work outside the sprint is not shown. Columns are ordered
// by the known workflow with unknown statuses last, and a grand total
// aggregates every open status.
func (s *Store) OpenByStatus() (OpenBoard, error) {
	const query = `
		SELECT status, ` + sizeTallyColumns + `
		FROM issue
		WHERE status_category != 'Done'
		  AND type IN (` + rollupTypes + `)
		  AND active_sprint IS NOT NULL
		GROUP BY status`

	rows, err := s.db.Query(query)
	if err != nil {
		return OpenBoard{}, fmt.Errorf("open by status: %w", err)
	}
	defer rows.Close()

	var board OpenBoard
	for rows.Next() {
		var c StatusColumn
		if err := rows.Scan(&c.Status, &c.S, &c.M, &c.L, &c.NoEstimate, &c.Points); err != nil {
			return OpenBoard{}, fmt.Errorf("scan open status: %w", err)
		}
		board.Columns = append(board.Columns, c)
		board.Total.S += c.S
		board.Total.M += c.M
		board.Total.L += c.L
		board.Total.NoEstimate += c.NoEstimate
		board.Total.Points += c.Points
	}
	if err := rows.Err(); err != nil {
		return OpenBoard{}, fmt.Errorf("iterate open statuses: %w", err)
	}

	sortColumnsByWorkflow(board.Columns)
	return board, nil
}

// sortColumnsByWorkflow orders columns by the known workflow; any status not in
// the workflow sorts after the known ones, alphabetically among themselves.
func sortColumnsByWorkflow(cols []StatusColumn) {
	sort.SliceStable(cols, func(i, j int) bool {
		return workflowLess(cols[i].Status, cols[j].Status)
	})
}

// LastSyncedAt returns the most recent synced_at stamp across stored issue
// snapshots — how fresh the projected data is. ok is false on an empty store.
func (s *Store) LastSyncedAt() (t time.Time, ok bool, err error) {
	var v sql.NullString
	if err = s.db.QueryRow(`SELECT MAX(synced_at) FROM issue`).Scan(&v); err != nil {
		return time.Time{}, false, fmt.Errorf("read last synced_at: %w", err)
	}
	if !v.Valid {
		return time.Time{}, false, nil
	}
	t, err = time.Parse(time.RFC3339, v.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse synced_at %q: %w", v.String, err)
	}
	return t, true, nil
}

// CompletedInRange tallies the work items whose completion falls in [from, to).
//
// "Completed at T" is the timestamp of a status transition crossing FROM a
// non-Done status INTO a Done-category status (see doneStatuses). A move between
// the two Done statuses is within-category and is not a new completion; on
// reopen (Done -> non-Done -> Done) the LATEST crossing wins, so each issue is
// counted at most once at its most recent crossing. Counts use the CURRENT size
// (S=1/M=2/L=3, NULL = no estimate) and only the rollup issue types.
//
// Callers own the calendar: from/to are absolute instants (typically an ISO
// week or preset range computed in Europe/Berlin). A completion is included
// when its stored UTC instant is >= from and < to. The Velocity rollup reuses
// this method — one call per ISO week — rather than reimplementing crossing
// detection.
func (s *Store) CompletedInRange(from, to time.Time) (SizeTally, error) {
	in := strings.TrimSuffix(strings.Repeat("?,", len(doneStatuses)), ",")
	query := `
		SELECT ` + sizeTallyColumns + `
		FROM issue
		JOIN (
			SELECT issue_key, MAX(transitioned_at) AS completed_at
			FROM status_transition
			WHERE field = 'status'
			  AND to_status IN (` + in + `)
			  AND (from_status IS NULL OR from_status NOT IN (` + in + `))
			GROUP BY issue_key
		) crossing ON crossing.issue_key = issue.key
		WHERE issue.type IN (` + rollupTypes + `)
		  AND crossing.completed_at >= ? AND crossing.completed_at < ?`

	args := make([]any, 0, 2*len(doneStatuses)+2)
	for range 2 {
		for _, st := range doneStatuses {
			args = append(args, st)
		}
	}
	args = append(args, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))

	tally, err := scanTally(s.db.QueryRow(query, args...))
	if err != nil {
		return SizeTally{}, fmt.Errorf("completed in range: %w", err)
	}
	return tally, nil
}

// BoardCard is one issue on the sprint board — the fields a card renders.
type BoardCard struct {
	Key     string
	Summary string
	Size    string // T-shirt label 'S'/'M'/'L', or "" for no estimate
	Type    string // Task, Bug or Story
}

// BoardColumn is one workflow-status column of the sprint board and the
// active-sprint cards currently in that status.
type BoardColumn struct {
	Status string
	Cards  []BoardCard
}

// Board is the active-sprint Kanban projection: one column per workflow status
// present in the active sprint, in workflow order (unknown statuses last). The
// board is NOT filtered to open work, so Done-category columns (e.g. DONE (This
// Sprint), Released / Deployed) appear alongside the open ones — it mirrors the
// Jira sprint board for data-quality validation.
type Board struct {
	Columns []BoardColumn
}

// ActiveSprintBoard projects the ACTIVE sprint as a Kanban board: every
// active-sprint issue (active_sprint IS NOT NULL) of a rollup type
// (Task/Bug/Story; Epics and Sub-tasks are stored but excluded, consistent with
// the rollups) grouped into a column per workflow status. Columns follow the
// workflow order with unknown statuses last; cards within a column are ordered
// by issue key for a stable render. Unlike OpenByStatus this keeps Done-category
// statuses, since a sprint board shows its done columns too.
func (s *Store) ActiveSprintBoard() (Board, error) {
	const query = `
		SELECT status, key, summary, size, type
		FROM issue
		WHERE active_sprint IS NOT NULL
		  AND type IN (` + rollupTypes + `)
		ORDER BY key`

	rows, err := s.db.Query(query)
	if err != nil {
		return Board{}, fmt.Errorf("active sprint board: %w", err)
	}
	defer rows.Close()

	// Collect cards per status, preserving the key-ordered arrival order, then
	// build the columns in workflow order.
	byStatus := map[string][]BoardCard{}
	var statuses []string
	for rows.Next() {
		var status string
		var card BoardCard
		var size sql.NullString
		if err := rows.Scan(&status, &card.Key, &card.Summary, &size, &card.Type); err != nil {
			return Board{}, fmt.Errorf("scan board card: %w", err)
		}
		card.Size = size.String // "" when NULL (no estimate)
		if _, seen := byStatus[status]; !seen {
			statuses = append(statuses, status)
		}
		byStatus[status] = append(byStatus[status], card)
	}
	if err := rows.Err(); err != nil {
		return Board{}, fmt.Errorf("iterate board cards: %w", err)
	}

	sort.SliceStable(statuses, func(i, j int) bool { return workflowLess(statuses[i], statuses[j]) })
	board := Board{Columns: make([]BoardColumn, 0, len(statuses))}
	for _, status := range statuses {
		board.Columns = append(board.Columns, BoardColumn{Status: status, Cards: byStatus[status]})
	}
	return board, nil
}
