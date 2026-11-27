package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

const (
	PrioProject   = "DCAI"
	EstimateField = sizeFieldID
)

// EditField describes the set operation Jira currently permits on an issue.
type EditField struct {
	Name          string       `json:"name"`
	Required      bool         `json:"required"`
	Operations    []string     `json:"operations"`
	AllowedValues []EditOption `json:"allowedValues"`
}

type EditOption struct {
	ID, Name, Value, AccountID, DisplayName string
	Disabled                                bool
}

// UnmarshalJSON also accepts Jira metadata whose allowed values are scalars.
func (o *EditOption) UnmarshalJSON(raw []byte) error {
	type option EditOption
	if len(raw) != 0 && raw[0] == '{' {
		// Other Jira field types can report objects with unrelated schemas.
		_ = json.Unmarshal(raw, (*option)(o))
	} else {
		_ = json.Unmarshal(raw, &o.Value)
	}
	return nil
}

type IssueTransition struct {
	Transition
	Fields    map[string]EditField
	HasScreen bool
}

// IssueActions is the metadata and mutation surface of a personal Jira client
// used by the focused Prio panel.
type IssueActions interface {
	FetchEditFields(context.Context, string) (map[string]EditField, error)
	FetchIssueTransitions(context.Context, string) ([]IssueTransition, error)
	ReadPrioIssueDescription(context.Context, string) (json.RawMessage, error)
	EditIssueField(context.Context, string, string, any, Person) error
	ApplyIssueTransition(context.Context, string, string, Person) error
	AddIssueComment(context.Context, string, json.RawMessage, Person) error
	CreatePrioEpic(context.Context, string, json.RawMessage, Person) (string, error)
}

func (c *LiveClient) FetchEditFields(ctx context.Context, key string) (map[string]EditField, error) {
	var result struct {
		Fields map[string]EditField `json:"fields"`
	}
	err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/editmeta", nil, &result)
	return result.Fields, err
}

// ReadPrioIssueDescription checks the issue still belongs to DCAI. Jira also
// accepts old keys for moved issues, so a prefix check alone cannot guard writes.
func (c *LiveClient) ReadPrioIssueDescription(ctx context.Context, key string) (json.RawMessage, error) {
	var issue struct {
		Key    string
		Fields struct {
			Project     struct{ Key string }
			Description json.RawMessage
		}
	}
	err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key), url.Values{"fields": {"project,description"}}, &issue)
	if err != nil {
		return nil, err
	}
	if issue.Key != key || issue.Fields.Project.Key != PrioProject {
		return nil, errors.New("Der Jira-Key gehört nicht mehr zu diesem DCAI-Ticket.")
	}
	return issue.Fields.Description, nil
}

func (c *LiveClient) FetchIssueTransitions(ctx context.Context, key string) ([]IssueTransition, error) {
	var result transitionsResponse
	if err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", url.Values{"expand": {"transitions.fields"}}, &result); err != nil {
		return nil, err
	}
	transitions := toTransitions(result.Transitions)
	detailed := make([]IssueTransition, 0, len(transitions))
	for i, transition := range transitions {
		dto := result.Transitions[i]
		detailed = append(detailed, IssueTransition{transition, dto.Fields, dto.HasScreen})
	}
	return detailed, nil
}

func (c *LiveClient) ApplyIssueTransition(ctx context.Context, key, id string, _ Person) error {
	return c.TransitionIssue(ctx, key, id)
}

func (c *LiveClient) EditIssueField(ctx context.Context, key, field string, value any, _ Person) error {
	return c.put(ctx, "/rest/api/3/issue/"+url.PathEscape(key), map[string]any{
		"fields": map[string]any{field: value},
	})
}

func (c *LiveClient) AddIssueComment(ctx context.Context, key string, body json.RawMessage, _ Person) error {
	return c.post(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", map[string]any{"body": body})
}

func (c *LiveClient) CreatePrioEpic(ctx context.Context, summary string, description json.RawMessage, _ Person) (string, error) {
	typeID, err := c.prioEpicType(ctx)
	if err != nil {
		return "", err
	}
	fields := map[string]any{
		"project":   map[string]string{"key": PrioProject},
		"issuetype": map[string]string{"id": typeID},
		"summary":   summary,
	}
	if len(description) != 0 {
		fields["description"] = description
	}
	var created struct{ Key string }
	if err := c.writeJSON(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}, &created); err != nil {
		return "", err
	}
	return created.Key, nil
}

func (c *LiveClient) prioEpicType(ctx context.Context) (string, error) {
	const path = "/rest/api/3/issue/createmeta/" + PrioProject + "/issuetypes"
	for start := 0; ; {
		var page struct {
			IssueTypes []struct {
				ID, Name string
				Subtask  bool
			}
			Total int
		}
		if err := c.get(ctx, path, url.Values{"startAt": {strconv.Itoa(start)}}, &page); err != nil {
			return "", err
		}
		for _, issueType := range page.IssueTypes {
			if issueType.Name == "Epic" && !issueType.Subtask && issueType.ID != "" {
				return issueType.ID, nil
			}
		}
		start += len(page.IssueTypes)
		if len(page.IssueTypes) == 0 || start >= page.Total {
			return "", errors.New("Jira bietet den Vorgangstyp Epic für DCAI nicht an.")
		}
	}
}

func editOptionName(option EditOption) string {
	if option.Name != "" {
		return option.Name
	}
	return option.Value
}

// EstimateSize maps Jira's option metadata to the T-shirt sizes displayed by
// the dashboard. An empty return means an unsupported or absent option.
func EstimateSize(option EditOption) string {
	return mapSize(&selectDTO{Value: editOptionName(option)})
}
