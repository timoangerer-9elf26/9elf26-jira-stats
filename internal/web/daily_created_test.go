package web_test

// Integration tests for the Daily view's "tickets I created" section (#44) over
// the HTTP seam. The section lists tickets whose immutable Jira Creator matches
// the configured "me" (NOT the selected assignee), created within the window,
// NOT limited to the active sprint; its count feeds the digest headline
// alongside the movement count.

import (
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// createdIssue builds an issue authored by `creator` at `createdAt`, in the
// active sprint only when active is true, with no status transitions.
func createdIssue(key, typ, creator string, active bool, createdAt time.Time) jira.Issue {
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: "Ready to Do",
		StatusCategory: "To Do", Size: "M", Assignee: creator,
		Creator: creator, CreatedAt: createdAt,
	}
	if active {
		iss.ActiveSprint = "KW29"
	}
	return iss
}

// dailyCreatedFixture pins now = Thu 2026-07-16 10:00 Berlin, so the default
// Today preset window is [2026-07-16 00:00, 2026-07-17 00:00).
func dailyCreatedFixture(t *testing.T) (*jira.FakeClient, time.Time) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		// Created by Ada in window, in the active sprint.
		createdIssue("DCAI-1", "Story", "Ada", true, at(16, 8)),
		// Created by Ada in window, NOT in the active sprint — must still count.
		createdIssue("DCAI-2", "Task", "Ada", false, at(16, 7)),
		// Created by Grace in window — excluded when me is Ada.
		createdIssue("DCAI-3", "Bug", "Grace", true, at(16, 9)),
		// Created by Ada BEFORE the window — excluded.
		createdIssue("DCAI-4", "Task", "Ada", true, at(13, 9)),
	}}, now
}

// TestDailyCreatedSectionScopedAndNotSprintLimited: the section lists me's
// in-window created tickets regardless of active-sprint membership, and excludes
// other creators and out-of-window tickets.
func TestDailyCreatedSectionScopedAndNotSprintLimited(t *testing.T) {
	client, now := dailyCreatedFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("Ada"))

	body := get(t, app.URL+"/daily") // defaults to Ada + Last 24h

	if !strings.Contains(body, `data-testid="daily-created"`) {
		t.Fatalf("created section should render:\n%s", body)
	}
	// Ada's two in-window tickets appear — including DCAI-2, which is NOT in the
	// active sprint (the section is not sprint-scoped).
	for _, key := range []string{"DCAI-1", "DCAI-2"} {
		if !strings.Contains(body, `data-testid="created-ticket:`+key+`"`) {
			t.Errorf("created section should include %s:\n%s", key, body)
		}
	}
	// Grace's ticket and Ada's out-of-window ticket must not appear.
	for _, key := range []string{"DCAI-3", "DCAI-4"} {
		if strings.Contains(body, `data-testid="created-ticket:`+key+`"`) {
			t.Errorf("created section should NOT include %s", key)
		}
	}
	// The count shows in the section header.
	if !strings.Contains(body, "Tickets I created (2)") {
		t.Errorf("created section header count wrong:\n%s", body)
	}
}

// TestDailyCreatedPinnedToMeNotSelectedAssignee: the section stays on the
// configured me even when the dropdown re-scopes the movement digest to a
// teammate (AC1: "Creator display name == configured me").
func TestDailyCreatedPinnedToMeNotSelectedAssignee(t *testing.T) {
	client, now := dailyCreatedFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("Ada"))

	// Explicitly view Grace's Daily; the created section must still list Ada's
	// authored tickets, not Grace's.
	body := get(t, app.URL+"/daily/results?assignee=Grace&preset=today")

	for _, key := range []string{"DCAI-1", "DCAI-2"} {
		if !strings.Contains(body, `data-testid="created-ticket:`+key+`"`) {
			t.Errorf("created section should stay on me (Ada), include %s:\n%s", key, body)
		}
	}
	if strings.Contains(body, `data-testid="created-ticket:DCAI-3"`) {
		t.Errorf("created section must not switch to Grace's DCAI-3 when Grace is selected")
	}
}

// TestDailyCreatedCountFeedsDigestHeadline: the created count is reported in the
// digest headline alongside the movement count.
func TestDailyCreatedCountFeedsDigestHeadline(t *testing.T) {
	client, now := dailyCreatedFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("Ada"))

	// No movements in this fixture, so the headline is created-only.
	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-testid="daily-digest-headline"`) {
		t.Fatalf("digest should be present when tickets were created:\n%s", body)
	}
	if !strings.Contains(body, "created 2") {
		t.Errorf("headline should report the created count:\n%s", body)
	}
}

// TestDailyCreatedEmptyWhenMeAuthoredNothing: a configured me who created nothing
// in the window shows the section's empty state and a zero count.
func TestDailyCreatedEmptyWhenMeAuthoredNothing(t *testing.T) {
	client, now := dailyCreatedFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("Nobody"))

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-testid="daily-created-empty"`) {
		t.Errorf("empty created section should show its empty state:\n%s", body)
	}
	if !strings.Contains(body, "Tickets I created (0)") {
		t.Errorf("empty created section should show a zero count:\n%s", body)
	}
}

// TestDailyCreatedEmptyWhenNoMeConfigured: with no me configured the section is
// empty (not everyone's tickets) and the page does not crash (AC6).
func TestDailyCreatedEmptyWhenNoMeConfigured(t *testing.T) {
	client, now := dailyCreatedFixture(t)
	app := newTestAppAt(t, client, now) // no WithMe

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-testid="daily-created-empty"`) {
		t.Errorf("with no me configured the created section should be empty:\n%s", body)
	}
	if !strings.Contains(body, "Tickets I created (0)") {
		t.Errorf("with no me configured the count should be zero:\n%s", body)
	}
}
