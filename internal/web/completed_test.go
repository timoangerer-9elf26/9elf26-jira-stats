package web_test

// Integration tests for the "Completed" view over the HTTP seam.
//
// Fixtures are built as status transitions (via jira.FakeClient) crossing into
// the Done category at known Berlin-local instants; the tests drive the real
// handlers and assert on rendered HTML. A fixed clock is injected so the presets
// (This week / Last week / Active sprint / Last 2 weeks) resolve deterministic
// ranges.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// newTestAppAt is like newTestApp but pins the server clock, so preset ranges
// (which are relative to "now") are deterministic.
func newTestAppAt(t *testing.T, client jira.Client, now time.Time) *testApp {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := sync.Once(context.Background(), client, st); err != nil {
		t.Fatalf("sync: %v", err)
	}
	srv, err := web.NewServer(st, web.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}
	return loc
}

// completedIssue builds a Task/Bug/Story that crossed into Done at `at`.
func completedIssue(key, size string, at time.Time) jira.Issue {
	return jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: "DONE (This Sprint)",
		StatusCategory: "Done", Size: size,
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: "In Progress", To: "DONE (This Sprint)", Timestamp: at},
		},
	}
}

func TestCompletedPageRendersStandaloneWithPicker(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/completed")
	if !strings.Contains(body, "<!DOCTYPE") || !strings.Contains(body, "<html") {
		t.Fatalf("/completed must render a full standalone page:\n%s", body)
	}
	// The reusable date-range picker and its presets are present.
	if !strings.Contains(body, `data-testid="date-range-picker"`) {
		t.Fatalf("completed page missing date-range picker:\n%s", body)
	}
	for _, preset := range []string{"this-week", "last-week", "active-sprint", "last-2-weeks"} {
		if !strings.Contains(body, "preset="+preset) {
			t.Errorf("picker missing preset %q", preset)
		}
	}
	// Results are embedded on first load (default preset = active sprint).
	if !strings.Contains(body, `data-testid="completed-results"`) {
		t.Fatalf("completed page missing results fragment:\n%s", body)
	}
}

func TestCompletedResultsFragmentCustomRange(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "S", time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)),
		completedIssue("DCAI-2", "L", time.Date(2026, time.July, 16, 9, 0, 0, 0, loc)),
		completedIssue("DCAI-3", "", time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)),
		// Outside the custom window.
		completedIssue("DCAI-4", "M", time.Date(2026, time.July, 1, 9, 0, 0, 0, loc)),
	}}, now)

	// Custom range covering 2026-07-14 .. 2026-07-18 (inclusive-exclusive).
	body := get(t, app.URL+"/completed/results?preset=custom&from=2026-07-14&to=2026-07-18")
	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Fatalf("results endpoint must return a partial, got full document:\n%s", body)
	}
	wants := []string{
		`data-testid="completed:s">1<`,
		`data-testid="completed:m">0<`,
		`data-testid="completed:l">1<`,
		`data-testid="completed:none">1<`,
		`data-testid="completed:points">4<`, // S(1) + L(3)
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("custom-range fragment missing %q\n%s", w, body)
		}
	}
}

func TestCompletedResultsThisWeekPresetDrivesQuery(t *testing.T) {
	loc := berlin(t)
	// "now" is Wed 2026-07-15; this ISO week is Mon 07-13 .. Mon 07-20.
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)), // this week
		completedIssue("DCAI-2", "L", time.Date(2026, time.July, 9, 9, 0, 0, 0, loc)),  // last week
	}}, now)

	thisWeek := get(t, app.URL+"/completed/results?preset=this-week")
	if !strings.Contains(thisWeek, `data-testid="completed:m">1<`) ||
		!strings.Contains(thisWeek, `data-testid="completed:l">0<`) ||
		!strings.Contains(thisWeek, `data-testid="completed:points">2<`) {
		t.Errorf("this-week preset wrong tally:\n%s", thisWeek)
	}

	lastWeek := get(t, app.URL+"/completed/results?preset=last-week")
	if !strings.Contains(lastWeek, `data-testid="completed:l">1<`) ||
		!strings.Contains(lastWeek, `data-testid="completed:m">0<`) ||
		!strings.Contains(lastWeek, `data-testid="completed:points">3<`) {
		t.Errorf("last-week preset wrong tally:\n%s", lastWeek)
	}
}

// TestCompletedActiveSprintPresetUsesStoredWindow asserts the "Active sprint"
// preset resolves to the real sprint window captured during sync
// (active_sprint_start..active_sprint_end), not the current ISO week. The clock
// is pinned to a week OUTSIDE the sprint, so a current-week fallback would give
// the opposite answer: the in-window completion counts and the out-of-window one
// does not, and the range label reflects the stored sprint window.
func TestCompletedActiveSprintPresetUsesStoredWindow(t *testing.T) {
	loc := berlin(t)
	// "now" is two weeks after the sprint, so a current-week fallback would NOT
	// include the in-window completion (and WOULD include the out-of-window one).
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, loc)

	sprintStart := time.Date(2026, time.July, 13, 0, 0, 0, 0, time.UTC)
	sprintEnd := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)

	// This issue both defines the active sprint window (captured in meta) and is a
	// completion inside it.
	inWindow := completedIssue("DCAI-1", "M", time.Date(2026, time.July, 15, 9, 0, 0, 0, loc))
	inWindow.ActiveSprint = "KW29"
	inWindow.ActiveSprintStart = sprintStart
	inWindow.ActiveSprintEnd = sprintEnd
	// A completion just after the sprint end — outside the window.
	outWindow := completedIssue("DCAI-2", "L", time.Date(2026, time.July, 21, 9, 0, 0, 0, loc))

	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{inWindow, outWindow}}, now)

	body := get(t, app.URL+"/completed/results?preset=active-sprint")
	wants := []string{
		`data-testid="completed:m">1<`, // in-window completion counts
		`data-testid="completed:l">0<`, // out-of-window completion excluded
		`data-testid="completed:points">2<`,
		`data-testid="completed-range">13 Jul – 19 Jul 2026<`, // the stored sprint window
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("active-sprint preset did not use the stored window; missing %q\n%s", w, body)
		}
	}
}

func TestCompletedPageReachableAndWiredForHTMX(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, jira.NewFakeClient(), now)

	body := get(t, app.URL+"/completed")
	if !strings.Contains(body, `hx-get="/completed/results`) {
		t.Errorf("picker not wired to swap results via HTMX:\n%s", body)
	}
	if !strings.Contains(body, `hx-target="#completed-results"`) {
		t.Errorf("picker missing hx-target for results fragment:\n%s", body)
	}
}
