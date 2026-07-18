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

	// Once settled, the status endpoint reports the idle (synced) state.
	if statusBody := get(t, ts.URL+"/resync/status"); !strings.Contains(statusBody, `data-testid="resync-idle"`) {
		t.Errorf("GET /resync/status not idle after completion:\n%s", statusBody)
	}
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
