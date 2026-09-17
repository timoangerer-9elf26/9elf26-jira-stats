package jira

// Unit tests for the project-member read (#222): Jira's assignable-user search
// for the project, filtered to people. Jira reports its automation as app
// accounts on the very same endpoint, and a member list that offers a bot as an
// assign target is the regression these tests exist to catch (docs/adr/0012).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// assignableSearchBody mirrors the live payload shape: a bare JSON array of
// users, each carrying accountId, accountType, displayName and the full avatar
// size set.
const assignableSearchBody = `[
  {
    "accountId": "5b10a2844c20165700ede21g",
    "accountType": "atlassian",
    "displayName": "Ada Lovelace",
    "avatarUrls": {
      "48x48": "https://avatar.example/ada/48.png",
      "32x32": "https://avatar.example/ada/32.png",
      "24x24": "https://avatar.example/ada/24.png",
      "16x16": "https://avatar.example/ada/16.png"
    }
  },
  {
    "accountId": "712020:1c1e0f2a-0000-4000-8000-000000000001",
    "accountType": "app",
    "displayName": "Jira Coding Agent",
    "avatarUrls": {"48x48": "https://avatar.example/bot/48.png"}
  },
  {
    "accountId": "5b10a2844c20165700ede21h",
    "accountType": "atlassian",
    "displayName": "Grace Hopper",
    "avatarUrls": {"32x32": "https://avatar.example/grace/32.png"}
  }
]`

func TestFetchProjectMembersKeepsPeopleAndDropsAppAccounts(t *testing.T) {
	var gotPath, gotProject string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotProject = r.URL.Query().Get("project")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, assignableSearchBody)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t", ProjectKey: "DCAI"})
	members, err := c.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}

	if want := "/rest/api/3/user/assignable/search"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotProject != "DCAI" {
		t.Errorf("project query = %q, want DCAI", gotProject)
	}

	if len(members) != 2 {
		t.Fatalf("members = %+v, want the two people only", members)
	}
	for _, m := range members {
		if m.DisplayName == "Jira Coding Agent" {
			t.Fatalf("app account %q became a project member", m.DisplayName)
		}
	}

	ada := members[0]
	if ada.AccountID != "5b10a2844c20165700ede21g" || ada.DisplayName != "Ada Lovelace" {
		t.Errorf("first member = %+v, want Ada Lovelace with her account id", ada)
	}
	// Largest-first, exactly as the issue assignee avatar is chosen.
	if want := "https://avatar.example/ada/48.png"; ada.AvatarURL != want {
		t.Errorf("Ada AvatarURL = %q, want %q", ada.AvatarURL, want)
	}
	// A partial avatar set still yields the largest URL present.
	if want := "https://avatar.example/grace/32.png"; members[1].AvatarURL != want {
		t.Errorf("Grace AvatarURL = %q, want %q", members[1].AvatarURL, want)
	}
}

// Jira's assignable search is startAt-paginated and answers a short page at the
// end, so the walk must keep going until one arrives.
func TestFetchProjectMembersWalksEveryPage(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.URL.Query().Get("startAt"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = io.WriteString(w, fullPageOfPeople())
			return
		}
		_, _ = io.WriteString(w, `[{"accountId":"last","accountType":"atlassian","displayName":"Last Person","avatarUrls":{}}]`)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t", ProjectKey: "DCAI"})
	members, err := c.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}
	if len(starts) != 2 || starts[0] != "0" || starts[1] != "50" {
		t.Fatalf("startAt sequence = %v, want [0 50]", starts)
	}
	if len(members) != memberPageSize+1 {
		t.Fatalf("members = %d, want %d", len(members), memberPageSize+1)
	}
	if members[len(members)-1].DisplayName != "Last Person" {
		t.Errorf("last member = %q, want Last Person", members[len(members)-1].DisplayName)
	}
}

// fullPageOfPeople builds a page of exactly memberPageSize people, the "there
// may be more" signal the walk must follow.
func fullPageOfPeople() string {
	body := "["
	for i := 0; i < memberPageSize; i++ {
		if i > 0 {
			body += ","
		}
		id := strconv.Itoa(i)
		body += `{"accountId":"p` + id + `","accountType":"atlassian","displayName":"Person ` + id + `","avatarUrls":{}}`
	}
	return body + "]"
}
