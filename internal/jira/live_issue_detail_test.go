package jira_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestFetchIssueIncludesPagedCommentsAndChangelog(t *testing.T) {
	const adf = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Meeting notes"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/issue/DCAI-42":
			fields := strings.Split(r.URL.Query().Get("fields"), ",")
			for _, want := range []string{"description", "comment", "resolutiondate", "issuelinks"} {
				if !slices.Contains(fields, want) {
					t.Errorf("requested fields omit %s", want)
				}
			}
			fmt.Fprintf(w, `{
				"key":"DCAI-42",
				"fields":{
					"description":%s, "assignee":{"accountId":"ada","displayName":"Ada"},
					"labels":["Technical"], "priority":{"name":"High"}, "parent":{"key":"DCAI-12"},
					"customfield_10020":[{"id":8,"name":"Sprint 8","state":"active"}],
					"resolutiondate":"2026-11-22T18:00:00.456+0100",
					"issuelinks":[
						{"id":"1","type":{"inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"DCAI-7","fields":{"summary":"Preparation"}}},
						{"id":"2","type":{"inward":"is blocked by","outward":"blocks"},"outwardIssue":{"key":"DCAI-8","fields":{"summary":"Follow-up"}}}
					],
					"comment":{"startAt":0,"total":3,"comments":[{"id":"1"}]}
				},
				"changelog":{"total":2,"histories":[]}
			}`, adf)
		case "/rest/api/3/issue/DCAI-42/comment":
			switch r.URL.Query().Get("startAt") {
			case "0":
				fmt.Fprintf(w, `{"startAt":0,"total":3,"comments":[{"id":"1","body":%s,"author":{"accountId":"ada","displayName":"Ada"},"created":"2026-11-20T09:00:00+0100"}]}`, adf)
			case "1":
				fmt.Fprint(w, `{"startAt":1,"total":3,"comments":[{"id":"private","visibility":{"type":"role","identifier":"Managers"}}]}`)
			case "2":
				fmt.Fprint(w, `{"startAt":2,"total":3,"comments":[{"id":"2","author":{"accountId":"grace","displayName":"Grace"}}]}`)
			default:
				t.Errorf("unexpected comment page %s", r.URL.RawQuery)
				w.WriteHeader(http.StatusBadRequest)
			}
		case "/rest/api/3/issue/DCAI-42/changelog":
			switch r.URL.Query().Get("startAt") {
			case "0":
				fmt.Fprint(w, `{"startAt":0,"total":2,"values":[{"id":"4","created":"2026-11-22T18:00:00.456+0100","author":{"accountId":"ada","displayName":"Ada"},"items":[
					{"field":"status","from":"1","fromString":"In Progress","to":"2","toString":"DONE (This Sprint)"},
					{"fieldId":"priority","from":"3","fromString":"Medium","to":"2","toString":"High"},
					{"fieldId":"resolution","from":null,"fromString":null,"to":"1","toString":"Done"}
				]}]}`)
			case "1":
				fmt.Fprint(w, `{"startAt":1,"total":2,"values":[{"id":"5","created":"2026-11-23T09:00:00+0100","author":{"accountId":"grace","displayName":"Grace"},"items":[
					{"field":"status","from":"2","fromString":"DONE (This Sprint)","to":"1","toString":"In Progress"}
				]}]}`)
			default:
				t.Errorf("unexpected changelog page %s", r.URL.RawQuery)
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := jira.NewLiveClient(jira.Config{BaseURL: server.URL})
	issue, err := client.FetchIssue(t.Context(), "DCAI-42")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(issue.Description); got != adf || issue.AssigneeAccountID != "ada" || issue.Assignee != "Ada" ||
		issue.ParentKey != "DCAI-12" || issue.Sprint != "Sprint 8" || issue.Priority != "High" || !slices.Equal(issue.Labels, []string{"Technical"}) {
		t.Fatalf("issue details = %+v", issue)
	}
	if got := issue.ResolvedAt.UTC().Format(time.RFC3339Nano); got != "2026-11-22T17:00:00.456Z" {
		t.Errorf("resolution = %s", got)
	}
	if len(issue.Comments) != 2 || issue.Comments[0].Author.DisplayName != "Ada" || string(issue.Comments[0].Body) != adf ||
		issue.Comments[1].ID != "2" || issue.Comments[1].Author.AccountID != "grace" {
		t.Errorf("comments = %+v", issue.Comments)
	}
	wantLinks := []jira.IssueLink{
		{Key: "DCAI-7", Summary: "Preparation", Relation: "is blocked by"},
		{Key: "DCAI-8", Summary: "Follow-up", Relation: "blocks"},
	}
	if !slices.Equal(issue.Links, wantLinks) {
		t.Errorf("links = %+v", issue.Links)
	}
	if len(issue.Changelog) != 4 {
		t.Fatalf("changelog = %+v", issue.Changelog)
	}
	priority := issue.Changelog[1]
	if priority.Field != "priority" || priority.From != "Medium" || priority.To != "High" ||
		priority.FromID != "3" || priority.ToID != "2" || priority.Author.AccountID != "ada" ||
		priority.Timestamp.UTC().Format(time.RFC3339Nano) != "2026-11-22T17:00:00.456Z" {
		t.Errorf("priority change = %+v", priority)
	}
	resolution := issue.Changelog[2]
	if resolution.Field != "resolution" || resolution.From != "" || resolution.To != "Done" || resolution.ToID != "1" {
		t.Errorf("resolution change = %+v", resolution)
	}
	reopen := issue.Changelog[3]
	if reopen.Field != "status" || reopen.From != "DONE (This Sprint)" || reopen.To != "In Progress" || reopen.Author.DisplayName != "Grace" {
		t.Errorf("reopen = %+v", reopen)
	}
}
