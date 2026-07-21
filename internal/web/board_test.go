package web_test

// Integration tests for the sprint Kanban board view over the HTTP seam:
// active-sprint cards grouped into workflow-order columns (Done columns
// included), each card showing key/title/size/type and linking to Jira, plus
// the shared nav marking Board active. Non-active-sprint work and Epics/
// Sub-tasks must not appear.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// newBoardApp syncs the fixture and serves with the given Server options, so a
// test can pin the Jira base URL (or leave it unset).
func newBoardApp(t *testing.T, client jira.Client, opts ...web.Option) *testApp {
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
	srv, err := web.NewServer(st, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// boardFixture is an active-sprint (KW29) mix across several statuses including
// a Done-category one, plus excluded work: an Epic, a Sub-task, and Task/Story
// outside the active sprint.
func boardFixture() *jira.FakeClient {
	active := func(iss jira.Issue) jira.Issue {
		iss.ActiveSprint = "KW29"
		return iss
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		active(jira.Issue{Key: "DCAI-10", Type: "Story", Summary: "Refine the widget", Status: "Refinement", StatusCategory: "To Do", Size: "S"}),
		active(jira.Issue{Key: "DCAI-11", Type: "Task", Summary: "Wire the gadget", Status: "In Progress", StatusCategory: "In Progress", Size: ""}),
		active(jira.Issue{Key: "DCAI-12", Type: "Bug", Summary: "Fix the sprocket", Status: "Review / Testing", StatusCategory: "In Progress", Size: "M"}),
		active(jira.Issue{Key: "DCAI-13", Type: "Story", Summary: "Ship the doohickey", Status: "DONE (This Sprint)", StatusCategory: "Done", Size: "L"}),
		// Excluded types.
		active(jira.Issue{Key: "DCAI-14", Type: "Epic", Summary: "Big theme", Status: "In Progress", StatusCategory: "In Progress", Size: "L"}),
		active(jira.Issue{Key: "DCAI-15", Type: "Sub-task", Summary: "A subtask", Status: "In Progress", StatusCategory: "In Progress", Size: "S"}),
		// Excluded by sprint scope.
		{Key: "DCAI-20", Type: "Story", Summary: "Old work", Status: "In Progress", StatusCategory: "In Progress", Size: "L", Sprint: "KW28"},
	}}
}

func TestBoardShowsActiveSprintCardsInColumns(t *testing.T) {
	app := newBoardApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/board")

	// The full fixed set of workflow columns renders in order, INCLUDING the
	// empty ones (Ready To Do, Ready for Release, Released / Deployed) and the
	// Done-category columns.
	assertOrder(t, body,
		`data-status="Refinement"`,
		`data-status="Ready To Do"`,
		`data-status="In Progress"`,
		`data-status="Review / Testing"`,
		`data-status="DONE (This Sprint)"`,
		`data-status="Ready for Release"`,
		`data-status="Released / Deployed"`,
	)

	// Each card shows key, title, size and a type badge.
	wants := []string{
		"DCAI-10", "Refine the widget",
		"DCAI-11", "Wire the gadget",
		"DCAI-12", "Fix the sprocket",
		"DCAI-13", "Ship the doohickey",
		`data-testid="card:DCAI-10:type">Story<`,
		`data-testid="card:DCAI-11:type">Task<`,
		`data-testid="card:DCAI-12:type">Bug<`,
		`data-testid="card:DCAI-10:size">S<`,
		`data-testid="card:DCAI-11:size">no estimate<`,
		`data-testid="card:DCAI-12:size">M<`,
		`data-testid="card:DCAI-13:size">L<`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("board missing %q\n%s", w, body)
		}
	}

	// The card links to the Jira issue in a new tab.
	link := `href="https://9elf26.atlassian.net/browse/DCAI-12" target="_blank" rel="noopener"`
	if !strings.Contains(body, link) {
		t.Errorf("board card missing Jira link %q\n%s", link, body)
	}

	// The heading names the active sprint.
	if !strings.Contains(body, "KW29") {
		t.Errorf("board heading missing active-sprint name\n%s", body)
	}

	// Excluded work must not appear anywhere (the empty Released / Deployed
	// column still renders — it just carries no cards).
	for _, absent := range []string{"DCAI-14", "DCAI-15", "DCAI-20", "Old work"} {
		if strings.Contains(body, absent) {
			t.Errorf("board leaked excluded content %q", absent)
		}
	}
}

// TestBoardStripScrollsHorizontallyOnly asserts the /board column strip is a
// horizontal-only scroll box (overflow-x auto paired with overflow-y hidden),
// so vertical scrolling is handed back to the browser window rather than nested
// inside the strip (#120).
func TestBoardStripScrollsHorizontallyOnly(t *testing.T) {
	app := newBoardApp(t, boardFixture())
	body := get(t, app.URL+"/board")

	if !strings.Contains(body, "overflow-x-auto") {
		t.Errorf("board strip must keep horizontal scrolling (overflow-x-auto)\n%s", body)
	}
	if !strings.Contains(body, "overflow-y:hidden") {
		t.Errorf("board strip must disable vertical scrolling (overflow-y:hidden)\n%s", body)
	}
}

// avatarFixture is an active-sprint (KW29) mix exercising the three assignee
// states a Board card renders (#68): assigned with a Jira avatar image,
// assigned without one (initials fallback), and unassigned (neutral circle).
func avatarFixture() *jira.FakeClient {
	active := func(iss jira.Issue) jira.Issue {
		iss.Type, iss.Status, iss.StatusCategory, iss.ActiveSprint = "Task", "In Progress", "In Progress", "KW29"
		return iss
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		active(jira.Issue{Key: "DCAI-10", Summary: "Has an avatar", Assignee: "Ada Lovelace", AssigneeAvatarURL: "https://avatar.example/ada/48.png"}),
		active(jira.Issue{Key: "DCAI-11", Summary: "Initials fallback", Assignee: "Grace Hopper"}),
		active(jira.Issue{Key: "DCAI-12", Summary: "Unassigned"}),
	}}
}

// TestBoardCardShowsAssigneeAvatar asserts each Board card renders its assignee
// as a Jira avatar image, falling back to computed initials when no image is
// present, and a neutral (empty) circle when unassigned.
func TestBoardCardShowsAssigneeAvatar(t *testing.T) {
	app := newBoardApp(t, avatarFixture())
	body := get(t, app.URL+"/board")

	// Assigned with an avatar: the Jira image renders, labelled with the assignee,
	// and carries an onerror handler plus a hidden initials fallback so a broken
	// image (404 / expired avatar) still degrades to initials, not a broken icon.
	for _, want := range []string{
		`data-testid="card:DCAI-10:avatar-img"`,
		`src="https://avatar.example/ada/48.png"`,
		`alt="Ada Lovelace"`,
		`onerror=`,
		`data-testid="card:DCAI-10:avatar-initials">AL<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board card DCAI-10 missing %q\n%s", want, body)
		}
	}

	// Assigned without an avatar: initials render (first + last initial).
	if !strings.Contains(body, `data-testid="card:DCAI-11:avatar-initials">GH<`) {
		t.Errorf("board card DCAI-11 missing initials fallback\n%s", body)
	}
	if strings.Contains(body, `data-testid="card:DCAI-11:avatar-img"`) {
		t.Errorf("board card DCAI-11 must not render an image without an avatar URL")
	}

	// Unassigned: a neutral empty circle, no initials and no image.
	if !strings.Contains(body, `data-testid="card:DCAI-12:avatar-empty"`) {
		t.Errorf("board card DCAI-12 missing neutral empty circle\n%s", body)
	}
	for _, absent := range []string{
		`data-testid="card:DCAI-12:avatar-img"`,
		`data-testid="card:DCAI-12:avatar-initials"`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("unassigned board card DCAI-12 must not render %q", absent)
		}
	}
}

// epicFixture is an active-sprint (KW29) mix exercising the epic pill (#69): a
// coloured epic and children under it, a child with an unset epic colour (pill
// defaults to purple), a child with no parent (no pill), and a long epic name
// (truncated with a hover title).
func epicFixture() *jira.FakeClient {
	longName := "A very very long epic name that will not fit on one board card line"
	epic := func(key, summary, color string) jira.Issue {
		return jira.Issue{Key: key, Type: "Epic", Summary: summary, Status: "In Progress", StatusCategory: "In Progress", EpicColor: color}
	}
	child := func(key, summary, parent string) jira.Issue {
		return jira.Issue{Key: key, Type: "Task", Summary: summary, Status: "In Progress", StatusCategory: "In Progress", ActiveSprint: "KW29", ParentKey: parent}
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		epic("DCAI-100", "Checkout revamp", "green"),
		epic("DCAI-101", longName, "dark_teal"),
		epic("DCAI-102", "Uncoloured epic", ""), // no Issue color → pill defaults to purple
		child("DCAI-10", "child of green epic", "DCAI-100"),
		child("DCAI-11", "no parent", ""),
		child("DCAI-12", "child of long-named epic", "DCAI-101"),
		child("DCAI-13", "child of uncoloured epic", "DCAI-102"),
	}}
}

// TestBoardCardShowsEpicPill asserts each Board card shows its parent epic's name
// as a pill under the title, coloured by the epic's Jira Issue color (purple by
// default), truncated with a hover title for long names, and no pill when the
// ticket has no parent epic.
func TestBoardCardShowsEpicPill(t *testing.T) {
	longName := "A very very long epic name that will not fit on one board card line"
	app := newBoardApp(t, epicFixture())
	body := get(t, app.URL+"/board")

	// Named, coloured pill for a child with a coloured epic.
	for _, want := range []string{
		`data-testid="card:DCAI-10:epic"`,
		"Checkout revamp",
		"background-color:#36B37E", // green
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board card DCAI-10 missing %q\n%s", want, body)
		}
	}

	// An epic with no Issue color falls back to purple.
	if !strings.Contains(body, "background-color:#6554C0") {
		t.Errorf("board card DCAI-13 epic pill should default to purple\n%s", body)
	}

	// Long epic names truncate (truncate class) and carry the full name as a title.
	if !strings.Contains(body, `title="`+longName+`"`) {
		t.Errorf("long epic name should carry a hover title\n%s", body)
	}

	// No parent epic → no pill.
	if strings.Contains(body, `data-testid="card:DCAI-11:epic"`) {
		t.Errorf("board card DCAI-11 has no parent epic and must render no pill")
	}

	// The epics themselves never render as board cards.
	for _, absent := range []string{`data-key="DCAI-100"`, `data-key="DCAI-101"`, `data-key="DCAI-102"`} {
		if strings.Contains(body, absent) {
			t.Errorf("epic leaked onto the board as a card: %q", absent)
		}
	}
}

// TestBoardColumnHeaderShowsCount asserts each column header carries its card
// count.
func TestBoardColumnHeaderShowsCount(t *testing.T) {
	app := newBoardApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net"))
	body := get(t, app.URL+"/board")
	if !strings.Contains(body, `data-testid="col:Refinement:count">1<`) {
		t.Errorf("board column header missing card count\n%s", body)
	}
}

// TestBoardWithoutBaseURLRendersCardsWithoutLink asserts cards still render (no
// broken href) when JIRA_BASE_URL is unset.
func TestBoardWithoutBaseURLRendersCardsWithoutLink(t *testing.T) {
	app := newBoardApp(t, boardFixture()) // no WithJiraBaseURL
	body := get(t, app.URL+"/board")

	if !strings.Contains(body, "DCAI-10") {
		t.Errorf("cards should still render without a base URL\n%s", body)
	}
	if strings.Contains(body, "/browse/") {
		t.Errorf("cards must not render a Jira link when the base URL is unset\n%s", body)
	}
}

// TestBoardNoActiveSprintRendersFriendlyEmptyState drives the board with only
// out-of-sprint work: no active sprint is known, so a friendly empty state
// renders (200, no panic).
func TestBoardNoActiveSprintRendersFriendlyEmptyState(t *testing.T) {
	app := newBoardApp(t, &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-20", Type: "Story", Summary: "Old work", Status: "In Progress", StatusCategory: "In Progress", Size: "L", Sprint: "KW28"},
	}})
	body := get(t, app.URL+"/board") // get() fails on a non-200 status.
	if !strings.Contains(body, "No active sprint") {
		t.Errorf("board without an active sprint should show a friendly empty state\n%s", body)
	}
}

// TestBoardInNavOnEveryPage asserts Board is marked active on /board and appears
// in the nav on the other pages too.
func TestBoardInNavOnEveryPage(t *testing.T) {
	app := newBoardApp(t, boardFixture())

	board := get(t, app.URL+"/board")
	if !strings.Contains(board, `data-nav="board" aria-current="page"`) {
		t.Errorf("/board must mark Board active\n%s", board)
	}
	if n := strings.Count(board, `aria-current="page"`); n != 1 {
		t.Errorf("/board: expected exactly one active nav item, got %d", n)
	}

	for _, path := range []string{"/", "/sprint", "/velocity"} {
		body := get(t, app.URL+path)
		if !strings.Contains(body, `href="/board"`) {
			t.Errorf("%s: nav missing Board link\n%s", path, body)
		}
	}
}
