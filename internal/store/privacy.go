package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// PrivacyAccount carries the oldest retrieval time of any retained identity
// linked to this Atlassian account.
type PrivacyAccount struct {
	AccountID string    `json:"accountId"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func reportableID(id string) string {
	if jira.IsReportableAccountID(id) {
		return id
	}
	return ""
}

func privacyHash(account string) string {
	sum := sha256.Sum256([]byte(account))
	return hex.EncodeToString(sum[:])
}

// PrivacyDue takes a consistent inventory before making external requests.
// Jira event timestamps are used only for app-authored audit rows, where the
// actor identity was captured at the same instant.
func (s *Store) PrivacyDue(ctx context.Context, now time.Time, limit int) ([]PrivacyAccount, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT MAX(retry_at, claim_until) > ?
		OR EXISTS (SELECT 1 FROM jira_privacy_inbox) FROM jira_privacy_poll WHERE id = 1`, now.UnixMicro()).Scan(&blocked); err != nil {
		return nil, err
	}
	if blocked {
		return nil, nil
	}

	suppressed := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT account_hash FROM jira_privacy_state
		WHERE closed = 1 OR last_reported_at + 1000000 * MAX(cycle_seconds, (SELECT cycle_seconds FROM jira_privacy_poll WHERE id = 1)) > ?
		UNION SELECT account_hash FROM jira_privacy_attempt WHERE expires_at > ?`, now.UnixMicro(), now.UnixMicro())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			rows.Close()
			return nil, err
		}
		suppressed[hash] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `
		SELECT account_id, strftime('%Y-%m-%dT%H:%M:%SZ', retrieved_at, 'unixepoch') FROM jira_auth_grant
		UNION ALL SELECT account_id, strftime('%Y-%m-%dT%H:%M:%SZ', retrieved_at, 'unixepoch') FROM jira_auth_session
		UNION ALL SELECT account_id, retrieved_at FROM project_member
		UNION ALL SELECT assignee_account_id, synced_at FROM issue WHERE assignee_account_id <> ''
		UNION ALL SELECT creator_account_id, synced_at FROM issue WHERE creator_account_id <> ''
		UNION ALL SELECT author_account_id, author_retrieved_at FROM status_transition WHERE author_account_id <> ''
		UNION ALL SELECT account_id, ended_at FROM prio_meeting
		UNION ALL SELECT account_id, occurred_at FROM prio_meeting_change
		UNION ALL SELECT account_id, retrieved_at FROM jira_privacy_profile WHERE retrieved_at <> ''
		UNION ALL SELECT comment.value ->> '$.Author.AccountID', issue.synced_at
			FROM issue, json_each(CAST(comments AS TEXT)) AS comment
			WHERE comment.value ->> '$.Author.AccountID' IS NOT NULL
		UNION ALL SELECT node.value ->> '$.attrs.id', issue.synced_at
			FROM issue, json_tree(json_array(json(CAST(description AS TEXT)), json(CAST(comments AS TEXT)))) AS node
			WHERE node.type = 'object' AND node.value ->> '$.type' = 'mention'
				AND COALESCE(node.value ->> '$.attrs.userType', '') <> 'SPECIAL'
				AND LOWER(node.value ->> '$.attrs.id') NOT IN ('all', 'here', 'everyone')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	oldest := make(map[string]time.Time)
	for rows.Next() {
		var id, retrievedAt string
		if err := rows.Scan(&id, &retrievedAt); err != nil {
			return nil, err
		}
		if reportableID(id) == "" {
			continue
		}
		if _, seen := oldest[id]; !seen && suppressed[privacyHash(id)] {
			continue
		}
		retrieved, err := time.Parse(time.RFC3339Nano, retrievedAt)
		if err != nil {
			return nil, errors.New("invalid privacy retrieval timestamp")
		}
		if previous, ok := oldest[id]; !ok || retrieved.Before(previous) {
			oldest[id] = retrieved.UTC()
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	accounts := make([]PrivacyAccount, 0, len(oldest))
	for id, retrieved := range oldest {
		accounts = append(accounts, PrivacyAccount{AccountID: id, UpdatedAt: retrieved})
	}
	slices.SortFunc(accounts, func(a, b PrivacyAccount) int {
		if cmp := a.UpdatedAt.Compare(b.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.AccountID, b.AccountID)
	})
	if limit > 0 && len(accounts) > limit {
		accounts = accounts[:limit]
	}
	return accounts, nil
}

// ApplyPrivacyReport commits the report cadence and all returned actions
// together. clearGrantProfile removes cached personal data from an otherwise
// usable OAuth grant. It can return nil to discard an unreadable grant.
func (s *Store) ApplyPrivacyReport(ctx context.Context, reportedAt time.Time, cycle time.Duration, accounts []PrivacyAccount,
	actions map[string]string, clearGrantProfile func(string, []byte) []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := applyPrivacyReport(ctx, tx, reportedAt, cycle, accounts, actions, clearGrantProfile); err != nil {
		return err
	}
	return tx.Commit()
}

func applyPrivacyReport(ctx context.Context, tx *sql.Tx, reportedAt time.Time, cycle time.Duration, accounts []PrivacyAccount,
	actions map[string]string, clearGrantProfile func(string, []byte) []byte) error {
	reported := ceilingUnixMicro(reportedAt)
	if _, err := tx.ExecContext(ctx, `UPDATE jira_privacy_poll SET cycle_seconds = ? WHERE id = 1`,
		int64(cycle/time.Second)); err != nil {
		return err
	}
	affected := make(map[string]identityRule)
	for _, account := range accounts {
		id := account.AccountID
		status := actions[id]
		hash := privacyHash(id)
		if _, err := tx.ExecContext(ctx, `DELETE FROM jira_privacy_attempt WHERE account_hash = ?`, hash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO jira_privacy_state (account_hash, last_reported_at, cycle_seconds, closed)
			VALUES (?, ?, ?, ?) ON CONFLICT(account_hash) DO UPDATE SET last_reported_at = excluded.last_reported_at,
			cycle_seconds = excluded.cycle_seconds, closed = MAX(closed, excluded.closed)`,
			hash, reported, int64(cycle/time.Second), status == "closed"); err != nil {
			return err
		}

		switch status {
		case "closed":
			affected[id] = identityRule{closed: true}
			for _, stmt := range []string{
				`DELETE FROM jira_auth_session WHERE account_id = ?`,
				`DELETE FROM jira_auth_grant WHERE account_id = ?`,
				`DELETE FROM project_member WHERE account_id = ?`,
				`DELETE FROM jira_privacy_profile WHERE account_id = ?`,
				`UPDATE prio_meeting SET account_id = '' WHERE account_id = ?`,
				`UPDATE prio_meeting_change SET account_id = '' WHERE account_id = ?`,
				`UPDATE issue SET assignee_account_id = '', assignee = NULL, assignee_avatar_url = NULL WHERE assignee_account_id = ?`,
				`UPDATE issue SET creator_account_id = '', creator = NULL WHERE creator_account_id = ?`,
				`UPDATE status_transition SET author_account_id = '', author_display_name = '', author_retrieved_at = '' WHERE author_account_id = ?`,
			} {
				if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
					return err
				}
			}
		case "updated":
			affected[id] = identityRule{update: true}
			for _, stmt := range []string{
				`INSERT INTO jira_privacy_profile (account_id) VALUES (?) ON CONFLICT(account_id) DO UPDATE
					SET display_name = '', avatar_url = '', retrieved_at = ''`,
				`UPDATE project_member SET display_name = '', avatar_url = NULL WHERE account_id = ?`,
				`UPDATE issue SET assignee = NULL, assignee_avatar_url = NULL WHERE assignee_account_id = ?`,
				`UPDATE issue SET creator = NULL WHERE creator_account_id = ?`,
				`UPDATE status_transition SET author_display_name = '' WHERE author_account_id = ?`,
			} {
				if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
					return err
				}
			}
			if clearGrantProfile != nil {
				if err := sanitizeGrantProfile(ctx, tx, id, clearGrantProfile); err != nil {
					return err
				}
			}
		}
	}
	if len(affected) > 0 {
		if err := removeDocumentIdentities(ctx, tx, affected); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeGrantProfile(ctx context.Context, tx *sql.Tx, id string, clear func(string, []byte) []byte) error {
	var grant []byte
	err := tx.QueryRowContext(ctx, `SELECT ciphertext FROM jira_auth_grant WHERE account_id = ?`, id).Scan(&grant)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	grant = clear(id, grant)
	if grant != nil {
		_, err := tx.ExecContext(ctx, `UPDATE jira_auth_grant SET ciphertext = ? WHERE account_id = ?`, grant, id)
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jira_auth_session WHERE account_id = ?`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM jira_auth_grant WHERE account_id = ?`, id)
	return err
}

func (s *Store) PrivacyRetryAt(ctx context.Context) (time.Time, error) {
	var retryUnix int64
	err := s.db.QueryRowContext(ctx, `SELECT retry_at FROM jira_privacy_poll WHERE id = 1`).Scan(&retryUnix)
	if err != nil || retryUnix == 0 {
		return time.Time{}, err
	}
	return time.UnixMicro(retryUnix), nil
}

// ClaimPrivacyBatch revalidates the selected accounts under the SQLite writer
// lock. One worker may send reports per database. Its short lease expires
// after a crash, but the accounts remain suppressed for the full cycle.
func (s *Store) ClaimPrivacyBatch(ctx context.Context, batch []PrivacyAccount, now, leaseUntil time.Time) (string, time.Duration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	claim := rand.Text()
	var cycleSeconds int64
	err = tx.QueryRowContext(ctx, `UPDATE jira_privacy_poll SET claim = ?, claim_until = ?
		WHERE id = 1 AND claim_until <= ? AND retry_at <= ? AND NOT EXISTS (SELECT 1 FROM jira_privacy_inbox)
		RETURNING cycle_seconds`, claim, ceilingUnixMicro(leaseUntil), now.UnixMicro(), now.UnixMicro()).Scan(&cycleSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	cycle := time.Duration(cycleSeconds) * time.Second
	if _, err := tx.ExecContext(ctx, `DELETE FROM jira_privacy_attempt WHERE expires_at <= ?`, now.UnixMicro()); err != nil {
		return "", 0, err
	}
	for _, account := range batch {
		hash := privacyHash(account.AccountID)
		result, err := tx.ExecContext(ctx, `INSERT INTO jira_privacy_attempt (account_hash, expires_at, claim)
			SELECT ?, ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM jira_privacy_state WHERE account_hash = ?
				AND (closed = 1 OR last_reported_at + 1000000 * MAX(cycle_seconds, ?) > ?))
			ON CONFLICT(account_hash) DO NOTHING`,
			hash, ceilingUnixMicro(leaseUntil.Add(cycle)), claim, hash, cycleSeconds, now.UnixMicro())
		if err != nil {
			return "", 0, err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows == 0 {
			return "", 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return claim, cycle, nil
}

var errPrivacyClaimLost = errors.New("reporting claim expired")

func finishPrivacyClaim(ctx context.Context, tx *sql.Tx, claim string, retryAt time.Time) error {
	var retry int64
	if !retryAt.IsZero() {
		retry = ceilingUnixMicro(retryAt)
	}
	result, err := tx.ExecContext(ctx, `UPDATE jira_privacy_poll SET claim = '', claim_until = 0, retry_at = MAX(retry_at, ?)
		WHERE id = 1 AND claim = ?`, retry, claim)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return errPrivacyClaimLost
	}
	return err
}

// FinishPrivacyBatch releases a rejected batch or retains its attempt deadline
// when the server may have accepted the request.
func (s *Store) FinishPrivacyBatch(ctx context.Context, claim string, retryAt time.Time, uncertain bool, uncertainUntil time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := finishPrivacyClaim(ctx, tx, claim, retryAt); err != nil {
		return err
	}
	if uncertain {
		if _, err := tx.ExecContext(ctx, `UPDATE jira_privacy_attempt SET expires_at = MAX(expires_at, ?) WHERE claim = ?`,
			ceilingUnixMicro(uncertainUntil), claim); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM jira_privacy_attempt WHERE claim = ?`, claim); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordPrivacyReport durably records a known result before touching any
// projection. Once recorded, it can be applied after a restart without Jira.
func (s *Store) RecordPrivacyReport(ctx context.Context, claim string, reportedAt time.Time, cycle time.Duration,
	accounts []PrivacyAccount, actions map[string]string) error {
	batch, err := json.Marshal(accounts)
	if err != nil {
		return err
	}
	// Persist actions only for the accounts included in the request.
	relevant := make(map[string]string)
	for _, account := range accounts {
		if status := actions[account.AccountID]; status != "" {
			relevant[account.AccountID] = status
		}
	}
	directives, err := json.Marshal(relevant)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := finishPrivacyClaim(ctx, tx, claim, time.Time{}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jira_privacy_inbox (id, reported_at, cycle_seconds, accounts, actions)
		VALUES (1, ?, ?, ?, ?)`, ceilingUnixMicro(reportedAt), int64(cycle/time.Second), batch, directives); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyPendingPrivacyReport removes the inbox record in the same transaction
// that applies it. A failed write leaves it available for local retry.
func (s *Store) ApplyPendingPrivacyReport(ctx context.Context, clearGrantProfile func(string, []byte) []byte) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var reported, seconds int64
	var batch, directives []byte
	err = tx.QueryRowContext(ctx, `DELETE FROM jira_privacy_inbox WHERE id = 1 RETURNING reported_at, cycle_seconds, accounts, actions`).
		Scan(&reported, &seconds, &batch, &directives)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var accounts []PrivacyAccount
	var actions map[string]string
	if err := json.Unmarshal(batch, &accounts); err != nil {
		return 0, err
	}
	if err := json.Unmarshal(directives, &actions); err != nil {
		return 0, err
	}
	if err := applyPrivacyReport(ctx, tx, time.UnixMicro(reported), time.Duration(seconds)*time.Second,
		accounts, actions, clearGrantProfile); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(accounts), nil
}

// NextPrivacyDeadline returns the next future reporting deadline. The worker
// still scans daily to discover newly retained accounts and refresh profiles.
func (s *Store) NextPrivacyDeadline(ctx context.Context, now time.Time) (time.Time, error) {
	var deadline int64
	err := s.db.QueryRowContext(ctx, `
		WITH poll AS (SELECT * FROM jira_privacy_poll WHERE id = 1),
		deadlines(at) AS (
			SELECT last_reported_at + 1000000 * MAX(state.cycle_seconds, poll.cycle_seconds)
			FROM jira_privacy_state AS state, poll WHERE closed = 0
			UNION ALL SELECT expires_at FROM jira_privacy_attempt
		)
		SELECT CASE
			WHEN EXISTS (SELECT 1 FROM jira_privacy_inbox) THEN ?
			WHEN MAX(retry_at, claim_until) > ? THEN MAX(retry_at, claim_until)
			ELSE COALESCE((SELECT MIN(at) FROM deadlines WHERE at > ?), 0)
		END FROM poll`, ceilingUnixMicro(now.Add(time.Minute)), now.UnixMicro(), now.UnixMicro()).Scan(&deadline)
	if err != nil || deadline == 0 {
		return time.Time{}, err
	}
	return time.UnixMicro(deadline), nil
}

func ceilingUnixMicro(t time.Time) int64 {
	micros := t.UnixMicro()
	if t.Nanosecond()%1000 != 0 {
		micros++
	}
	return micros
}
