package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// IssueDetail is the current projected ticket, including the ordered Jira
// comments visible without group or role restrictions and directional links.
type IssueDetail struct {
	PrioIssue
	Description       json.RawMessage
	Comments          []jira.Comment
	Links             []jira.IssueLink
	Assignee          string
	AssigneeAccountID string
	AssigneeAvatarURL string
	Size              string
	Sprint            string
}

// IssueDetail reads the issue by Jira key from the local projection. found is
// false when the issue has not been projected.
func (s *Store) IssueDetail(key string) (detail IssueDetail, found bool, err error) {
	const query = `SELECT ` + prioIssueColumns + `,
		description, comments, links, COALESCE(assignee, ''), assignee_account_id,
		COALESCE(assignee_avatar_url, ''), COALESCE(size, ''), COALESCE(sprint, '')
		FROM issue WHERE key = ?`

	var description, comments, links []byte
	detail.PrioIssue, err = scanPrioIssue(s.db.QueryRow(query, key),
		&description, &comments, &links, &detail.Assignee, &detail.AssigneeAccountID,
		&detail.AssigneeAvatarURL, &detail.Size, &detail.Sprint)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueDetail{}, false, nil
	}
	if err != nil {
		return IssueDetail{}, false, fmt.Errorf("read issue detail %s: %w", key, err)
	}

	detail.Description = description
	if len(comments) > 0 {
		if err := json.Unmarshal(comments, &detail.Comments); err != nil {
			return IssueDetail{}, false, fmt.Errorf("decode issue %s comments: %w", key, err)
		}
	}
	if len(links) > 0 {
		if err := json.Unmarshal(links, &detail.Links); err != nil {
			return IssueDetail{}, false, fmt.Errorf("decode issue %s links: %w", key, err)
		}
	}
	return detail, true, nil
}
