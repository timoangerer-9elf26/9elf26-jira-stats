package web_test

// Integration tests for the editable estimate pill on the Daily board (#115,
// docs/adr/0005 amended): the Daily card's estimate pill is a second editable
// surface that reuses the Board's write path (POST /board/estimate) — write to
// Jira → re-read → show the authoritative value, or revert + inline error on
// failure. Only the surface differs; there is no new write path. The Sprint
// drill-down stays read-only (covered in board_estimate_test.go).

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// dailyEstimateApp wires the Daily board fixture (one in-window active-sprint
// ticket, DCAI-40, sized M) with a real Estimator and a clock pinned so the
// Today preset includes the ticket. Returns the app and the backing fake so a
// test can arm a write failure.
func dailyEstimateApp(t *testing.T) (*testApp, *jira.FakeClient) {
	t.Helper()
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := time.Date(2026, time.July, 16, 8, 0, 0, 0, loc)
	fake := &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			dailyIssue("DCAI-40", "Task", "alice", true, "Ready to Do", "In Progress", at),
		},
	}
	app := newEstimateApp(t, fake,
		web.WithClock(func() time.Time { return now }),
		web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	return app, fake
}

// TestDailyEstimatePillIsEditable asserts the Daily card's estimate pill is the
// interactive popover (S / M / L / No estimate) posting to the existing
// /board/estimate, while still showing the card's current value.
func TestDailyEstimatePillIsEditable(t *testing.T) {
	app, _ := dailyEstimateApp(t)
	body := get(t, app.URL+"/daily/results?assignee=all&preset=today")

	for _, want := range []string{
		`data-testid="card:DCAI-40:estimate"`,
		`hx-post="/board/estimate"`,
		`data-testid="card:DCAI-40:estimate-opt:s"`,
		`data-testid="card:DCAI-40:estimate-opt:m"`,
		`data-testid="card:DCAI-40:estimate-opt:l"`,
		`data-testid="card:DCAI-40:estimate-opt:none"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Daily estimate control missing %q\n%s", want, body)
		}
	}
	// The current value is still shown in the pill (read state preserved).
	if !strings.Contains(body, `data-testid="card:DCAI-40:size">M<`) {
		t.Errorf("Daily pill lost its current value M\n%s", body)
	}
}

// TestDailyEstimateWriteReflectsOnDailyBoard asserts a choice PUTs the value to
// Jira and a subsequent Daily load shows the authoritative re-read value — the
// same write→refetch→persist path as the Board, surfaced on Daily.
func TestDailyEstimateWriteReflectsOnDailyBoard(t *testing.T) {
	app, _ := dailyEstimateApp(t)

	code, body := postForm(t, app.URL+"/board/estimate",
		url.Values{"key": {"DCAI-40"}, "size": {"L"}, "prior": {"M"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/estimate: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-40:size">L<`) {
		t.Errorf("write response did not show the authoritative value L\n%s", body)
	}
	if strings.Contains(body, `data-testid="card:DCAI-40:estimate-error"`) {
		t.Errorf("successful write must not render an inline error\n%s", body)
	}

	// A fresh Daily load reflects the persisted size (the pill is now L).
	if reload := get(t, app.URL+"/daily/results?assignee=all&preset=today"); !strings.Contains(reload, `data-testid="card:DCAI-40:size">L<`) {
		t.Errorf("Daily board did not reflect the persisted size L\n%s", reload)
	}
}

// TestDailyEstimateFailureRevertsWithInlineError asserts a failed Jira write from
// the Daily surface reverts the pill to its prior value and shows the inline
// error, leaving the Daily projection unchanged.
func TestDailyEstimateFailureRevertsWithInlineError(t *testing.T) {
	app, fake := dailyEstimateApp(t)
	fake.WriteErr = errors.New("jira says no (permissions)")

	code, body := postForm(t, app.URL+"/board/estimate",
		url.Values{"key": {"DCAI-40"}, "size": {"L"}, "prior": {"M"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/estimate: status %d, want 200", code)
	}
	// Pill reverts to the prior value (M), not the attempted L.
	if !strings.Contains(body, `data-testid="card:DCAI-40:size">M<`) {
		t.Errorf("failed write did not revert the pill to its prior value M\n%s", body)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-40:estimate-error"`) {
		t.Errorf("failed write did not render an inline error\n%s", body)
	}

	// The projection is untouched: a fresh Daily load still shows M.
	if reload := get(t, app.URL+"/daily/results?assignee=all&preset=today"); !strings.Contains(reload, `data-testid="card:DCAI-40:size">M<`) {
		t.Errorf("failed write leaked into the Daily projection (should still be M)\n%s", reload)
	}
}
