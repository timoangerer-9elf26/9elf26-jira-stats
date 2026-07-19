package jira

// Unit tests for the sync-parse of the parent-epic link and the epic's Jira
// "Issue color" (#69): a child issue carries its parent's key; an Epic issue
// carries its customfield_10017 colour. Both are static per-issue fields present
// on every fetch, so a normal re-sync populates them — no changelog work.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestToIssueParsesParentKey(t *testing.T) {
	const body = `{
      "key": "DCAI-10",
      "fields": {
        "summary": "A child of an epic",
        "issuetype": {"name": "Task"},
        "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
        "assignee": null,
        "parent": {"key": "DCAI-100", "fields": {"summary": "Checkout revamp", "issuetype": {"name": "Epic"}}},
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
	if iss.ParentKey != "DCAI-100" {
		t.Errorf("ParentKey = %q, want %q", iss.ParentKey, "DCAI-100")
	}
	// A child issue carries no epic colour of its own.
	if iss.EpicColor != "" {
		t.Errorf("EpicColor = %q, want empty on a child issue", iss.EpicColor)
	}
}

func TestToIssueParsesEpicColor(t *testing.T) {
	const body = `{
      "key": "DCAI-100",
      "fields": {
        "summary": "Checkout revamp",
        "issuetype": {"name": "Epic"},
        "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
        "assignee": null,
        "customfield_10017": {"value": "dark_teal"},
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
	if iss.EpicColor != "dark_teal" {
		t.Errorf("EpicColor = %q, want %q", iss.EpicColor, "dark_teal")
	}
	if iss.ParentKey != "" {
		t.Errorf("ParentKey = %q, want empty for a top-level epic", iss.ParentKey)
	}
}

func TestToIssueToleratesMissingParentAndColor(t *testing.T) {
	const body = `{
      "key": "DCAI-11",
      "fields": {
        "summary": "Standalone",
        "issuetype": {"name": "Task"},
        "status": {"name": "Triage", "statusCategory": {"name": "To Do"}},
        "assignee": null,
        "customfield_10017": null,
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
	if iss.ParentKey != "" || iss.EpicColor != "" {
		t.Errorf("want empty ParentKey/EpicColor, got %q/%q", iss.ParentKey, iss.EpicColor)
	}
}
