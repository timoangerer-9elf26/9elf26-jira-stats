package jira

// Unit test for the sync-parse of the immutable `created` timestamp and
// `creator` display name (issue #44). These are static per-issue fields present
// on every fetch, so a normal re-sync populates them — no changelog work.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestToIssueParsesCreatedAndCreator(t *testing.T) {
	// A search DTO whose embedded changelog is complete (Total == len(histories))
	// so toIssue never reaches for the HTTP fallback and a zero-value client is
	// enough to exercise the mapping.
	const body = `{
      "key": "DCAI-7",
      "fields": {
        "summary": "Author a ticket",
        "issuetype": {"name": "Task"},
        "status": {"name": "Ready to Do", "statusCategory": {"name": "To Do"}},
        "assignee": {"displayName": "Grace"},
        "created": "2026-07-14T09:30:00.000+0200",
        "creator": {"displayName": "Ada"},
        "customfield_10040": null,
        "customfield_10020": []
      },
      "changelog": {"startAt": 0, "maxResults": 100, "total": 0, "histories": []}
    }`

	var dto issueDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	c := &LiveClient{}
	iss, err := c.toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}

	// Creator is the immutable author, distinct from the current assignee.
	if iss.Creator != "Ada" {
		t.Errorf("Creator = %q, want %q", iss.Creator, "Ada")
	}
	if iss.Assignee != "Grace" {
		t.Errorf("Assignee = %q, want %q (creator must not overwrite assignee)", iss.Assignee, "Grace")
	}
	want := time.Date(2026, time.July, 14, 7, 30, 0, 0, time.UTC)
	if !iss.CreatedAt.UTC().Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", iss.CreatedAt.UTC(), want)
	}
}

func TestToIssueToleratesMissingCreatedAndCreator(t *testing.T) {
	// A ticket Jira returned with no created/creator must map to zero/empty, not
	// error (consistent with the app's other nullable-field handling).
	const body = `{
      "key": "DCAI-8",
      "fields": {
        "summary": "No author fields",
        "issuetype": {"name": "Bug"},
        "status": {"name": "Triage", "statusCategory": {"name": "To Do"}},
        "assignee": null,
        "customfield_10040": null,
        "customfield_10020": []
      },
      "changelog": {"startAt": 0, "maxResults": 100, "total": 0, "histories": []}
    }`

	var dto issueDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	c := &LiveClient{}
	iss, err := c.toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}
	if iss.Creator != "" {
		t.Errorf("Creator = %q, want empty", iss.Creator)
	}
	if !iss.CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v, want zero", iss.CreatedAt)
	}
}
