package jira

// Wire-level tests for the priority write (#212): the Prio view's priority edit
// writes the level to Jira BY NAME via PUT /rest/api/3/issue/{key} with
// {"fields":{"priority":{"name":"<Level>"}}} — the same field shape the app
// already reads (priorityDTO), so no id column or lookup is needed.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveUpdateIssuePriorityPutsTheLevelByName(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssuePriority(context.Background(), "DCAI-1921", "High"); err != nil {
		t.Fatalf("UpdateIssuePriority: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/rest/api/3/issue/DCAI-1921" {
		t.Errorf("path = %q", gotPath)
	}
	fields, _ := gotBody["fields"].(map[string]any)
	prio, _ := fields["priority"].(map[string]any)
	if prio["name"] != "High" {
		t.Errorf("body = %v, want {\"fields\":{\"priority\":{\"name\":\"High\"}}}", gotBody)
	}
	if _, hasID := prio["id"]; hasID {
		t.Errorf("body carries a priority id; the write must go by name only: %v", gotBody)
	}
}

func TestLiveUpdateIssuePriorityRejectsAnUnknownLevelBeforeCalling(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssuePriority(context.Background(), "DCAI-1921", "Blocker"); err == nil {
		t.Fatal("UpdateIssuePriority accepted an unknown level")
	}
	if called {
		t.Error("an unknown level reached Jira; it must be rejected client-side")
	}
}

func TestLiveUpdateIssuePrioritySurfacesAJiraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errorMessages":["You do not have permission"]}`)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssuePriority(context.Background(), "DCAI-1921", "Low"); err == nil {
		t.Fatal("UpdateIssuePriority succeeded on a 403, want an error")
	}
}
