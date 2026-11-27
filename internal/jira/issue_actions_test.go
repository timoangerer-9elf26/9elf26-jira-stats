package jira_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestPersonalPrioJiraUsesCurrentMetadataAndBearer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/DCAI-41/editmeta", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fields":{"priority":{"operations":["set"],"allowedValues":[{"id":"2","name":"High"}]},"unrelated":{"allowedValues":["red","blue",13]}}}`)
	})
	mux.HandleFunc("GET /rest/api/3/issue/DCAI-41/transitions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expand") != "transitions.fields" {
			t.Error("transition request did not include fields")
		}
		fmt.Fprint(w, `{"transitions":[{"id":"6","name":"Cancel","hasScreen":true,"to":{"name":"Canceled"},"fields":{"resolution":{"required":true,"name":"Resolution"}}}]}`)
	})
	mux.HandleFunc("GET /rest/api/3/issue/DCAI-41", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"key":"OTHER-1","fields":{"project":{"key":"OTHER"}}}`)
	})
	mux.HandleFunc("POST /rest/api/3/issue/DCAI-41/comment", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		text, plain := jira.ADFText(body["body"])
		if len(body) != 1 || !plain || text != "Personal comment" {
			t.Errorf("comment request = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /rest/api/3/issue/createmeta/DCAI/issuetypes", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "0":
			fmt.Fprint(w, `{"issueTypes":[{"id":"10001","name":"Task"}],"total":2}`)
		case "1":
			fmt.Fprint(w, `{"issueTypes":[{"id":"10014","name":"Epic"}],"total":2}`)
		default:
			t.Error("unexpected project issue type page")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	mux.HandleFunc("POST /rest/api/3/issue", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Fields map[string]json.RawMessage }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fields := body.Fields
		if len(fields) != 4 || string(fields["project"]) != `{"key":"DCAI"}` ||
			string(fields["issuetype"]) != `{"id":"10014"}` || string(fields["summary"]) != `"Meeting Epic"` {
			t.Errorf("unexpected create fields: %s", fields)
		}
		description, _ := jira.ADFText(fields["description"])
		if description != "Epic description" {
			t.Errorf("create description = %q", description)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"702","key":"DCAI-42"}`)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer personal-jira-token" {
			t.Error("Jira request did not use the signed-in user's bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := jira.NewBearerClient(jira.Config{BaseURL: server.URL}, "personal-jira-token", server.Client())
	ctx := context.Background()
	fields, err := client.FetchEditFields(ctx, "DCAI-41")
	if err != nil || len(fields["priority"].AllowedValues) != 1 || fields["priority"].AllowedValues[0].ID != "2" {
		t.Fatalf("edit metadata = %+v, %v", fields, err)
	}
	transitions, err := client.FetchIssueTransitions(ctx, "DCAI-41")
	if err != nil || len(transitions) != 1 || transitions[0].ToStatusName != "Canceled" ||
		!transitions[0].HasScreen || !transitions[0].Fields["resolution"].Required {
		t.Fatalf("transition metadata = %+v, %v", transitions, err)
	}
	if _, err := client.ReadPrioIssueDescription(ctx, "DCAI-41"); err == nil {
		t.Fatal("a stale project key alias was accepted")
	}
	if err := client.AddIssueComment(ctx, "DCAI-41", jira.PlainADF("Personal comment"), jira.Person{AccountID: "personal-account"}); err != nil {
		t.Fatal(err)
	}
	key, err := client.CreatePrioEpic(ctx, "Meeting Epic", jira.PlainADF("Epic description"), jira.Person{AccountID: "personal-account"})
	if err != nil || key != "DCAI-42" {
		t.Fatalf("created key = %q, %v", key, err)
	}
}
