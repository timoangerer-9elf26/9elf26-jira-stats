package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

type identityRule struct {
	closed, update bool
	name, avatar   string
}

func readIdentityRule(tx *sql.Tx, id string) (rule identityRule, err error) {
	if id == "" {
		return rule, nil
	}
	if reportableID(id) == "" {
		return identityRule{closed: true}, nil
	}
	err = tx.QueryRow(`SELECT
		EXISTS (SELECT 1 FROM jira_privacy_state WHERE account_hash = ? AND closed = 1),
		COALESCE((SELECT display_name FROM jira_privacy_profile WHERE account_id = ?), ''),
		COALESCE((SELECT avatar_url FROM jira_privacy_profile WHERE account_id = ?), ''),
		EXISTS (SELECT 1 FROM jira_privacy_profile WHERE account_id = ?)`,
		privacyHash(id), id, id, id).Scan(&rule.closed, &rule.name, &rule.avatar, &rule.update)
	return rule, err
}

func (r identityRule) person(p jira.Person) jira.Person {
	if r.closed {
		return jira.Person{}
	}
	if r.update {
		p.DisplayName = r.name
	}
	return p
}

func protectIssueIdentities(tx *sql.Tx, issue jira.Issue) (jira.Issue, error) {
	rules := make(map[string]identityRule)
	ruleFor := func(id string) (identityRule, error) {
		if rule, ok := rules[id]; ok {
			return rule, nil
		}
		rule, err := readIdentityRule(tx, id)
		rules[id] = rule
		return rule, err
	}
	assignee, err := ruleFor(issue.AssigneeAccountID)
	if err != nil {
		return issue, err
	}
	if assignee.closed {
		issue.AssigneeAccountID, issue.Assignee, issue.AssigneeAvatarURL = "", "", ""
	} else if assignee.update {
		issue.Assignee, issue.AssigneeAvatarURL = assignee.name, assignee.avatar
	}
	creator, err := ruleFor(issue.CreatorAccountID)
	if err != nil {
		return issue, err
	}
	author := creator.person(jira.Person{AccountID: issue.CreatorAccountID, DisplayName: issue.Creator})
	issue.CreatorAccountID, issue.Creator = author.AccountID, author.DisplayName
	issue.Changelog = append([]jira.ChangelogEntry(nil), issue.Changelog...)
	for i, change := range issue.Changelog {
		rule, err := ruleFor(change.Author.AccountID)
		if err != nil {
			return issue, err
		}
		issue.Changelog[i].Author = rule.person(change.Author)
	}
	issue.Comments = append([]jira.Comment(nil), issue.Comments...)
	issue.Description, _, err = protectMentions(issue.Description, ruleFor)
	if err != nil {
		return issue, err
	}
	for i, comment := range issue.Comments {
		rule, err := ruleFor(comment.Author.AccountID)
		if err != nil {
			return issue, err
		}
		issue.Comments[i].Author = rule.person(comment.Author)
		issue.Comments[i].Body, _, err = protectMentions(comment.Body, ruleFor)
		if err != nil {
			return issue, err
		}
	}
	return issue, nil
}

// ADF mentions have structured account IDs and cached display text. Keep their
// node shape so a ticket with rich content remains a Jira-only edit.
func protectMentions(raw json.RawMessage, ruleFor func(string) (identityRule, error)) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	var content any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&content); err != nil {
		return nil, false, err
	}
	root, ok := content.(map[string]any)
	if !ok {
		return raw, false, nil
	}
	var changed bool
	var visit func(map[string]any) error
	visit = func(node map[string]any) error {
		if node["type"] == "mention" {
			attrs, _ := node["attrs"].(map[string]any)
			id, _ := attrs["id"].(string)
			if attrs["userType"] == "SPECIAL" {
				return nil
			}
			rule, err := ruleFor(id)
			if id == "" {
				rule.closed = true
			}
			if err != nil {
				return err
			}
			if rule.closed && (id != "unknown" || len(attrs) > 1) {
				node["attrs"] = map[string]any{"id": "unknown"}
				changed = true
			} else if rule.update && len(attrs) > 1 {
				node["attrs"] = map[string]any{"id": id}
				changed = true
			}
		}
		children, _ := node["content"].([]any)
		for _, child := range children {
			if nested, ok := child.(map[string]any); ok {
				if err := visit(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, false, err
	}
	if !changed {
		return raw, false, nil
	}
	modified, err := json.Marshal(root)
	return modified, true, err
}

func removeDocumentIdentities(ctx context.Context, tx *sql.Tx, affected map[string]identityRule) error {
	rows, err := tx.QueryContext(ctx, `SELECT key, description, comments FROM issue
		WHERE description IS NOT NULL OR comments IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type documents struct {
		key                   string
		description, comments []byte
	}
	var edits []documents
	ruleFor := func(id string) (identityRule, error) { return affected[id], nil }
	for rows.Next() {
		var doc documents
		if err := rows.Scan(&doc.key, &doc.description, &doc.comments); err != nil {
			return err
		}
		description, changed, err := protectMentions(doc.description, ruleFor)
		if err != nil {
			return err
		}
		var comments []jira.Comment
		if len(doc.comments) > 0 {
			if err := json.Unmarshal(doc.comments, &comments); err != nil {
				return err
			}
		}
		commentsChanged := false
		for i, comment := range comments {
			person := affected[comment.Author.AccountID].person(comment.Author)
			body, bodyChanged, err := protectMentions(comment.Body, ruleFor)
			if err != nil {
				return err
			}
			commentsChanged = commentsChanged || bodyChanged || person != comment.Author
			comments[i].Author, comments[i].Body = person, body
		}
		if commentsChanged {
			doc.comments, err = json.Marshal(comments)
			if err != nil {
				return err
			}
		}
		if changed || commentsChanged {
			doc.description = description
			edits = append(edits, doc)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, doc := range edits {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET description = ?, comments = ? WHERE key = ?`,
			doc.description, doc.comments, doc.key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PendingPrivacyProfiles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id FROM jira_privacy_profile WHERE retrieved_at = '' ORDER BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []string
	for rows.Next() {
		var account string
		if err := rows.Scan(&account); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// RefreshPrivacyProfile replaces display data everywhere it appears. The
// account references and their retrieval times stay with their original rows.
func (s *Store) RefreshPrivacyProfile(ctx context.Context, profile jira.ProjectMember, retrieved time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id, name, avatar := profile.AccountID, profile.DisplayName, profile.AvatarURL
	result, err := tx.ExecContext(ctx, `
		UPDATE jira_privacy_profile SET display_name = ?, avatar_url = ?, retrieved_at = ?
		WHERE account_id = ? AND retrieved_at = ''`, name, avatar, retrieved.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated == 0 {
		return err
	}
	for _, edit := range []struct {
		query string
		args  []any
	}{
		{`UPDATE project_member SET display_name = ?, avatar_url = NULLIF(?, '') WHERE account_id = ?`, []any{name, avatar, id}},
		{`UPDATE issue SET assignee = ?, assignee_avatar_url = NULLIF(?, '') WHERE assignee_account_id = ?`, []any{name, avatar, id}},
		{`UPDATE issue SET creator = ? WHERE creator_account_id = ?`, []any{name, id}},
		{`UPDATE status_transition SET author_display_name = ? WHERE author_account_id = ?`, []any{name, id}},
	} {
		if _, err := tx.ExecContext(ctx, edit.query, edit.args...); err != nil {
			return err
		}
	}
	if err := removeDocumentIdentities(ctx, tx, map[string]identityRule{id: {update: true, name: name}}); err != nil {
		return err
	}
	return tx.Commit()
}
