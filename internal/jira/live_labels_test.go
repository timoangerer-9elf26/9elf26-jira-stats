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

// Labels is the standard Jira field `fields.labels` — a plain array of strings
// (Jira labels carry no whitespace), NOT an array of objects like most Jira
// fields. This decodes a REAL-SHAPED v3 payload rather than a hand-simplified
// fixture, guarding the repo's known trap where fake fixtures diverge from live
// payload shapes.
func TestToIssueParsesLabels(t *testing.T) {
	const body = `{
      "key": "DCAI-42",
      "fields": {
        "summary": "Ship the thing",
        "issuetype": {"name": "Task"},
        "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
        "assignee": null,
        "priority": {"name": "High", "id": "2"},
        "labels": ["Technical", "Product", "needs-design"],
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
	want := []string{"Technical", "Product", "needs-design"}
	if len(iss.Labels) != len(want) {
		t.Fatalf("Labels = %#v, want %#v", iss.Labels, want)
	}
	for i, w := range want {
		if iss.Labels[i] != w {
			t.Errorf("Labels[%d] = %q, want %q (order must be preserved)", i, iss.Labels[i], w)
		}
	}
}

// An unlabelled issue is the common case: Jira sends an empty array, and older
// or partial payloads can omit the key entirely. Both must map to no labels
// rather than a phantom empty-string label.
func TestToIssueToleratesNoLabels(t *testing.T) {
	for name, body := range map[string]string{
		"empty array": `{
          "key": "DCAI-43",
          "fields": {
            "summary": "Unlabelled",
            "issuetype": {"name": "Task"},
            "status": {"name": "Triage", "statusCategory": {"name": "To Do"}},
            "labels": [],
            "customfield_10020": []
          },
          "changelog": {"total": 0, "histories": []}
        }`,
		"field absent": `{
          "key": "DCAI-44",
          "fields": {
            "summary": "Unlabelled",
            "issuetype": {"name": "Task"},
            "status": {"name": "Triage", "statusCategory": {"name": "To Do"}},
            "customfield_10020": []
          },
          "changelog": {"total": 0, "histories": []}
        }`,
	} {
		t.Run(name, func(t *testing.T) {
			var dto issueDTO
			if err := json.Unmarshal([]byte(body), &dto); err != nil {
				t.Fatalf("decode issue: %v", err)
			}
			iss, err := (&LiveClient{}).toIssue(context.Background(), dto)
			if err != nil {
				t.Fatalf("toIssue: %v", err)
			}
			if len(iss.Labels) != 0 {
				t.Errorf("Labels = %#v, want none", iss.Labels)
			}
		})
	}
}

// Decoding labels is worthless if the client never asks Jira for the field, so
// assert the wire request too (Jira omits unrequested fields silently).
func TestLiveSearchRequestsLabelsAndMapsThem(t *testing.T) {
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
            "labels": ["Technical"]
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
	if !strings.Contains(gotFields, "labels") {
		t.Errorf("fields query = %q, does not request labels", gotFields)
	}
	if len(issues) != 1 || len(issues[0].Labels) != 1 || issues[0].Labels[0] != "Technical" {
		t.Fatalf("issues = %+v, want one issue labelled Technical", issues)
	}
}
