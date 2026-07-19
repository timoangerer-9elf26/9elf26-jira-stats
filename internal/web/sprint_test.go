package web_test

// Integration tests for the "Sprint" planning view over the HTTP seam.
//
// The view always centers on the current active sprint over the window
// [sprint start, now) — there is no window selector (see #53). Fixtures are
// built as status- and sprint-membership changes (via jira.FakeClient) scoped to
// the active sprint; the tests drive the real handlers and assert on rendered
// HTML. A fixed clock is injected so the window bounds resolve deterministically.

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
// (which are relative to "now") are deterministic. Shared by the Sprint, Daily
// and Velocity suites.
func newTestAppAt(t *testing.T, client jira.Client, now time.Time, opts ...web.Option) *testApp {
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
	opts = append([]web.Option{web.WithClock(func() time.Time { return now })}, opts...)
	srv, err := web.NewServer(st, opts...)
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

// sprintStatus builds one status changelog change for the Sprint HTTP fixtures.
func sprintStatus(id, from, to string, at time.Time) jira.ChangelogEntry {
	return jira.ChangelogEntry{ID: id, Field: "status", From: from, To: to, Timestamp: at}
}

// enteredSprintID / leftSprintID build one membership change into / out of a
// specific sprint id, so a fixture can model a carry-over across two sprints.
func enteredSprintID(entryID string, sprintID int, name string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: sprintID, SprintName: name, Entered: true, Timestamp: at}
}

func leftSprintID(entryID string, sprintID int, name string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: sprintID, SprintName: name, Entered: false, Timestamp: at}
}

// sprintIssue builds an active-sprint (activeSprint name) Story with a status
// changelog and sprint-membership changes, so the Sprint categories reconstruct
// its status and membership at the window bounds. current is the CURRENT status
// (drives the status_category); size is the CURRENT size.
func sprintIssue(key, size, current, activeSprint string, changelog []jira.ChangelogEntry, sprintChanges []jira.SprintMembershipChange) jira.Issue {
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	return jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: current, StatusCategory: cat,
		Size: size, ActiveSprint: activeSprint,
		Changelog:     changelog,
		SprintChanges: sprintChanges,
	}
}

// TestSprintPageRendersStandaloneWithoutSelector asserts /sprint renders a full
// standalone page centered on the active sprint — with the sprint name and its
// window in the header and NO window selector — and embeds the results.
func TestSprintPageRendersStandaloneWithoutSelector(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29()}, now)

	body := get(t, app.URL+"/sprint")
	if !strings.Contains(body, "<!DOCTYPE") || !strings.Contains(body, "<html") {
		t.Fatalf("/sprint must render a full standalone page:\n%s", body)
	}
	// The window selector is gone entirely.
	for _, gone := range []string{"window-selector", `name="window"`, "work-week", "live-sprint"} {
		if strings.Contains(body, gone) {
			t.Errorf("/sprint must not carry a window selector; found %q", gone)
		}
	}
	// The header shows the active sprint name and its window.
	if !strings.Contains(body, `data-testid="sprint-name">KW29<`) {
		t.Errorf("/sprint header missing the active sprint name:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="sprint-window-label"`) {
		t.Errorf("/sprint header missing the window label:\n%s", body)
	}
}

// TestSprintResultsFragmentIsPartial asserts the results endpoint returns a
// fragment (no full document).
func TestSprintResultsFragmentIsPartial(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29()}, now)

	body := get(t, app.URL+"/sprint/results")
	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Fatalf("results endpoint must return a partial, got full document:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="sprint-results"`) {
		t.Errorf("results fragment missing the results block:\n%s", body)
	}
}

// TestSprintTableCoversAllCategories drives the sprint window [sprint start, now)
// end-to-end over the required cases, anchored on the sprint's activation instant
// (KW29 activated 2026-07-13 09:00 Berlin), with now late on Friday:
//   - DCAI-1: open + member at the sprint start, finishes Friday (in window) →
//     Started with + finished-from-started.
//   - DCAI-2: open + member at the sprint start, still open → Started with.
//   - DCAI-3: entered the sprint after the start, never finished → Added.
//   - DCAI-4: entered after the start and finished in-window → Added +
//     finished-from-added.
func TestSprintTableCoversAllCategories(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 17, 18, 0, 0, 0, loc)
	// KW29 activated 2026-07-13 09:00 Berlin (07:00 UTC, per activeSprintKW29).
	beforeStart := time.Date(2026, time.July, 12, 0, 0, 0, 0, loc)
	afterStart := time.Date(2026, time.July, 14, 10, 0, 0, 0, loc) // entered mid-sprint
	fri := time.Date(2026, time.July, 17, 9, 0, 0, 0, loc)         // in window

	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			sprintIssue("DCAI-1", "M", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("s1a", "Ready To Do", "In Progress", beforeStart),
					sprintStatus("s1b", "In Progress", "DONE (This Sprint)", fri),
				},
				[]jira.SprintMembershipChange{enteredSprintID("m1", 29, "KW29", beforeStart)}),
			sprintIssue("DCAI-2", "L", "In Progress", "KW29",
				[]jira.ChangelogEntry{sprintStatus("s2a", "Ready To Do", "In Progress", beforeStart)},
				[]jira.SprintMembershipChange{enteredSprintID("m2", 29, "KW29", beforeStart)}),
			sprintIssue("DCAI-3", "S", "Ready To Do", "KW29",
				nil,
				[]jira.SprintMembershipChange{enteredSprintID("m3", 29, "KW29", afterStart)}),
			sprintIssue("DCAI-4", "M", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("s4a", "Ready To Do", "In Progress", afterStart),
					sprintStatus("s4b", "In Progress", "DONE (This Sprint)", fri),
				},
				[]jira.SprintMembershipChange{enteredSprintID("m4", 29, "KW29", afterStart)}),
		},
	}, now)

	body := get(t, app.URL+"/sprint/results")
	wants := []string{
		// Started-with cohort: DCAI-1 finished (M), DCAI-2 still open (L).
		`data-testid="sprint-cell:started:finished:tickets">1<`,
		`data-testid="sprint-cell:started:finished:points">2<`,
		`data-testid="sprint-cell:started:open:tickets">1<`,
		`data-testid="sprint-cell:started:open:points">3<`,
		`data-testid="sprint-cell:started:removed:tickets">0<`,
		`data-testid="sprint-cell:started:total:tickets">2<`,
		`data-testid="sprint-cell:started:total:points">5<`,
		// Added cohort: DCAI-3 still open (S), DCAI-4 finished (M).
		`data-testid="sprint-cell:added:open:tickets">1<`,
		`data-testid="sprint-cell:added:open:points">1<`,
		`data-testid="sprint-cell:added:finished:tickets">1<`,
		`data-testid="sprint-cell:added:finished:points">2<`,
		`data-testid="sprint-cell:added:total:tickets">2<`,
		`data-testid="sprint-cell:added:total:points">3<`,
		// Total row = column-wise Started-with + Added.
		`data-testid="sprint-cell:total:finished:tickets">2<`,
		`data-testid="sprint-cell:total:finished:points">4<`,
		`data-testid="sprint-cell:total:total:tickets">4<`,
		`data-testid="sprint-cell:total:total:points">8<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("sprint table wrong; missing %q\n%s", w, body)
		}
	}
}

// TestSprintCarryOverLandsInStartedWithNotAdded is the crux of #53: across two
// consecutive sprints, a ticket carried from the previous sprint into the new one
// at rollover (its entry into the new sprint at/around the new sprint's start
// instant) must land in Started-with, NOT Added — while a genuine mid-sprint add
// lands in Added. Under the old Monday work-week anchor the carry-over wrongly
// fell into Added; anchoring on the sprint's own start fixes it.
func TestSprintCarryOverLandsInStartedWithNotAdded(t *testing.T) {
	loc := berlin(t)
	// KW30 is the active sprint, started Monday 2026-07-20 09:00 Berlin; now is Wed.
	start := time.Date(2026, time.July, 20, 9, 0, 0, 0, loc)
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, loc)
	inKW29 := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc) // when the carry-over first entered KW29
	afterStart := time.Date(2026, time.July, 21, 10, 0, 0, 0, loc)

	fake := &jira.FakeClient{
		Sprints: []jira.Sprint{
			{ID: 29, Name: "KW29", State: "closed", ActivatedAt: time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)},
			{ID: 30, Name: "KW30", State: "active", ActivatedAt: start.UTC()},
		},
		Issues: []jira.Issue{
			// Carry-over: opened and joined KW29 last week, then at rollover left KW29
			// and joined KW30 exactly at KW30's start. Still open. → Started-with.
			sprintIssue("DCAI-CO", "M", "In Progress", "KW30",
				[]jira.ChangelogEntry{sprintStatus("co-s", "Ready To Do", "In Progress", inKW29)},
				[]jira.SprintMembershipChange{
					enteredSprintID("co-29", 29, "KW29", inKW29),
					leftSprintID("co-29b", 29, "KW29", start),
					enteredSprintID("co-30", 30, "KW30", start),
				}),
			// Genuine mid-sprint add: joined KW30 after it started. → Added.
			sprintIssue("DCAI-ADD", "S", "Ready To Do", "KW30",
				nil,
				[]jira.SprintMembershipChange{enteredSprintID("add-30", 30, "KW30", afterStart)}),
		},
	}
	app := newTestAppAt(t, fake, now)

	body := get(t, app.URL+"/sprint/results")
	wants := []string{
		`data-testid="sprint-name">KW30<`,
		// Started with = the carry-over DCAI-CO (M) only: 1 ticket, 2 pts.
		`data-testid="sprint-cell:started:total:tickets">1<`,
		`data-testid="sprint-cell:started:total:points">2<`,
		// Added = the genuine mid-sprint add DCAI-ADD (S) only: 1 ticket, 1 pt.
		`data-testid="sprint-cell:added:total:tickets">1<`,
		`data-testid="sprint-cell:added:total:points">1<`,
		`data-testid="sprint-cell:total:total:tickets">2<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("carry-over not anchored on the sprint start; missing %q\n%s", w, body)
		}
	}
}

// TestSprintCohortOutcomeAsymmetryAndTooltips drives the #70 removal asymmetry
// end-to-end and asserts the help tooltips. In KW29:
//   - SW-CANCEL: started, cancelled → Started-with Removed.
//   - SW-LEFT: started, reprioritised out → Started-with Removed (kept).
//   - AD-CANCEL: added, cancelled → Added Removed (cancellation reaches Removed).
//   - AD-LEFT: added, reprioritised out → dropped entirely (in NO cell).
//
// It also checks the `?` help tooltips on the four column headers and the
// Started-with / Added row labels (native title/aria-label), and that the Total
// row carries no help marker.
func TestSprintCohortOutcomeAsymmetryAndTooltips(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, loc)
	atStart := time.Date(2026, time.July, 13, 8, 0, 0, 0, loc)    // before/at start → Started-with
	afterGrace := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc) // after grace → Added
	mid := time.Date(2026, time.July, 16, 9, 0, 0, 0, loc)        // a leave / cancel, in-window

	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues: []jira.Issue{
			sprintIssue("SW-CANCEL", "L", "Canceled", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("swc1", "Ready To Do", "In Progress", atStart),
					sprintStatus("swc2", "In Progress", "Canceled", mid),
				},
				[]jira.SprintMembershipChange{enteredSprintID("sw-c", 29, "KW29", atStart)}),
			sprintIssue("SW-LEFT", "S", "In Progress", "KW29",
				[]jira.ChangelogEntry{sprintStatus("swl", "Ready To Do", "In Progress", atStart)},
				[]jira.SprintMembershipChange{
					enteredSprintID("sw-l", 29, "KW29", atStart),
					leftSprintID("sw-l2", 29, "KW29", mid),
				}),
			sprintIssue("AD-CANCEL", "M", "Canceled", "KW29",
				[]jira.ChangelogEntry{
					sprintStatus("adc1", "Ready To Do", "In Progress", afterGrace),
					sprintStatus("adc2", "In Progress", "Canceled", mid),
				},
				[]jira.SprintMembershipChange{enteredSprintID("ad-c", 29, "KW29", afterGrace)}),
			sprintIssue("AD-LEFT", "L", "In Progress", "KW29",
				[]jira.ChangelogEntry{sprintStatus("adl", "Ready To Do", "In Progress", afterGrace)},
				[]jira.SprintMembershipChange{
					enteredSprintID("ad-l", 29, "KW29", afterGrace),
					leftSprintID("ad-l2", 29, "KW29", mid),
				}),
		},
	}, now)

	body := get(t, app.URL+"/sprint/results")
	wants := []string{
		// Started-with keeps BOTH the cancelled and the reprioritised-out ticket.
		`data-testid="sprint-cell:started:removed:tickets">2<`,
		`data-testid="sprint-cell:started:removed:points">4<`,
		`data-testid="sprint-cell:started:total:tickets">2<`,
		`data-testid="sprint-cell:started:open:tickets">0<`,
		// Added counts ONLY the cancelled one; AD-LEFT is dropped entirely.
		`data-testid="sprint-cell:added:removed:tickets">1<`,
		`data-testid="sprint-cell:added:removed:points">2<`,
		`data-testid="sprint-cell:added:total:tickets">1<`,
		`data-testid="sprint-cell:added:open:tickets">0<`,
		// Column-header help tooltips.
		`data-testid="sprint-col:open:help"`,
		`data-testid="sprint-col:finished:help"`,
		`data-testid="sprint-col:removed:help"`,
		`data-testid="sprint-col:total:help"`,
		// Row-label help tooltips on the two cohorts.
		`data-testid="sprint-row:started:help"`,
		`data-testid="sprint-row:added:help"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("cohort/outcome table wrong; missing %q\n%s", w, body)
		}
	}
	// The Total row needs no explanation — no help marker.
	if strings.Contains(body, `data-testid="sprint-row:total:help"`) {
		t.Errorf("Total row should carry no help tooltip\n%s", body)
	}
	// The Removed tooltip must spell out the asymmetry.
	if !strings.Contains(body, "reprioritised-out adds are dropped") {
		t.Errorf("Removed column tooltip should explain the asymmetry\n%s", body)
	}
	// Help markers render as an info icon (an inline SVG), not a plain `?`.
	if !strings.Contains(body, `class="sprint-help"`) {
		t.Errorf("help markers should use the sprint-help info-icon component\n%s", body)
	}
	if !strings.Contains(body, "<svg") {
		t.Errorf("help markers should render an SVG info icon\n%s", body)
	}
	// The old bare-`?` marker must be gone.
	if strings.Contains(body, `class="text-slate-400" title=`) || strings.Contains(body, `>?</span>`) {
		t.Errorf("help markers should no longer be a plain `?` with a native title\n%s", body)
	}
	// The CSS tooltip reveals after ~200ms (clearly faster than the native delay).
	if !strings.Contains(body, "200ms") {
		t.Errorf("help tooltip should reveal on a ~200ms delay\n%s", body)
	}
	// The tooltip copy stays reachable for assistive tech via aria-label.
	if !strings.Contains(body, `aria-label="Crossed into Done`) {
		t.Errorf("help marker should keep an aria-label carrying the tooltip copy\n%s", body)
	}
}

// TestSprintGraceWindowAcrossTwoSprints exercises the one-hour grace window (#65)
// in the two-consecutive-sprint (rollover) setting: KW29 is closed and KW30 is
// the active sprint. Anchored on KW30's own start + 1h:
//   - DCAI-CO carried over from KW29, re-joining KW30 within the grace hour →
//     Started with (rollover churn absorbed, not scope creep).
//   - DCAI-BORN created directly into KW30 at its start (no Sprint changelog) →
//     Started with (synthetic membership at the start instant).
//   - DCAI-LATE joins KW30 just AFTER the grace window → Added.
func TestSprintGraceWindowAcrossTwoSprints(t *testing.T) {
	loc := berlin(t)
	start := time.Date(2026, time.July, 20, 9, 0, 0, 0, loc) // KW30 start
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, loc)
	inKW29 := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)
	inGrace := start.Add(20 * time.Minute) // within KW30's first hour
	afterGrace := start.Add(70 * time.Minute)

	fake := &jira.FakeClient{
		Sprints: []jira.Sprint{
			{ID: 29, Name: "KW29", State: "closed", ActivatedAt: time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)},
			{ID: 30, Name: "KW30", State: "active", ActivatedAt: start.UTC()},
		},
		Issues: []jira.Issue{
			// Carry-over re-added inside the grace hour → Started with.
			sprintIssue("DCAI-CO", "M", "In Progress", "KW30",
				[]jira.ChangelogEntry{sprintStatus("co-s", "Ready To Do", "In Progress", inKW29)},
				[]jira.SprintMembershipChange{
					enteredSprintID("co-29", 29, "KW29", inKW29),
					leftSprintID("co-29b", 29, "KW29", start),
					enteredSprintID("co-30", 30, "KW30", inGrace),
				}),
			// Created directly into KW30 at its start → Started with (synthetic entry).
			{
				Key: "DCAI-BORN", Type: "Task", Summary: "born at start", Status: "In Progress",
				StatusCategory: "In Progress", Size: "S",
				ActiveSprint: "KW30", ActiveSprintID: 30, CreatedAt: start.UTC(),
			},
			// Joined just after the grace window → Added.
			sprintIssue("DCAI-LATE", "L", "Ready To Do", "KW30",
				nil,
				[]jira.SprintMembershipChange{enteredSprintID("late-30", 30, "KW30", afterGrace)}),
		},
	}
	app := newTestAppAt(t, fake, now)

	body := get(t, app.URL+"/sprint/results")
	wants := []string{
		`data-testid="sprint-name">KW30<`,
		// Started with = DCAI-CO (M) + DCAI-BORN (S): 2 tickets, 3 pts (all open).
		`data-testid="sprint-cell:started:total:tickets">2<`,
		`data-testid="sprint-cell:started:total:points">3<`,
		// Added = DCAI-LATE (L) only: 1 ticket, 3 pts.
		`data-testid="sprint-cell:added:total:tickets">1<`,
		`data-testid="sprint-cell:added:total:points">3<`,
		`data-testid="sprint-cell:total:total:tickets">3<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("grace window across two sprints wrong; missing %q\n%s", w, body)
		}
	}
}

// TestSprintIncludesTicketCreatedDirectlyIntoSprint is the #55 impact at the
// view: a ticket created directly into the active sprint (its Sprint field set at
// creation, so no "Sprint" changelog item and no SprintChanges) must still be
// counted. Created mid-sprint (after the start, the observed real case), it lands
// in Added via the store's synthetic membership entry at its created instant —
// where before the fix it was dropped from the tallies entirely.
func TestSprintIncludesTicketCreatedDirectlyIntoSprint(t *testing.T) {
	loc := berlin(t)
	start := time.Date(2026, time.July, 20, 9, 0, 0, 0, loc)
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, loc)
	created := time.Date(2026, time.July, 21, 10, 0, 0, 0, loc) // created after the sprint started
	beforeStart := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)

	fake := &jira.FakeClient{
		Sprints: []jira.Sprint{{ID: 30, Name: "KW30", State: "active", ActivatedAt: start.UTC()}},
		Issues: []jira.Issue{
			// Normal started-with ticket, for contrast (real membership + status).
			sprintIssue("DCAI-STD", "L", "In Progress", "KW30",
				[]jira.ChangelogEntry{sprintStatus("std-s", "Ready To Do", "In Progress", beforeStart)},
				[]jira.SprintMembershipChange{enteredSprintID("std-m", 30, "KW30", beforeStart)}),
			// Created directly into KW30 after it started: no Sprint changelog item,
			// so no SprintChanges — only the current active-sprint id + created carry
			// it. The store synthesizes its membership entry at `created`.
			{
				Key: "DCAI-BORN", Type: "Task", Summary: "born mid-sprint", Status: "In Progress",
				StatusCategory: "In Progress", Size: "M",
				ActiveSprint: "KW30", ActiveSprintID: 30, CreatedAt: created.UTC(),
			},
		},
	}
	app := newTestAppAt(t, fake, now)

	body := get(t, app.URL+"/sprint/results")
	wants := []string{
		// Added = the created-into-sprint DCAI-BORN (M) only: 1 ticket, 2 pts.
		`data-testid="sprint-cell:added:total:tickets">1<`,
		`data-testid="sprint-cell:added:total:points">2<`,
		// Started with = the normal DCAI-STD (L).
		`data-testid="sprint-cell:started:total:tickets">1<`,
		`data-testid="sprint-cell:total:total:tickets">2<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("created-into-sprint ticket missing from the Sprint tallies; want %q\n%s", w, body)
		}
	}
}

// TestSprintNoActiveSprintRendersEmptyState asserts that with no active sprint
// recorded, the Sprint view shows the Board-style no-sprint empty state rather
// than a row of zeros.
func TestSprintNoActiveSprintRendersEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	// A completion exists, but no sprint is active (closed sprint only).
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/sprint")
	if !strings.Contains(body, `data-testid="sprint-no-sprint"`) || !strings.Contains(body, "No active sprint") {
		t.Errorf("sprint view without an active sprint should show the no-sprint empty state\n%s", body)
	}
	if strings.Contains(body, `data-testid="sprint-table"`) {
		t.Errorf("sprint view without an active sprint must not render a table of zeros\n%s", body)
	}
}

// TestSprintCannedDatasetPopulatesTable is the fixture-regression guard for #50
// carried into #53: booting the built-in canned fake under the pinned review
// clock (REVIEW_NOW=2026-07-15T12:00:00Z, sprint KW29 activated 2026-07-13), the
// Sprint view renders the populated three-category table — NOT the empty state —
// over the sprint window [13 Jul, 15 Jul). It fails if canned_issues.json ever
// regresses to carrying no sprint-membership history.
//
// The canned KW29 story (over the sprint window, now = Wed):
//   - DCAI-1 (L): started-with, finishes Tue → finished-from-started
//   - DCAI-2 (M), DCAI-8 (S), DCAI-9 (M): started-with, still open
//   - DCAI-3 (S): started-with; crosses Done THURSDAY (after now) → not finished
//   - DCAI-4 (no estimate): added mid-sprint, still open
//   - DCAI-5 (M): added mid-sprint, finishes Tue → finished-from-added
//   - DCAI-6 (Epic): excluded from every rollup (rollup types are Task/Bug/Story)
func TestSprintCannedDatasetPopulatesTable(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	app := newTestAppAt(t, jira.NewFakeClient(), now)

	body := get(t, app.URL+"/sprint/results")
	if strings.Contains(body, `data-testid="sprint-empty"`) {
		t.Fatalf("canned dataset renders the EMPTY Sprint state; fixture regressed\n%s", body)
	}
	wants := []string{
		`data-testid="sprint-table"`,
		`data-testid="sprint-window-label">13 Jul – 14 Jul 2026<`,
		// Started-with cohort = DCAI-1(L)+DCAI-2(M)+DCAI-3(S)+DCAI-8(S)+DCAI-9(M):
		// Total 5 / 9pts. DCAI-1 finished; the rest still open (DCAI-3 crosses Thu,
		// AFTER now). Nothing cancelled or removed.
		`data-testid="sprint-cell:started:total:tickets">5<`,
		`data-testid="sprint-cell:started:total:points">9<`,
		`data-testid="sprint-cell:started:finished:tickets">1<`,
		`data-testid="sprint-cell:started:finished:points">3<`,
		`data-testid="sprint-cell:started:open:tickets">4<`,
		`data-testid="sprint-cell:started:open:points">6<`,
		`data-testid="sprint-cell:started:removed:tickets">0<`,
		// Added cohort = DCAI-4(none, open) + DCAI-5(M, finished): Total 2 / 2pts.
		`data-testid="sprint-cell:added:total:tickets">2<`,
		`data-testid="sprint-cell:added:total:points">2<`,
		`data-testid="sprint-cell:added:finished:tickets">1<`,
		`data-testid="sprint-cell:added:finished:points">2<`,
		`data-testid="sprint-cell:added:open:tickets">1<`,
		// Total row.
		`data-testid="sprint-cell:total:total:tickets">7<`,
		`data-testid="sprint-cell:total:total:points">11<`,
		`data-testid="sprint-cell:total:finished:tickets">2<`,
		`data-testid="sprint-cell:total:finished:points">5<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("canned Sprint table missing %q\n%s", w, body)
		}
	}
}
