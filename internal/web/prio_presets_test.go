package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/auth"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

func TestPrioOverviewFilterSwapReturnsReadyJiraResults(t *testing.T) {
	fixture := &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Status: "Triage"},
	}}
	app := newTestAppWith(t, fixture, time.Now(), web.WithFakeJira(agendaFilterJira{fixture}))
	address := app.URL + "/prio?preset=42"
	if page := get(t, address); !strings.Contains(page, "data-prio-preset-loading") {
		t.Fatal("a cold standalone page must still render progressively")
	}

	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Boosted", "true")
	req.Header.Set("X-Prio-Agenda-Revision", "2")
	req.Header.Set("HX-Trigger", "prio-filter-controls")
	resp := response(t)(app.Client().Do(req))
	page := readResponse(t, resp, http.StatusOK)
	if !strings.Contains(page, `data-key="DCAI-1"`) || strings.Contains(page, "data-prio-preset-loading") {
		t.Fatal("a filter swap must already contain its Jira results")
	}
	if got := resp.Header.Get("HX-Push-Url"); got != "/prio?preset=42&step=overview" {
		t.Errorf("push URL = %q", got)
	}
}

func TestPrioOverviewRunsTheSelectedPresetAndRendersLocalRows(t *testing.T) {
	const saved = `project = DCAI AND (status CHANGED AFTER "2026-11-20 00:00" OR priority CHANGED after '2026-11-20 00:00') AND status CHANGED BEFORE "2026-11-20 00:00" AND priority CHANGED AFTER "2026-11-20 00:01" AND status CHANGED AFTER startOfDay() AND summary ~ "AFTER \"2026-11-20 00:00\"" ORDER BY rank ASC`
	const unmarked = `project = DCAI AND status CHANGED AFTER "2026-11-20 00:00"`
	var searches, discoveries atomic.Int32
	jiraServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/filter/search":
			discoveries.Add(1)
			writePlanningFilters(w,
				jira.PlanningPreset{ID: "42", Name: "Planning Since Meeting", JQL: saved, Description: `stichtag: "2026-11-20 00:00"`},
				jira.PlanningPreset{ID: "22", Name: "Planning Epics", JQL: unmarked},
				jira.PlanningPreset{ID: "23", Name: "Planning Broken", JQL: `project = DCAI AND status CHANGED BEFORE "2026-11-20 00:00"`, Description: `stichtag: "2026-11-20 00:00"`})
		case "/rest/api/3/search/jql":
			n := searches.Add(1)
			want := unmarked
			if n <= 2 {
				date := "2026-03-08 01:59"
				if n == 2 {
					date = "2026-03-08 03:00"
				}
				want = fmt.Sprintf(`project = DCAI AND (status CHANGED AFTER "%s" OR priority CHANGED after '%s') AND status CHANGED BEFORE "2026-11-20 00:00" AND priority CHANGED AFTER "2026-11-20 00:01" AND status CHANGED AFTER startOfDay() AND summary ~ "AFTER \"2026-11-20 00:00\"" ORDER BY rank ASC`, date, date)
			}
			if q := r.URL.Query().Get("jql"); q != want {
				t.Errorf("query %d = %s, want %s", n, q, want)
			}
			if n == 4 {
				http.Error(w, "offline", http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"issues":[{"key":"DCAI-2"},{"key":"DCAI-404"},{"key":"DCAI-1"},{"key":"DCAI-4"},{"key":"OTHER-12"}]}`)
		default:
			t.Errorf("unexpected Jira request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer jiraServer.Close()

	fixture := &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Status: "Triage", Priority: "Highest", Summary: "Stored first"},
		{Key: "DCAI-2", Type: "Task", Status: "Triage", Priority: "Low", Summary: "Stored second", Changelog: []jira.ChangelogEntry{
			{ID: "status-2", Field: "status", From: "Refinement", To: "Triage", Timestamp: time.Date(2026, 3, 7, 18, 0, 0, 0, time.UTC)},
		}},
		{Key: "DCAI-3", Type: "Task", Status: "Triage", Priority: "High", Summary: "Local only"},
		{Key: "DCAI-4", Type: "Task", Status: "Triage", Priority: "High", Summary: "Recently changed", Changelog: []jira.ChangelogEntry{
			{ID: "status-4", Field: "status", From: "Refinement", To: "Triage", Timestamp: time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC)},
		}},
	}}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	client := jira.NewBearerClient(jira.Config{BaseURL: jiraServer.URL}, "personal", jiraServer.Client())
	app := newTestAppWith(t, fixture, now, web.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }), web.WithLocation(location), web.WithJiraBaseURL(jiraServer.URL), web.WithFakeJira(client))

	const first = "cutoff=2026-03-08T07:59&ticket=DCAI-2&level=all"
	page := get(t, app.URL+"/prio/preset-results?"+first+"&preset=42")
	assertOrder(t, page, "Keiner", `<option value="42"`, "Planning Epics")
	assertOrder(t, page, `data-key="DCAI-2"`, `data-key="DCAI-1"`, `data-key="DCAI-4"`)
	for _, want := range []string{"DCAI-404", "/browse/DCAI-404", "synchronisiert", "Jira-Reihenfolge", "prio-drawer"} {
		if !strings.Contains(page, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	_, searchURL, ok := strings.Cut(page, jiraServer.URL+"/issues/?jql=")
	jql, _, _ := strings.Cut(searchURL, `"`)
	decoded, err := url.QueryUnescape(html.UnescapeString(jql))
	if !ok || err != nil || !strings.Contains(decoded, `AFTER "2026-03-08 01:59"`) {
		t.Fatalf("Jira link does not use the effective preset JQL: %q (%v)", decoded, err)
	}
	if strings.Contains(page, `data-key="DCAI-3"`) || strings.Contains(page, "OTHER-12") {
		t.Fatal("overview included a ticket outside the DCAI preset results")
	}
	page = get(t, app.URL+"/prio/results?"+first+"&preset=42&sort=priority")
	assertOrder(t, page, `data-key="DCAI-1"`, `data-key="DCAI-4"`, `data-key="DCAI-2"`)
	page = get(t, app.URL+"/prio/results?"+first+"&preset=42&sort=recent")
	assertOrder(t, page, `data-key="DCAI-4"`, `data-key="DCAI-2"`, `data-key="DCAI-1"`)
	get(t, app.URL+"/prio/preset-results?cutoff=2026-03-08T08:00&preset=42")
	get(t, app.URL+"/prio/preset-results?"+first+"&preset=22")
	get(t, app.URL+"/prio/preset-results?cutoff=2026-03-09T08:00&preset=22")
	page = get(t, app.URL+"/prio/preset-results?"+first+"&preset=23")
	if !strings.Contains(page, "Konfigurationsfehler") || !strings.Contains(page, "preset=all&amp;step=overview&amp;ticket=DCAI-2") {
		t.Errorf("invalid opt-in needs a visible error and a fallback preserving the selection")
	}
	get(t, app.URL+"/prio?step=backlog&preset=23")
	if searches.Load() != 3 || discoveries.Load() != 1 {
		t.Fatalf("before expiry Jira discovery/search calls = %d/%d, want 1/3", discoveries.Load(), searches.Load())
	}

	clock.Store(now.Add(45 * time.Second).UnixNano())
	page = get(t, app.URL+"/prio/preset-results?"+first+"&preset=22")
	if !strings.Contains(page, "Jira-Abfrage fehlgeschlagen") || !strings.Contains(page, "preset=all&amp;step=overview&amp;ticket=DCAI-2") {
		t.Error("failed Jira search needs a visible local fallback preserving selection")
	}
	page = get(t, app.URL+"/prio?"+first+"&preset=all")
	if !strings.Contains(page, `data-key="DCAI-3"`) || !strings.Contains(page, "prio-drawer") {
		t.Error("local fallback needs the local project and preserved panel selection")
	}
	if searches.Load() != 4 || discoveries.Load() != 2 {
		t.Errorf("after expiry Jira discovery/search calls = %d/%d, want 2/4", discoveries.Load(), searches.Load())
	}
}

func writePlanningFilters(w http.ResponseWriter, filters ...jira.PlanningPreset) {
	type filter struct {
		jira.PlanningPreset
		Shares json.RawMessage `json:"sharePermissions"`
	}
	rows := make([]filter, 0, len(filters))
	for _, preset := range filters {
		rows = append(rows, filter{preset, json.RawMessage(`[{"type":"project","project":{"id":"10004"}}]`)})
	}
	json.NewEncoder(w).Encode(map[string]any{"isLast": true, "values": rows})
}

func TestPrioPresetCachesAreIsolatedByJiraAccount(t *testing.T) {
	var logins, discoveries, searches atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := r.Header.Get("Authorization")
		person := strings.TrimPrefix(bearer, "Bearer ")
		if r.URL.Path != "/oauth/token" && person != "person-1" && person != "person-2" {
			t.Errorf("request %s used unexpected credentials %q", r.URL.Path, bearer)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/oauth/token":
			n := logins.Add(1)
			fmt.Fprintf(w, `{"access_token":"person-%d","refresh_token":"refresh-%d","expires_in":3600}`, n, n)
		case "/oauth/token/accessible-resources":
			fmt.Fprint(w, validResources)
		case myPermissionsPath:
			writeProjectPermission(t, w, r, validProjectPermission)
		case "/ex/jira/cloud-1/rest/api/3/myself":
			fmt.Fprintf(w, `{"accountId":%q,"timeZone":"Europe/Berlin","active":true}`, person)
		case "/ex/jira/cloud-1/rest/api/3/filter/search":
			discoveries.Add(1)
			writePlanningFilters(w,
				jira.PlanningPreset{ID: "42", Name: "Planning Since Meeting", JQL: "project = DCAI"},
				jira.PlanningPreset{ID: "99", Name: "Planning " + person})
		case "/ex/jira/cloud-1/rest/api/3/search/jql":
			searches.Add(1)
			fmt.Fprintf(w, `{"issues":[{"key":"DCAI-90%s"}]}`, strings.TrimPrefix(person, "person-"))
		default:
			t.Errorf("unexpected Jira request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	app := newOAuthApp(t, provider, func(cfg *auth.Config) { cfg.AllowedAccountIDs = "person-1,person-2" })
	first, second := signIn(t, app), signIn(t, app)
	for _, tc := range []struct {
		session    *http.Cookie
		own, other string
	}{{first, "1", "2"}, {second, "2", "1"}, {first, "1", "2"}, {second, "2", "1"}} {
		req, err := http.NewRequest(http.MethodGet, app.URL+"/prio/preset-results?preset=42", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(tc.session)
		body := readResponse(t, response(t)(oauthClient(app).Do(req)), http.StatusOK)
		for _, pair := range [][2]string{{"DCAI-90" + tc.own, "DCAI-90" + tc.other}, {"Planning person-" + tc.own, "Planning person-" + tc.other}} {
			if !strings.Contains(body, pair[0]) || strings.Contains(body, pair[1]) {
				t.Errorf("account %s should see %q and should never see %q", tc.own, pair[0], pair[1])
			}
		}
	}
	if discoveries.Load() != 2 || searches.Load() != 2 {
		t.Errorf("discovery/search calls = %d/%d, want one of each per account", discoveries.Load(), searches.Load())
	}
}

type delayedPresetJira struct {
	casePlanningJira
	started, release chan struct{}
	searches         atomic.Int32
}

func (c *delayedPresetJira) SearchIssueKeys(ctx context.Context, jql string) ([]string, error) {
	keys, err := c.casePlanningJira.SearchIssueKeys(ctx, jql)
	if c.searches.Add(1) == 1 {
		close(c.started)
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return keys, err
}

func TestPrioInlinePrioritySupersedesInflightPresetSearch(t *testing.T) {
	client := &delayedPresetJira{
		casePlanningJira: casePlanningJira{&jira.FakeClient{Issues: []jira.Issue{{
			Key: "DCAI-41", Type: "Task", Status: "Triage", Priority: "High",
		}}}},
		started: make(chan struct{}), release: make(chan struct{}),
	}
	app := newWritablePrioApp(t, client)
	preset := app.URL + "/prio/preset-results?step=overview&preset=42"
	firstDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(preset)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-client.started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial preset search did not start")
	}
	code, _ := postForm(t, app.URL+"/prio/priority", url.Values{"key": {"DCAI-41"}, "priority": {"Medium"}})
	close(client.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Fatalf("inline priority route returned %d", code)
	}
	if page := get(t, preset); strings.Contains(page, `data-key="DCAI-41"`) {
		t.Fatal("pre-write search results replaced the post-write preset membership")
	}
}
