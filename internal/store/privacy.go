package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

// PrivacyAccount carries the oldest retrieval time of any retained identity
// linked to this Atlassian account.
type PrivacyAccount struct {
	AccountID string    `json:"accountId"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9:-]{1,128}$`)

func reportableID(id string) string {
	switch strings.ToLower(id) {
	case "unknown", "stub", "anonymous", "unassigned", "-1":
		return ""
	}
	if !accountIDPattern.MatchString(id) {
		return ""
	}
	return id
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

	reported := ceilingUnixMicro(reportedAt)
	if _, err := tx.ExecContext(ctx, `UPDATE jira_privacy_poll SET cycle_seconds = ?, retry_at = 0 WHERE id = 1`,
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
	return tx.Commit()
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

func (s *Store) PrivacyPoll(ctx context.Context) (cycle time.Duration, retryAt time.Time, err error) {
	var cycleSeconds, retryUnix int64
	err = s.db.QueryRowContext(ctx, `SELECT cycle_seconds, retry_at FROM jira_privacy_poll WHERE id = 1`).Scan(&cycleSeconds, &retryUnix)
	return time.Duration(cycleSeconds) * time.Second, time.UnixMicro(retryUnix), err
}

// ReservePrivacyBatch preserves the earliest safe retry if the process stops
// before it can tell whether Atlassian accepted the batch.
func (s *Store) ReservePrivacyBatch(ctx context.Context, batch []PrivacyAccount, now, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM jira_privacy_attempt WHERE expires_at <= ?`, now.UnixMicro()); err != nil {
		return err
	}
	for _, account := range batch {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jira_privacy_attempt (account_hash, expires_at) VALUES (?, ?)
			ON CONFLICT(account_hash) DO UPDATE SET expires_at = MAX(expires_at, excluded.expires_at)`,
			privacyHash(account.AccountID), ceilingUnixMicro(expires)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReleasePrivacyBatch lets a definitively rejected batch be sent again, after
// the deadline if Atlassian also supplied a rate-limit directive.
func (s *Store) ReleasePrivacyBatch(ctx context.Context, batch []PrivacyAccount, retryAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !retryAt.IsZero() {
		if _, err := tx.ExecContext(ctx, `UPDATE jira_privacy_poll SET retry_at = ? WHERE id = 1`, ceilingUnixMicro(retryAt)); err != nil {
			return err
		}
	}
	for _, account := range batch {
		if _, err := tx.ExecContext(ctx, `DELETE FROM jira_privacy_attempt WHERE account_hash = ?`, privacyHash(account.AccountID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ceilingUnixMicro(t time.Time) int64 {
	micros := t.UnixMicro()
	if t.Nanosecond()%1000 != 0 {
		micros++
	}
	return micros
}
