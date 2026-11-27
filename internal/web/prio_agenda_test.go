package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

func TestPrioAgendaOpensAtOverviewWithBerlinFallback(t *testing.T) {
	now := time.Date(2026, time.November, 27, 16, 45, 0, 0, time.UTC)
	app := newTestAppWith(t, prioFixture(), now)
	body := get(t, app.URL+"/prio")

	assertOrder(t, body, "0 · Übersicht", "2 · Fertig", "3 · Neu", "4 · Backlog")
	if strings.Contains(body, `data-prio-step="epic-check"`) {
		t.Error("the agenda should skip an empty Epic-Prüfung")
	}
	if !strings.Contains(body, `step=done" data-prio-next>Zur Besprechung`) {
		t.Error("the overview should advance directly to Fertig")
	}
	for _, want := range []string{`value="2026-11-20T17:45"`, "Vor sieben Tagen", "Seit letztem Prio-Meeting", "Alle Tickets", "Meeting abschließen"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}

	empty := get(t, app.URL+"/prio?step=epic-check")
	if !strings.Contains(empty, "Keine Epics zur Prüfung") {
		t.Error("Epic-Prüfung needs an explicit empty state")
	}
}

func TestPrioMeetingCompletionDeduplicatesWindowAndSurvivesReset(t *testing.T) {
	now := time.Date(2026, time.November, 27, 16, 45, 31, 0, time.UTC)
	fixture := prioFixture()
	app := newTestAppWith(t, fixture, now, web.WithFakeJira(fixture))

	page := get(t, app.URL+"/prio?cutoff=2026-11-23T11:30")
	assertOrder(t, page, "Zusammenfassung", "Abschließen und Stichtag setzen")
	if !strings.Contains(page, `name="meeting-window" value="0"`) {
		t.Fatal("the first meeting must use a common completion window")
	}

	form := url.Values{"meeting-window": {"0"}, "cutoff": {"2026-11-23T11:30"}}
	codes := make(chan int, 2)
	for range 2 {
		go func() {
			resp, err := noRedirectClient().PostForm(app.URL+"/prio/meeting/complete", form)
			if err != nil {
				codes <- 0
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	for range 2 {
		if code := <-codes; code != http.StatusSeeOther {
			t.Errorf("meeting completion status = %d, want 303", code)
		}
	}

	if err := app.Store.Reset(); err != nil {
		t.Fatal(err)
	}
	page = get(t, app.URL+"/prio")
	for _, want := range []string{`datetime="2026-11-27T16:45:31Z"`, `value="2026-11-27T17:45"`, `name="meeting-window" value="1"`, "jira-stats-fake"} {
		if !strings.Contains(page, want) {
			t.Errorf("the one completed meeting is missing %q after reset", want)
		}
	}
}

func TestPrioMeetingCompletionRequiresJiraIdentity(t *testing.T) {
	app := newTestApp(t, prioFixture())
	code, _ := postForm(t, app.URL+"/prio/meeting/complete", url.Values{"meeting-window": {"0"}})
	if code != http.StatusUnauthorized {
		t.Errorf("meeting completion without an identity = %d, want 401", code)
	}
}

func TestPrioAgendaQueuesUseProjectHistoryAndCurrentBacklog(t *testing.T) {
	cutoff := time.Date(2026, time.November, 20, 10, 0, 0, 0, time.UTC)
	after := cutoff.Add(time.Hour)
	before := cutoff.Add(-time.Hour)
	enterDone := jira.ChangelogEntry{ID: "enter", Field: "status", From: "In Progress", To: "Ready for Release", Timestamp: after}
	fixture := &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Summary: "Reopened epic", Status: "In Progress", CreatedAt: before, Changelog: []jira.ChangelogEntry{
			enterDone, {ID: "reopen", Field: "status", From: "Ready for Release", To: "In Progress", Timestamp: after.Add(time.Minute)},
		}},
		{Key: "DCAI-2", Type: "Epic", Status: "Released / Deployed", Changelog: []jira.ChangelogEntry{
			{ID: "done-internal", Field: "status", From: "Ready for Release", To: "Released / Deployed", Timestamp: after},
		}},
		{Key: "DCAI-3", Type: "Epic", Status: "Ready for Release", Changelog: []jira.ChangelogEntry{
			{ID: "canceled", Field: "status", From: "Canceled", To: "Ready for Release", Timestamp: after},
		}},
		{Key: "DCAI-4", Type: "Task", Status: "Ready for Release", Changelog: []jira.ChangelogEntry{
			{ID: "task-done", Field: "status", From: "In Progress", To: "Ready for Release", Timestamp: after},
		}},
		{Key: "DCAI-5", Type: "Task", Status: "Triage", CreatedAt: after, Priority: "High"},
		{Key: "DCAI-6", Type: "Task", Status: "Ready To Do", CreatedAt: before, Priority: "Low"},
		{Key: "DCAI-7", Type: "Task", Status: "Canceled", CreatedAt: before.Add(-time.Hour), Priority: "Highest"},
	}}
	app := newTestApp(t, fixture)
	base := app.URL + "/prio?cutoff=2026-11-20T11:00&preset=all&step="
	for _, tc := range []struct {
		step string
		want []string
	}{
		{"done", []string{"DCAI-1"}},
		{"new", []string{"DCAI-5"}},
		{"backlog", []string{"DCAI-5", "DCAI-6"}},
		{"backlog&backlog-sort=age", []string{"DCAI-6", "DCAI-5"}},
	} {
		t.Run(tc.step, func(t *testing.T) {
			page := get(t, base+tc.step)
			if n := strings.Count(page, "data-prio-queue-key="); n != len(tc.want) {
				t.Fatalf("queue has %d tickets, want %v", n, tc.want)
			}
			for i, key := range tc.want {
				tc.want[i] = `data-prio-queue-key="` + key + `"`
			}
			assertOrder(t, page, tc.want...)
		})
	}
}

func TestPrioAgendaOverviewGroupsReopenedTicketsAndKeepsHistory(t *testing.T) {
	cutoff := time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC) // First 02:30 in Berlin.
	summer := cutoff.Add(15 * time.Minute)
	winter := cutoff.Add(time.Hour)
	fixture := &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Task", Status: "Triage"},
		{Key: "DCAI-2", Type: "Epic", Status: "In Progress", Summary: "Reopened feature", CreatedAt: summer, Changelog: []jira.ChangelogEntry{
			{ID: "1", Field: "status", From: "In Progress", To: "Ready for Release", Timestamp: summer},
			{ID: "2", Field: "resolution", From: "", To: "Done", Timestamp: summer},
			{ID: "3", Field: "priority", From: "Medium", To: "High", Timestamp: summer},
			{ID: "4", Field: "priority", From: "High", To: "Medium", Timestamp: winter, Author: jira.Person{DisplayName: "Lea Hoffmann"}},
			{ID: "5", Field: "status", From: "Ready for Release", To: "In Progress", Timestamp: winter},
			{ID: "6", Field: "resolution", From: "Done", To: "", Timestamp: winter},
			{ID: "7", Field: "priority", From: "Lowest", To: "Low", Timestamp: cutoff},
		}},
		{Key: "DCAI-3", Type: "Task", Status: "Ready for Release", ResolvedAt: summer},
		{Key: "DCAI-4", Type: "Task", Status: "Canceled"},
	}}
	app := newTestApp(t, fixture)
	page := get(t, app.URL+"/prio?"+url.Values{
		"cutoff": {cutoff.Format(time.RFC3339)}, "ticket": {"DCAI-2"}, "preset": {"all"},
	}.Encode())

	assertOrder(t, page,
		`data-prio-group="planned"`, `data-key="DCAI-1"`,
		`data-prio-group="doing"`, `data-key="DCAI-2"`,
		`data-prio-group="done"`, `data-key="DCAI-3"`,
		`data-prio-group="other"`, `data-key="DCAI-4"`,
	)
	for _, want := range []string{
		`value="2026-10-25T02:30"`, "Neu", "Status geändert", "Aufgelöst",
		"↑ Prio geändert · Mittel → Hoch", "↓ Prio geändert · Hoch → Mittel",
		"Änderungsverlauf seit Stichtag", "Lea Hoffmann", "25.10.2026 02:30",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	if strings.Contains(page, "Niedrigste → Niedrig") {
		t.Error("change at the cutoff should be excluded")
	}
}
