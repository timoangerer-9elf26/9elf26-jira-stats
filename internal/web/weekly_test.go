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

// weeklyEntered / weeklyStatus build one membership / status changelog change for
// the Weekly HTTP fixtures.
func weeklyEntered(entryID string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: 29, SprintName: "KW29", Entered: true, Timestamp: at}
}

func weeklyStatus(id, from, to string, at time.Time) jira.ChangelogEntry {
	return jira.ChangelogEntry{ID: id, Field: "status", From: from, To: to, Timestamp: at}
}

// weeklyIssue builds a KW29 active-sprint Story with a status changelog and
// sprint-membership changes, so the Weekly categories reconstruct its status and
// membership at the window bounds. current is the CURRENT status (drives the
// status_category); size is the CURRENT size.
func weeklyIssue(key, size, current string, changelog []jira.ChangelogEntry, sprintChanges []jira.SprintMembershipChange) jira.Issue {
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	return jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: current, StatusCategory: cat,
		Size: size, ActiveSprint: "KW29",
		Changelog:     changelog,
		SprintChanges: sprintChanges,
	}
}

// TestWeeklyWorkWeekTableCoversAllCategories drives the Work week window (Mon
// 00:00 → Sat 00:00 Europe/Berlin) end-to-end over the four required cases:
//   - DCAI-1: open + in the sprint at the window start, finishes Friday (in
//     window) → Started with + finished-from-started.
//   - DCAI-2: open + in the sprint at the window start, finishes Saturday (the
//     weekend is excluded) → Started with, NOT finished.
//   - DCAI-3: entered the sprint mid-window, never finished → Added.
//   - DCAI-4: entered the sprint mid-window and finished mid-window → Added +
//     finished-from-added.
//
// It also asserts the Total row = Started-with + Added, and the finished total.
func TestWeeklyWorkWeekTableCoversAllCategories(t *testing.T) {
	loc := berlin(t)
	// This ISO week is Mon 07-13 .. Sat 07-18 (Berlin). now is Friday afternoon.
	now := time.Date(2026, time.July, 17, 18, 0, 0, 0, loc)
	beforeStart := time.Date(2026, time.July, 12, 0, 0, 0, 0, loc) // before Mon 00:00
	tue := time.Date(2026, time.July, 14, 10, 0, 0, 0, loc)        // mid-window
	wed := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)         // mid-window
	fri := time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)         // in window
	sat := time.Date(2026, time.July, 18, 9, 0, 0, 0, loc)         // Saturday, excluded

	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			weeklyIssue("DCAI-1", "M", "DONE (This Sprint)",
				[]jira.ChangelogEntry{
					weeklyStatus("s1a", "Ready To Do", "In Progress", beforeStart),
					weeklyStatus("s1b", "In Progress", "DONE (This Sprint)", fri),
				},
				[]jira.SprintMembershipChange{weeklyEntered("m1", beforeStart)}),
			weeklyIssue("DCAI-2", "L", "DONE (This Sprint)",
				[]jira.ChangelogEntry{
					weeklyStatus("s2a", "Ready To Do", "In Progress", beforeStart),
					weeklyStatus("s2b", "In Progress", "DONE (This Sprint)", sat),
				},
				[]jira.SprintMembershipChange{weeklyEntered("m2", beforeStart)}),
			weeklyIssue("DCAI-3", "S", "Ready To Do",
				nil,
				[]jira.SprintMembershipChange{weeklyEntered("m3", tue)}),
			weeklyIssue("DCAI-4", "M", "DONE (This Sprint)",
				[]jira.ChangelogEntry{
					weeklyStatus("s4a", "Ready To Do", "In Progress", tue),
					weeklyStatus("s4b", "In Progress", "DONE (This Sprint)", wed),
				},
				[]jira.SprintMembershipChange{weeklyEntered("m4", tue)}),
		},
	}, now)

	body := get(t, app.URL+"/weekly/results?window=work-week")
	wants := []string{
		`data-testid="weekly-window-label">13 Jul – 17 Jul 2026<`, // Mon → Fri (Sat exclusive)
		// Started with = DCAI-1 (M) + DCAI-2 (L): 2 tickets, 5 pts.
		`data-testid="weekly-started:tickets">2<`,
		`data-testid="weekly-started:points">5<`,
		// Added = DCAI-3 (S) + DCAI-4 (M): 2 tickets, 3 pts.
		`data-testid="weekly-added:tickets">2<`,
		`data-testid="weekly-added:points">3<`,
		// Total row = Started-with + Added: 4 tickets, 8 pts.
		`data-testid="weekly-total:tickets">4<`,
		`data-testid="weekly-total:points">8<`,
		// Finished-from-started = DCAI-1 (Friday); DCAI-2 (Saturday) excluded.
		`data-testid="weekly-started:finished-tickets">1<`,
		`data-testid="weekly-started:finished-points">2<`,
		// Finished-from-added = DCAI-4.
		`data-testid="weekly-added:finished-tickets">1<`,
		`data-testid="weekly-added:finished-points">2<`,
		// Finished total = 2 tickets, 4 pts.
		`data-testid="weekly-total:finished-tickets">2<`,
		`data-testid="weekly-total:finished-points">4<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("work-week table wrong; missing %q\n%s", w, body)
		}
	}
}

// TestWeeklyLiveSprintWindowStartsAtActivation asserts the Live sprint window
// starts at the sprint's ACTUAL activation instant (not Monday 00:00): a ticket
// whose Done crossing falls between Monday and activation is excluded from
// Finished, and a ticket already Done at activation is not "started with". Under a
// (wrong) Monday-start window both would count — so the numbers distinguish the
// modes.
func TestWeeklyLiveSprintWindowStartsAtActivation(t *testing.T) {
	loc := berlin(t)
	// KW29 activated 2026-07-13 09:00 Berlin (07:00 UTC, per activeSprintKW29).
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, loc)
	beforeMonday := time.Date(2026, time.July, 11, 0, 0, 0, 0, loc)
	beforeActivation := time.Date(2026, time.July, 13, 8, 0, 0, 0, loc) // Mon 08:00, before activation 09:00
	afterActivation := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)

	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			// Started with (open + member at activation), finishes after activation.
			weeklyIssue("DCAI-1", "M", "DONE (This Sprint)",
				[]jira.ChangelogEntry{
					weeklyStatus("s1a", "Ready To Do", "In Progress", beforeMonday),
					weeklyStatus("s1b", "In Progress", "DONE (This Sprint)", afterActivation),
				},
				[]jira.SprintMembershipChange{weeklyEntered("m1", beforeMonday)}),
			// Crossed into Done BEFORE activation: at activation it is already Done
			// (not open → not started-with) and its crossing precedes the window
			// start → not finished. Under a Monday-start window it would be both.
			weeklyIssue("DCAI-2", "L", "DONE (This Sprint)",
				[]jira.ChangelogEntry{
					weeklyStatus("s2a", "Ready To Do", "In Progress", beforeMonday),
					weeklyStatus("s2b", "In Progress", "DONE (This Sprint)", beforeActivation),
				},
				[]jira.SprintMembershipChange{weeklyEntered("m2", beforeMonday)}),
		},
	}, now)

	body := get(t, app.URL+"/weekly/results?window=live-sprint")
	wants := []string{
		`data-testid="weekly-window-label">13 Jul – 17 Jul 2026<`, // activation day → day before now
		`data-testid="weekly-started:tickets">1<`,                 // only DCAI-1 (DCAI-2 already Done at activation)
		`data-testid="weekly-started:points">2<`,
		`data-testid="weekly-total:tickets">1<`,
		`data-testid="weekly-started:finished-tickets">1<`, // DCAI-1 only
		`data-testid="weekly-total:finished-points">2<`,    // DCAI-2 crossing before activation excluded
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("live-sprint window did not start at the activation instant; missing %q\n%s", w, body)
		}
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
