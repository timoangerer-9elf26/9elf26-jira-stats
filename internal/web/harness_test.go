package web_test

// Reusable integration-test harness (walking-skeleton pattern for every later
// ticket).
//
// The seam under test is the HTTP boundary: a real net/http handler, backed by
// a real (temp) SQLite store, fed by a real one-shot sync from a fake Jira. No
// test reaches into private internals; assertions are on rendered HTML and, in
// later tickets, on the SQLite projection.
//
// To reuse this in a later ticket:
//   1. Build a fixture: jira.NewFakeClient() for the canned dataset, or a
//      &jira.FakeClient{Issues: ...} you construct inline for a targeted case.
//   2. newTestApp(t, client) opens a temp DB, runs the sync, and returns an
//      httptest.Server plus the store for direct projection assertions.
//   3. Drive it with real HTTP requests (http.Get(app.URL + "/...")) and assert
//      on the response.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// kw29Activated is the activation instant of the standard active-sprint fixture.
var kw29Activated = time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)

// activeSprintKW29 is the active-sprint entity the view fixtures pair with issues
// carrying ActiveSprint="KW29", so ActiveSprintWindow reports it (name +
// activation instant). Membership stays on the issues; this supplies the entity.
func activeSprintKW29() []jira.Sprint {
	return []jira.Sprint{{ID: 29, Name: "KW29", State: "active", ActivatedAt: kw29Activated}}
}

type testApp struct {
	*httptest.Server
	Store *store.Store
}

// newTestApp opens a fresh temp SQLite DB, syncs the given fake Jira into it,
// and starts the real handlers on an httptest server. The server and DB are
// cleaned up automatically when the test ends.
func newTestApp(t *testing.T, client jira.Client) *testApp {
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

	srv, err := web.NewServer(st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return &testApp{Server: ts, Store: st}
}
