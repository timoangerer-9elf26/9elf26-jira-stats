package web_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// nowFixture is a known open-work mix driven through the whole slice (fake Jira
// -> sync -> temp SQLite -> real handlers): several open statuses with S/M/L and
// unsized issues, plus an Epic, a Sub-task and Done-category issues that must
// never appear as open work.
func nowFixture() *jira.FakeClient {
	return &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-10", Type: "Story", Summary: "a", Status: "Refinement", StatusCategory: "To Do", Size: "S"},
		{Key: "DCAI-11", Type: "Task", Summary: "b", Status: "Refinement", StatusCategory: "To Do", Size: ""},
		{Key: "DCAI-12", Type: "Bug", Summary: "c", Status: "Ready to Do", StatusCategory: "To Do", Size: "M"},
		{Key: "DCAI-13", Type: "Story", Summary: "d", Status: "In Progress", StatusCategory: "In Progress", Size: "L"},
		{Key: "DCAI-14", Type: "Task", Summary: "e", Status: "In Progress", StatusCategory: "In Progress", Size: "S"},
		{Key: "DCAI-15", Type: "Bug", Summary: "f", Status: "Review / Testing", StatusCategory: "In Progress", Size: "M"},
		{Key: "DCAI-16", Type: "Story", Summary: "g", Status: "Review / Testing", StatusCategory: "In Progress", Size: ""},
		// Excluded: wrong types (Epic, Sub-task) and Done-category issues.
		{Key: "DCAI-17", Type: "Epic", Summary: "h", Status: "In Progress", StatusCategory: "In Progress", Size: "L"},
		{Key: "DCAI-18", Type: "Sub-task", Summary: "i", Status: "In Progress", StatusCategory: "In Progress", Size: "S"},
		{Key: "DCAI-19", Type: "Story", Summary: "j", Status: "DONE (This Sprint)", StatusCategory: "Done", Size: "M"},
		{Key: "DCAI-20", Type: "Story", Summary: "k", Status: "Released / Deployed", StatusCategory: "Done", Size: "L"},
	}}
}

// TestNowViewRendersOpenBoard drives the "Now" view over HTTP and asserts the
// per-status S/M/L/no-estimate counts and points, the grand total, that unsized
// issues land in the no-estimate bucket, and that Epics/Sub-tasks/Done issues
// are excluded.
func TestNowViewRendersOpenBoard(t *testing.T) {
	app := newTestApp(t, nowFixture())
	body := get(t, app.URL+"/")

	// Per-status counts (S/M/L/no-estimate) and points.
	wants := []string{
		`data-testid="col:Refinement:s">1<`,
		`data-testid="col:Refinement:none">1<`,
		`data-testid="col:Refinement:points">1<`,
		`data-testid="col:Ready to Do:m">1<`,
		`data-testid="col:Ready to Do:points">2<`,
		`data-testid="col:In Progress:s">1<`,
		`data-testid="col:In Progress:l">1<`,
		`data-testid="col:In Progress:points">4<`, // Epic(L) and Sub-task(S) excluded
		`data-testid="col:Review / Testing:m">1<`,
		`data-testid="col:Review / Testing:none">1<`,
		`data-testid="col:Review / Testing:points">2<`,
		// Grand total across all open statuses.
		`data-testid="total:s">2<`,
		`data-testid="total:m">2<`,
		`data-testid="total:l">1<`,
		`data-testid="total:none">2<`,
		`data-testid="total:points">9<`,
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("rendered Now view missing %q\n%s", want, body)
		}
	}

	// Done-category statuses must not appear as columns.
	for _, absent := range []string{`data-status="DONE (This Sprint)"`, `data-status="Released / Deployed"`} {
		if strings.Contains(body, absent) {
			t.Errorf("Now view shows Done-category column %q; must be excluded", absent)
		}
	}

	// Columns render in workflow order.
	assertOrder(t, body,
		`data-status="Refinement"`,
		`data-status="Ready to Do"`,
		`data-status="In Progress"`,
		`data-status="Review / Testing"`,
	)
}

// TestNowViewSelfPollsAndShowsFreshness asserts the HTMX self-poll wiring and
// the "updated ... ago" freshness indicator.
func TestNowViewSelfPollsAndShowsFreshness(t *testing.T) {
	app := newTestApp(t, nowFixture())
	body := get(t, app.URL+"/")

	if !strings.Contains(body, `hx-get="/now/board"`) || !strings.Contains(body, `hx-trigger="every 30s"`) {
		t.Errorf("Now view is not wired to self-poll via HTMX:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="updated-ago"`) || !strings.Contains(body, "ago") {
		t.Errorf("Now view does not show data freshness:\n%s", body)
	}
}

// TestNowBoardFragmentReturnsPartial asserts the polling endpoint returns just
// the board partial (no full HTML document).
func TestNowBoardFragmentReturnsPartial(t *testing.T) {
	app := newTestApp(t, nowFixture())
	body := get(t, app.URL+"/now/board")

	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Errorf("fragment must be a partial, got full document:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="col:In Progress:points">4<`) {
		t.Errorf("fragment missing board content:\n%s", body)
	}
	if !strings.Contains(body, `hx-get="/now/board"`) {
		t.Errorf("fragment must keep self-polling after swap:\n%s", body)
	}
}

func TestIndexServesEmbeddedAssetsWithoutCDN(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	page := get(t, app.URL+"/")
	if strings.Contains(page, "unpkg.com") || strings.Contains(page, "cdn.") {
		t.Fatalf("page references a CDN; assets must be embedded and self-served:\n%s", page)
	}

	css := get(t, app.URL+"/static/output.css")
	if !strings.Contains(css, "tabular-nums") {
		t.Fatalf("output.css did not contain expected utility class")
	}

	js := get(t, app.URL+"/static/htmx.min.js")
	if !strings.Contains(js, "htmx") {
		t.Fatalf("htmx.min.js was not served from embedded assets")
	}
}

// assertOrder fails unless each needle appears in body in the given order.
func assertOrder(t *testing.T, body string, needles ...string) {
	t.Helper()
	prev := -1
	for _, n := range needles {
		i := strings.Index(body, n)
		if i < 0 {
			t.Fatalf("expected %q in body", n)
		}
		if i < prev {
			t.Fatalf("expected %q to appear after the previous column (order wrong)", n)
		}
		prev = i
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
