package web_test

// Integration tests for the v1 wire-up polish: the shared cross-view navigation
// and the friendly empty/error states, all driven over the HTTP seam.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// newEmptyTestApp starts the real handlers over a freshly opened temp store that
// has never been synced — the "before first sync" cold-start state a developer
// sees on first run.
func newEmptyTestApp(t *testing.T) *testApp {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := web.NewServer(st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// TestSharedNavRendersWithActiveItem asserts every view renders the one shared
// nav (links to all three views) and marks the current view active.
func TestSharedNavRendersWithActiveItem(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	cases := []struct{ path, activeKey string }{
		{"/", "now"},
		{"/board", "board"},
		{"/sprint", "sprint"},
		{"/velocity", "velocity"},
	}
	for _, c := range cases {
		body := get(t, app.URL+c.path)

		if !strings.Contains(body, `data-testid="nav"`) {
			t.Errorf("%s: missing shared nav\n%s", c.path, body)
		}
		for _, link := range []string{`href="/"`, `href="/board"`, `href="/sprint"`, `href="/velocity"`} {
			if !strings.Contains(body, link) {
				t.Errorf("%s: nav missing link %q", c.path, link)
			}
		}
		// The active tab (and only it) carries aria-current="page".
		activeMarker := `data-nav="` + c.activeKey + `" aria-current="page"`
		if !strings.Contains(body, activeMarker) {
			t.Errorf("%s: active nav item not marked (%q)\n%s", c.path, activeMarker, body)
		}
		if n := strings.Count(body, `aria-current="page"`); n != 1 {
			t.Errorf("%s: expected exactly one active nav item, got %d", c.path, n)
		}
	}
}

// TestViewsRenderFriendlyEmptyStateBeforeFirstSync drives each view against a
// never-synced (empty) store and asserts a friendly empty state renders with a
// 200 and no panic, rather than a blank page or a 500.
func TestViewsRenderFriendlyEmptyStateBeforeFirstSync(t *testing.T) {
	app := newEmptyTestApp(t)

	cases := []struct{ path, want string }{
		{"/", "No open work"},
		{"/now/board", "No open work"},
		// A never-synced store has no active sprint, so Sprint shows the no-sprint
		// empty state (same treatment as the Board), not a finished-work note.
		{"/sprint", "No active sprint"},
		{"/velocity", "No completed work"},
	}
	for _, c := range cases {
		body := get(t, app.URL+c.path) // get() already fails on a non-200 status.
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: empty state missing %q\n%s", c.path, c.want, body)
		}
	}
}

// failingRollups is a Rollups whose queries always error, to exercise the
// friendly error state at the HTTP seam.
type failingRollups struct{}

func (failingRollups) OpenByStatus() (store.OpenBoard, error) {
	return store.OpenBoard{}, errBoom
}
func (failingRollups) CompletedInRange(_, _ time.Time) (store.SizeTally, error) {
	return store.SizeTally{}, errBoom
}
func (failingRollups) SprintCategoriesInWindow(_ int, _, _ time.Time) (store.SprintCategories, error) {
	return store.SprintCategories{}, errBoom
}
func (failingRollups) LastSyncedAt() (time.Time, bool, error) {
	return time.Time{}, false, errBoom
}
func (failingRollups) ActiveSprintWindow() (store.ActiveSprint, bool, error) {
	return store.ActiveSprint{}, false, errBoom
}
func (failingRollups) ActiveSprintBoard() (store.Board, error) {
	return store.Board{}, errBoom
}
func (failingRollups) DailyStatusChanges(_ string, _, _ time.Time) ([]store.DailyTicket, error) {
	return nil, errBoom
}
func (failingRollups) ActiveSprintAssignees() ([]string, error) {
	return nil, errBoom
}
func (failingRollups) IssuesCreatedInRange(_ string, _, _ time.Time) ([]store.CreatedTicket, error) {
	return nil, errBoom
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }

// TestRollupErrorRendersFriendlyMessage asserts a failing rollup query renders a
// clear message rather than leaking a stack trace.
func TestRollupErrorRendersFriendlyMessage(t *testing.T) {
	srv, err := web.NewServer(failingRollups{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	for _, path := range []string{"/", "/board", "/sprint", "/velocity"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s: expected 500 on rollup error, got %d", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "Couldn't load this view") {
			t.Errorf("%s: missing friendly error message\n%s", path, body)
		}
	}
}
