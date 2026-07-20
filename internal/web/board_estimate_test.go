package web_test

// Integration tests for the editable Board estimate pill (#108, docs/adr/0005)
// over the HTTP seam: on the Board the size pill is an interactive popover whose
// choices POST /board/estimate, which writes the size back to Jira, re-reads the
// issue and swaps the pill to the authoritative value; a failed write reverts the
// pill and shows an inline error, leaving the projection unchanged. The same pill
// stays read-only display everywhere else (Daily / created-list / Sprint
// drill-down), i.e. editability does not leak in via the shared board-card.

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

// newEstimateApp syncs the fixture into a temp store and serves the handlers
// wired with a real Syncer as the Board estimate Estimator, so a POST
// /board/estimate exercises the whole write→refetch→save path against the fake
// Jira (its in-memory write). Returns the app and the backing fake so a test can
// inspect Jira-side state or arm a write failure.
func newEstimateApp(t *testing.T, fake *jira.FakeClient, opts ...web.Option) *testApp {
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
	opts = append(opts, web.WithEstimator(syncer))
	srv, err := web.NewServer(st, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// postForm posts form values and returns the status code and body.
func postForm(t *testing.T, url string, vals url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(url, vals)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp.StatusCode, readAll(t, resp)
}

// TestBoardEstimatePillIsEditable asserts the Board renders the size pill as an
// interactive popover offering S / M / L / No estimate, each choice posting to
// /board/estimate, while still displaying the card's current value.
func TestBoardEstimatePillIsEditable(t *testing.T) {
	app := newEstimateApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="card:DCAI-11:estimate"`,
		`hx-post="/board/estimate"`,
		`data-testid="card:DCAI-11:estimate-opt:s"`,
		`data-testid="card:DCAI-11:estimate-opt:m"`,
		`data-testid="card:DCAI-11:estimate-opt:l"`,
		`data-testid="card:DCAI-11:estimate-opt:none"`,
		`No estimate`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Board estimate control missing %q", want)
		}
	}
	// The current value is still shown in the pill (read state preserved).
	if !strings.Contains(body, `data-testid="card:DCAI-10:size">S<`) {
		t.Errorf("Board pill lost its current value S\n%s", body)
	}
}

// TestBoardEstimateWritePersistsAuthoritativeValue asserts selecting a value PUTs
// it to Jira and the returned fragment (and a subsequent Board load) shows the
// authoritative re-read value.
func TestBoardEstimateWritePersistsAuthoritativeValue(t *testing.T) {
	fake := boardFixture()
	app := newEstimateApp(t, fake)

	code, body := postForm(t, app.URL+"/board/estimate",
		url.Values{"key": {"DCAI-11"}, "size": {"L"}, "prior": {""}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/estimate: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-11:size">L<`) {
		t.Errorf("write response did not show the authoritative value L\n%s", body)
	}
	if strings.Contains(body, `data-testid="card:DCAI-11:estimate-error"`) {
		t.Errorf("successful write must not render an inline error\n%s", body)
	}

	// The projection now reflects the write (the pill on a fresh Board load is L).
	if reboard := get(t, app.URL+"/board"); !strings.Contains(reboard, `data-testid="card:DCAI-11:size">L<`) {
		t.Errorf("Board did not reflect the persisted size L\n%s", reboard)
	}
}

// TestBoardEstimateNoEstimateClears asserts choosing "No estimate" writes null and
// the pill shows "no estimate".
func TestBoardEstimateNoEstimateClears(t *testing.T) {
	fake := boardFixture()
	app := newEstimateApp(t, fake)

	code, body := postForm(t, app.URL+"/board/estimate",
		url.Values{"key": {"DCAI-10"}, "size": {""}, "prior": {"S"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/estimate: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-10:size">no estimate<`) {
		t.Errorf("clearing the estimate did not render \"no estimate\"\n%s", body)
	}
}

// TestBoardEstimateFailureRevertsWithInlineError asserts a failed Jira write
// reverts the pill to its prior value and shows an inline error on that card,
// leaving the projection unchanged.
func TestBoardEstimateFailureRevertsWithInlineError(t *testing.T) {
	fake := boardFixture()
	fake.WriteErr = errors.New("jira says no (permissions)")
	app := newEstimateApp(t, fake)

	code, body := postForm(t, app.URL+"/board/estimate",
		url.Values{"key": {"DCAI-12"}, "size": {"L"}, "prior": {"M"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/estimate: status %d, want 200", code)
	}
	// Pill reverts to the prior value (M), not the attempted L.
	if !strings.Contains(body, `data-testid="card:DCAI-12:size">M<`) {
		t.Errorf("failed write did not revert the pill to its prior value M\n%s", body)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-12:estimate-error"`) {
		t.Errorf("failed write did not render an inline error\n%s", body)
	}

	// The projection is untouched: a fresh Board load still shows M.
	if reboard := get(t, app.URL+"/board"); !strings.Contains(reboard, `data-testid="card:DCAI-12:size">M<`) {
		t.Errorf("failed write leaked into the projection (should still be M)\n%s", reboard)
	}
}

// TestBoardEstimateRejectsBadRequest asserts a missing key or an out-of-range size
// is a 400, never a write.
func TestBoardEstimateRejectsBadRequest(t *testing.T) {
	app := newEstimateApp(t, boardFixture())
	for _, vals := range []url.Values{
		{"size": {"L"}},                         // no key
		{"key": {"DCAI-11"}, "size": {"XL"}},    // bad size
		{"key": {"DCAI-11"}, "size": {"Small"}}, // Jira value, not the T-shirt label
	} {
		if code, _ := postForm(t, app.URL+"/board/estimate", vals); code != http.StatusBadRequest {
			t.Errorf("POST %v: status %d, want 400", vals, code)
		}
	}
}

// TestEstimatePillReadOnlyOffBoard asserts the same size pill stays read-only on
// the Sprint drill-down (which reuses the board-card partial): no /board/estimate
// affordance leaks in there.
func TestEstimatePillReadOnlyOffBoard(t *testing.T) {
	app := drillFixtureApp(t)
	body := get(t, app.URL+"/sprint/cell?row=started&col=total")

	if !strings.Contains(body, `data-testid="card:DCAI-1:size">M<`) {
		t.Errorf("Sprint drill-down card lost its read-only size chip\n%s", body)
	}
	if strings.Contains(body, "/board/estimate") {
		t.Errorf("editable estimate control leaked into the Sprint drill-down\n%s", body)
	}
	if strings.Contains(body, `data-testid="card:DCAI-1:estimate"`) {
		t.Errorf("Sprint drill-down must not render the editable estimate control\n%s", body)
	}
}
