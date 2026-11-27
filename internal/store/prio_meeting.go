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
		SELECT ?, ? WHERE COALESCE((SELECT MAX(id) FROM prio_meeting), 0) = ?`,
		endedAt.UTC().Format(time.RFC3339Nano), accountID, window)
	if err != nil {
		return fmt.Errorf("complete Prio meeting: %w", err)
	}
	return nil
}
