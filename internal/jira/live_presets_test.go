package jira_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestPlanningPresetsWalkVisibleProjectShares(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/rest/api/3/filter/search" || r.Header.Get("Authorization") != "Bearer personal" ||
			q.Get("projectId") != "10004" || q.Get("filterName") != "Planning" ||
			q.Get("expand") != "jql,description,sharePermissions" || q.Get("overrideSharePermissions") == "true" {
			t.Errorf("unexpected filter discovery request: %s", r.URL)
		}
		offsets = append(offsets, q.Get("startAt"))
		switch q.Get("startAt") {
		case "0":
			fmt.Fprint(w, `{"total":4,"isLast":false,"values":[
				{"id":"10519","name":"Planning Since Meeting","jql":"project = DCAI","description":"configured","sharePermissions":[{"type":"project","project":{"id":"10004"}}]},
				{"id":"20","name":"Old Planning","sharePermissions":[{"type":"project","project":{"id":"10004"}}]}
			]}`)
		case "2":
			fmt.Fprint(w, `{"total":4,"isLast":true,"values":[
				{"id":"21","name":"Planning Other Project","sharePermissions":[{"type":"project","project":{"id":"10000"}},{"type":"global"}]},
				{"id":"22","name":"Planning Epics","sharePermissions":[{"type":"projectRole","project":{"id":"10004"},"role":{"id":10002}}]}
			]}`)
		default:
			http.Error(w, "unexpected offset", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := jira.NewBearerClient(jira.Config{BaseURL: server.URL}, "personal", server.Client())
	filters, err := client.PlanningPresets(context.Background())
	want := []jira.PlanningPreset{
		{ID: "10519", Name: "Planning Since Meeting", JQL: "project = DCAI", Description: "configured"},
		{ID: "22", Name: "Planning Epics"},
	}
	if err != nil || !slices.Equal(filters, want) || !slices.Equal(offsets, []string{"0", "2"}) {
		t.Fatalf("filters = %+v, offsets = %v, err = %v", filters, offsets, err)
	}
}

func TestPlanningSearchReadsOnlyKeysInPageOrder(t *testing.T) {
	const jql = "project = DCAI ORDER BY rank ASC"
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/rest/api/3/search/jql" || r.Header.Get("Authorization") != "Bearer personal" ||
			q.Get("jql") != jql || q.Get("fields") != "key" || q.Has("expand") {
			t.Errorf("unexpected key search request: %s", r.URL)
		}
		tokens = append(tokens, q.Get("nextPageToken"))
		switch q.Get("nextPageToken") {
		case "":
			fmt.Fprint(w, `{"isLast":false,"nextPageToken":"page+2","issues":[{"key":"DCAI-9"},{"key":"DCAI-2"}]}`)
		case "page+2":
			fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DCAI-7"}]}`)
		default:
			http.Error(w, "unexpected token", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := jira.NewBearerClient(jira.Config{BaseURL: server.URL}, "personal", server.Client())
	keys, err := client.SearchIssueKeys(context.Background(), jql)
	if err != nil || !slices.Equal(keys, []string{"DCAI-9", "DCAI-2", "DCAI-7"}) || !slices.Equal(tokens, []string{"", "page+2"}) {
		t.Fatalf("keys = %v, tokens = %v, err = %v", keys, tokens, err)
	}
}

func TestProjectMembershipReadsCanonicalIssueKeyAndProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			query := r.URL.Query()
			if query.Get("jql") != `project = "DCAI" ORDER BY key ASC` || query.Get("fields") != "key" {
				t.Errorf("unexpected project search: %s", r.URL)
			}
			fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DCAI-1"}]}`)
		case "/rest/api/3/issue/DCAI-1":
			if r.URL.Query().Get("fields") != "project" {
				t.Errorf("unexpected issue lookup: %s", r.URL)
			}
			fmt.Fprint(w, `{"key":"DCAI-1","fields":{"project":{"key":"DCAI"}}}`)
		case "/rest/api/3/issue/DCAI-2":
			http.NotFound(w, r)
		case "/rest/api/3/issue/DCAI-3":
			fmt.Fprint(w, `{"key":"OTHER-3","fields":{"project":{"key":"OTHER"}}}`)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	client := jira.NewBearerClient(jira.Config{BaseURL: server.URL, ProjectKey: "DCAI"}, "service", server.Client())
	keys, err := client.FetchProjectIssueKeys(t.Context())
	if err != nil || !slices.Equal(keys, []string{"DCAI-1"}) {
		t.Fatalf("project keys = %v, err = %v", keys, err)
	}
	for _, tc := range []struct {
		key     string
		kept    bool
		wantErr bool
	}{{"DCAI-1", true, false}, {"DCAI-2", false, false}, {"DCAI-3", false, false}, {"DCAI-4", false, true}} {
		t.Run(tc.key, func(t *testing.T) {
			kept, err := client.IssueInProject(t.Context(), tc.key)
			if kept != tc.kept || (err != nil) != tc.wantErr {
				t.Errorf("membership = %v, err = %v, want kept %v and error %v", kept, err, tc.kept, tc.wantErr)
			}
		})
	}
}
