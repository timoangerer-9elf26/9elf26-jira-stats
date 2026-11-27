package web_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

type prioGatedReads struct {
	casePlanningJira
	searchStarted, fieldsStarted, transitionsStarted chan struct{}
	release                                          chan struct{}
}

func (c *prioGatedReads) SearchIssueKeys(ctx context.Context, jql string) ([]string, error) {
	close(c.searchStarted)
	select {
	case <-c.release:
		return c.casePlanningJira.SearchIssueKeys(ctx, jql)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *prioGatedReads) FetchEditFields(ctx context.Context, key string) (map[string]jira.EditField, error) {
	close(c.fieldsStarted)
	select {
	case <-c.release:
		return c.FakeClient.FetchEditFields(ctx, key)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *prioGatedReads) FetchIssueTransitions(ctx context.Context, key string) ([]jira.IssueTransition, error) {
	close(c.transitionsStarted)
	select {
	case <-c.release:
		return c.FakeClient.FetchIssueTransitions(ctx, key)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPrioPaintsProjectedDetailsWhilePersonalJiraReadsWait(t *testing.T) {
	client := &prioGatedReads{
		casePlanningJira: casePlanningJira{&jira.FakeClient{Issues: []jira.Issue{{
			Key: "DCAI-41", Type: "Epic", Status: "Triage", Priority: "High", Summary: "Projected ticket",
		}}}},
		searchStarted: make(chan struct{}), fieldsStarted: make(chan struct{}),
		transitionsStarted: make(chan struct{}), release: make(chan struct{}),
	}
	app := newWritablePrioApp(t, client)
	released := false
	defer func() {
		if !released {
			close(client.release)
		}
	}()
	const query = "?step=overview&ticket=DCAI-41&preset=42"
	body := get(t, app.URL+"/prio"+query)
	if !strings.Contains(body, "Projected ticket") || !strings.Contains(body, "/prio/ticket/controls?") ||
		!strings.Contains(body, "/prio/preset-results?") || strings.Contains(body, `data-key="DCAI-41"`) {
		t.Fatal("cold page must render the selected issue and defer Jira controls and preset membership")
	}
	for _, started := range []chan struct{}{client.searchStarted, client.fieldsStarted, client.transitionsStarted} {
		select {
		case <-started:
			t.Fatal("a personal Jira read started in the initial render")
		default:
		}
	}
	type reply struct {
		response *http.Response
		err      error
	}
	results := make(chan reply, 1)
	controls := make(chan reply, 1)
	read := func(path string, result chan<- reply) {
		res, err := app.Client().Get(app.URL + path + query)
		result <- reply{res, err}
	}
	go read("/prio/preset-results", results)
	go read("/prio/ticket/controls", controls)
	for _, started := range []chan struct{}{client.searchStarted, client.fieldsStarted, client.transitionsStarted} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("the deferred reads did not start independently")
		}
	}
	body = get(t, app.URL+"/prio/drawer"+query)
	if !strings.Contains(body, "Projected ticket") || strings.Contains(body, "prio-table") {
		t.Fatal("drawer did not return only the projected ticket while Jira was pending")
	}
	close(client.release)
	released = true
	if result := <-results; !strings.Contains(ticketResponse(t, result.response, result.err), `data-key="DCAI-41"`) {
		t.Fatal("deferred preset did not return its matching row")
	}
	if result := <-controls; !strings.Contains(ticketResponse(t, result.response, result.err), `id="prio-edit-summary"`) {
		t.Fatal("deferred controls did not return the edit form")
	}
}
