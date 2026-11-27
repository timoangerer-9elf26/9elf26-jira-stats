package store

import "context"

func (s *Store) IssueKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM issue ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// RemoveIssues deletes issue snapshots and their rebuildable logs atomically.
func (s *Store) RemoveIssues(ctx context.Context, keys []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Foreign keys are not enforced on this connection.
	for _, key := range keys {
		for _, stmt := range []string{
			`DELETE FROM sprint_membership_transition WHERE issue_key = ?`,
			`DELETE FROM status_transition WHERE issue_key = ?`,
			`DELETE FROM issue WHERE key = ?`,
		} {
			if _, err := tx.ExecContext(ctx, stmt, key); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
