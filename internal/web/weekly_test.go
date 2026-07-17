package web_test

// Integration tests for the "Weekly" sprint-planning view over the HTTP seam.
//
// Fixtures are built as status transitions (via jira.FakeClient) crossing into
// the Done set at known Berlin-local instants, scoped to the active sprint; the
// tests drive the real handlers and assert on rendered HTML. A fixed clock is
// injected so the window modes (Work week / Live sprint) resolve deterministic
// bounds.

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

// newTestAppAt is like newTestApp but pins the server clock, so window bounds
// (which are relative to "now") are deterministic. Shared by the Weekly, Daily
// and Velocity suites.
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

// completedIssue builds a Task/Bug/Story that crossed into Done at `at`, with no
// sprint membership (whole-project completion). Used by the Velocity suite.
func completedIssue(key, size string, at time.Time) jira.Issue {
	return jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: "DONE (This Sprint)",
		StatusCategory: "Done", Size: size,
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: "In Progress", To: "DONE (This Sprint)", Timestamp: at},
		},
	}
}

// finishedIssue builds an ACTIVE-SPRINT (KW29) Task/Bug/Story that crossed into
// the given Done status at `at` — the shape the Weekly "Finished this week" tally
// counts. toStatus lets a test exercise the Ready-for-Release crossing too.
func finishedIssue(key, size, toStatus string, at time.Time) jira.Issue {
	return jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: toStatus,
		StatusCategory: "Done", Size: size, ActiveSprint: "KW29",
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: "In Progress", To: toStatus, Timestamp: at},
		},
	}
}

// TestWeeklyPageRendersStandaloneWithWindowSelector asserts /weekly renders a
// full standalone page carrying the two-mode window selector, with Work week the
// default selection, and the results embedded on first load.
func TestWeeklyPageRendersStandaloneWithWindowSelector(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues:  []jira.Issue{finishedIssue("DCAI-1", "M", "DONE (This Sprint)", time.Date(2026, time.July, 15, 9, 0, 0, 0, loc))},
	}, now)

	body := get(t, app.URL+"/weekly")
	if !strings.Contains(body, "<!DOCTYPE") || !strings.Contains(body, "<html") {
		t.Fatalf("/weekly must render a full standalone page:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="weekly-window-selector"`) {
		t.Fatalf("weekly page missing window selector:\n%s", body)
	}
	for _, mode := range []string{"work-week", "live-sprint"} {
		if !strings.Contains(body, `data-testid="weekly-window:`+mode+`"`) {
			t.Errorf("selector missing window mode %q", mode)
		}
	}
	// Work week is the default and is highlighted; Live sprint is not.
	if !modeIsActive(body, "work-week") {
		t.Errorf("work-week should be the default active mode:\n%s", body)
	}
	if modeIsActive(body, "live-sprint") {
		t.Errorf("live-sprint should not be active by default:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="weekly-results"`) {
		t.Fatalf("weekly page missing results fragment:\n%s", body)
	}
}

// TestWeeklyResultsFragmentIsPartial asserts the results endpoint returns a
// fragment (no full document) wired to swap the whole panel via HTMX.
func TestWeeklyResultsFragmentIsPartial(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29()}, now)

	body := get(t, app.URL+"/weekly/results")
	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Fatalf("results endpoint must return a partial, got full document:\n%s", body)
	}
	if !strings.Contains(body, `hx-get="/weekly/results"`) {
		t.Errorf("selector not wired to swap results via HTMX:\n%s", body)
	}
	if !strings.Contains(body, `hx-target="#weekly-panel"`) {
		t.Errorf("selector must target #weekly-panel so the mode re-renders:\n%s", body)
	}
}

// TestWeeklyResultsFragmentReflectsSelectedMode is a regression guard (cf. the
// #10 picker fix): switching mode must re-render the selector with the clicked
// mode highlighted, not just the numbers. The swap carries the whole panel.
func TestWeeklyResultsFragmentReflectsSelectedMode(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29()}, now)

	body := get(t, app.URL+"/weekly/results?window=live-sprint")
	if !strings.Contains(body, `data-testid="weekly-window-selector"`) {
		t.Fatalf("results fragment must include the selector so it re-renders:\n%s", body)
	}
	if !modeIsActive(body, "live-sprint") {
		t.Errorf("live-sprint should be active after selecting it:\n%s", body)
	}
	if modeIsActive(body, "work-week") {
		t.Errorf("work-week should NOT be active after selecting live-sprint:\n%s", body)
	}
}

// TestWeeklyWorkWeekWindowIsMondayToSaturdayBerlin asserts the Work week window
// is Mon 00:00 → Sat 00:00 Europe/Berlin for the week containing now: a Friday
// completion counts, a Saturday one does not (the weekend is excluded).
func TestWeeklyWorkWeekWindowIsMondayToSaturdayBerlin(t *testing.T) {
	loc := berlin(t)
	// now is Wed 2026-07-15; this ISO week is Mon 07-13 .. Sat 07-18.
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			finishedIssue("DCAI-1", "M", "DONE (This Sprint)", time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)), // Friday, in window
			finishedIssue("DCAI-2", "L", "DONE (This Sprint)", time.Date(2026, time.July, 18, 9, 0, 0, 0, loc)), // Saturday, excluded
			finishedIssue("DCAI-3", "S", "DONE (This Sprint)", time.Date(2026, time.July, 19, 9, 0, 0, 0, loc)), // Sunday, excluded
		},
	}, now)

	body := get(t, app.URL+"/weekly/results?window=work-week")
	wants := []string{
		`data-testid="finished:m">1<`, // Friday counts
		`data-testid="finished:l">0<`, // Saturday excluded
		`data-testid="finished:s">0<`, // Sunday excluded
		`data-testid="finished:points">2<`,
		`data-testid="weekly-window-label">13 Jul – 17 Jul 2026<`, // Mon → Fri (Sat exclusive)
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("work-week window wrong; missing %q\n%s", w, body)
		}
	}
}

// TestWeeklyLiveSprintWindowIsActivationToNow asserts the Live sprint window runs
// from the active sprint's ACTUAL activation instant to now: a completion before
// activation is excluded; one after counts.
func TestWeeklyLiveSprintWindowIsActivationToNow(t *testing.T) {
	loc := berlin(t)
	// KW29 activated 2026-07-13 09:00 Berlin (07:00 UTC, per activeSprintKW29).
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			finishedIssue("DCAI-1", "M", "DONE (This Sprint)", time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)), // after activation, counts
			finishedIssue("DCAI-2", "L", "DONE (This Sprint)", time.Date(2026, time.July, 12, 9, 0, 0, 0, loc)), // before activation, excluded
		},
	}, now)

	body := get(t, app.URL+"/weekly/results?window=live-sprint")
	wants := []string{
		`data-testid="finished:m">1<`,
		`data-testid="finished:l">0<`,
		`data-testid="finished:points">2<`,
		`data-testid="weekly-window-label">13 Jul – 17 Jul 2026<`, // activation day → day before now
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("live-sprint window did not use the activation instant; missing %q\n%s", w, body)
		}
	}
}

// TestWeeklyFinishedTallyScopedToActiveSprintInclReadyForRelease asserts the
// finished tally counts active-sprint crossings only (a non-active-sprint
// completion in the same window is excluded) and that a crossing into Ready for
// Release counts as finished (the corrected Done set).
func TestWeeklyFinishedTallyScopedToActiveSprintInclReadyForRelease(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	fri := time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)
	inWeek := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			finishedIssue("DCAI-1", "S", "DONE (This Sprint)", inWeek),
			finishedIssue("DCAI-2", "M", "Ready for Release", fri), // Ready for Release is a Done state
			// Completed in-window but NOT in the active sprint → excluded from the tally.
			completedIssue("DCAI-9", "L", inWeek),
		},
	}, now)

	body := get(t, app.URL+"/weekly/results?window=work-week")
	wants := []string{
		`data-testid="finished:s">1<`,      // DCAI-1
		`data-testid="finished:m">1<`,      // DCAI-2 (Ready for Release counts)
		`data-testid="finished:l">0<`,      // DCAI-9 excluded (not active sprint)
		`data-testid="finished:points">3<`, // S(1) + M(2)
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("finished tally wrong; missing %q\n%s", w, body)
		}
	}
	if strings.Contains(body, "DCAI-9") {
		t.Errorf("non-active-sprint completion leaked into the weekly tally\n%s", body)
	}
}

// TestWeeklyNoActiveSprintRendersEmptyState asserts that with no active sprint
// recorded, the Weekly view shows the Board-style no-sprint empty state rather
// than a row of zeros.
func TestWeeklyNoActiveSprintRendersEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	// A completion exists, but no sprint is active (closed sprint only).
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/weekly")
	if !strings.Contains(body, `data-testid="weekly-no-sprint"`) || !strings.Contains(body, "No active sprint") {
		t.Errorf("weekly view without an active sprint should show the no-sprint empty state\n%s", body)
	}
	if strings.Contains(body, `data-testid="finished:points"`) {
		t.Errorf("weekly view without an active sprint must not render a row of zeros\n%s", body)
	}
}

// modeIsActive reports whether the window-mode radio for the given key is checked
// (i.e. the highlighted selection).
func modeIsActive(html, mode string) bool {
	marker := `data-testid="weekly-window:` + mode + `"`
	start := strings.Index(html, marker)
	if start == -1 {
		return false
	}
	end := strings.Index(html[start:], ">")
	if end == -1 {
		return false
	}
	return strings.Contains(html[start:start+end], "checked")
}
