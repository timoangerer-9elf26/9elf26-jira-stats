package store

// Integration test for the real Jira client + full-project backfill.
//
// Seam: a fake Jira Cloud HTTP server (net/http/httptest) returns canned v3
// search + changelog JSON; the REAL jira.LiveClient fetches through it; a real
// sync.Backfill persists into a temp SQLite store. Assertions are on the
// resulting SQLite projection (snapshots + transition log), never on private
// client funcs.
//
// Covers the ticket's required cases:
//   - pagination (DCAI-3 only appears on the second search page)
//   - truncation -> per-issue /changelog fallback (DCAI-3's embedded changelog
//     is truncated; the full history is fetched and paginated)
//   - dedup on re-sync (a second backfill inserts no duplicate transitions)
//   - field mapping (size Large/Medium/none, type, status + category, sprint,
//     assignee) and Estimated-Time transitions alongside status transitions,
//     including two tracked items sharing one changelog entry id.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func TestBackfillProjectsFakeJiraIntoStore(t *testing.T) {
	jiraSrv := httptest.NewServer(http.HandlerFunc(fakeJira))
	t.Cleanup(jiraSrv.Close)

	client := jira.NewLiveClient(jira.Config{
		BaseURL:    jiraSrv.URL,
		Email:      "svc@example.com",
		APIToken:   "token",
		ProjectKey: "DCAI",
		BoardID:    "8",
	})

	st := openTempStore(t)

	n, err := sync.Backfill(context.Background(), client, st)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 3 {
		t.Fatalf("backfill persisted %d issues, want 3", n)
	}

	// --- Snapshots ---
	if got := countRows(t, st, "SELECT COUNT(*) FROM issue"); got != 3 {
		t.Fatalf("issue rows = %d, want 3", got)
	}

	one := readIssue(t, st, "DCAI-1")
	assertEq(t, "DCAI-1 type", one.typ, "Story")
	assertEq(t, "DCAI-1 summary", one.summary, "Wire up the dashboard shell")
	assertEq(t, "DCAI-1 status", one.status, "In Progress")
	assertEq(t, "DCAI-1 category", one.statusCategory, "In Progress")
	assertEq(t, "DCAI-1 size", one.size, "L")
	assertEq(t, "DCAI-1 sprint", one.sprint, "Sprint 42")
	// The active sprint entry (state=="active") is captured; the closed Sprint 41
	// entry it also carries is ignored.
	assertEq(t, "DCAI-1 active sprint", one.activeSprint, "Sprint 42")
	assertEq(t, "DCAI-1 assignee", one.assignee, "Ada")

	two := readIssue(t, st, "DCAI-2")
	assertEq(t, "DCAI-2 size", two.size, "M")
	assertEq(t, "DCAI-2 active sprint", two.activeSprint, "Sprint 42")
	assertEq(t, "DCAI-2 assignee (unassigned)", two.assignee, "")

	three := readIssue(t, st, "DCAI-3")
	assertEq(t, "DCAI-3 size (no estimate)", three.size, "")
	assertEq(t, "DCAI-3 category", three.statusCategory, "Done")
	// DCAI-3 is in no active sprint (empty sprint array) -> NULL active_sprint.
	assertEq(t, "DCAI-3 active sprint (none)", three.activeSprint, "")
	assertEq(t, "DCAI-3 assignee", three.assignee, "Alan")

	// The active-sprint window derives from the sprint ENTITY fetched from the
	// Agile API: its name and window-start instant, taken from startDate (Jira Cloud
	// has no activatedDate; see #49), not the planned dates carried on the issues.
	sprint, ok, err := st.ActiveSprintWindow()
	if err != nil || !ok {
		t.Fatalf("ActiveSprintWindow ok=%v err=%v", ok, err)
	}
	assertEq(t, "active sprint name", sprint.Name, "Sprint 42")
	assertEq(t, "active sprint activation", sprint.Activated.UTC().Format(time.RFC3339), "2026-07-13T07:00:00Z")

	// Both sprints persist as entities; the closed one exposes its completion
	// instant (completeDate), the active one has none yet.
	sprints, err := st.Sprints()
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	if len(sprints) != 2 {
		t.Fatalf("sprint entities = %d, want 2", len(sprints))
	}
	byName := map[string]Sprint{}
	for _, sp := range sprints {
		byName[sp.Name] = sp
	}
	assertEq(t, "Sprint 41 state", byName["Sprint 41"].State, "closed")
	assertEq(t, "Sprint 41 completion", byName["Sprint 41"].CompletedAt.UTC().Format(time.RFC3339), "2026-07-13T06:30:00Z")
	if !byName["Sprint 42"].CompletedAt.IsZero() {
		t.Fatalf("active Sprint 42 must have no completion instant, got %v", byName["Sprint 42"].CompletedAt)
	}

	// --- Transitions ---
	if got := countRows(t, st, "SELECT COUNT(*) FROM status_transition"); got != 6 {
		t.Fatalf("total transition rows = %d, want 6", got)
	}

	// DCAI-1: one status transition + one Estimated Time transition.
	assertEq(t, "DCAI-1 status transitions",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-1' AND field='status'"), 1)
	assertEq(t, "DCAI-1 estimated-time transitions",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-1' AND field='Estimated Time'"), 1)

	// DCAI-2: one changelog entry carrying BOTH a status and an Estimated Time
	// item must yield two distinct rows (the composite key must not collide).
	assertEq(t, "DCAI-2 transitions from shared entry",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-2'"), 2)

	// DCAI-3: embedded changelog was truncated (total 2, one history) so the
	// per-issue /changelog fallback must recover both status transitions.
	assertEq(t, "DCAI-3 transitions after fallback",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-3'"), 2)

	// The Done-crossing transition is present with correct endpoints.
	assertEq(t, "DCAI-3 Done crossing",
		countRows(t, st, "SELECT COUNT(*) FROM status_transition WHERE issue_key='DCAI-3' AND to_status='DONE (This Sprint)' AND from_status='Review / Testing'"), 1)

	// --- Sprint-membership history (parsed from the real "Sprint" changelog) ---
	// DCAI-1's changelog moves it from {41} to {41,42}: only sprint 42 (id, the
	// active sprint) is entered, at 2026-07-13T06:00:00Z (08:00 +0200). Sprint 41
	// is unchanged and records nothing.
	assertEq(t, "DCAI-1 sprint 42 entry rows",
		countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition WHERE issue_key='DCAI-1' AND sprint_id=42 AND entered=1"), 1)
	assertEq(t, "no rows for unchanged sprint 41",
		countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition WHERE sprint_id=41"), 0)

	entry, ok, err := st.SprintEntry(42, "DCAI-1")
	if err != nil || !ok {
		t.Fatalf("SprintEntry(42, DCAI-1) ok=%v err=%v", ok, err)
	}
	assertEq(t, "DCAI-1 sprint 42 entry instant", entry.UTC().Format(time.RFC3339), "2026-07-13T06:00:00Z")

	// DCAI-1 entered sprint 42 (06:00Z) before its activation (07:00Z, from
	// startDate), so it is a member at activation — "present from the start".
	members, err := st.IssuesInSprintAt(42, sprint.Activated)
	if err != nil {
		t.Fatalf("IssuesInSprintAt: %v", err)
	}
	if len(members) != 1 || members[0] != "DCAI-1" {
		t.Fatalf("members of sprint 42 at activation = %v, want [DCAI-1]", members)
	}

	// --- Dedup on re-sync ---
	if _, err := sync.Backfill(context.Background(), client, st); err != nil {
		t.Fatalf("re-backfill: %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM status_transition"); got != 6 {
		t.Fatalf("after re-sync transition rows = %d, want 6 (dedup failed)", got)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM issue"); got != 3 {
		t.Fatalf("after re-sync issue rows = %d, want 3", got)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM sprint_membership_transition"); got != 1 {
		t.Fatalf("after re-sync sprint membership rows = %d, want 1 (dedup failed)", got)
	}
}

// --- test store helpers (same package: direct SQL against the projection) ---

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func countRows(t *testing.T, st *Store, query string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

type issueRow struct {
	typ, summary, status, statusCategory, size, sprint, activeSprint, assignee string
}

func readIssue(t *testing.T, st *Store, key string) issueRow {
	t.Helper()
	var r issueRow
	var size, sprint, activeSprint, assignee any
	err := st.db.QueryRow(
		`SELECT type, summary, status, status_category, size, sprint, active_sprint, assignee FROM issue WHERE key=?`, key,
	).Scan(&r.typ, &r.summary, &r.status, &r.statusCategory, &size, &sprint, &activeSprint, &assignee)
	if err != nil {
		t.Fatalf("read issue %s: %v", key, err)
	}
	r.size = nullStr(size)
	r.sprint = nullStr(sprint)
	r.activeSprint = nullStr(activeSprint)
	r.assignee = nullStr(assignee)
	return r
}

func nullStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func assertEq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
