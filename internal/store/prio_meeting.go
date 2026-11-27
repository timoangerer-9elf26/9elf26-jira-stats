package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PrioMeeting records a completed meeting independently of the Jira projection.
// Its ID identifies the following meeting window. ID zero is the initial window.
type PrioMeeting struct {
	ID        int64
	EndedAt   time.Time
	AccountID string
}

func (s *Store) LastPrioMeeting() (PrioMeeting, error) {
	var meeting PrioMeeting
	var endedAt string
	err := s.db.QueryRow(`SELECT id, ended_at, account_id FROM prio_meeting ORDER BY id DESC LIMIT 1`).
		Scan(&meeting.ID, &endedAt, &meeting.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return PrioMeeting{}, nil
	}
	if err != nil {
		return PrioMeeting{}, fmt.Errorf("read last Prio meeting: %w", err)
	}
	meeting.EndedAt, err = time.Parse(time.RFC3339Nano, endedAt)
	if err != nil {
		return PrioMeeting{}, fmt.Errorf("parse Prio meeting end: %w", err)
	}
	return meeting, nil
}

// CompletePrioMeeting inserts the end only while window is still current.
// Checking and advancing the window are one atomic SQLite statement.
func (s *Store) CompletePrioMeeting(window int64, endedAt time.Time, accountID string) error {
	_, err := s.db.Exec(`
		INSERT INTO prio_meeting (ended_at, account_id)
		SELECT ?, CASE WHEN EXISTS (SELECT 1 FROM jira_privacy_state WHERE account_hash = ? AND closed = 1)
		               THEN '' ELSE ? END
		WHERE COALESCE((SELECT MAX(id) FROM prio_meeting), 0) = ?`,
		endedAt.UTC().Format(time.RFC3339Nano), privacyHash(accountID), reportableID(accountID), window)
	if err != nil {
		return fmt.Errorf("complete Prio meeting: %w", err)
	}
	return nil
}

// PrioMeetingChange records an app mutation Jira confirmed.
type PrioMeetingChange struct {
	IssueKey, Label, AccountID string
	OccurredAt                 time.Time
}

// RecordPrioMeetingChange assigns the successful write to the current window
// atomically with respect to meeting completion.
func (s *Store) RecordPrioMeetingChange(change PrioMeetingChange) error {
	_, err := s.db.Exec(`
		INSERT INTO prio_meeting_change (meeting_window, issue_key, label, account_id, occurred_at)
		VALUES (COALESCE((SELECT MAX(id) FROM prio_meeting), 0), ?, ?,
		        CASE WHEN EXISTS (SELECT 1 FROM jira_privacy_state WHERE account_hash = ? AND closed = 1)
		             THEN '' ELSE ? END, ?)`,
		change.IssueKey, change.Label, privacyHash(change.AccountID), reportableID(change.AccountID),
		change.OccurredAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) PrioMeetingChanges(window int64) ([]PrioMeetingChange, error) {
	rows, err := s.db.Query(`
		SELECT issue_key, label, account_id, occurred_at
		FROM prio_meeting_change WHERE meeting_window = ? ORDER BY id`, window)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []PrioMeetingChange
	for rows.Next() {
		var change PrioMeetingChange
		var occurredAt string
		if err := rows.Scan(&change.IssueKey, &change.Label, &change.AccountID, &occurredAt); err != nil {
			return nil, err
		}
		change.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}
