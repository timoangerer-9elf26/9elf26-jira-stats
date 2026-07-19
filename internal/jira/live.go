package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
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
	sprintPageSize    = 50 // Jira Agile's usual sprint page cap
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
	return c.search(ctx, fmt.Sprintf("project = %q ORDER BY created ASC", c.cfg.ProjectKey))
}

// FetchIssuesUpdatedSince walks only issues updated at or after the given bound
// via the same search path, so incremental syncs stay cheap. Jira interprets
// the JQL date in its own timezone; the caller's overlap window absorbs any
// resulting skew.
func (c *LiveClient) FetchIssuesUpdatedSince(ctx context.Context, since time.Time) ([]Issue, error) {
	jql := fmt.Sprintf("project = %q AND updated >= %q ORDER BY created ASC",
		c.cfg.ProjectKey, since.Format("2006-01-02 15:04"))
	return c.search(ctx, jql)
}

// FetchSprints walks the board's sprints via the Jira Agile API
// (GET /rest/agile/1.0/board/{boardID}/sprint), startAt-paginated until isLast.
// It maps each sprint to the domain entity (see toSprint): Jira Cloud exposes no
// activatedDate, so the window-start instant comes from startDate; completeDate
// remains the completion instant. The planned endDate is not carried.
func (c *LiveClient) FetchSprints(ctx context.Context) ([]Sprint, error) {
	var sprints []Sprint
	startAt := 0
	for {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(sprintPageSize))
		q.Set("state", "active,closed,future")

		var page sprintsResponse
		if err := c.get(ctx, "/rest/agile/1.0/board/"+url.PathEscape(c.cfg.BoardID)+"/sprint", q, &page); err != nil {
			return nil, fmt.Errorf("jira sprints: %w", err)
		}

		for _, dto := range page.Values {
			sp, err := toSprint(dto)
			if err != nil {
				return nil, err
			}
			sprints = append(sprints, sp)
		}

		startAt += len(page.Values)
		if page.IsLast || len(page.Values) == 0 {
			break
		}
	}
	return sprints, nil
}

// search runs a token-paginated JQL search (expand=changelog) and maps every
// matching issue into the domain snapshot shape.
func (c *LiveClient) search(ctx context.Context, jql string) ([]Issue, error) {
	var issues []Issue
	pageToken := ""
	for {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("maxResults", strconv.Itoa(searchPageSize))
		q.Set("fields", "summary,issuetype,status,assignee,created,creator,"+sizeFieldID+","+sprintFieldID)
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

	sprintChanges, err := toSprintChanges(histories)
	if err != nil {
		return Issue{}, fmt.Errorf("sprint changelog for %s: %w", dto.Key, err)
	}

	createdAt, err := optionalJiraTime(dto.Fields.Created)
	if err != nil {
		return Issue{}, fmt.Errorf("created for %s: %w", dto.Key, err)
	}

	iss := Issue{
		Key:               dto.Key,
		Type:              dto.Fields.IssueType.Name,
		Summary:           dto.Fields.Summary,
		Status:            dto.Fields.Status.Name,
		StatusCategory:    dto.Fields.Status.Category.Name,
		Size:              mapSize(dto.Fields.Size),
		Sprint:            currentSprint(dto.Fields.Sprint),
		Assignee:          assigneeName(dto.Fields.Assignee),
		AssigneeAvatarURL: assigneeAvatarURL(dto.Fields.Assignee),
		CreatedAt:         createdAt,
		Creator:           assigneeName(dto.Fields.Creator),
		Changelog:         entries,
		SprintChanges:     sprintChanges,
	}

	// Record active-sprint MEMBERSHIP (name + id) only; the sprint's window comes
	// from the Sprint entity (FetchSprints), never the planned dates on the issue.
	// The id lets the store synthesize membership for a ticket created directly
	// into the sprint (no "Sprint" changelog item; see #55).
	if sp, ok := activeSprint(dto.Fields.Sprint); ok {
		iss.ActiveSprint = sp.Name
		iss.ActiveSprintID = sp.ID
	}

	return iss, nil
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
	Created   string       `json:"created"` // Jira's immutable creation timestamp
	Creator   *userDTO     `json:"creator"` // immutable author (NOT the mutable reporter)
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
	DisplayName string        `json:"displayName"`
	AvatarUrls  avatarUrlsDTO `json:"avatarUrls"`
}

// avatarUrlsDTO is Jira's per-user avatar image set, keyed by pixel size. The
// board captures the largest for a crisp render in its small circle.
type avatarUrlsDTO struct {
	Size48 string `json:"48x48"`
	Size32 string `json:"32x32"`
	Size24 string `json:"24x24"`
	Size16 string `json:"16x16"`
}

type selectDTO struct {
	Value string `json:"value"`
}

// sprintDTO is the issue-field sprint entry. id + name + state are parsed: they
// establish per-issue active-sprint MEMBERSHIP and its id (the key membership
// history is stored under, needed to synthesize membership for a ticket created
// directly into a sprint — see #55). The planned startDate/endDate on this entry
// are deliberately ignored — the trusted window comes from the sprint entity
// (see FetchSprints / agileSprintDTO).
type sprintDTO struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // "active", "closed", or "future"
}

// sprintsResponse is the Jira Agile board-sprints page; agileSprintDTO is one
// sprint entity. Jira Cloud's Agile REST API exposes NO actual-activation field
// (there is no activatedDate); startDate — the value set in the "Start sprint"
// dialog — is the only anchor for the sprint window's start, with createdDate as
// a fallback. completeDate remains the trusted completion instant.
type sprintsResponse struct {
	Values []agileSprintDTO `json:"values"`
	IsLast bool             `json:"isLast"`
}

type agileSprintDTO struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	StartDate    string `json:"startDate"`
	CreatedDate  string `json:"createdDate"`
	CompleteDate string `json:"completeDate"`
}

// toSprint maps a Jira Agile sprint DTO into the domain Sprint entity. Because
// Jira Cloud has no activatedDate, the entity's activation/window-start instant
// (ActivatedAt) is taken from startDate, falling back to createdDate when a
// (future) sprint has never been started. completeDate drives CompletedAt.
func toSprint(dto agileSprintDTO) (Sprint, error) {
	sp := Sprint{ID: dto.ID, Name: dto.Name, State: dto.State}
	start := dto.StartDate
	if start == "" {
		start = dto.CreatedDate
	}
	var err error
	if sp.ActivatedAt, err = optionalJiraTime(start); err != nil {
		return Sprint{}, fmt.Errorf("sprint %d startDate: %w", dto.ID, err)
	}
	if sp.CompletedAt, err = optionalJiraTime(dto.CompleteDate); err != nil {
		return Sprint{}, fmt.Errorf("sprint %d completeDate: %w", dto.ID, err)
	}
	return sp, nil
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
	From    string `json:"fromString"` // human-readable prior value (status/sprint names)
	To      string `json:"toString"`   // human-readable new value
	FromID  string `json:"from"`       // raw prior value: comma-separated sprint ids
	ToID    string `json:"to"`         // raw new value: comma-separated sprint ids
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

// activeSprint returns the issue's active sprint entry (state == "active"), if
// any. An issue belongs to the active sprint iff one of its sprint entries is
// active; this yields the active sprint's NAME for per-issue membership (the
// window comes from the Sprint entity, not this entry).
func activeSprint(sprints []sprintDTO) (sprintDTO, bool) {
	for _, sp := range sprints {
		if sp.State == "active" {
			return sp, true
		}
	}
	return sprintDTO{}, false
}

// optionalJiraTime parses a Jira sprint boundary timestamp, treating an empty
// value as the zero time (the boundary is simply unknown).
func optionalJiraTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return parseJiraTime(s)
}

func assigneeName(u *userDTO) string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

// assigneeAvatarURL returns the largest available avatar image URL for a user,
// or "" when there is no user or no avatar. Jira always populates the full size
// set together, but each is checked so a partial payload still yields a URL.
func assigneeAvatarURL(u *userDTO) string {
	if u == nil {
		return ""
	}
	for _, url := range []string{u.AvatarUrls.Size48, u.AvatarUrls.Size32, u.AvatarUrls.Size24, u.AvatarUrls.Size16} {
		if url != "" {
			return url
		}
	}
	return ""
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

// toSprintChanges flattens the "Sprint" changelog items into per-sprint
// membership transitions. Each Sprint item carries the before/after sprint sets
// as comma-separated ids (from/to) with parallel names (fromString/toString); a
// single change can add and/or remove several sprints, so it expands to one
// SprintMembershipChange per sprint id whose membership actually changed —
// entered (in the "to" set but not "from") or left (in "from" but not "to").
// Sprints present in both sets are unchanged and yield nothing. Output is
// ordered by history then sprint id so re-syncs are deterministic.
func toSprintChanges(histories []historyDTO) ([]SprintMembershipChange, error) {
	var changes []SprintMembershipChange
	for _, h := range histories {
		for _, it := range h.Items {
			if it.Field != "Sprint" && it.FieldID != sprintFieldID {
				continue
			}
			from, err := parseSprintRefs(it.FromID, it.From)
			if err != nil {
				return nil, err
			}
			to, err := parseSprintRefs(it.ToID, it.To)
			if err != nil {
				return nil, err
			}
			ts, err := parseJiraTime(h.Created)
			if err != nil {
				return nil, err
			}

			for _, id := range sortedIDs(to) {
				if _, stillThere := from[id]; !stillThere {
					changes = append(changes, SprintMembershipChange{
						EntryID: h.ID, SprintID: id, SprintName: to[id], Entered: true, Timestamp: ts,
					})
				}
			}
			for _, id := range sortedIDs(from) {
				if _, stillThere := to[id]; !stillThere {
					changes = append(changes, SprintMembershipChange{
						EntryID: h.ID, SprintID: id, SprintName: from[id], Entered: false, Timestamp: ts,
					})
				}
			}
		}
	}
	return changes, nil
}

// parseSprintRefs zips a comma-separated sprint-id list (from/to) with its
// parallel name list (fromString/toString) into an id→name map. Blank ids are
// skipped (an empty set, e.g. a ticket entering its first sprint); a name index
// past the ids is tolerated as "". A non-numeric id is a parse error.
func parseSprintRefs(ids, names string) (map[int]string, error) {
	refs := map[int]string{}
	idParts := splitCSV(ids)
	nameParts := splitCSV(names)
	for i, raw := range idParts {
		id, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("parse sprint id %q: %w", raw, err)
		}
		name := ""
		if i < len(nameParts) {
			name = nameParts[i]
		}
		refs[id] = name
	}
	return refs, nil
}

// splitCSV splits a Jira changelog comma-separated list, trimming spaces and
// dropping empties (so "" yields no parts).
func splitCSV(s string) []string {
	var parts []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// sortedIDs returns a map's sprint ids in ascending order for deterministic output.
func sortedIDs(m map[int]string) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
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
