package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Priority is the standard Jira field `fields.priority`, an object whose `name`
// is one of the five DCAI levels. This decodes a REAL-SHAPED v3 payload (self /
// iconUrl / name / id) rather than a hand-simplified fixture, guarding the
// repo's known trap where fake fixtures diverge from live payload shapes.
func TestToIssueParsesPriority(t *testing.T) {
	const body = `{
      "key": "DCAI-42",
      "fields": {
        "summary": "Ship the thing",
        "issuetype": {"name": "Task"},
        "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
        "assignee": null,
        "priority": {
          "self": "https://9elf26.atlassian.net/rest/api/3/priority/2",
          "iconUrl": "https://9elf26.atlassian.net/images/icons/priorities/high.svg",
          "name": "High",
          "id": "2"
        },
        "customfield_10040": null,
        "customfield_10020": []
      },
      "changelog": {"total": 0, "histories": []}
    }`

	var dto issueDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	iss, err := (&LiveClient{}).toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}
	if iss.Priority != "High" {
		t.Errorf("Priority = %q, want %q", iss.Priority, "High")
	}
}

func TestToIssueToleratesMissingPriority(t *testing.T) {
	const body = `{
      "key": "DCAI-43",
      "fields": {
        "summary": "No priority set",
        "issuetype": {"name": "Task"},
        "status": {"name": "Triage", "statusCategory": {"name": "To Do"}},
        "assignee": null,
        "priority": null,
        "customfield_10040": null,
        "customfield_10020": []
      },
      "changelog": {"total": 0, "histories": []}
    }`

	var dto issueDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	iss, err := (&LiveClient{}).toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}
	if iss.Priority != "" {
		t.Errorf("Priority = %q, want empty", iss.Priority)
	}
}

// The live client must ASK Jira for the priority field: a field missing from the
// `fields` query decodes as empty on every issue, which no decode test can catch.
// Driven at the wire, so it asserts the request the client actually sends.
func TestLiveSearchRequestsPriorityAndMapsIt(t *testing.T) {
	var gotFields string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFields = r.URL.Query().Get("fields")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"issues":[{
          "key": "DCAI-42",
          "fields": {
            "summary": "Ship the thing",
            "issuetype": {"name": "Task"},
            "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
            "priority": {"name": "Highest", "id": "1"}
          },
          "changelog": {"total": 0, "histories": []}
        }]}`)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t", ProjectKey: "DCAI"})
	issues, err := c.FetchIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if !strings.Contains(gotFields, "priority") {
		t.Errorf("fields query = %q, does not request priority", gotFields)
	}
	if len(issues) != 1 || issues[0].Priority != "Highest" {
		t.Fatalf("issues = %+v, want one issue with Priority %q", issues, "Highest")
	}
}
