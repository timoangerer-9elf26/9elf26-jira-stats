package store

import (
	"context"
	"errors"
)

var ErrOAuthAccountUnavailable = errors.New("Atlassian account unavailable")

// SaveOAuthFlow records the hashed state and browser binding for one authorization attempt.
func (s *Store) SaveOAuthFlow(ctx context.Context, state, browser, next string, now, expires int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM jira_auth_flow WHERE expires_at <= ?", now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO jira_auth_flow (state_hash, browser_hash, next_path, expires_at) VALUES (?, ?, ?, ?)",
		state, browser, next, expires)
	return err
}

// ConsumeOAuthFlow atomically validates and consumes an unexpired browser-bound attempt.
func (s *Store) ConsumeOAuthFlow(ctx context.Context, state, browser string, now int64) (string, error) {
	var next string
	err := s.db.QueryRowContext(ctx, `DELETE FROM jira_auth_flow
		WHERE state_hash = ? AND browser_hash = ? AND expires_at > ? RETURNING next_path`,
		state, browser, now).Scan(&next)
	return next, err
}

// SaveOAuthLogin atomically replaces the account grant and creates a new session.
// An existing session in the same browser is revoked as part of the rotation.
func (s *Store) SaveOAuthLogin(ctx context.Context, account string, grant []byte, token, previous string, now, expires int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rule, err := readIdentityRule(tx, account)
	if err != nil {
		return err
	}
	if account == "" || rule.closed {
		return ErrOAuthAccountUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jira_auth_grant (account_id, ciphertext, retrieved_at) VALUES (?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET ciphertext = excluded.ciphertext, retrieved_at = excluded.retrieved_at`, account, grant, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM jira_auth_session WHERE token_hash = ? OR expires_at <= ?", previous, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO jira_auth_session (token_hash, account_id, expires_at, retrieved_at) VALUES (?, ?, ?, ?)",
		token, account, expires, now); err != nil {
		return err
	}
	return tx.Commit()
}

// OAuthSessionAccount returns the account of an unexpired session.
func (s *Store) OAuthSessionAccount(ctx context.Context, token string, now int64) (string, error) {
	var account string
	err := s.db.QueryRowContext(ctx, "SELECT account_id FROM jira_auth_session WHERE token_hash = ? AND expires_at > ?",
		token, now).Scan(&account)
	return account, err
}

// DeleteOAuthSession revokes a single session.
func (s *Store) DeleteOAuthSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM jira_auth_session WHERE token_hash = ?", token)
	return err
}

// OAuthGrant reads an account's encrypted token pair.
func (s *Store) OAuthGrant(ctx context.Context, account string) ([]byte, error) {
	var grant []byte
	err := s.db.QueryRowContext(ctx, "SELECT ciphertext FROM jira_auth_grant WHERE account_id = ?", account).Scan(&grant)
	return grant, err
}

// ReplaceOAuthGrant updates the entire encrypted token pair in one statement.
func (s *Store) ReplaceOAuthGrant(ctx context.Context, account string, grant []byte) error {
	_, err := s.db.ExecContext(ctx, "UPDATE jira_auth_grant SET ciphertext = ? WHERE account_id = ?", grant, account)
	return err
}

// DeleteOAuthGrant invalidates all sessions of an account whose grant was lost.
func (s *Store) DeleteOAuthGrant(ctx context.Context, account string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM jira_auth_session WHERE account_id = ?", account); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM jira_auth_grant WHERE account_id = ?", account); err != nil {
		return err
	}
	return tx.Commit()
}
