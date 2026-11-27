package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (c *FakeClient) ReadPrioIssueDescription(ctx context.Context, key string) (json.RawMessage, error) {
	issue, err := c.FetchIssue(ctx, key)
	return issue.Description, err
}

func (c *FakeClient) FetchIssueTransitions(ctx context.Context, key string) ([]IssueTransition, error) {
	transitions, err := c.FetchTransitions(ctx, key)
	if err != nil {
		return nil, err
	}
	detailed := make([]IssueTransition, 0, len(transitions))
	for _, transition := range transitions {
		fields := c.TransitionFields[transition.ID]
		detailed = append(detailed, IssueTransition{Transition: transition, Fields: fields, HasScreen: len(fields) != 0})
	}
	return detailed, nil
}

func (c *FakeClient) ApplyIssueTransition(ctx context.Context, key, id string, author Person) error {
	return c.transitionIssue(ctx, key, id, &author)
}

func (c *FakeClient) FetchEditFields(ctx context.Context, key string) (map[string]EditField, error) {
	if _, err := c.FetchIssue(ctx, key); err != nil {
		return nil, err
	}
	fields := make(map[string]EditField)
	for _, key := range []string{"summary", "description", "assignee", "labels", "parent"} {
		fields[key] = EditField{Operations: []string{"set"}, Required: key == "summary"}
	}
	priority := EditField{Operations: []string{"set"}, Required: true}
	for _, name := range Priorities {
		priority.AllowedValues = append(priority.AllowedValues, EditOption{Name: name})
	}
	fields["priority"] = priority
	fields[EstimateField] = EditField{Operations: []string{"set"},
		AllowedValues: []EditOption{{Value: "Small"}, {Value: "Medium"}, {Value: "Large"}}}
	return fields, nil
}

func (c *FakeClient) EditIssueField(ctx context.Context, key, field string, value any, author Person) error {
	if field == "assignee" {
		var accountID string
		if value != nil {
			accountID = value.(map[string]string)["accountId"]
		}
		return c.UpdateIssueAssignee(ctx, key, accountID)
	}
	return c.mutatePanelIssue(key, func(issue *Issue) {
		switch field {
		case "summary":
			issue.Summary = value.(string)
		case "description":
			if value == nil {
				issue.Description = nil
			} else {
				issue.Description = value.(json.RawMessage)
			}
		case "priority":
			next := value.(map[string]string)["name"]
			c.recordPanelChange(issue, field, issue.Priority, next, author)
			issue.Priority = next
		case EstimateField:
			issue.Size = ""
			if value != nil {
				issue.Size = EstimateSize(EditOption{Value: value.(map[string]string)["value"]})
			}
		case "labels":
			issue.Labels = value.([]string)
		case "parent":
			issue.ParentKey = ""
			if value != nil {
				issue.ParentKey = value.(map[string]string)["key"]
			}
		}
	})
}

func (c *FakeClient) AddIssueComment(ctx context.Context, key string, body json.RawMessage, author Person) error {
	return c.mutatePanelIssue(key, func(issue *Issue) {
		now := time.Now().UTC()
		comment := Comment{ID: strconv.FormatInt(now.UnixNano(), 10), Author: author, Body: body, CreatedAt: now, UpdatedAt: now}
		issue.Comments = append(slices.Clone(issue.Comments), comment)
	})
}

func (c *FakeClient) CreatePrioEpic(ctx context.Context, summary string, description json.RawMessage, author Person) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.WriteErr != nil {
		return "", c.WriteErr
	}
	maxNumber := 0
	for _, issue := range c.Issues {
		number, err := strconv.Atoi(strings.TrimPrefix(issue.Key, PrioProject+"-"))
		if err == nil {
			maxNumber = max(maxNumber, number)
		}
	}
	key := fmt.Sprintf("%s-%d", PrioProject, maxNumber+1)
	c.Issues = append(c.Issues, Issue{Key: key, Type: "Epic", Summary: summary, Description: description,
		Status: "Triage", StatusCategory: "To Do", Priority: "Medium", Creator: author.DisplayName, CreatedAt: time.Now().UTC()})
	return key, nil
}

func (c *FakeClient) mutatePanelIssue(key string, mutate func(*Issue)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.WriteErr != nil {
		return c.WriteErr
	}
	for i := range c.Issues {
		if c.Issues[i].Key == key {
			mutate(&c.Issues[i])
			return nil
		}
	}
	return fmt.Errorf("fake Jira: issue %q not found", key)
}

func (c *FakeClient) recordPanelChange(issue *Issue, field, from, to string, author Person) {
	if from != to {
		now := time.Now().UTC()
		issue.Changelog = append(slices.Clone(issue.Changelog), ChangelogEntry{
			ID: strconv.FormatInt(now.UnixNano(), 10), Field: field, From: from, To: to, Timestamp: now, Author: author,
		})
	}
}
