package jira

// Wire-level tests for the assignee write (#223): the Board's assignee edit
// writes the target person's ACCOUNT ID to Jira via
// PUT /rest/api/3/issue/{key}/assignee with {"accountId":"..."}, and clears the
// assignee with an explicit {"accountId":null}. There is no assign-by-name
// endpoint, which is why docs/adr/0012 put people in the projection.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveUpdateIssueAssigneePutsTheAccountID(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssueAssignee(context.Background(), "DCAI-1921", "5b10a2844c20165700ede21g"); err != nil {
		t.Fatalf("UpdateIssueAssignee: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/rest/api/3/issue/DCAI-1921/assignee" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["accountId"] != "5b10a2844c20165700ede21g" {
		t.Errorf("body = %v, want the account id", gotBody)
	}
	if _, hasName := gotBody["name"]; hasName {
		t.Errorf("body carries a name; Jira assigns by account id only: %v", gotBody)
	}
}

// Clearing is its own wire shape: a JSON null, not an omitted key and not an
// empty string, both of which Jira rejects.
func TestLiveUpdateIssueAssigneeClearsWithAnExplicitNull(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssueAssignee(context.Background(), "DCAI-1921", UnassignedAccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee(clear): %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	accountID, present := body["accountId"]
	if !present {
		t.Fatalf("body = %s, want an explicit accountId key", raw)
	}
	if string(accountID) != "null" {
		t.Errorf("accountId = %s, want null", accountID)
	}
}

func TestLiveUpdateIssueAssigneeSurfacesAJiraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errorMessages":["You do not have permission to assign issues"]}`)
	}))
	defer srv.Close()

	c := NewLiveClient(Config{BaseURL: srv.URL, Email: "e", APIToken: "t"})
	if err := c.UpdateIssueAssignee(context.Background(), "DCAI-1921", "acct-1"); err == nil {
		t.Fatal("UpdateIssueAssignee succeeded on a 403, want an error")
	}
}
