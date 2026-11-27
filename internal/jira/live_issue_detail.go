package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

const commentPageSize = 100

type commentPageDTO struct {
	StartAt  int          `json:"startAt"`
	Total    int          `json:"total"`
	Comments []commentDTO `json:"comments"`
}

type commentDTO struct {
	ID         string          `json:"id"`
	Author     *userDTO        `json:"author"`
	Body       json.RawMessage `json:"body"`
	Created    string          `json:"created"`
	Updated    string          `json:"updated"`
	Visibility json.RawMessage `json:"visibility"`
}

type issueLinkDTO struct {
	Type struct {
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	Inward  *linkedIssueDTO `json:"inwardIssue"`
	Outward *linkedIssueDTO `json:"outwardIssue"`
}

type linkedIssueDTO struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
	} `json:"fields"`
}

// issueComments uses the embedded field when complete. When Jira truncates it,
// read the whole ordered collection from the comment endpoint.
func (c *LiveClient) issueComments(ctx context.Context, key string, embedded commentPageDTO) ([]Comment, error) {
	if embedded.StartAt == 0 && len(embedded.Comments) >= embedded.Total {
		return toComments(embedded.Comments)
	}

	var comments []Comment
	for startAt := 0; ; {
		q := url.Values{
			"startAt":    {strconv.Itoa(startAt)},
			"maxResults": {strconv.Itoa(commentPageSize)},
		}
		var page commentPageDTO
		if err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", q, &page); err != nil {
			return nil, err
		}
		if page.StartAt != startAt {
			return nil, fmt.Errorf("expected comment offset %d, got %d", startAt, page.StartAt)
		}
		mapped, err := toComments(page.Comments)
		if err != nil {
			return nil, err
		}
		comments = append(comments, mapped...)
		startAt += len(page.Comments)
		if startAt >= page.Total {
			return comments, nil
		}
		if len(page.Comments) == 0 {
			return nil, fmt.Errorf("incomplete comment page at offset %d of %d", startAt, page.Total)
		}
	}
}

func toComments(dtos []commentDTO) ([]Comment, error) {
	var comments []Comment
	for _, dto := range dtos {
		// The projection is shared. A service-account read can see comments
		// that some meeting participants are not authorized to see in Jira.
		if nonNullJSON(dto.Visibility) != nil {
			continue
		}
		created, err := optionalJiraTime(dto.Created)
		if err != nil {
			return nil, fmt.Errorf("comment %s created: %w", dto.ID, err)
		}
		updated, err := optionalJiraTime(dto.Updated)
		if err != nil {
			return nil, fmt.Errorf("comment %s updated: %w", dto.ID, err)
		}
		comments = append(comments, Comment{
			ID: dto.ID, Author: person(dto.Author), Body: nonNullJSON(dto.Body),
			CreatedAt: created, UpdatedAt: updated,
		})
	}
	return comments, nil
}

func nonNullJSON(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}

func person(dto *userDTO) Person {
	if dto == nil {
		return Person{}
	}
	return Person{AccountID: dto.AccountID, DisplayName: dto.DisplayName}
}

func toIssueLinks(dtos []issueLinkDTO) []IssueLink {
	var links []IssueLink
	for _, dto := range dtos {
		link, relation := dto.Outward, dto.Type.Outward
		if dto.Inward != nil {
			link, relation = dto.Inward, dto.Type.Inward
		}
		if link != nil && link.Key != "" {
			links = append(links, IssueLink{Key: link.Key, Summary: link.Fields.Summary, Relation: relation})
		}
	}
	return links
}
