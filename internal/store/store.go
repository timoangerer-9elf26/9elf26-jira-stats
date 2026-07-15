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
	if _, err := tx.Exec(
		`INSERT INTO issue (key, type, summary, status, status_category, size, sprint, assignee, synced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     type=excluded.type, summary=excluded.summary, status=excluded.status,
		     status_category=excluded.status_category, size=excluded.size,
		     sprint=excluded.sprint, assignee=excluded.assignee, synced_at=excluded.synced_at`,
		iss.Key, iss.Type, iss.Summary, iss.Status, iss.StatusCategory, size, iss.Sprint, iss.Assignee, syncedAt,
	); err != nil {
		return fmt.Errorf("upsert issue %s: %w", iss.Key, err)
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

// OpenByStatus tallies open work items per workflow status. Open = current
// status not in the Done category, restricted to the rollup issue types
// Task/Bug/Story (Epics and Sub-tasks are stored but excluded). Columns are
// ordered by the known workflow with unknown statuses last, and a grand total
// aggregates every open status.
func (s *Store) OpenByStatus() (OpenBoard, error) {
	const query = `
		SELECT status, ` + sizeTallyColumns + `
		FROM issue
		WHERE status_category != 'Done'
		  AND type IN (` + rollupTypes + `)
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
	rank := make(map[string]int, len(workflowOrder))
	for i, status := range workflowOrder {
		rank[status] = i
	}
	sort.SliceStable(cols, func(i, j int) bool {
		ri, iKnown := rank[cols[i].Status]
		rj, jKnown := rank[cols[j].Status]
		switch {
		case iKnown && jKnown:
			return ri < rj
		case iKnown != jKnown:
			return iKnown // known statuses before unknown ones
		default:
			return cols[i].Status < cols[j].Status
		}
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
