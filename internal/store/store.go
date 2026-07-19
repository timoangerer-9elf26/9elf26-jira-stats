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
	var createdAt any
	if !iss.CreatedAt.IsZero() {
		createdAt = iss.CreatedAt.UTC().Format(time.RFC3339)
	}
	var creator any
	if iss.Creator != "" {
		creator = iss.Creator
	}
	var assigneeAvatarURL any
	if iss.AssigneeAvatarURL != "" {
		assigneeAvatarURL = iss.AssigneeAvatarURL
	}
	var parentKey any
	if iss.ParentKey != "" {
		parentKey = iss.ParentKey
	}
	var epicColor any
	if iss.EpicColor != "" {
		epicColor = iss.EpicColor
	}
	if _, err := tx.Exec(
		`INSERT INTO issue (key, type, summary, status, status_category, size, sprint, active_sprint, assignee, assignee_avatar_url, parent_key, epic_color, created_at, creator, synced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     type=excluded.type, summary=excluded.summary, status=excluded.status,
		     status_category=excluded.status_category, size=excluded.size,
		     sprint=excluded.sprint, active_sprint=excluded.active_sprint,
		     assignee=excluded.assignee, assignee_avatar_url=excluded.assignee_avatar_url,
		     parent_key=excluded.parent_key, epic_color=excluded.epic_color,
		     created_at=excluded.created_at,
		     creator=excluded.creator, synced_at=excluded.synced_at`,
		iss.Key, iss.Type, iss.Summary, iss.Status, iss.StatusCategory, size, iss.Sprint, activeSprint, iss.Assignee, assigneeAvatarURL, parentKey, epicColor, createdAt, creator, syncedAt,
	); err != nil {
		return fmt.Errorf("upsert issue %s: %w", iss.Key, err)
	}

	// NB: the active-sprint WINDOW is no longer captured here. It is now derived
	// from the sprint entity (see SaveSprint / ActiveSprintWindow); the
	// active_sprint column above still records per-issue MEMBERSHIP so the Now
	// board and Daily view can scope to active-sprint work.

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

	// Sprint-membership history: each entering/leaving of a sprint, deduped by
	// (changelog entry id, sprint id) so a single Sprint change adding/removing
	// several sprints stays distinct and re-syncs insert no duplicates.
	for _, sc := range iss.SprintChanges {
		entered := 0
		if sc.Entered {
			entered = 1
		}
		var sprintName any
		if sc.SprintName != "" {
			sprintName = sc.SprintName
		}
		if _, err := tx.Exec(
			`INSERT INTO sprint_membership_transition
			     (changelog_entry_id, issue_key, sprint_id, sprint_name, entered, transitioned_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(changelog_entry_id, sprint_id) DO NOTHING`,
			sc.EntryID, iss.Key, sc.SprintID, sprintName, entered, sc.Timestamp.UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("insert sprint membership %s/%d: %w", sc.EntryID, sc.SprintID, err)
		}
	}

	if err := synthesizeCreatedMembership(tx, iss); err != nil {
		return err
	}

	return tx.Commit()
}

// synthesizeCreatedMembership records the membership entry that a ticket created
// directly into a sprint is missing (#55). Such a ticket has its Sprint field
// set at creation and never changed, so its changelog carries no "Sprint" item
// and nothing entered it into sprint_membership_transition — leaving it invisible
// to membership history even though it currently belongs to the active sprint.
//
// When the issue currently belongs to an active sprint (ActiveSprintID != 0) for
// which the changelog holds NO entering transition, it inserts a synthetic entry
// at the issue's created instant (falling back to the sprint's activation instant
// when created is unknown), keyed by a stable synthetic id so re-syncs are
// idempotent. When a real changelog entry for that sprint DOES exist, it instead
// clears any stale synthetic shadow, so a synthetic never duplicates or shadows a
// real entry. Only the issue's CURRENT active sprint is reconciled here; a
// synthetic recorded earlier for a since-superseded active sprint is left as-is
// (the ticket was a member of it from creation). Runs inside SaveIssue's
// transaction.
func synthesizeCreatedMembership(tx *sql.Tx, iss jira.Issue) error {
	if iss.ActiveSprintID == 0 {
		return nil
	}
	entryID := fmt.Sprintf("synthetic-created:%s:%d", iss.Key, iss.ActiveSprintID)

	if hasEnteredChange(iss.SprintChanges, iss.ActiveSprintID) {
		// A real entering transition covers this sprint; drop any stale synthetic
		// (e.g. from an earlier sync before the real move was recorded).
		if _, err := tx.Exec(
			`DELETE FROM sprint_membership_transition WHERE changelog_entry_id = ? AND sprint_id = ?`,
			entryID, iss.ActiveSprintID,
		); err != nil {
			return fmt.Errorf("clear stale synthetic membership %s/%d: %w", iss.Key, iss.ActiveSprintID, err)
		}
		return nil
	}

	at := iss.CreatedAt
	if at.IsZero() {
		// Fallback: the sprint's activation instant (its startDate), the best
		// available "in the sprint since" anchor when the created time is unknown.
		start, ok, err := sprintActivatedTx(tx, iss.ActiveSprintID)
		if err != nil {
			return err
		}
		if !ok {
			return nil // no anchor to place the entry at; leave it unrecorded
		}
		at = start
	}

	var sprintName any
	if iss.ActiveSprint != "" {
		sprintName = iss.ActiveSprint
	}
	if _, err := tx.Exec(
		`INSERT INTO sprint_membership_transition
		     (changelog_entry_id, issue_key, sprint_id, sprint_name, entered, transitioned_at)
		 VALUES (?, ?, ?, ?, 1, ?)
		 ON CONFLICT(changelog_entry_id, sprint_id) DO NOTHING`,
		entryID, iss.Key, iss.ActiveSprintID, sprintName, at.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("insert synthetic membership %s/%d: %w", iss.Key, iss.ActiveSprintID, err)
	}
	return nil
}

// hasEnteredChange reports whether the changelog-derived membership changes carry
// an entering transition into the given sprint.
func hasEnteredChange(changes []jira.SprintMembershipChange, sprintID int) bool {
	for _, sc := range changes {
		if sc.Entered && sc.SprintID == sprintID {
			return true
		}
	}
	return false
}

// sprintActivatedTx reads a sprint's activation instant within a transaction. ok
// is false when the sprint is unknown or has no recorded activation.
func sprintActivatedTx(tx *sql.Tx, sprintID int) (time.Time, bool, error) {
	var activated sql.NullString
	switch err := tx.QueryRow(`SELECT activated_at FROM sprint WHERE id = ?`, sprintID).Scan(&activated); {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read sprint %d activation: %w", sprintID, err)
	}
	t, err := parseNullableInstant(activated)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, !t.IsZero(), nil
}

// lastSyncKey is the meta row holding the RFC3339 UTC timestamp of the most
// recent successful sync cycle — the incremental-sync heartbeat (CONTEXT.md →
// Sync). It bounds the incremental query and is surfaced as the "last
// incremental sync" time.
const lastSyncKey = "last_sync"

// lastFullResyncKey is the meta row holding the RFC3339 UTC timestamp of the
// most recent successful, user-triggered FULL RESYNC (CONTEXT.md → Sync). It is
// written only by Syncer.resync on success — never by the cold-start backfill —
// so it reads "never" until the first manual full resync, and it deliberately
// SURVIVES Reset (a full resync is Reset then re-backfill then set this).
const lastFullResyncKey = "last_full_resync"

// Sprint is a stored board sprint entity: its identity, state, and lifecycle
// instants. ActivatedAt is the window-start instant (Jira has no activatedDate,
// so it is taken from startDate; see jira.Sprint); CompletedAt is zero until it is
// completed. The planned end date is deliberately not stored (not trusted for
// windowing).
type Sprint struct {
	ID          int
	Name        string
	State       string
	ActivatedAt time.Time
	CompletedAt time.Time
}

// SaveSprint upserts a sprint entity (by Jira sprint id). Zero lifecycle
// instants persist as NULL, so an active/future sprint's completion — and a
// future sprint's activation — read back as the zero time.
func (s *Store) SaveSprint(sp jira.Sprint) error {
	var activated, completed any
	if !sp.ActivatedAt.IsZero() {
		activated = sp.ActivatedAt.UTC().Format(time.RFC3339)
	}
	if !sp.CompletedAt.IsZero() {
		completed = sp.CompletedAt.UTC().Format(time.RFC3339)
	}
	if _, err := s.db.Exec(
		`INSERT INTO sprint (id, name, state, activated_at, completed_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     name=excluded.name, state=excluded.state,
		     activated_at=excluded.activated_at, completed_at=excluded.completed_at`,
		sp.ID, sp.Name, sp.State, activated, completed,
	); err != nil {
		return fmt.Errorf("upsert sprint %d: %w", sp.ID, err)
	}
	return nil
}

// Sprints returns every stored sprint entity, ordered by id. It is the one
// source for sprint lifecycle reads: completed sprints expose their completion
// instant here (CompletedAt), active/future ones read back with a zero
// CompletedAt.
func (s *Store) Sprints() ([]Sprint, error) {
	rows, err := s.db.Query(`SELECT id, name, state, activated_at, completed_at FROM sprint ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list sprints: %w", err)
	}
	defer rows.Close()

	var sprints []Sprint
	for rows.Next() {
		sp, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		sprints = append(sprints, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sprints: %w", err)
	}
	return sprints, nil
}

// ActiveSprint is the active sprint window derived from the sprint entity: its
// name and its activation instant, which is the sprint window START
// (docs/adr/0002). The window is open-ended (end = now), resolved by the caller,
// so no end instant is carried here.
type ActiveSprint struct {
	// ID is the Jira sprint id — the key the sprint-membership history is stored
	// under (sprint_membership_transition.sprint_id). It reconciles the per-issue
	// active_sprint snapshot (keyed by NAME) with the history (keyed by ID): given
	// the active window, a caller can query membership by this id.
	ID        int
	Name      string
	Activated time.Time
}

// ActiveSprintWindow returns the currently active sprint (state = "active") as a
// window: its name and activation instant. ok is false when no sprint is active
// (a fresh DB, or between sprints). This supersedes the old meta planned-date
// window: the window start is the sprint's startDate-derived activation instant
// (Jira Cloud exposes no dedicated activation field; see jira.Sprint). It is the
// single source for the Now heading, the sprint board's existence gate, the Daily
// heading, and the Completed "active sprint" preset.
func (s *Store) ActiveSprintWindow() (ActiveSprint, bool, error) {
	var id int
	var name string
	var activated sql.NullString
	err := s.db.QueryRow(
		`SELECT id, name, activated_at FROM sprint
		 WHERE LOWER(state) = 'active'
		 ORDER BY activated_at DESC
		 LIMIT 1`).Scan(&id, &name, &activated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ActiveSprint{}, false, nil
	case err != nil:
		return ActiveSprint{}, false, fmt.Errorf("read active sprint: %w", err)
	}
	sprint := ActiveSprint{ID: id, Name: name}
	if activated.Valid && activated.String != "" {
		if sprint.Activated, err = time.Parse(time.RFC3339, activated.String); err != nil {
			return ActiveSprint{}, false, fmt.Errorf("parse activation instant %q: %w", activated.String, err)
		}
	}
	return sprint, true, nil
}

// membersAtSubquery reconstructs sprint membership at an instant by replaying the
// membership log: for each issue the latest entering/leaving transition at or
// before the instant decides membership (entered => in). Binds two args in order:
// the sprint id and the RFC3339 instant. The single source of the membership-
// reconstruction SQL — IssuesInSprintAt and the Sprint categories both build on
// it so the "members at instant" logic never drifts.
const membersAtSubquery = `
		SELECT issue_key FROM (
			SELECT issue_key, entered,
			       ROW_NUMBER() OVER (
			           PARTITION BY issue_key
			           ORDER BY transitioned_at DESC, changelog_entry_id DESC
			       ) AS rn
			FROM sprint_membership_transition
			WHERE sprint_id = ? AND transitioned_at <= ?
		)
		WHERE rn = 1 AND entered = 1`

// statusAtSubquery reconstructs each issue's status at an instant: the to_status
// of its latest `status` transition at or before the instant (issue_key,
// to_status columns). Binds one arg: the RFC3339 instant. Mirrors
// membersAtSubquery for status, so "open at the window start" is derived the same
// way membership is.
const statusAtSubquery = `
		SELECT issue_key, to_status FROM (
			SELECT issue_key, to_status,
			       ROW_NUMBER() OVER (
			           PARTITION BY issue_key
			           ORDER BY transitioned_at DESC, changelog_entry_id DESC
			       ) AS rn
			FROM status_transition
			WHERE field = 'status' AND transitioned_at <= ?
		)
		WHERE rn = 1`

// IssuesInSprintAt reconstructs which issues were members of the sprint with the
// given id at instant `at`, by replaying the membership-transition log: for each
// issue, the latest entering/leaving transition at or before `at` decides
// membership (entered => in, left => out). It mirrors how status history
// reconstructs status at an instant. Returns the member issue keys sorted for a
// stable result. This is the started-with/added primitive for the Sprint view:
// members at the sprint's activation instant are "started with"; members later
// that were not members at activation are "added during the window".
func (s *Store) IssuesInSprintAt(sprintID int, at time.Time) ([]string, error) {
	query := membersAtSubquery + `
		ORDER BY issue_key`

	rows, err := s.db.Query(query, sprintID, at.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("issues in sprint %d at %v: %w", sprintID, at, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan sprint member: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sprint members: %w", err)
	}
	return keys, nil
}

// SprintEntry returns the instant an issue first entered the sprint with the
// given id (its earliest "entered" membership transition). ok is false when no
// entering transition is recorded (the issue was never captured joining that
// sprint). It answers "when did issue X enter sprint S", enabling "added during
// the window" detection against the sprint's activation instant.
func (s *Store) SprintEntry(sprintID int, issueKey string) (time.Time, bool, error) {
	var at sql.NullString
	err := s.db.QueryRow(
		`SELECT MIN(transitioned_at) FROM sprint_membership_transition
		 WHERE sprint_id = ? AND issue_key = ? AND entered = 1`,
		sprintID, issueKey).Scan(&at)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("sprint entry %d/%s: %w", sprintID, issueKey, err)
	}
	if !at.Valid || at.String == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, at.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse sprint entry instant %q: %w", at.String, err)
	}
	return t, true, nil
}

// scanSprint reads one sprint row (id, name, state, activated_at, completed_at),
// mapping NULL/blank lifecycle instants to the zero time.
func scanSprint(row interface{ Scan(...any) error }) (Sprint, error) {
	var sp Sprint
	var activated, completed sql.NullString
	if err := row.Scan(&sp.ID, &sp.Name, &sp.State, &activated, &completed); err != nil {
		return Sprint{}, fmt.Errorf("scan sprint: %w", err)
	}
	var err error
	if sp.ActivatedAt, err = parseNullableInstant(activated); err != nil {
		return Sprint{}, err
	}
	if sp.CompletedAt, err = parseNullableInstant(completed); err != nil {
		return Sprint{}, err
	}
	return sp, nil
}

// parseNullableInstant parses a nullable RFC3339 column into a time, yielding
// the zero time when the value is NULL or blank.
func parseNullableInstant(v sql.NullString) (time.Time, error) {
	if !v.Valid || v.String == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse instant %q: %w", v.String, err)
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

// LastFullResync returns the timestamp of the last successful full resync. ok is
// false until the first user-triggered full resync completes (the cold-start
// backfill does not set it), so a fresh — or never-fully-resynced — store reads
// "never". Mirrors LastSync.
func (s *Store) LastFullResync() (t time.Time, ok bool, err error) {
	var v string
	switch err = s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, lastFullResyncKey).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read last_full_resync: %w", err)
	}
	t, err = time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse last_full_resync %q: %w", v, err)
	}
	return t, true, nil
}

// SetLastFullResync records the completion instant of a successful full resync
// (stored UTC). Called only by Syncer.resync on success. Mirrors SetLastSync.
func (s *Store) SetLastFullResync(t time.Time) error {
	if _, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		lastFullResyncKey, t.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("set last_full_resync: %w", err)
	}
	return nil
}

// Reset empties the whole rebuildable projection: every issue snapshot, the
// status- and sprint-membership transition logs, the sprint entities, and the
// last_sync bookkeeping row. It backs the full-resync button (#52): after Reset
// the store looks cold, so the next sync cycle re-backfills all issues, full
// changelog history and sprints from Jira. Child tables are deleted before the
// issue table they reference (foreign keys are not enforced on the connection),
// and the whole wipe runs in one transaction so a resync never observes a
// half-cleared projection. Only last_sync is removed so the cold-store
// re-backfill path is taken; every other meta row is preserved — crucially the
// last_full_resync stamp, which MUST survive Reset since a full resync is Reset
// then re-backfill then record last_full_resync.
func (s *Store) Reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Child transition logs are deleted before the issue rows they reference.
	tables := []string{
		`DELETE FROM sprint_membership_transition`,
		`DELETE FROM status_transition`,
		`DELETE FROM issue`,
		`DELETE FROM sprint`,
	}
	for _, stmt := range tables {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("reset projection: %w", err)
		}
	}
	// Only last_sync is cleared (parameterized, matching the other meta queries)
	// so the next cycle takes the cold-store re-backfill path; any other meta rows
	// — notably last_full_resync — are preserved.
	if _, err := tx.Exec(`DELETE FROM meta WHERE key = ?`, lastSyncKey); err != nil {
		return fmt.Errorf("reset last_sync: %w", err)
	}
	return tx.Commit()
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

// openStatuses and doneStatuses are the authoritative DCAI status buckets — the
// SINGLE source of truth for open/finished across every view (Board, Sprint,
// Velocity). They come straight from CONTEXT.md's "Ticket
// status buckets", NOT from Jira's status_category, which does not match the
// DCAI buckets: Jira categorizes Canceled as "Done" and Triage as "To Do", so a
// category-based test would wrongly count Canceled as finished and Triage as
// open. Both buckets are matched case-insensitively (see normalizeStatus).
//
// openStatuses is a POSITIVE membership test: "open" means one of exactly these
// four active-sprint statuses, not "any status Jira doesn't call Done". Triage
// (pre-sprint), Canceled (abandoned) and every Done status are therefore not
// open, and an unknown status never counts as open either.
var openStatuses = []string{
	"Refinement",
	"Ready To Do",
	"In Progress",
	"Review / Testing",
}

// doneStatuses is the authoritative "finished" bucket: work completed within the
// sprint. Ready for Release sits AFTER DONE (This Sprint) in the workflow and is
// a done state, so it belongs here; Canceled is abandoned (not finished) and is
// excluded. A completion is a transition crossing from a non-Done status into
// this set; a move BETWEEN the three is within-set and is not a new completion.
var doneStatuses = []string{
	"DONE (This Sprint)",
	"Ready for Release",
	"Released / Deployed",
}

// doneStatusSet is doneStatuses as a normalized membership set, for in-memory
// Done-crossing tests (the Daily digest) that mirror the SQL crossing rollup
// without a round trip. Keyed by normalizeStatus for case-insensitive matching.
var doneStatusSet = func() map[string]bool {
	m := make(map[string]bool, len(doneStatuses))
	for _, st := range doneStatuses {
		m[normalizeStatus(st)] = true
	}
	return m
}()

// isDoneStatus reports whether a status is in the authoritative finished bucket
// (see doneStatuses), matched case-insensitively.
func isDoneStatus(status string) bool {
	return doneStatusSet[normalizeStatus(status)]
}

// statusInClause builds a case-insensitive `LOWER(col) IN (...)` match against a
// status bucket: the "?,?,..." placeholder list plus the normalized status
// values to bind. Both are derived from the one bucket definition so open/done
// membership tests never drift from openStatuses/doneStatuses.
func statusInClause(statuses []string) (placeholders string, args []any) {
	placeholders = strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")
	args = make([]any, len(statuses))
	for i, st := range statuses {
		args[i] = normalizeStatus(st)
	}
	return placeholders, args
}

// workflowOrder is the FULL DCAI workflow left-to-right, the single source of
// truth for column order (from the Jira status dropdown, the authoritative
// source). Per-status columns render in this order; a status not listed here
// (e.g. one newly added in Jira) sorts after the known ones, alphabetically.
// Note this lists every valid status including the two the sprint board keeps
// off-board (Triage, Canceled) — boardColumnOrder derives the board's seeded set
// from this list.
var workflowOrder = []string{
	"Triage",
	"Refinement",
	"Ready To Do",
	"In Progress",
	"Review / Testing",
	"DONE (This Sprint)",
	"Ready for Release",
	"Released / Deployed",
	"Canceled",
}

// boardExcludedStatuses are workflow statuses intentionally kept off the sprint
// board (a maintainer decision): active-sprint issues in these never render —
// no column, no card. Keyed by normalizeStatus for case-insensitive matching.
var boardExcludedStatuses = map[string]bool{
	normalizeStatus("Triage"):   true,
	normalizeStatus("Canceled"): true,
}

// boardColumnOrder is the fixed, ordered set of workflow columns the sprint
// board always renders (left→right), even when empty. It is workflowOrder minus
// the board-excluded statuses, so the column order lives in exactly one place.
var boardColumnOrder = func() []string {
	cols := make([]string, 0, len(workflowOrder))
	for _, status := range workflowOrder {
		if !boardExcludedStatuses[normalizeStatus(status)] {
			cols = append(cols, status)
		}
	}
	return cols
}()

// workflowRank maps each known workflow status to its left-to-right position,
// derived once from workflowOrder so the ordering lives in exactly one place.
// Keys are normalized (see normalizeStatus) so Jira casing quirks — e.g. a
// status synced as "Ready to Do" vs the constant's "Ready To Do" — still match
// their position.
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
	return s.completedTally(from, to, false)
}

// FinishedInWindow tallies the ACTIVE-SPRINT work items whose completion falls
// in [from, to). It is CompletedInRange scoped to the active-sprint membership
// snapshot (active_sprint IS NOT NULL): the Sprint view's "Finished" figure.
// Crossing detection, the Done set (incl. Ready for Release),
// current-size counting and the half-open [from, to) window are all identical to
// CompletedInRange; only the active-sprint scope is added.
//
// The snapshot scope is deliberate for the skeleton (issue #34): membership is
// "currently in the active sprint", not reconstructed at the window instant. The
// started-with/added split (#35) refines membership using the recorded
// sprint-membership history (IssuesInSprintAt / SprintEntry).
func (s *Store) FinishedInWindow(from, to time.Time) (SizeTally, error) {
	return s.completedTally(from, to, true)
}

// completedTally is the shared Done-crossing rollup behind CompletedInRange and
// FinishedInWindow. activeSprintOnly adds the active-sprint membership scope; the
// crossing detection, Done set and points arithmetic live here once so the two
// callers can never drift.
func (s *Store) completedTally(from, to time.Time, activeSprintOnly bool) (SizeTally, error) {
	crossSQL, crossArgs := doneCrossingClause()
	scope := ""
	if activeSprintOnly {
		scope = "\n\t\t  AND issue.active_sprint IS NOT NULL"
	}
	query := `
		SELECT ` + sizeTallyColumns + `
		FROM issue
		JOIN (` + crossSQL + `) crossing ON crossing.issue_key = issue.key
		WHERE issue.type IN (` + rollupTypes + `)` + scope + `
		  AND crossing.completed_at >= ? AND crossing.completed_at < ?`

	args := append(crossArgs, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))

	tally, err := scanTally(s.db.QueryRow(query, args...))
	if err != nil {
		return SizeTally{}, fmt.Errorf("completed tally: %w", err)
	}
	return tally, nil
}

// doneCrossingClause builds the subquery that finds each issue's latest Done
// crossing — a `status` transition from a non-Done status INTO the Done set (see
// doneStatuses) — projecting (issue_key, completed_at), plus the ordered args to
// bind. A move BETWEEN two Done statuses is within-set and not a crossing; on
// reopen the latest crossing wins (MAX). It is the single source of Done-crossing
// detection and of the Done set, shared by completedTally and the Sprint
// finished-split so the two can never drift.
func doneCrossingClause() (query string, args []any) {
	doneIn, doneArgs := statusInClause(doneStatuses)
	query = `
			SELECT issue_key, MAX(transitioned_at) AS completed_at
			FROM status_transition
			WHERE field = 'status'
			  AND LOWER(to_status) IN (` + doneIn + `)
			  AND (from_status IS NULL OR LOWER(from_status) NOT IN (` + doneIn + `))
			GROUP BY issue_key`
	args = make([]any, 0, 2*len(doneStatuses))
	args = append(args, doneArgs...)
	args = append(args, doneArgs...)
	return query, args
}

// SprintCohort is one cohort row of the Sprint table (Started with, Added, or
// their Total) broken into the outcome columns Open / Finished / Removed, plus
// the derived Total = Open + Finished + Removed. Each is a size tally at the
// ticket's CURRENT size.
type SprintCohort struct {
	// Open is cohort members still in the sprint, not cancelled, and not finished
	// within the window — the remainder after Finished and Removed.
	Open SizeTally
	// Finished is cohort members that crossed into the Done set within the window.
	Finished SizeTally
	// Removed is cohort members that did NOT finish and are cancelled or no longer
	// a member. The two cohorts differ (see SprintCategoriesInWindow): a
	// Started-with ticket reprioritised out counts here; an Added ticket
	// reprioritised out is dropped entirely, so only a cancelled Added ticket lands
	// here.
	Removed SizeTally
	// Total is Open + Finished + Removed.
	Total SizeTally
}

// SprintCategories is the Sprint view's cohort × outcome breakdown for a sprint
// over a window [from, to): the Started-with and Added cohorts and their
// column-wise Total (the Total row).
type SprintCategories struct {
	StartedWith SprintCohort
	Added       SprintCohort
	// Total is the column-wise sum of StartedWith and Added (the Total row).
	Total SprintCohort
}

// sprintCategory selects which cohort-membership predicate categoryTally builds.
type sprintCategory int

const (
	catStartedWith sprintCategory = iota
	catAdded
)

// sprintOutcome selects which outcome-column predicate categoryTally builds.
type sprintOutcome int

const (
	outOpen sprintOutcome = iota
	outFinished
	outRemoved
)

// sprintGraceWindow is how long after a sprint's start a ticket may still join
// and count as "Started with" rather than "Added" (#65). It absorbs the natural
// rollover churn — carry-overs re-added, tickets created directly into the
// sprint, last-minute pull-ins during planning — so only genuine later scope
// creep lands in Added. The single source of the grace length; the Started-with
// / Added anchor is `sprint start + sprintGraceWindow`.
const sprintGraceWindow = time.Hour

// firstEntryAfterSubquery selects the issues whose FIRST membership entry into a
// sprint falls strictly after an instant — the "Added" predicate. Binds two args
// in order: the sprint id and the RFC3339 instant (the grace-window end). A
// ticket present at/before the instant (even one that later left and re-entered)
// is excluded, since its earliest entering transition is not after the instant.
const firstEntryAfterSubquery = `
		SELECT issue_key FROM sprint_membership_transition
		WHERE sprint_id = ? AND entered = 1
		GROUP BY issue_key
		HAVING MIN(transitioned_at) > ?`

// finishedInWindowSubquery selects the issues that crossed into the Done set
// within [from, to) — the "Finished" predicate, shared by the Finished column
// (as a JOIN) and by the Open/Removed columns (as NOT IN, i.e. "not finished").
// It wraps doneCrossingClause and filters its crossing instant to the window.
// Binds, in order: the Done-crossing args (doneCrossingClause), then the fromStr
// and toStr window bounds. A fresh copy per call so each use site gets its own
// args slice.
func finishedInWindowSubquery(fromStr, toStr string) (query string, args []any) {
	crossSQL, crossArgs := doneCrossingClause()
	query = `
			SELECT issue_key FROM (` + crossSQL + `) crossing
			WHERE crossing.completed_at >= ? AND crossing.completed_at < ?`
	args = append(crossArgs, fromStr, toStr)
	return query, args
}

// SprintCategoriesInWindow computes the Sprint view's cohort × outcome breakdown
// for the sprint with the given id over [from, to): rows Started with / Added and
// their Total, each split into Open / Finished / Removed / Total columns.
//
// Cohorts. Started-with = the active-sprint members at the grace-window end (from
// + sprintGraceWindow), regardless of status (membersAtSubquery). Added = tickets
// whose first membership entry falls after the grace window (firstEntryAfterSubquery).
// The two cohorts are disjoint by construction.
//
// Outcomes, over the full [from, to) window (now = to). Finished = crossed into
// the Done set within the window (finishedInWindowSubquery). Removed = NOT finished
// and (cancelled OR no longer a member at now). Open = NOT finished, NOT cancelled,
// and still a member at now — the remainder. Total = Open + Finished + Removed.
//
// The removal asymmetry (#70): for the Added cohort, Removed counts ONLY
// cancellation — an added-then-reprioritised-out ticket (gone from the sprint, not
// cancelled, not finished) is dropped entirely, so it appears in no column and the
// Added Total is less than the raw cohort membership. For Started-with, a
// reprioritised-out ticket is kept under Removed. The Total row is the column-wise
// sum of the two cohorts.
func (s *Store) SprintCategoriesInWindow(sprintID int, from, to time.Time) (SprintCategories, error) {
	var wc SprintCategories
	var err error
	if wc.StartedWith, err = s.cohortTally(catStartedWith, sprintID, from, to); err != nil {
		return SprintCategories{}, err
	}
	if wc.Added, err = s.cohortTally(catAdded, sprintID, from, to); err != nil {
		return SprintCategories{}, err
	}
	wc.Total = SprintCohort{
		Open:     addTally(wc.StartedWith.Open, wc.Added.Open),
		Finished: addTally(wc.StartedWith.Finished, wc.Added.Finished),
		Removed:  addTally(wc.StartedWith.Removed, wc.Added.Removed),
		Total:    addTally(wc.StartedWith.Total, wc.Added.Total),
	}
	return wc, nil
}

// cohortTally computes one cohort's Open / Finished / Removed / Total columns by
// tallying each outcome bucket (categoryTally) and summing the three into Total.
func (s *Store) cohortTally(cohort sprintCategory, sprintID int, from, to time.Time) (SprintCohort, error) {
	var c SprintCohort
	var err error
	if c.Open, err = s.categoryTally(cohort, outOpen, sprintID, from, to); err != nil {
		return SprintCohort{}, err
	}
	if c.Finished, err = s.categoryTally(cohort, outFinished, sprintID, from, to); err != nil {
		return SprintCohort{}, err
	}
	if c.Removed, err = s.categoryTally(cohort, outRemoved, sprintID, from, to); err != nil {
		return SprintCohort{}, err
	}
	c.Total = addTally(addTally(c.Open, c.Finished), c.Removed)
	return c, nil
}

// categoryTally tallies the rollup issues in one cohort × outcome cell (at
// current size) using the shared cell predicate (sprintCellPredicate). Join-side
// and where-side args are concatenated in the order the placeholders appear.
func (s *Store) categoryTally(cohort sprintCategory, outcome sprintOutcome, sprintID int, from, to time.Time) (SizeTally, error) {
	joinSQL, whereSQL, joinArgs, whereArgs := sprintCellPredicate(cohort, outcome, sprintID, from, to)

	query := `SELECT ` + sizeTallyColumns + `
		FROM issue` + joinSQL + `
		WHERE issue.type IN (` + rollupTypes + `)` + whereSQL

	args := append(joinArgs, whereArgs...)
	tally, err := scanTally(s.db.QueryRow(query, args...))
	if err != nil {
		return SizeTally{}, fmt.Errorf("sprint category tally: %w", err)
	}
	return tally, nil
}

// sprintCellPredicate assembles the JOIN and WHERE fragments (and their ordered
// bind args) for one cohort × outcome cell of the Sprint view over [from, to):
// the cohort-membership join (Started-with / Added) plus the outcome predicate
// (Open / Finished / Removed), assembled from the shared SQL fragments so the
// membership reconstruction, Done-crossing and cancelled/member tests each live
// in one place. "Cancelled" and "no longer a member" read the ticket's current
// state — its snapshot status and its membership at `to` (now). It is the SINGLE
// source of the cell predicates, shared by the cell tally (categoryTally) and the
// drill-down card select (categoryCards) so the drill list can never drift from
// the count. joinArgs precede whereArgs, matching the order the placeholders
// appear in the assembled query.
func sprintCellPredicate(cohort sprintCategory, outcome sprintOutcome, sprintID int, from, to time.Time) (joinSQL, whereSQL string, joinArgs, whereArgs []any) {
	fromStr := from.UTC().Format(time.RFC3339)
	toStr := to.UTC().Format(time.RFC3339)
	graceEndStr := from.Add(sprintGraceWindow).UTC().Format(time.RFC3339)
	canceled := normalizeStatus("Canceled")

	var joinB, whereB strings.Builder

	// Cohort membership.
	switch cohort {
	case catStartedWith:
		// Every active-sprint member at the grace-window end (sprint start +
		// sprintGraceWindow), regardless of status — the capacity baseline. A
		// snapshot: later removal or status change does not rewrite it, and there
		// is no open-at-start gate, so a member with no status history still counts.
		joinB.WriteString(` JOIN (` + membersAtSubquery + `) started_m ON started_m.issue_key = issue.key`)
		joinArgs = append(joinArgs, sprintID, graceEndStr)
	case catAdded:
		// A ticket whose FIRST membership entry falls after the grace window
		// (genuine scope creep). Re-entry of a ticket already present within the
		// grace window stays out of Added, so the two cohorts are disjoint.
		joinB.WriteString(` JOIN (` + firstEntryAfterSubquery + `) added_m ON added_m.issue_key = issue.key`)
		joinArgs = append(joinArgs, sprintID, graceEndStr)
	}

	// Outcome predicate over the [from, to) window (now = to).
	switch outcome {
	case outFinished:
		// Crossed into the Done set within the window.
		finSQL, finArgs := finishedInWindowSubquery(fromStr, toStr)
		joinB.WriteString(` JOIN (` + finSQL + `) fin ON fin.issue_key = issue.key`)
		joinArgs = append(joinArgs, finArgs...)
	case outOpen:
		// Not finished, not cancelled, still a member at now — the remainder.
		finSQL, finArgs := finishedInWindowSubquery(fromStr, toStr)
		whereB.WriteString(` AND issue.key NOT IN (` + finSQL + `)`)
		whereArgs = append(whereArgs, finArgs...)
		whereB.WriteString(` AND LOWER(issue.status) <> ?`)
		whereArgs = append(whereArgs, canceled)
		whereB.WriteString(` AND issue.key IN (` + membersAtSubquery + `)`)
		whereArgs = append(whereArgs, sprintID, toStr)
	case outRemoved:
		// Not finished, and cancelled or no longer a member — with the asymmetry:
		// Started-with keeps reprioritised-out tickets (the "no longer a member"
		// arm); Added counts ONLY cancellation, so a reprioritised-out add drops
		// out of every cell.
		finSQL, finArgs := finishedInWindowSubquery(fromStr, toStr)
		whereB.WriteString(` AND issue.key NOT IN (` + finSQL + `)`)
		whereArgs = append(whereArgs, finArgs...)
		if cohort == catStartedWith {
			whereB.WriteString(` AND (LOWER(issue.status) = ? OR issue.key NOT IN (` + membersAtSubquery + `))`)
			whereArgs = append(whereArgs, canceled, sprintID, toStr)
		} else {
			whereB.WriteString(` AND LOWER(issue.status) = ?`)
			whereArgs = append(whereArgs, canceled)
		}
	}

	return joinB.String(), whereB.String(), joinArgs, whereArgs
}

// SprintCellIssue is one issue behind a Sprint-view cell drill-down (#79): the
// board-card fields (so it renders with the same card machinery as the Board)
// plus its CURRENT status, shown as a status pill.
type SprintCellIssue struct {
	BoardCard
	Status string
}

// SprintCohortSel selects which cohort a drill-down cell covers, including the
// Total row — the union of both cohorts.
type SprintCohortSel int

const (
	CohortStartedWith SprintCohortSel = iota
	CohortAdded
	CohortTotal
)

// bases expands a cohort selector into the base cohorts it unions. The two base
// cohorts are disjoint by construction, so the union count equals the summed
// tally shown in the Total row.
func (c SprintCohortSel) bases() ([]sprintCategory, error) {
	switch c {
	case CohortStartedWith:
		return []sprintCategory{catStartedWith}, nil
	case CohortAdded:
		return []sprintCategory{catAdded}, nil
	case CohortTotal:
		return []sprintCategory{catStartedWith, catAdded}, nil
	}
	return nil, fmt.Errorf("unknown sprint cohort selector %d", c)
}

// SprintOutcomeSel selects which outcome column a drill-down cell covers,
// including the Total column — the union of Open + Finished + Removed.
type SprintOutcomeSel int

const (
	OutcomeOpen SprintOutcomeSel = iota
	OutcomeFinished
	OutcomeRemoved
	OutcomeTotal
)

// bases expands an outcome selector into the base outcomes it unions. The three
// base outcomes are mutually exclusive by construction (a ticket is Finished, or
// Open, or Removed — never two), so the union count equals the summed tally shown
// in the Total column.
func (o SprintOutcomeSel) bases() ([]sprintOutcome, error) {
	switch o {
	case OutcomeOpen:
		return []sprintOutcome{outOpen}, nil
	case OutcomeFinished:
		return []sprintOutcome{outFinished}, nil
	case OutcomeRemoved:
		return []sprintOutcome{outRemoved}, nil
	case OutcomeTotal:
		return []sprintOutcome{outOpen, outFinished, outRemoved}, nil
	}
	return nil, fmt.Errorf("unknown sprint outcome selector %d", o)
}

// SprintCellIssues returns the issues behind one cohort × outcome cell of the
// Sprint view for the sprint over [from, to) — the exact rows the cell tallies,
// SELECTed as cards so a drill-down list always matches the cell's count. It
// reuses sprintCellPredicate (the same predicates behind SprintCategoriesInWindow),
// so the removal asymmetry and every membership/outcome rule are shared. The
// Total row and Total column expand to the union of their constituent base cells
// (cohorts are disjoint; the outcomes are mutually exclusive), deduped by key so
// the union count matches the summed tally. Cards are sorted by workflow order
// then issue key, so same-status cards group together in the flat list.
func (s *Store) SprintCellIssues(sprintID int, from, to time.Time, cohort SprintCohortSel, outcome SprintOutcomeSel) ([]SprintCellIssue, error) {
	cohorts, err := cohort.bases()
	if err != nil {
		return nil, err
	}
	outcomes, err := outcome.bases()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var out []SprintCellIssue
	for _, c := range cohorts {
		for _, o := range outcomes {
			issues, err := s.categoryCards(c, o, sprintID, from, to)
			if err != nil {
				return nil, err
			}
			for _, is := range issues {
				if seen[is.Key] {
					continue
				}
				seen[is.Key] = true
				out = append(out, is)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case workflowLess(a.Status, b.Status):
			return true
		case workflowLess(b.Status, a.Status):
			return false
		default:
			return a.Key < b.Key
		}
	})
	return out, nil
}

// categoryCards selects the issues in one base cohort × base outcome cell as
// drill-down cards (board-card fields + current status), using the shared cell
// predicate (sprintCellPredicate) so the row set matches categoryTally exactly.
// The self-join resolves each card's parent epic (name + colour) from the epic
// issue's own row, matched on parent_key and gated to Epic parents, mirroring
// ActiveSprintBoard.
func (s *Store) categoryCards(cohort sprintCategory, outcome sprintOutcome, sprintID int, from, to time.Time) ([]SprintCellIssue, error) {
	joinSQL, whereSQL, joinArgs, whereArgs := sprintCellPredicate(cohort, outcome, sprintID, from, to)

	query := `SELECT issue.key, issue.summary, issue.size, issue.type, issue.assignee,
	       issue.assignee_avatar_url, epic.summary, epic.epic_color, issue.status
		FROM issue
		LEFT JOIN issue epic ON epic.key = issue.parent_key AND epic.type = 'Epic'` + joinSQL + `
		WHERE issue.type IN (` + rollupTypes + `)` + whereSQL

	args := append(joinArgs, whereArgs...)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("sprint cell cards: %w", err)
	}
	defer rows.Close()

	var out []SprintCellIssue
	for rows.Next() {
		var is SprintCellIssue
		var size, assignee, avatarURL, epicName, epicColor sql.NullString
		if err := rows.Scan(&is.Key, &is.Summary, &size, &is.Type, &assignee, &avatarURL, &epicName, &epicColor, &is.Status); err != nil {
			return nil, fmt.Errorf("scan sprint cell card: %w", err)
		}
		is.Size = size.String                   // "" when NULL (no estimate)
		is.Assignee = assignee.String           // "" when NULL (unassigned)
		is.AssigneeAvatarURL = avatarURL.String // "" when NULL (no avatar)
		is.EpicName = epicName.String           // "" when no parent epic
		is.EpicColor = epicColor.String         // "" when the epic's colour is unset
		out = append(out, is)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sprint cell cards: %w", err)
	}
	return out, nil
}

// addTally sums two size tallies field-by-field — the Sprint cohort Total and
// Total-row cells.
func addTally(a, b SizeTally) SizeTally {
	return SizeTally{
		S:          a.S + b.S,
		M:          a.M + b.M,
		L:          a.L + b.L,
		NoEstimate: a.NoEstimate + b.NoEstimate,
		Points:     a.Points + b.Points,
	}
}

// BoardCard is one issue on the sprint board — the fields a card renders.
type BoardCard struct {
	Key     string
	Summary string
	Size    string // T-shirt label 'S'/'M'/'L', or "" for no estimate
	Type    string // Task, Bug or Story
	// Assignee is the current assignee's display name ("" when unassigned);
	// AssigneeAvatarURL is that assignee's public Jira avatar image URL ("" when
	// unassigned or Jira reported none). The card renders the image, falling back
	// to initials computed from Assignee, or a neutral circle when unassigned.
	Assignee          string
	AssigneeAvatarURL string
	// EpicName is the parent epic's summary ("" when the card has no parent epic);
	// EpicColor is that epic's raw Jira Issue color value ("" when unset). Both are
	// resolved by joining the card's parent link to the epic issue's own row. The
	// card renders EpicName as a pill coloured from EpicColor.
	EpicName  string
	EpicColor string
}

// BoardColumn is one workflow-status column of the sprint board and the
// active-sprint cards currently in that status.
type BoardColumn struct {
	Status string
	Cards  []BoardCard
}

// Board is the active-sprint Kanban projection: the fixed, ordered set of
// workflow columns (boardColumnOrder), always present when an active sprint
// exists — even the empty ones. The board is NOT filtered to open work, so
// Done-category columns (e.g. DONE (This Sprint), Released / Deployed) appear
// alongside the open ones — it mirrors the Jira sprint board for data-quality
// validation. When no active sprint is recorded, Columns is empty and the view
// renders its "no active sprint" state instead.
type Board struct {
	Columns []BoardColumn
}

// ActiveSprintBoard projects the ACTIVE sprint as a Kanban board. Seeding the
// fixed column set (boardColumnOrder) is gated on an active sprint existing: with
// none recorded it returns an empty Board so the view shows its "no active
// sprint" state rather than seven empty columns. When an active sprint exists it
// always renders those columns — even empty ones — and places every active-sprint
// issue (active_sprint IS NOT NULL) of a rollup type (Task/Bug/Story; Epics and
// Sub-tasks are stored but excluded, consistent with the rollups) into its column
// by case-insensitive status match. Issues in a board-excluded status (Triage,
// Canceled) are dropped. A card in a status that is neither seeded nor excluded
// (a brand-new Jira status) surfaces as an extra column AFTER the known ones
// rather than being dropped, so the board never silently loses a card. Cards
// within a column keep issue-key order for a stable render.
func (s *Store) ActiveSprintBoard() (Board, error) {
	// Seeding the fixed columns is gated on an active sprint existing.
	if _, ok, err := s.ActiveSprintWindow(); err != nil {
		return Board{}, err
	} else if !ok {
		return Board{}, nil
	}

	// The self-join resolves each card's parent epic (name + colour) from the epic
	// issue's own row, matched on the child's parent_key and gated to Epic parents
	// so a non-epic parent yields no pill.
	const query = `
		SELECT i.status, i.key, i.summary, i.size, i.type, i.assignee, i.assignee_avatar_url,
		       epic.summary, epic.epic_color
		FROM issue i
		LEFT JOIN issue epic ON epic.key = i.parent_key AND epic.type = 'Epic'
		WHERE i.active_sprint IS NOT NULL
		  AND i.type IN (` + rollupTypes + `)
		ORDER BY i.key`

	rows, err := s.db.Query(query)
	if err != nil {
		return Board{}, fmt.Errorf("active sprint board: %w", err)
	}
	defer rows.Close()

	// Seed the fixed columns up front so every one renders even when empty, and
	// index them by normalized status for case-insensitive card placement.
	seeded := make([]BoardColumn, len(boardColumnOrder))
	seededIndex := make(map[string]int, len(boardColumnOrder))
	for i, status := range boardColumnOrder {
		seeded[i] = BoardColumn{Status: status}
		seededIndex[normalizeStatus(status)] = i
	}

	// Cards in a status that is neither seeded nor board-excluded (a brand-new
	// Jira status) collect here, preserving key-ordered arrival, and surface as
	// extra columns after the known ones.
	extraByStatus := map[string][]BoardCard{}
	var extraStatuses []string

	for rows.Next() {
		var status string
		var card BoardCard
		var size, assignee, avatarURL, epicName, epicColor sql.NullString
		if err := rows.Scan(&status, &card.Key, &card.Summary, &size, &card.Type, &assignee, &avatarURL, &epicName, &epicColor); err != nil {
			return Board{}, fmt.Errorf("scan board card: %w", err)
		}
		card.Size = size.String                   // "" when NULL (no estimate)
		card.Assignee = assignee.String           // "" when NULL (unassigned)
		card.AssigneeAvatarURL = avatarURL.String // "" when NULL (no avatar)
		card.EpicName = epicName.String           // "" when no parent epic
		card.EpicColor = epicColor.String         // "" when the epic's colour is unset
		norm := normalizeStatus(status)
		switch i, seededHere := seededIndex[norm]; {
		case boardExcludedStatuses[norm]:
			// Off-board by maintainer decision: drop the card entirely.
		case seededHere:
			seeded[i].Cards = append(seeded[i].Cards, card)
		default:
			if _, seen := extraByStatus[status]; !seen {
				extraStatuses = append(extraStatuses, status)
			}
			extraByStatus[status] = append(extraByStatus[status], card)
		}
	}
	if err := rows.Err(); err != nil {
		return Board{}, fmt.Errorf("iterate board cards: %w", err)
	}

	sort.SliceStable(extraStatuses, func(i, j int) bool { return workflowLess(extraStatuses[i], extraStatuses[j]) })
	board := Board{Columns: make([]BoardColumn, 0, len(seeded)+len(extraStatuses))}
	board.Columns = append(board.Columns, seeded...)
	for _, status := range extraStatuses {
		board.Columns = append(board.Columns, BoardColumn{Status: status, Cards: extraByStatus[status]})
	}
	return board, nil
}
