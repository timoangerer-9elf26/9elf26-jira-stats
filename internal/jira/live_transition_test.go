package jira

// Wire-level tests for the transition seam against payloads captured from LIVE
// Jira (DCAI-1921, 2026-09-01). The fake returns already-mapped domain objects,
// so only a test against the real JSON shape can catch a wrong DTO (see #77).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A trimmed but shape-faithful capture of
// GET /rest/api/3/issue/DCAI-1921/transitions.
const liveTransitionsBody = `{
  "expand": "transitions",
  "transitions": [
    {
      "id": "5",
      "name": "DONE (This Sprint)",
      "to": {
        "self": "https://api.atlassian.com/ex/jira/62a0.../rest/api/3/status/10064",
        "description": "",
        "iconUrl": "https://api.atlassian.com/ex/jira/62a0.../",
        "name": "DONE (This Sprint)",
        "id": "10064",
        "statusCategory": {"self": "…/statuscategory/3", "id": 3, "key": "done", "colorName": "green", "name": "Done"}
      },
      "hasScreen": false, "isGlobal": true, "isInitial": false, "isAvailable": true,
      "isConditional": false, "isLooped": false
    },
    {
      "id": "31",
      "name": "Done",
      "to": {
        "self": "https://api.atlassian.com/ex/jira/62a0.../rest/api/3/status/10016",
        "description": "",
        "iconUrl": "https://api.atlassian.com/ex/jira/62a0.../",
        "name": "Ready for release",
        "id": "10016",
        "statusCategory": {"self": "…/statuscategory/3", "id": 3, "key": "done", "colorName": "green", "name": "Done"}
      },
      "hasScreen": false, "isGlobal": true, "isInitial": false, "isAvailable": true,
      "isConditional": false, "isLooped": false
    }
  ]
}`

func TestTransitionsResponseDecodesLiveShape(t *testing.T) {
	var got transitionsResponse
	if err := json.Unmarshal([]byte(liveTransitionsBody), &got); err != nil {
		t.Fatalf("decode transitions: %v", err)
	}
	trs := toTransitions(got.Transitions)
	if len(trs) != 2 {
		t.Fatalf("got %d transitions, want 2", len(trs))
	}
	want := Transition{ID: "5", Name: "DONE (This Sprint)", ToStatusID: "10064",
		ToStatusName: "DONE (This Sprint)", ToStatusCategory: "Done"}
	if trs[0] != want {
		t.Errorf("transitions[0] = %+v, want %+v", trs[0], want)
	}
	if trs[1].ToStatusName != "Ready for release" {
		t.Errorf("transitions[1] target = %q, want %q", trs[1].ToStatusName, "Ready for release")
	}
}

func TestLiveFetchTransitionsCallsTheIssueTransitionsEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, liveTransitionsBody)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	trs, err := c.FetchTransitions(context.Background(), "DCAI-1921")
	if err != nil {
		t.Fatalf("FetchTransitions: %v", err)
	}
	if gotPath != "/rest/api/3/issue/DCAI-1921/transitions" {
		t.Errorf("path = %q", gotPath)
	}
	if len(trs) != 2 {
		t.Fatalf("got %d transitions, want 2", len(trs))
	}
}

func TestLiveTransitionIssuePostsTheTransitionID(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.TransitionIssue(context.Background(), "DCAI-1921", "5"); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/rest/api/3/issue/DCAI-1921/transitions" {
		t.Errorf("path = %q", gotPath)
	}
	tr, ok := gotBody["transition"].(map[string]any)
	if !ok || tr["id"] != "5" {
		t.Errorf("body = %v, want {\"transition\":{\"id\":\"5\"}}", gotBody)
	}
}

func TestLiveTransitionIssueSurfacesAJiraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["Transition is not valid"]}`)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.TransitionIssue(context.Background(), "DCAI-1921", "999"); err == nil {
		t.Fatal("TransitionIssue succeeded on a 400, want an error")
	}
}
