package jira

// Unit test for the sync-parse of the assignee's public avatar image URL (#68):
// the highest-resolution entry from the assignee's avatarUrls, empty when the
// issue is unassigned. It is a static per-issue field on every fetch, so a
// normal re-sync populates it — no changelog work.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestToIssueParsesAssigneeAvatarURL(t *testing.T) {
	const body = `{
      "key": "DCAI-9",
      "fields": {
        "summary": "Has an avatar",
        "issuetype": {"name": "Task"},
        "status": {"name": "In Progress", "statusCategory": {"name": "In Progress"}},
        "assignee": {
          "displayName": "Ada Lovelace",
          "avatarUrls": {
            "48x48": "https://avatar.example/ada/48.png",
            "32x32": "https://avatar.example/ada/32.png",
            "24x24": "https://avatar.example/ada/24.png",
            "16x16": "https://avatar.example/ada/16.png"
          }
        },
        "customfield_10040": null,
        "customfield_10020": []
      },
      "changelog": {"startAt": 0, "maxResults": 100, "total": 0, "histories": []}
    }`

	var dto issueDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	iss, err := (&LiveClient{}).toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}
	// The largest available avatar is captured for a crisp render in the small circle.
	if want := "https://avatar.example/ada/48.png"; iss.AssigneeAvatarURL != want {
		t.Errorf("AssigneeAvatarURL = %q, want %q", iss.AssigneeAvatarURL, want)
	}
}

func TestToIssueUnassignedHasNoAvatarURL(t *testing.T) {
	const body = `{
      "key": "DCAI-8",
      "fields": {
        "summary": "Unassigned",
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
	iss, err := (&LiveClient{}).toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}
	if iss.AssigneeAvatarURL != "" {
		t.Errorf("AssigneeAvatarURL = %q, want empty for an unassigned issue", iss.AssigneeAvatarURL)
	}
}
