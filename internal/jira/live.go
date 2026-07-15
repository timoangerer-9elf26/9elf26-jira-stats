package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DCAI field mapping (see spec). These custom-field ids are fixed for the DCAI
// project; the project key and board are configurable.
const (
	sizeFieldID   = "customfield_10040" // "Estimated Time" single-select
	sprintFieldID = "customfield_10020" // Sprint
)

// searchPageSize is the JQL search page size; changelogPageSize the per-issue
// changelog fallback page size. Both are Jira's usual caps.
const (
	searchPageSize    = 100
	changelogPageSize = 100
)

// Config holds the connection settings for the live Jira client, populated from
// environment variables (see spec). No secrets are ever committed.
type Config struct {
	BaseURL    string
	Email      string
	APIToken   string
	ProjectKey string
	BoardID    string
}

// LiveClient is the real Jira Cloud REST v3 client. It performs an
// authenticated, token-paginated JQL search over the whole project with
// changelog expansion, and falls back to the paginated per-issue changelog
// endpoint whenever a search-embedded changelog is truncated.
type LiveClient struct {
	cfg  Config
	http *http.Client
}

// NewLiveClient builds a live client from config.
func NewLiveClient(cfg Config) *LiveClient {
	return &LiveClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// FetchIssues walks the entire project via JQL search (expand=changelog),
// mapping each issue to the DCAI snapshot shape and collecting its status- and
// Estimated-Time-change transitions. Issues whose embedded changelog is
// truncated have their full history fetched from the per-issue endpoint.
func (c *LiveClient) FetchIssues(ctx context.Context) ([]Issue, error) {
	jql := fmt.Sprintf("project = %q ORDER BY created ASC", c.cfg.ProjectKey)

	var issues []Issue
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("maxResults", strconv.Itoa(searchPageSize))
		q.Set("fields", "summary,issuetype,status,assignee,"+sizeFieldID+","+sprintFieldID)
		q.Set("expand", "changelog")
		if pageToken != "" {
			q.Set("nextPageToken", pageToken)
		}

		var page searchResponse
		if err := c.get(ctx, "/rest/api/3/search/jql", q, &page); err != nil {
			return nil, fmt.Errorf("jira search: %w", err)
		}

		for _, dto := range page.Issues {
			iss, err := c.toIssue(ctx, dto)
			if err != nil {
				return nil, err
			}
			issues = append(issues, iss)
		}

		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return issues, nil
}

// toIssue maps a search DTO into a domain Issue, recovering the full changelog
// when the embedded one is truncated.
func (c *LiveClient) toIssue(ctx context.Context, dto issueDTO) (Issue, error) {
	histories := dto.Changelog.Histories
	if dto.Changelog.Total > len(histories) {
		full, err := c.fetchFullChangelog(ctx, dto.Key)
		if err != nil {
			return Issue{}, fmt.Errorf("changelog fallback for %s: %w", dto.Key, err)
		}
		histories = full
	}

	entries, err := toChangelog(histories)
	if err != nil {
		return Issue{}, fmt.Errorf("changelog for %s: %w", dto.Key, err)
	}

	return Issue{
		Key:            dto.Key,
		Type:           dto.Fields.IssueType.Name,
		Summary:        dto.Fields.Summary,
		Status:         dto.Fields.Status.Name,
		StatusCategory: dto.Fields.Status.Category.Name,
		Size:           mapSize(dto.Fields.Size),
		Sprint:         currentSprint(dto.Fields.Sprint),
		Assignee:       assigneeName(dto.Fields.Assignee),
		Changelog:      entries,
	}, nil
}

// fetchFullChangelog pages the per-issue /changelog endpoint until the whole
// history is collected.
func (c *LiveClient) fetchFullChangelog(ctx context.Context, key string) ([]historyDTO, error) {
	var histories []historyDTO
	startAt := 0
	for {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(changelogPageSize))

		var page changelogResponse
		if err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/changelog", q, &page); err != nil {
			return nil, err
		}
		histories = append(histories, page.Values...)

		startAt += len(page.Values)
		if len(page.Values) == 0 || startAt >= page.Total {
			break
		}
	}
	return histories, nil
}

// get issues an authenticated GET and decodes the JSON body into out.
func (c *LiveClient) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.cfg.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.Email, c.cfg.APIToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// --- Jira v3 response DTOs (only the fields the DCAI mapping needs) ---

type searchResponse struct {
	Issues        []issueDTO `json:"issues"`
	NextPageToken string     `json:"nextPageToken"`
}

type issueDTO struct {
	Key       string       `json:"key"`
	Fields    fieldsDTO    `json:"fields"`
	Changelog changelogDTO `json:"changelog"`
}

type fieldsDTO struct {
	Summary   string       `json:"summary"`
	IssueType issueTypeDTO `json:"issuetype"`
	Status    statusDTO    `json:"status"`
	Assignee  *userDTO     `json:"assignee"`
	Size      *selectDTO   `json:"customfield_10040"`
	Sprint    []sprintDTO  `json:"customfield_10020"`
}

type issueTypeDTO struct {
	Name string `json:"name"`
}

type statusDTO struct {
	Name     string `json:"name"`
	Category struct {
		Name string `json:"name"`
	} `json:"statusCategory"`
}

type userDTO struct {
	DisplayName string `json:"displayName"`
}

type selectDTO struct {
	Value string `json:"value"`
}

type sprintDTO struct {
	Name string `json:"name"`
}

// changelogDTO is the search-embedded changelog; changelogResponse is the
// paginated per-issue endpoint. Both carry the same history shape.
type changelogDTO struct {
	Total     int          `json:"total"`
	Histories []historyDTO `json:"histories"`
}

type changelogResponse struct {
	Total  int          `json:"total"`
	Values []historyDTO `json:"values"`
}

type historyDTO struct {
	ID      string    `json:"id"`
	Created string    `json:"created"`
	Items   []itemDTO `json:"items"`
}

type itemDTO struct {
	Field   string `json:"field"`
	FieldID string `json:"fieldId"`
	From    string `json:"fromString"`
	To      string `json:"toString"`
}

// --- mapping helpers ---

// mapSize maps the "Estimated Time" select (Small/Medium/Large) to the T-shirt
// label the store expects; an absent estimate maps to "" (no-estimate bucket).
func mapSize(sel *selectDTO) string {
	if sel == nil {
		return ""
	}
	switch sel.Value {
	case "Small":
		return "S"
	case "Medium":
		return "M"
	case "Large":
		return "L"
	default:
		return ""
	}
}

// currentSprint returns the name of the last sprint on the issue (the active
// one, when present), or "" when unassigned to any sprint.
func currentSprint(sprints []sprintDTO) string {
	if len(sprints) == 0 {
		return ""
	}
	return sprints[len(sprints)-1].Name
}

func assigneeName(u *userDTO) string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

// toChangelog flattens history entries into the status- and Estimated-Time
// transitions the store records. A single history may carry both a status and
// an Estimated-Time item, so each transition's dedup id combines the stable
// history id with the field key to stay unique yet stable across re-syncs.
func toChangelog(histories []historyDTO) ([]ChangelogEntry, error) {
	var entries []ChangelogEntry
	for _, h := range histories {
		var ts time.Time
		parsed := false
		for _, it := range h.Items {
			field, ok := trackedField(it)
			if !ok {
				continue
			}
			if !parsed {
				t, err := parseJiraTime(h.Created)
				if err != nil {
					return nil, err
				}
				ts, parsed = t, true
			}
			entries = append(entries, ChangelogEntry{
				ID:        h.ID + "/" + fieldKey(field),
				Field:     field,
				From:      it.From,
				To:        it.To,
				Timestamp: ts,
			})
		}
	}
	return entries, nil
}

// trackedField returns the canonical stored field name for a changelog item we
// record (status and Estimated Time), and false for everything else.
func trackedField(it itemDTO) (string, bool) {
	switch {
	case it.Field == "status":
		return "status", true
	case it.FieldID == sizeFieldID || it.Field == "Estimated Time":
		return "Estimated Time", true
	default:
		return "", false
	}
}

func fieldKey(field string) string {
	if field == "status" {
		return "status"
	}
	return "estimated_time"
}

// parseJiraTime parses Jira's changelog timestamp (e.g.
// "2026-07-13T09:00:00.000+0200"), falling back to RFC3339.
func parseJiraTime(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02T15:04:05.000-0700", s); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse changelog timestamp %q: %w", s, err)
	}
	return t, nil
}
