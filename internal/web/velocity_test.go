package web_test

// Integration tests for the "Velocity" view over the HTTP seam.
//
// Fixtures place Done-crossings at known Berlin-local instants across several
// trailing ISO weeks (including an empty week in the middle and completions
// outside the window). The tests drive the real handler and assert on the
// rendered per-week point totals and bar labels. A fixed clock pins "now" so
// the "last N weeks" window is deterministic.

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
// (e.g. WithVelocityWeeks) so the Velocity window size can be pinned per test.
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

// TestVelocityViewPerWeekPoints drives the Velocity view with completions
// spread across several ISO weeks (with an empty week in the middle) and
// asserts the per-week point totals, that the empty week appears as zero, that
// the default window is 10 gapless weeks oldest-first, and that a completion
// before the window is excluded.
func TestVelocityViewPerWeekPoints(t *testing.T) {
	loc := berlin(t)
	// "now" is Wed 2026-07-15; this ISO week is Mon 2026-07-13 = KW29.
	// The default 10-week window is KW20 (Mon 2026-05-11) .. KW29 (Mon 2026-07-13).
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)), // KW29: 2
		completedIssue("DCAI-2", "L", time.Date(2026, time.July, 8, 9, 0, 0, 0, loc)),  // KW28: 3
		// KW27 (Mon 2026-06-29): intentionally empty.
		completedIssue("DCAI-3", "S", time.Date(2026, time.June, 24, 9, 0, 0, 0, loc)), // KW26: 1 + 2
		completedIssue("DCAI-4", "M", time.Date(2026, time.June, 25, 9, 0, 0, 0, loc)), // KW26
		// Before the window (KW19, Mon 2026-05-04): must be excluded entirely.
		completedIssue("DCAI-5", "L", time.Date(2026, time.May, 6, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/velocity")

	if !strings.Contains(body, "<!DOCTYPE") || !strings.Contains(body, "<html") {
		t.Fatalf("/velocity must render a full standalone page:\n%s", body)
	}

	// Per-week point totals, including the empty middle week as zero.
	wants := map[string]string{
		"KW29": `data-testid="week-points:KW29">2<`,
		"KW28": `data-testid="week-points:KW28">3<`,
		"KW27": `data-testid="week-points:KW27">0<`, // empty middle week
		"KW26": `data-testid="week-points:KW26">3<`,
		"KW20": `data-testid="week-points:KW20">0<`, // oldest week in window
	}
	for wk, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("velocity view missing %s total %q\n%s", wk, want, body)
		}
	}

	// A completion before the window must not surface as its own bar.
	if strings.Contains(body, `data-week="KW19"`) {
		t.Errorf("velocity view shows out-of-window week KW19; must be excluded:\n%s", body)
	}

	// The series must have no gaps: exactly 10 weeks, in chronological order.
	if n := strings.Count(body, `data-testid="velocity-week"`); n != 10 {
		t.Errorf("expected 10 gapless weekly bars, got %d\n%s", n, body)
	}
	assertOrder(t, body,
		`data-week="KW20"`, `data-week="KW21"`, `data-week="KW22"`, `data-week="KW23"`,
		`data-week="KW24"`, `data-week="KW25"`, `data-week="KW26"`, `data-week="KW27"`,
		`data-week="KW28"`, `data-week="KW29"`,
	)
}

// TestVelocityBarsAreAccessibleCSSBars asserts the reusable velocity-bar
// partial renders accessible CSS bars (no JS charting library) with a
// per-week aria-label carrying the week and its points.
func TestVelocityBarsAreAccessibleCSSBars(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, &jira.FakeClient{Issues: []jira.Issue{
		completedIssue("DCAI-1", "M", time.Date(2026, time.July, 14, 9, 0, 0, 0, loc)), // KW29: 2
	}}, now)

	body := get(t, app.URL+"/velocity")
	if !strings.Contains(body, `aria-label="KW29: 2 points"`) {
		t.Errorf("velocity bars missing accessible per-week label:\n%s", body)
	}
	// No JS charting library.
	for _, banned := range []string{"chart.js", "d3.js", "d3.min.js", "plotly", "apexcharts"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("velocity view must not use a JS charting library (found %q)", banned)
		}
	}
}

// TestVelocityWeekCountConfigurable asserts the trailing-week window size is
// configurable (WithVelocityWeeks) rather than fixed at the default.
func TestVelocityWeekCountConfigurable(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, loc)
	app := newTestAppWith(t, jira.NewFakeClient(), now, web.WithVelocityWeeks(4))

	body := get(t, app.URL+"/velocity")
	if n := strings.Count(body, `data-testid="velocity-week"`); n != 4 {
		t.Errorf("expected 4 weekly bars with WithVelocityWeeks(4), got %d\n%s", n, body)
	}
	// The window is KW26 .. KW29; older weeks must not appear.
	if strings.Contains(body, `data-week="KW25"`) {
		t.Errorf("4-week window should not include KW25:\n%s", body)
	}
	assertOrder(t, body, `data-week="KW26"`, `data-week="KW27"`, `data-week="KW28"`, `data-week="KW29"`)
}

// TestVelocityReachableFromNow asserts the Velocity view is linked from an
// existing page so it is reachable (full cross-view nav is a later ticket).
func TestVelocityReachableFromNow(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	body := get(t, app.URL+"/")
	if !strings.Contains(body, `href="/velocity"`) {
		t.Errorf("Now page must link to the Velocity view:\n%s", body)
	}
}
