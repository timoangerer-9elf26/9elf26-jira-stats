package jira

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const dcaiProjectID = "10004"

type PlanningPreset struct {
	ID, Name, Description, JQL string
}

// PlanningPresets discovers the Planning filters visible to this client and
// shared with DCAI, including filters shared with a DCAI project role.
func (c *LiveClient) PlanningPresets(ctx context.Context) ([]PlanningPreset, error) {
	var presets []PlanningPreset
	q := url.Values{
		"projectId":  {dcaiProjectID},
		"filterName": {"Planning"},
		"expand":     {"jql,description,sharePermissions"},
		"orderBy":    {"name"},
		"maxResults": {"50"},
	}
	for start := 0; ; {
		q.Set("startAt", strconv.Itoa(start))
		var page struct {
			IsLast bool
			Total  *int
			Values []struct {
				PlanningPreset
				SharePermissions []struct {
					Type    string
					Project struct{ ID string }
				}
			}
		}
		if err := c.get(ctx, "/rest/api/3/filter/search", q, &page); err != nil {
			return nil, fmt.Errorf("discover Jira Planning filters: %w", err)
		}

		for _, filter := range page.Values {
			if !strings.HasPrefix(filter.Name, "Planning") || filter.ID == "" {
				continue
			}
			for _, perm := range filter.SharePermissions {
				if perm.Project.ID == dcaiProjectID && (perm.Type == "project" || perm.Type == "projectRole") {
					presets = append(presets, filter.PlanningPreset)
					break
				}
			}
		}
		start += len(page.Values)
		if page.IsLast || (page.Total != nil && start >= *page.Total) {
			return presets, nil
		}
		if len(page.Values) == 0 {
			return nil, fmt.Errorf("incomplete Jira filter search at offset %d", start)
		}
	}
}

// SearchIssueKeys reads personal Jira search results in Jira's order. Issue
// fields and changelog data are rendered from the local projection.
func (c *LiveClient) SearchIssueKeys(ctx context.Context, jql string) ([]string, error) {
	q := url.Values{
		"jql":        {jql},
		"fields":     {"key"},
		"maxResults": {strconv.Itoa(searchPageSize)},
	}
	var keys []string
	seenTokens := make(map[string]bool)
	for {
		var page struct {
			Issues        []struct{ Key string }
			NextPageToken string
			IsLast        *bool
		}
		if err := c.get(ctx, "/rest/api/3/search/jql", q, &page); err != nil {
			return nil, fmt.Errorf("search Jira issue keys: %w", err)
		}
		for _, issue := range page.Issues {
			if issue.Key != "" {
				keys = append(keys, issue.Key)
			}
		}
		if page.NextPageToken == "" {
			if page.IsLast != nil && !*page.IsLast {
				return nil, fmt.Errorf("incomplete Jira issue search: missing next page token")
			}
			return keys, nil
		}
		if seenTokens[page.NextPageToken] {
			return nil, fmt.Errorf("incomplete Jira issue search: repeated page token")
		}
		seenTokens[page.NextPageToken] = true
		q.Set("nextPageToken", page.NextPageToken)
	}
}
