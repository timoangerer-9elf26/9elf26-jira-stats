package web_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

func ticketResponse(t *testing.T, response *http.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	return string(body)
}

func TestPrioTicketShowsSafeProjectedDetails(t *testing.T) {
	client := &jira.FakeClient{Issues: []jira.Issue{{
		Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage",
		Assignee: "Ada Example", Labels: []string{"panel-label"}, Sprint: "Sprint Beacon", ParentKey: "DCAI-40",
		Description: json.RawMessage(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"<script>description</script>"}]}]}`),
		Comments:    []jira.Comment{{Author: jira.Person{DisplayName: "Jira Author"}, Body: json.RawMessage(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Projected comment"}]}]}`)}},
		Links:       []jira.IssueLink{{Key: "DCAI-42", Relation: "blocks", Summary: "Related work"}},
	}}}
	app := newTestApp(t, client, web.WithJiraBaseURL("https://jira.example"))
	res, err := http.Get(app.URL + "/prio/results?step=overview&preset=all&ticket=DCAI-41")
	body := ticketResponse(t, res, err)
	for _, want := range []string{"&lt;script&gt;description&lt;/script&gt;", "Ada Example", "panel-label", "Sprint Beacon", "Jira Author", "Projected comment", "blocks", "https://jira.example/browse/DCAI-40", "https://jira.example/browse/DCAI-42"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel missing %q", want)
		}
	}
	if strings.Contains(body, "<script>description") || strings.Contains(body, `name="comment"`) {
		t.Fatal("read-only panel rendered unsafe content or an editable control")
	}
}

func postPrioTicket(t *testing.T, app *testApp, form url.Values) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, app.URL+"/prio/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	res, err := http.DefaultClient.Do(request)
	return ticketResponse(t, res, err)
}

func newWritablePrioApp(t *testing.T, client *jira.FakeClient) *testApp {
	t.Helper()
	app := newTestApp(t, client)
	server, err := web.NewServer(app.Store, web.WithFakeJira(client), web.WithJiraBaseURL("https://jira.example"),
		web.WithPrioIssueRefresher(sync.NewSyncer(client, app.Store, time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(server)
	t.Cleanup(local.Close)
	return &testApp{Server: local, Store: app.Store}
}

func TestPrioTicketCommentUsesSignedInJiraAccount(t *testing.T) {
	client := &jira.FakeClient{Issues: []jira.Issue{{
		Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage",
	}}}
	app := newWritablePrioApp(t, client)
	q := url.Values{"step": {"overview"}, "preset": {"all"}, "ticket": {"DCAI-41"}, "key": {"DCAI-41"},
		"action": {"comment"}, "comment": {"My meeting comment"}, "author": {"forged-account"}}
	body := postPrioTicket(t, app, q)
	for _, want := range []string{"My meeting comment", "jira-stats-fake", "DCAI-41 · Kommentar hinzugefügt", `data-ticket="DCAI-41"`} {
		if !strings.Contains(body, want) {
			t.Errorf("mutation response missing %q", want)
		}
	}
	res, err := http.Get(app.URL + "/prio/results?step=overview&preset=all&ticket=DCAI-41")
	if body := ticketResponse(t, res, err); !strings.Contains(body, "My meeting comment") || strings.Contains(body, "forged-account") {
		t.Fatal("refreshed ticket did not show the signed-in user's comment")
	}
}

func TestPrioTicketPreservesFormattedDescriptionWhenEditingTitle(t *testing.T) {
	rich := json.RawMessage(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bold Jira content","marks":[{"type":"strong"}]}]}]}`)
	client := &jira.FakeClient{Issues: []jira.Issue{{
		Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage", Description: rich,
	}}}
	app := newWritablePrioApp(t, client)
	q := url.Values{"step": {"backlog"}, "preset": {"all"}, "key": {"DCAI-41"}, "backlog-sort": {"age"},
		"action": {"summary"}, "summary": {"New Jira title"}, "description": {"unrelated form value"}}
	body := postPrioTicket(t, app, q)
	for _, want := range []string{"New Jira title", "Bold Jira content", "Formatierte Beschreibung in Jira", `data-ticket="DCAI-41"`, `name="backlog-sort" value="age"`} {
		if !strings.Contains(body, want) {
			t.Errorf("mutation response missing %q", want)
		}
	}
	if strings.Contains(body, `name="description"`) {
		t.Fatal("formatted ADF description was offered for plain text editing")
	}
	detail, found, err := app.Store.IssueDetail("DCAI-41")
	if err != nil || !found || string(detail.Description) != string(rich) {
		t.Fatalf("formatted ADF did not survive the refresh: %s (%v)", detail.Description, err)
	}
}

func TestPrioTicketOffersComplexTransitionInJiraAndRefreshesSimpleTransition(t *testing.T) {
	client := &jira.FakeClient{
		Issues: []jira.Issue{{Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage"}},
		TransitionFields: map[string]map[string]jira.EditField{
			"6": {"resolution": {Name: "Resolution", Required: true}},
		},
	}
	app := newWritablePrioApp(t, client)
	res, err := http.Get(app.URL + "/prio/results?step=backlog&ticket=DCAI-41")
	body := ticketResponse(t, res, err)
	for _, want := range []string{"Canceled in Jira", "Pflichtfelder · Resolution", `name="transition" value="21"`} {
		if !strings.Contains(body, want) {
			t.Errorf("transition choices missing %q", want)
		}
	}
	q := url.Values{"step": {"backlog"}, "preset": {"all"}, "key": {"DCAI-41"}, "action": {"transition"}, "transition": {"6"}}
	body = postPrioTicket(t, app, q)
	if !strings.Contains(body, "Bitte diesen Statusübergang in Jira ausführen.") {
		t.Fatalf("complex transition response: %s", body)
	}
	q.Set("transition", "21")
	body = postPrioTicket(t, app, q)
	for _, want := range []string{`data-ticket="DCAI-41"`, "DCAI-41 · Status → In Progress", "Triage → In Progress"} {
		if !strings.Contains(body, want) {
			t.Errorf("transition response missing %q", want)
		}
	}
	if strings.Contains(body, `data-prio-queue-key="DCAI-41"`) {
		t.Fatal("transitioned ticket still appears in planned backlog")
	}
}

func TestPrioTicketCreatesDCAIEpicAndKeepsSelection(t *testing.T) {
	client := &jira.FakeClient{Issues: []jira.Issue{{Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage"}}}
	app := newWritablePrioApp(t, client)
	body := postPrioTicket(t, app, url.Values{"step": {"overview"}, "preset": {"all"}, "ticket": {"DCAI-41"},
		"action": {"epic"}, "epic-summary": {"Meeting Epic"}, "epic-description": {"Proposed work"},
		"project": {"OTHER"}, "issuetype": {"Task"}})
	for _, want := range []string{`data-ticket="DCAI-41"`, "DCAI-42 · Epic angelegt", "Meeting Epic"} {
		if !strings.Contains(body, want) {
			t.Errorf("create response missing %q", want)
		}
	}
	res, err := http.Get(app.URL + "/prio/results?step=overview&preset=all&ticket=DCAI-42")
	body = ticketResponse(t, res, err)
	if !strings.Contains(body, "Proposed work") || !strings.Contains(body, `data-testid="prio:DCAI-42:type"`) {
		t.Fatal("new Epic was not projected and selectable")
	}
}

func TestPrioTicketReportsJiraError(t *testing.T) {
	client := &jira.FakeClient{Issues: []jira.Issue{{Key: "DCAI-41", Type: "Task", Summary: "Panel example", Status: "Triage"}},
		WriteErr: errors.New("Jira refused <comment>")}
	app := newWritablePrioApp(t, client)
	body := postPrioTicket(t, app, url.Values{"key": {"DCAI-41"}, "action": {"comment"}, "comment": {"Unsent comment"}})
	if !strings.Contains(body, "Jira refused &lt;comment&gt;") || strings.Contains(body, "Kommentar hinzugefügt") {
		t.Fatalf("failed write response: %s", body)
	}
}

func TestPrioTicketRejectsWriteOutsideProjectedDCAI(t *testing.T) {
	client := &jira.FakeClient{Issues: []jira.Issue{{Key: "OTHER-1", Type: "Task", Summary: "Other project", Status: "Triage"}}}
	app := newWritablePrioApp(t, client)
	for _, key := range []string{"OTHER-1", "DCAI-42"} {
		response, err := http.PostForm(app.URL+"/prio/ticket", url.Values{"key": {key}, "action": {"comment"}, "comment": {"Out of scope"}})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("write to %s returned %d", key, response.StatusCode)
		}
	}
}
