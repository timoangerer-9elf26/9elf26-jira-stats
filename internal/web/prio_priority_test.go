package web_test

// Integration tests for the Prio view's priority edit (#212) over the HTTP seam:
// a row's priority is a popover of the five levels whose choices POST
// /prio/priority, which writes the level to Jira (by name), re-reads the issue
// and re-renders the WHOLE prio-panel — re-sorted, the edited row highlighted,
// the active filters preserved. A failed write (or no write seam) renders the
// same panel with the row unmoved and an inline error, leaving Jira and the
// projection untouched.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// newPriorityApp syncs the fixture into a temp store and serves the handlers
// wired with a real Syncer as the Prioritizer, so a POST /prio/priority runs the
// whole write→refetch→save path against the fake Jira's in-memory write.
func newPriorityApp(t *testing.T, fake *jira.FakeClient, opts ...web.Option) *testApp {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := sync.Once(context.Background(), fake, st); err != nil {
		t.Fatalf("sync: %v", err)
	}
	syncer := sync.NewSyncer(fake, st, time.Minute)
	opts = append(opts, web.WithPrioritizer(syncer))
	srv, err := web.NewServer(st, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// everyFilterOff is the form-encoded shape of prioEveryFilterOff: what the
// hidden [data-filterparam] inputs carry when every default-on filter is off.
func everyFilterOff(extra url.Values) url.Values {
	vals := url.Values{"not-done": {"0"}, "not-started": {"0"}, "non-technical": {"0"}}
	for k, v := range extra {
		vals[k] = v
	}
	return vals
}

// The cell is a real <button> opening a native popover of exactly the five
// levels, each an hx-post to /prio/priority carrying the active filters and
// swapping the whole panel; no clear/none choice.
func TestPrioPriorityCellIsAnEditablePopoverOfFiveLevels(t *testing.T) {
	app := newPriorityApp(t, prioFixture())
	for _, path := range []string{"/prio?" + prioEveryFilterOff, "/prio/results?" + prioEveryFilterOff} {
		body := get(t, app.URL+path)
		for _, want := range []string{
			`<button type="button"`,
			`popovertarget="pri-menu-DCAI-3"`,
			`id="pri-menu-DCAI-3" popover`,
			`data-testid="prio:DCAI-3:priority-trigger"`,
			`data-testid="prio:DCAI-3:priority-menu"`,
			`data-testid="prio:DCAI-3:priority-opt:highest"`,
			`data-testid="prio:DCAI-3:priority-opt:high"`,
			`data-testid="prio:DCAI-3:priority-opt:medium"`,
			`data-testid="prio:DCAI-3:priority-opt:low"`,
			`data-testid="prio:DCAI-3:priority-opt:lowest"`,
			`hx-post="/prio/priority"`,
			`hx-include="[data-filterparam]"`,
			`hx-target="#prio-panel"`,
			`hx-indicator="#prio-panel"`,
			// The current value still reads as before (read state preserved).
			`data-testid="prio:DCAI-3:priority-name">Low<`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: priority control missing %q\n%s", path, want, body)
			}
		}
		if n := strings.Count(body, `data-testid="prio:DCAI-3:priority-opt:`); n != 5 {
			t.Errorf("%s: DCAI-3 menu offers %d choices, want exactly 5", path, n)
		}
		for _, absent := range []string{`priority-opt:none`, `No priority`, `Clear priority`} {
			if strings.Contains(body, absent) {
				t.Errorf("%s: menu must not offer a clear/none choice (%q)\n%s", path, absent, body)
			}
		}
		// The per-row anchor names ride in inline styles; html/template blanks an
		// unsafe CSS value to ZgotmplZ, so make sure the keys survive its filter.
		if strings.Contains(body, "ZgotmplZ") {
			t.Errorf("%s: html/template rejected an inline style value\n%s", path, body)
		}
		if !strings.Contains(body, `style="anchor-name:--pri-DCAI-3"`) || !strings.Contains(body, `style="position-anchor:--pri-DCAI-3"`) {
			t.Errorf("%s: popover is not anchored to its trigger\n%s", path, body)
		}
		// The menu's icons must not masquerade as the row's own icon.
		if n := strings.Count(body, `data-testid="prio:DCAI-3:priority-icon"`); n != 1 {
			t.Errorf("%s: DCAI-3 renders %d row icons, want 1", path, n)
		}
	}
	// The popover styling ships once per page, outside the swapped panel.
	page := get(t, app.URL+"/prio")
	if strings.Count(page, `.pri-row--edited td {`) != 1 {
		t.Errorf("Prio page should carry the priority popover style block exactly once\n%s", page)
	}
	if strings.Contains(get(t, app.URL+"/prio/results"), `.pri-row--edited td {`) {
		t.Errorf("the results fragment must not re-ship the style block")
	}
}

// Picking a level writes it to Jira and the response is the whole panel,
// re-sorted so the row sits at its new rank (ties by key), highlighted.
func TestPrioPriorityWriteResortsAndHighlightsTheRow(t *testing.T) {
	fake := prioFixture()
	app := newPriorityApp(t, fake)

	code, body := postForm(t, app.URL+"/prio/priority",
		everyFilterOff(url.Values{"key": {"DCAI-3"}, "priority": {"Highest"}}))
	if code != http.StatusOK {
		t.Fatalf("POST /prio/priority: status %d, want 200", code)
	}
	// Whole panel: chrome + filters + table.
	for _, want := range []string{`data-testid="page-chrome"`, `data-testid="prio-filters"`, `data-testid="prio"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response is not the whole panel, missing %q\n%s", want, body)
		}
	}
	// DCAI-3 (was Low) now ranks among the Highest, between DCAI-2 and DCAI-4 by key.
	assertOrder(t, body,
		`data-key="DCAI-2"`,
		`data-key="DCAI-3"`,
		`data-key="DCAI-4"`,
		`data-key="DCAI-6"`,
		`data-key="DCAI-1"`,
		`data-key="DCAI-5"`,
		`data-key="DCAI-7"`,
	)
	if !strings.Contains(body, `data-testid="prio:DCAI-3:priority-name">Highest<`) {
		t.Errorf("edited row does not show the authoritative value Highest\n%s", body)
	}
	if !strings.Contains(body, `data-key="DCAI-3" data-edited="true"`) {
		t.Errorf("edited row is not highlighted\n%s", body)
	}
	if n := strings.Count(body, `data-edited="true"`); n != 1 {
		t.Errorf("%d rows highlighted, want exactly the edited one", n)
	}
	if strings.Contains(body, `priority-error"`) {
		t.Errorf("successful write must not render an inline error\n%s", body)
	}
	// Jira took the write; the projection reflects it on a fresh load, unhighlighted.
	if iss, _ := fake.FetchIssue(context.Background(), "DCAI-3"); iss.Priority != "Highest" {
		t.Errorf("Jira-side priority = %q, want Highest", iss.Priority)
	}
	reload := get(t, app.URL+"/prio?"+prioEveryFilterOff)
	if !strings.Contains(reload, `data-testid="prio:DCAI-3:priority-name">Highest<`) {
		t.Errorf("projection did not persist Highest\n%s", reload)
	}
	if strings.Contains(reload, `data-edited`) {
		t.Errorf("highlight must be per-response, not persisted\n%s", reload)
	}
}

// The request carries the active filters, so the re-rendered panel keeps them
// applied and their toggles unchanged — indistinguishable from a filter toggle.
func TestPrioPriorityWritePreservesActiveFilters(t *testing.T) {
	app := newPriorityApp(t, prioFixture())

	// Non-default state: Not started OFF (In Progress work visible), No parent ON.
	vals := url.Values{"key": {"DCAI-1"}, "priority": {"Lowest"}, "not-started": {"0"}, "no-parent": {"1"}}
	code, body := postForm(t, app.URL+"/prio/priority", vals)
	if code != http.StatusOK {
		t.Fatalf("POST /prio/priority: status %d, want 200", code)
	}
	assertToggle(t, body, "prio-not-started", false, "/prio/results")
	assertToggle(t, body, "prio-no-parent", true, "/prio/results")
	assertToggle(t, body, "prio-not-done", true, "/prio/results?not-done=0")
	for _, want := range []string{
		`<input type="hidden" data-filterparam name="not-started" value="0">`,
		`<input type="hidden" data-filterparam name="no-parent" value="1">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("filter state not re-emitted: %q\n%s", want, body)
		}
	}
	// Filters applied: In Progress rows show (Not started off), the released
	// DCAI-5 and the Technical DCAI-4 stay hidden.
	assertPrioRows(t, body, []string{"DCAI-1", "DCAI-2"}, []string{"DCAI-5", "DCAI-4"})
	if !strings.Contains(body, `data-key="DCAI-1" data-edited="true"`) {
		t.Errorf("edited row not highlighted under non-default filters\n%s", body)
	}

	// Without any filter params the panel comes back at its defaults.
	_, dflt := postForm(t, app.URL+"/prio/priority", url.Values{"key": {"DCAI-6"}, "priority": {"Low"}})
	assertToggle(t, dflt, "prio-not-started", true, "/prio/results?not-started=0")
	assertToggle(t, dflt, "prio-no-parent", false, "/prio/results?no-parent=1")
}

// A failed write changes nothing in Jira or the projection; the panel comes back
// with the row at its old rank and an inline error on that row, no banner.
func TestPrioPriorityFailureLeavesRowAndShowsInlineError(t *testing.T) {
	fake := prioFixture()
	fake.WriteErr = errors.New("jira says no (permissions)")
	app := newPriorityApp(t, fake)

	code, body := postForm(t, app.URL+"/prio/priority",
		everyFilterOff(url.Values{"key": {"DCAI-3"}, "priority": {"Highest"}}))
	if code != http.StatusOK {
		t.Fatalf("POST /prio/priority: status %d, want 200", code)
	}
	// Unmoved: still Low, still after DCAI-1 (Medium).
	assertOrder(t, body, `data-key="DCAI-1"`, `data-key="DCAI-3"`, `data-key="DCAI-5"`)
	if !strings.Contains(body, `data-testid="prio:DCAI-3:priority-name">Low<`) {
		t.Errorf("failed write did not keep the prior value Low\n%s", body)
	}
	if !strings.Contains(body, `data-testid="prio:DCAI-3:priority-error"`) {
		t.Errorf("failed write did not render an inline error on the row\n%s", body)
	}
	if n := strings.Count(body, `priority-error"`); n != 1 {
		t.Errorf("%d inline errors rendered, want exactly one on the edited row", n)
	}
	if strings.Contains(body, `data-edited="true"`) {
		t.Errorf("a failed write must not highlight the row as edited\n%s", body)
	}
	if iss, _ := fake.FetchIssue(context.Background(), "DCAI-3"); iss.Priority != "Low" {
		t.Errorf("failed write reached Jira: %q", iss.Priority)
	}
	if reload := get(t, app.URL+"/prio?"+prioEveryFilterOff); !strings.Contains(reload, `data-testid="prio:DCAI-3:priority-name">Low<`) {
		t.Errorf("failed write leaked into the projection\n%s", reload)
	}
}

// With no write seam wired the edit is reported as a failure, never silently
// dropped or shown as a success.
func TestPrioPriorityWithoutSeamReportsFailure(t *testing.T) {
	app := newTestApp(t, prioFixture()) // no WithPrioritizer

	code, body := postForm(t, app.URL+"/prio/priority",
		everyFilterOff(url.Values{"key": {"DCAI-3"}, "priority": {"Highest"}}))
	if code != http.StatusOK {
		t.Fatalf("POST /prio/priority: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="prio:DCAI-3:priority-error"`) {
		t.Errorf("no seam wired, yet no inline error\n%s", body)
	}
	if !strings.Contains(body, `data-testid="prio:DCAI-3:priority-name">Low<`) || strings.Contains(body, `data-edited="true"`) {
		t.Errorf("no seam wired, yet the edit appeared to succeed\n%s", body)
	}
}

// A missing key or a level outside the five is a 400, never a write.
func TestPrioPriorityRejectsBadRequest(t *testing.T) {
	fake := prioFixture()
	app := newPriorityApp(t, fake)
	for _, vals := range []url.Values{
		{"priority": {"High"}},                        // no key
		{"key": {"DCAI-3"}},                           // no priority
		{"key": {"DCAI-3"}, "priority": {""}},         // empty = "clear", which does not exist
		{"key": {"DCAI-3"}, "priority": {"Critical"}}, // not one of the five
		{"key": {"DCAI-3"}, "priority": {"highest"}},  // case matters (Jira names)
	} {
		if code, _ := postForm(t, app.URL+"/prio/priority", vals); code != http.StatusBadRequest {
			t.Errorf("POST %v: status %d, want 400", vals, code)
		}
	}
	if iss, _ := fake.FetchIssue(context.Background(), "DCAI-3"); iss.Priority != "Low" {
		t.Errorf("a rejected request reached Jira: %q", iss.Priority)
	}
}
