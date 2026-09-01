package web_test

// Integration tests for Board drag-and-drop transitions (#195, docs/adr/0010)
// over the HTTP seam. Dragging is a browser gesture, so what is testable here is
// everything either side of it: the markup that declares which columns take part
// (the five legal ones) and which are frozen out, and POST /board/move — the
// write, which resolves the transition by TARGET STATUS ID, re-reads the issue,
// persists it and re-renders the board fragment through the request's filters.
// A failed write must change nothing and answer with a generic message.

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

// dragColumns are the five columns that take part in dragging (#195), in board
// order; frozenColumns are the two Board columns deliberately left out of the
// drag system entirely (neither drop target nor drag source).
var (
	dragColumns   = []string{"Refinement", "Ready To Do", "In Progress", "Review / Testing", "DONE (This Sprint)"}
	frozenColumns = []string{"Ready for Release", "Released / Deployed"}
)

// moveFixture has one card in every Board column, so a test can assert both the
// draggable and the frozen columns from a single render.
func moveFixture() *jira.FakeClient {
	active := func(key, status, category string) jira.Issue {
		return jira.Issue{Key: key, Type: "Task", Summary: "Card " + key, Status: status,
			StatusCategory: category, ActiveSprint: "KW29"}
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		active("DCAI-1", "Refinement", "To Do"),
		active("DCAI-2", "Ready To Do", "To Do"),
		active("DCAI-3", "In Progress", "In Progress"),
		active("DCAI-4", "Review / Testing", "In Progress"),
		active("DCAI-5", "DONE (This Sprint)", "Done"),
		active("DCAI-6", "Ready for Release", "Done"),
		active("DCAI-7", "Released / Deployed", "Done"),
	}}
}

// newMoveApp serves the handlers wired with a real Syncer as the Board
// Transitioner, so POST /board/move exercises the whole
// transition → re-read → persist path against the fake Jira.
func newMoveApp(t *testing.T, fake *jira.FakeClient, opts ...web.Option) *testApp {
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
	opts = append(opts, web.WithTransitioner(sync.NewSyncer(fake, st, time.Minute)))
	srv, err := web.NewServer(st, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// columnOf reports the data-status of the board column whose markup contains the
// given card, or "" when the card is not on the board at all.
func columnOf(t *testing.T, body, key string) string {
	t.Helper()
	const marker = `data-testid="board-column"`
	chunks := strings.Split(body, marker)
	for _, chunk := range chunks[1:] {
		status := attrValue(chunk, `data-status="`)
		if strings.Contains(chunk, `data-key="`+key+`"`) {
			return status
		}
	}
	return ""
}

func attrValue(chunk, prefix string) string {
	i := strings.Index(chunk, prefix)
	if i < 0 {
		return ""
	}
	rest := chunk[i+len(prefix):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// TestBoardDeclaresTheDragSurface asserts the rendered Board marks exactly the
// five legal columns as drag participants, freezes the other two (their cards
// are not draggable either), and loads the vendored drag library and the board
// drag script from /static — never a CDN.
func TestBoardDeclaresTheDragSurface(t *testing.T) {
	app := newMoveApp(t, moveFixture())
	body := get(t, app.URL+"/board")

	for _, status := range dragColumns {
		if !columnHasDragTarget(body, status) {
			t.Errorf("column %q is not marked as a drag target\n", status)
		}
	}
	for _, status := range frozenColumns {
		if columnHasDragTarget(body, status) {
			t.Errorf("column %q must not be a drag target", status)
		}
	}
	// The frozen columns' cards cannot be dragged either (one rule, not two).
	for _, key := range []string{"DCAI-6", "DCAI-7"} {
		if !cardIsDragLocked(body, key) {
			t.Errorf("card %s in a frozen column must not be draggable", key)
		}
	}
	for _, key := range []string{"DCAI-1", "DCAI-3", "DCAI-5"} {
		if cardIsDragLocked(body, key) {
			t.Errorf("card %s in a legal column must stay draggable", key)
		}
	}

	for _, want := range []string{`src="/static/sortable.min.js"`, `src="/static/board-drag.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Board does not load %s", want)
		}
	}
	if strings.Contains(body, "cdn.jsdelivr") || strings.Contains(body, "unpkg.com") {
		t.Errorf("Board references a CDN; the drag library must be vendored and embedded")
	}
}

func columnHasDragTarget(body, status string) bool {
	const marker = `data-testid="board-column"`
	for _, chunk := range strings.Split(body, marker)[1:] {
		if attrValue(chunk, `data-status="`) == status {
			return strings.Contains(chunk[:min(len(chunk), 400)], `data-drag-target="true"`)
		}
	}
	return false
}

func cardIsDragLocked(body, key string) bool {
	i := strings.Index(body, `data-key="`+key+`"`)
	if i < 0 {
		return false
	}
	start := strings.LastIndex(body[:i], "<")
	end := strings.Index(body[i:], ">")
	if start < 0 || end < 0 {
		return false
	}
	return strings.Contains(body[start:i+end], `draggable="false"`)
}

// TestBoardMoveWritesTheTransitionForTheTargetStatus asserts a move picks the
// transition by target status ID (not by its ambiguous name), re-reads the issue
// and re-renders the board with the card in its new column, and that the
// projection carries the new status afterwards.
func TestBoardMoveWritesTheTransitionForTheTargetStatus(t *testing.T) {
	fake := moveFixture()
	app := newMoveApp(t, fake)

	code, body := postForm(t, app.URL+"/board/move",
		url.Values{"key": {"DCAI-3"}, "status": {"DONE (This Sprint)"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/move: status %d, want 200\n%s", code, body)
	}
	// Transition id 5 lands in DONE (This Sprint); id 31 is the decoy labelled
	// "Done" that lands in Ready for release (docs/adr/0010).
	if len(fake.TransitionCalls) != 1 {
		t.Fatalf("transition calls = %v, want exactly one", fake.TransitionCalls)
	}
	if got := fake.TransitionCalls[0]; got.Key != "DCAI-3" || got.TransitionID != "5" {
		t.Errorf("transition call = %+v, want {DCAI-3 5}", got)
	}
	if got := columnOf(t, body, "DCAI-3"); got != "DONE (This Sprint)" {
		t.Errorf("after the move the card renders in column %q, want DONE (This Sprint)", got)
	}
	// The response is the whole board panel, so the swap re-renders the board.
	if !strings.Contains(body, `data-testid="board-card-strip"`) {
		t.Errorf("move response is not the board panel fragment\n%s", body)
	}
	// The projection reflects the authoritative re-read.
	if got := columnOf(t, get(t, app.URL+"/board"), "DCAI-3"); got != "DONE (This Sprint)" {
		t.Errorf("a fresh Board shows the card in %q, want DONE (This Sprint)", got)
	}
}

// TestBoardMoveAllowsEveryOrderedPair asserts all twenty ordered pairs among the
// five legal columns are permitted.
func TestBoardMoveAllowsEveryOrderedPair(t *testing.T) {
	for _, from := range dragColumns {
		for _, to := range dragColumns {
			if from == to {
				continue
			}
			t.Run(from+"→"+to, func(t *testing.T) {
				fake := moveFixture()
				app := newMoveApp(t, fake)
				key := keyInColumn(t, fake, from)

				code, body := postForm(t, app.URL+"/board/move",
					url.Values{"key": {key}, "status": {to}})
				if code != http.StatusOK {
					t.Fatalf("POST %s %s→%s: status %d", key, from, to, code)
				}
				if got := columnOf(t, body, key); got != to {
					t.Errorf("%s %s→%s landed in %q", key, from, to, got)
				}
			})
		}
	}
}

func keyInColumn(t *testing.T, fake *jira.FakeClient, status string) string {
	t.Helper()
	for _, iss := range fake.Issues {
		if iss.Status == status {
			return iss.Key
		}
	}
	t.Fatalf("fixture has no issue in %q", status)
	return ""
}

// TestBoardMoveRejectsTargetsOutsideTheDragSurface asserts the excluded Board
// columns and the two off-board statuses are unreachable by dragging — the
// server refuses them outright and writes nothing, even though Jira offers those
// transitions.
func TestBoardMoveRejectsTargetsOutsideTheDragSurface(t *testing.T) {
	for _, status := range []string{"Ready for Release", "Released / Deployed", "Canceled", "Triage", "", "Done", "nonsense"} {
		t.Run("target="+status, func(t *testing.T) {
			fake := moveFixture()
			app := newMoveApp(t, fake)
			code, _ := postForm(t, app.URL+"/board/move",
				url.Values{"key": {"DCAI-3"}, "status": {status}})
			if code != http.StatusBadRequest {
				t.Errorf("POST target %q: status %d, want 400", status, code)
			}
			if len(fake.TransitionCalls) != 0 {
				t.Errorf("POST target %q wrote transitions %v, want none", status, fake.TransitionCalls)
			}
		})
	}
}

// TestBoardMoveRejectsAMissingKey asserts a move with no issue key is a 400.
func TestBoardMoveRejectsAMissingKey(t *testing.T) {
	app := newMoveApp(t, moveFixture())
	if code, _ := postForm(t, app.URL+"/board/move", url.Values{"status": {"In Progress"}}); code != http.StatusBadRequest {
		t.Errorf("POST with no key: status %d, want 400", code)
	}
}

// TestBoardMoveFailureChangesNothing asserts a failed Jira write answers with a
// non-OK status and a generic message (no technical detail), leaving the
// projection untouched so the card can snap back to its origin column.
func TestBoardMoveFailureChangesNothing(t *testing.T) {
	fake := moveFixture()
	fake.WriteErr = errors.New("jira says no (permissions)")
	app := newMoveApp(t, fake)

	code, body := postForm(t, app.URL+"/board/move",
		url.Values{"key": {"DCAI-3"}, "status": {"Review / Testing"}})
	if code == http.StatusOK {
		t.Fatalf("failed write answered 200; the client must be able to detect the failure")
	}
	if strings.Contains(body, "permissions") || strings.Contains(body, "jira says no") {
		t.Errorf("failure response leaks technical detail: %q", body)
	}
	if strings.TrimSpace(body) == "" {
		t.Errorf("failure response carries no message for the inline error")
	}
	if got := columnOf(t, get(t, app.URL+"/board"), "DCAI-3"); got != "In Progress" {
		t.Errorf("failed write moved the card to %q; the projection must be unchanged", got)
	}
}

// TestBoardMoveWithoutATransitionerFails asserts a server built without a write
// path reports the move as a failure rather than pretending it worked.
func TestBoardMoveWithoutATransitionerFails(t *testing.T) {
	app := newBoardApp(t, moveFixture())
	if code, _ := postForm(t, app.URL+"/board/move",
		url.Values{"key": {"DCAI-3"}, "status": {"Refinement"}}); code == http.StatusOK {
		t.Errorf("move without a transitioner answered 200, want a failure status")
	}
}

// TestBoardMoveRendersThroughTheActiveFilters asserts the re-rendered fragment
// is filtered exactly as the board the user is looking at: a moved card that no
// longer matches an active filter is simply absent, and the filter state
// round-trips into the swapped chrome.
func TestBoardMoveRendersThroughTheActiveFilters(t *testing.T) {
	fake := moveFixture()
	app := newMoveApp(t, fake)

	// ?q=DCAI-4 narrows the board to that one card; moving DCAI-3 still writes,
	// but the moved card does not match the active search and must not appear.
	code, body := postForm(t, app.URL+"/board/move",
		url.Values{"key": {"DCAI-3"}, "status": {"Review / Testing"}, "q": {"DCAI-4"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/move: status %d, want 200", code)
	}
	if strings.Contains(body, `data-key="DCAI-3"`) {
		t.Errorf("moved card is still rendered despite not matching the active filter\n%s", body)
	}
	if !strings.Contains(body, `data-key="DCAI-4"`) {
		t.Errorf("the matching card is missing from the re-rendered board\n%s", body)
	}
	if !strings.Contains(body, `value="DCAI-4"`) {
		t.Errorf("the active search term did not round-trip into the swapped chrome\n%s", body)
	}
	// The write itself still happened.
	if got := columnOf(t, get(t, app.URL+"/board"), "DCAI-3"); got != "Review / Testing" {
		t.Errorf("filtered move did not write: card is in %q", got)
	}
}
