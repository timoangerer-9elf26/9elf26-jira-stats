package web_test

// Integration tests for the full-resync button (#52), driven over the HTTP seam.
//
// The button lives in the shared nav so it is present on every view; POST
// /resync kicks off a background rebuild of the projection from Jira and returns
// promptly with an in-progress state, and GET /resync/status reports progress.

import (
	"context"
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

// testResyncer adapts a *sync.Syncer to web.Resyncer, binding a background
// context (never a request context) as the app would in main.
type testResyncer struct {
	ctx    context.Context
	syncer *sync.Syncer
}

func (r testResyncer) Resync() bool    { return r.syncer.TriggerResync(r.ctx) }
func (r testResyncer) Resyncing() bool { return r.syncer.Resyncing() }

// TestResyncButtonPresentOnAllViews asserts the resync control is in the shared
// nav, so it renders on every view.
func TestResyncButtonPresentOnAllViews(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	for _, path := range []string{"/", "/board", "/daily", "/sprint", "/velocity"} {
		body := get(t, app.URL+path)
		if !strings.Contains(body, `data-testid="resync-button"`) {
			t.Errorf("%s: missing resync button", path)
		}
	}
}

// TestResyncTriggersRebuildAtHTTPSeam wires the real sync engine behind the
// server and asserts POST /resync repopulates the projection from the fake
// backend: a stale issue absent from the fake is dropped, and the fake's issues
// land. The POST returns promptly with an in-progress state.
func TestResyncTriggersRebuildAtHTTPSeam(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// A stale snapshot that the fake dataset does not contain: a correct resync
	// (clear + re-backfill) must drop it.
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-STALE", Type: "Task", Summary: "gone next resync", Status: "In Progress",
		StatusCategory: "In Progress",
	}, "2026-07-01T00:00:00Z"); err != nil {
		t.Fatalf("seed stale issue: %v", err)
	}

	fake := jira.NewFakeClient()
	syncer := sync.NewSyncer(fake, st, time.Minute)
	srv, err := web.NewServer(st, web.WithResyncer(testResyncer{ctx: context.Background(), syncer: syncer}))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.PostForm(ts.URL+"/resync", url.Values{})
	if err != nil {
		t.Fatalf("POST /resync: %v", err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resync: status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `data-testid="resync-running"`) {
		t.Errorf("POST /resync did not report an in-progress state:\n%s", body)
	}

	// The rebuild runs in the background; wait for it to settle.
	deadline := time.Now().Add(5 * time.Second)
	for syncer.Resyncing() || time.Now().Before(deadline) {
		if !syncer.Resyncing() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if syncer.Resyncing() {
		t.Fatal("resync did not finish within timeout")
	}

	// The fake's canned dataset does not include DCAI-STALE, so a count matching
	// the fake's issue set proves the stale row was cleared and the fake's issues
	// repopulated (the exact stale-key drop is asserted at the store seam).
	count, err := st.IssueCount()
	if err != nil {
		t.Fatalf("issue count: %v", err)
	}
	if want := len(fake.Issues); count != want {
		t.Errorf("issue count after resync = %d, want %d (the fake's dataset)", count, want)
	}
	if count <= 1 {
		t.Errorf("projection not rebuilt: count %d did not grow past the single stale row", count)
	}

	// Once settled, the status endpoint no longer reports the running state and the
	// tooltip's last-full-resync flips from "never" to a relative time.
	statusBody := get(t, ts.URL+"/resync/status")
	if strings.Contains(statusBody, `data-testid="resync-running"`) {
		t.Errorf("GET /resync/status still running after completion:\n%s", statusBody)
	}
	if strings.Contains(statusBody, `<span data-testid="resync-last-full">never</span>`) {
		t.Errorf("last full resync still \"never\" after a successful resync:\n%s", statusBody)
	}
}

// TestResyncButtonIsIconWithTooltip asserts the resync control renders as an
// inline-SVG refresh icon (no text label) with an accessible name, and that the
// old native "Resync full database" title tooltip is gone (replaced by the
// CSS hover/focus tooltip fragment) (#82).
func TestResyncButtonIsIconWithTooltip(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	body := get(t, app.URL+"/sprint")

	button := resyncButtonMarkup(t, body)
	if !strings.Contains(button, "<svg") {
		t.Errorf("resync button is not an inline SVG icon:\n%s", button)
	}
	if strings.Contains(button, ">Resync</button>") {
		t.Errorf("resync button still renders the text label instead of an icon:\n%s", button)
	}
	if !strings.Contains(button, `aria-label=`) {
		t.Errorf("resync button missing an accessible name:\n%s", button)
	}
	// No CDN/icon-font dependency — the icon must be inline SVG.
	if strings.Contains(body, "font-awesome") || strings.Contains(body, "cdn.") {
		t.Errorf("resync icon must be inline SVG, not a CDN/icon-font:\n%s", body)
	}
}

// TestResyncNoFreshnessLabel asserts the old "Synced … ago" data-freshness label
// (MAX(synced_at)) is gone from the resync control fragment, replaced by the
// tooltip with the two dedicated timestamps and the button explanation (#82).
func TestResyncNoFreshnessLabel(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	status := get(t, app.URL+"/resync/status")

	if strings.Contains(status, "Synced ") || strings.Contains(status, "Not synced yet") {
		t.Errorf("data-freshness label still present; want it removed:\n%s", status)
	}
	for _, want := range []string{
		`data-testid="resync-tooltip"`,
		`data-testid="resync-last-full"`,
		`data-testid="resync-last-incremental"`,
		`data-testid="resync-explain"`,
	} {
		if !strings.Contains(status, want) {
			t.Errorf("resync tooltip missing %q:\n%s", want, status)
		}
	}
}

// TestResyncTooltipNeverThenRelativeTimes asserts the tooltip reads "never" for
// the last full resync before any has run and shows the two relative times once
// both timestamps are recorded — rendered against a pinned clock so the labels
// are deterministic (#82).
func TestResyncTooltipNeverThenRelativeTimes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	srv, err := web.NewServer(st, web.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Before any full resync: last full resync reads "never".
	before := get(t, ts.URL+"/resync/status")
	if !strings.Contains(before, `<span data-testid="resync-last-full">never</span>`) {
		t.Errorf("last full resync should read \"never\" before the first one:\n%s", before)
	}

	// Record both heartbeats and re-read: relative times appear.
	if err := st.SetLastFullResync(now.Add(-3 * time.Hour)); err != nil {
		t.Fatalf("set last full resync: %v", err)
	}
	if err := st.SetLastSync(now.Add(-45 * time.Second)); err != nil {
		t.Fatalf("set last sync: %v", err)
	}
	after := get(t, ts.URL+"/resync/status")
	if !strings.Contains(after, `<span data-testid="resync-last-full">3h ago</span>`) {
		t.Errorf("last full resync should read \"3h ago\":\n%s", after)
	}
	if !strings.Contains(after, `<span data-testid="resync-last-incremental">45s ago</span>`) {
		t.Errorf("last incremental sync should read \"45s ago\":\n%s", after)
	}
}

// TestResyncControlAlwaysPolls asserts the control fragment self-refreshes even
// when idle (always-on poll ~30s), not only while a resync runs (#82).
func TestResyncControlAlwaysPolls(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	status := get(t, app.URL+"/resync/status")
	if !strings.Contains(status, `data-testid="resync-poll"`) {
		t.Errorf("idle fragment missing the always-on poller:\n%s", status)
	}
	if !strings.Contains(status, "hx-get=\"/resync/status\"") {
		t.Errorf("idle poller does not target /resync/status:\n%s", status)
	}
}

// TestResyncIconSpinWiredToRunningState guards the static CSS wiring that makes
// the icon spin while a resync runs: a rotate keyframe, plus a single rule that
// ties the running fragment (data-testid="resync-running") to the button's svg
// via an animation. It cannot observe the runtime spin itself (CSS applied to a
// swapped-in DOM state) — that is verified live in acceptance-review; this only
// asserts the wiring ships and stays keyed off the running state (#67).
func TestResyncIconSpinWiredToRunningState(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())
	body := get(t, app.URL+"/sprint")

	if !strings.Contains(body, "@keyframes") {
		t.Errorf("no spin keyframes shipped with the resync control:\n%s", body)
	}
	// One rule must connect the running-state hook to the button icon's animation;
	// assert the whole selector→animation shape, not three incidental substrings.
	style := body
	if i := strings.Index(style, "<style>"); i >= 0 {
		style = style[i:]
	}
	rule := `[data-resync-control]:has([data-testid="resync-running"]) [data-testid="resync-button"] svg {
    animation: resync-spin`
	if !strings.Contains(style, rule) {
		t.Errorf("icon spin is not wired from the running state to the button svg animation:\n%s", style)
	}
}

// resyncButtonMarkup returns the <button data-testid="resync-button" …>…</button>
// substring of body, failing the test if it is absent.
func resyncButtonMarkup(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<button type="submit" data-testid="resync-button"`)
	if start < 0 {
		t.Fatalf("resync button not found in body:\n%s", body)
	}
	end := strings.Index(body[start:], "</button>")
	if end < 0 {
		t.Fatalf("resync button not closed in body:\n%s", body)
	}
	return body[start : start+end+len("</button>")]
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(b)
}
