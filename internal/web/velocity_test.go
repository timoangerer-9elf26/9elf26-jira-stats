package web_test

// Integration tests for the per-sprint "Velocity" view over the HTTP seam.
//
// Each bar is one sprint's Finished points, computed via the SAME
// SprintCategoriesInWindow path as the Sprint view's Total-row Finished, so the
// active sprint's bar equals the live Sprint view. Fixtures build sprints (via
// jira.FakeClient.Sprints) with membership + Done crossings; a fixed clock pins
// "now" so the trailing window and the active sprint's [start, now) window are
// deterministic.

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

// newTestAppWith is like newTestAppAt but also threads extra Server options
// (e.g. WithVelocitySprints) so the Velocity window size can be pinned per test.
func newTestAppWith(t *testing.T, client jira.Client, now time.Time, opts ...web.Option) *testApp {
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

// twoSprintFixture builds a closed KW28 and an active KW29, each with a
// Done-crossing member finished inside its own window. KW28: an M (2 pts);
// KW29: an S (1 pt). Berlin-local instants keep the date line deterministic.
func twoSprintFixture(loc *time.Location) *jira.FakeClient {
	start28 := time.Date(2026, time.July, 6, 9, 0, 0, 0, loc)
	end28 := time.Date(2026, time.July, 10, 17, 0, 0, 0, loc)
	start29 := time.Date(2026, time.July, 13, 9, 0, 0, 0, loc)
	return &jira.FakeClient{
		Sprints: []jira.Sprint{
			{ID: 28, Name: "KW28", State: "closed", ActivatedAt: start28.UTC(), CompletedAt: end28.UTC()},
			{ID: 29, Name: "KW29", State: "active", ActivatedAt: start29.UTC()},
		},
		Issues: []jira.Issue{
			sprintIssue("K28-FIN", "M", "DONE (This Sprint)", "KW28",
				[]jira.ChangelogEntry{sprintStatus("k28", "In Progress", "DONE (This Sprint)", start28.Add(24*time.Hour))},
				[]jira.SprintMembershipChange{enteredSprintID("k28-m", 28, "KW28", start28)}),
			sprintIssue("K29-FIN", "S", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{sprintStatus("k29", "In Progress", "DONE (This Sprint)", start29.Add(24*time.Hour))},
				[]jira.SprintMembershipChange{enteredSprintID("k29-m", 29, "KW29", start29)}),
		},
	}
}

// TestVelocityPerSprintPoints drives the Velocity view and asserts one bar per
// sprint, labelled by the sprint's NAME, with per-sprint Finished points and
// oldest-first order.
func TestVelocityPerSprintPoints(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now)

	body := get(t, app.URL+"/velocity")
	if !strings.Contains(body, "<!DOCTYPE") || !strings.Contains(body, "<html") {
		t.Fatalf("/velocity must render a full standalone page:\n%s", body)
	}

	wants := map[string]string{
		"KW28": `data-testid="sprint-points:KW28">2<`,
		"KW29": `data-testid="sprint-points:KW29">1<`,
	}
	for sp, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("velocity view missing %s points %q\n%s", sp, want, body)
		}
	}
	// One bar per sprint (only two sprints exist), labelled by name.
	if n := strings.Count(body, `data-testid="velocity-sprint"`); n != 2 {
		t.Errorf("expected 2 per-sprint bars, got %d\n%s", n, body)
	}
	if !strings.Contains(body, `data-testid="sprint-label:KW29">KW29<`) {
		t.Errorf("bar must be labelled by the sprint name:\n%s", body)
	}
	assertOrder(t, body, `data-sprint="KW28"`, `data-sprint="KW29"`)
}

// TestVelocityBarMatchesSprintViewFinished is the alignment guarantee: the
// current sprint's Velocity bar points EQUAL the live Sprint view's Total-row
// Finished points (same rendered number), because both derive from
// SprintCategoriesInWindow.
func TestVelocityBarMatchesSprintViewFinished(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	start := time.Date(2026, time.July, 13, 9, 0, 0, 0, loc)
	afterGrace := time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)
	doneAt := time.Date(2026, time.July, 14, 15, 0, 0, 0, loc)
	prior := time.Date(2026, time.July, 9, 9, 0, 0, 0, loc) // crossed in a prior sprint

	app := newTestAppWith(t, &jira.FakeClient{
		Sprints: []jira.Sprint{{ID: 29, Name: "KW29", State: "active", ActivatedAt: start.UTC()}},
		Issues: []jira.Issue{
			// Started-with, finished in-window (M = 2).
			sprintIssue("SW-FIN", "M", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{sprintStatus("swf", "In Progress", "DONE (This Sprint)", doneAt)},
				[]jira.SprintMembershipChange{enteredSprintID("sw", 29, "KW29", start)}),
			// Added, finished in-window (S = 1).
			sprintIssue("AD-FIN", "S", "DONE (This Sprint)", "KW29",
				[]jira.ChangelogEntry{sprintStatus("adf", "In Progress", "DONE (This Sprint)", doneAt)},
				[]jira.SprintMembershipChange{enteredSprintID("ad", 29, "KW29", afterGrace)}),
			// Pre-finished carry-over: currently Done, crossed before the window → excluded.
			sprintIssue("CARRY", "L", "Ready for Release", "KW29",
				[]jira.ChangelogEntry{sprintStatus("cy", "In Progress", "Ready for Release", prior)},
				[]jira.SprintMembershipChange{enteredSprintID("cy-m", 29, "KW29", start)}),
		},
	}, now)

	sprintBody := get(t, app.URL+"/sprint")
	velBody := get(t, app.URL+"/velocity")

	// The Sprint view Total-row Finished points and the Velocity KW29 bar must be
	// the same rendered number (3 = M + S; carry-over excluded).
	if !strings.Contains(sprintBody, `data-testid="sprint-cell:total:finished:points">3<`) {
		t.Fatalf("Sprint view Total Finished points not 3 as expected:\n%s", sprintBody)
	}
	if !strings.Contains(velBody, `data-testid="sprint-points:KW29">3<`) {
		t.Fatalf("Velocity KW29 bar must equal the Sprint view Finished (3):\n%s", velBody)
	}
}

// TestVelocityDateLine asserts the start–end date line (date only, Europe/Berlin):
// a completed sprint as "start – end"; the active sprint as "start – now (ongoing)".
func TestVelocityDateLine(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now)

	body := get(t, app.URL+"/velocity")
	if !strings.Contains(body, `data-testid="sprint-dates:KW28">6 Jul – 10 Jul<`) {
		t.Errorf("completed sprint date line wrong (want \"6 Jul – 10 Jul\"):\n%s", body)
	}
	if !strings.Contains(body, `data-testid="sprint-dates:KW29">13 Jul – now (ongoing)<`) {
		t.Errorf("active sprint date line wrong (want \"13 Jul – now (ongoing)\"):\n%s", body)
	}
}

// TestVelocityBarsAreAccessibleCSSBars asserts the reusable velocity-bar partial
// renders accessible CSS bars (no JS charting library) with a per-sprint
// aria-label carrying the sprint name and its points.
func TestVelocityBarsAreAccessibleCSSBars(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now)

	body := get(t, app.URL+"/velocity")
	if !strings.Contains(body, `aria-label="KW29: 1 points"`) {
		t.Errorf("velocity bars missing accessible per-sprint label:\n%s", body)
	}
	for _, banned := range []string{"chart.js", "d3.js", "d3.min.js", "plotly", "apexcharts"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("velocity view must not use a JS charting library (found %q)", banned)
		}
	}
}

// TestVelocityTallestBarHasHeadroom asserts the tallest sprint's bar does not
// fill its plot box to the very top: it scales below 100% so there is visible
// headroom above it and it never overflows/touches the box's top edge (#103).
func TestVelocityTallestBarHasHeadroom(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now)

	body := get(t, app.URL+"/velocity")
	// twoSprintFixture: KW28 = 2 pts (the tallest), KW29 = 1 pt. The tallest bar
	// must leave headroom, so its height style must not be 100%.
	if strings.Contains(body, "height: 100%") {
		t.Errorf("tallest bar must leave headroom (no 100%% height):\n%s", body)
	}
}

// TestVelocityPlotBoxesTopAligned asserts the bar row top-aligns its columns so
// every sprint's fixed-height plot box shares a common top (and, being equal
// height, a common bottom) even when a column's date label wraps to two lines
// (the active "… now (ongoing)" sprint) (#103).
func TestVelocityPlotBoxesTopAligned(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now)

	body := get(t, app.URL+"/velocity")
	if !strings.Contains(body, `class="mt-6 flex items-start gap-2 sm:gap-3"`) {
		t.Errorf("bar row must top-align columns (items-start) so a two-line date does not shove its plot box up:\n%s", body)
	}
}

// TestVelocitySprintCountConfigurable asserts the trailing-sprint window size is
// configurable (WithVelocitySprints): with a limit of 1, only the most recent
// sprint's bar renders.
func TestVelocitySprintCountConfigurable(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, twoSprintFixture(loc), now, web.WithVelocitySprints(1))

	body := get(t, app.URL+"/velocity")
	if n := strings.Count(body, `data-testid="velocity-sprint"`); n != 1 {
		t.Errorf("expected 1 bar with WithVelocitySprints(1), got %d\n%s", n, body)
	}
	if strings.Contains(body, `data-sprint="KW28"`) {
		t.Errorf("1-sprint window should not include the older KW28:\n%s", body)
	}
	if !strings.Contains(body, `data-sprint="KW29"`) {
		t.Errorf("1-sprint window should include the most recent KW29:\n%s", body)
	}
}

// TestVelocityReachableFromNav asserts the Velocity view is linked from the
// shared nav so it is reachable.
func TestVelocityReachableFromNav(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	body := get(t, app.URL+"/sprint")
	if !strings.Contains(body, `href="/velocity"`) {
		t.Errorf("shared nav must link to the Velocity view:\n%s", body)
	}
}
