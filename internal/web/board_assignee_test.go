package web_test

// Integration tests for the Board's Assignee edit (#224, docs/adr/0012) over the
// HTTP seam: a card's assignee avatar is a popover of the project's members plus
// Unassigned, each choice POSTing /board/assignee, which writes the assignee to
// Jira, re-reads the issue and answers with the whole board panel re-rendered
// from the projection. A failed write changes nothing and leaves an inline
// message on that card.
//
// The avatar is editable on the BOARD only. The same partial draws the Daily
// board's cards, the Sprint drill-down's cards and the assignee filter's chips,
// so the leak tests below are not decoration — they are the thing docs/adr/0012
// singles out as the failure this control is most likely to ship with.

import (
	"context"
	"errors"
	"fmt"
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

// assignMembers is the project's assignable-user set the assign fixture syncs:
// three people and one app account, so every test also pins that automation is
// filtered out before it can reach the popover (docs/adr/0012).
func assignMembers() []jira.AssignableUser {
	return []jira.AssignableUser{
		{AccountID: "acct-ada", DisplayName: "Ada Lovelace", AvatarURL: "/static/avatars/ada.svg", AccountType: jira.AccountTypePerson},
		{AccountID: "acct-grace", DisplayName: "Grace Hopper", AvatarURL: "/static/avatars/grace.svg", AccountType: jira.AccountTypePerson},
		{AccountID: "acct-lise", DisplayName: "Lise Meitner", AccountType: jira.AccountTypePerson},
		{AccountID: "acct-bot", DisplayName: "Jira Coding Agent", AccountType: jira.AccountTypeApp},
	}
}

// assignFixture is the Board fixture for the assignee edit: four active-sprint
// cards covering the states the popover has to render — assigned to a member,
// unassigned, and assigned to someone who is NOT on the project any more (the
// card that must still show its person without offering them).
func assignFixture() *jira.FakeClient {
	active := func(iss jira.Issue) jira.Issue {
		iss.ActiveSprint = "KW29"
		return iss
	}
	return &jira.FakeClient{
		Sprints:         activeSprintKW29(),
		AssignableUsers: assignMembers(),
		Issues: []jira.Issue{
			active(jira.Issue{Key: "DCAI-10", Type: "Story", Summary: "Refine the widget", Status: "Refinement", StatusCategory: "To Do",
				Assignee: "Ada Lovelace", AssigneeAvatarURL: "/static/avatars/ada.svg"}),
			active(jira.Issue{Key: "DCAI-11", Type: "Task", Summary: "Wire the gadget", Status: "In Progress", StatusCategory: "In Progress"}),
			active(jira.Issue{Key: "DCAI-12", Type: "Bug", Summary: "Fix the sprocket", Status: "Review / Testing", StatusCategory: "In Progress",
				Assignee: "Grace Hopper", AssigneeAvatarURL: "/static/avatars/grace.svg"}),
			// Assigned to someone no longer on the project: still shown, never offered.
			active(jira.Issue{Key: "DCAI-13", Type: "Story", Summary: "Ship the doohickey", Status: "DONE (This Sprint)", StatusCategory: "Done",
				Assignee: "Hedy Lamarr"}),
		},
	}
}

// newAssignApp syncs the fixture into a temp store and serves the handlers wired
// with a real Syncer as the Assigner, so a POST /board/assignee exercises the
// whole write → re-read → persist path against the fake Jira.
func newAssignApp(t *testing.T, fake *jira.FakeClient, opts ...web.Option) *testApp {
	t.Helper()
	syncer := func(st *store.Store) web.Assigner { return sync.NewSyncer(fake, st, time.Minute) }
	return newAssignAppWith(t, fake, syncer, opts...)
}

// newAssignAppWith is newAssignApp with the Assigner chosen by the caller, so a
// test can substitute a stub that reproduces a failure mode the fake Jira cannot
// (notably sync.ErrWriteLanded — a write that landed but could not be
// reconciled). A nil assigner leaves the server without one.
func newAssignAppWith(t *testing.T, fake *jira.FakeClient, assigner func(*store.Store) web.Assigner, opts ...web.Option) *testApp {
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
	if assigner != nil {
		opts = append(opts, web.WithAssigner(assigner(st)))
	}
	srv, err := web.NewServer(st, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

// stubAssigner is a web.Assigner returning a fixed answer, used for the failure
// modes the fake Jira cannot produce on its own.
type stubAssigner struct {
	name string
	err  error
}

func (a stubAssigner) SetAssignee(ctx context.Context, key, accountID string) (string, error) {
	return a.name, a.err
}

// between returns the slice of body between the first occurrence of start and
// the following end, failing the test when either marker is missing. It is how a
// "does editability leak into THAT region" assertion stays precise: the whole
// page always contains the control somewhere, so the region has to be isolated.
func between(t *testing.T, body, start, end string) string {
	t.Helper()
	i := strings.Index(body, start)
	if i < 0 {
		t.Fatalf("body has no %q\n%s", start, body)
	}
	rest := body[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("body has no %q after %q\n%s", end, start, body)
	}
	return rest[:j]
}

// TestBoardAvatarOffersEveryProjectMemberPlusUnassigned asserts the Board draws
// the avatar as an assign popover listing the project's PEOPLE (never its app
// accounts) plus Unassigned, each choice posting to /board/assignee.
func TestBoardAvatarOffersEveryProjectMemberPlusUnassigned(t *testing.T) {
	app := newAssignApp(t, assignFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="card:DCAI-11:assignee"`,
		`data-testid="card:DCAI-11:assignee-trigger"`,
		`hx-post="/board/assignee"`,
		`data-testid="card:DCAI-11:assignee-opt:acct-ada"`,
		`data-testid="card:DCAI-11:assignee-opt:acct-grace"`,
		`data-testid="card:DCAI-11:assignee-opt:acct-lise"`,
		`data-testid="card:DCAI-11:assignee-opt:unassigned"`,
		"Ada Lovelace",
		"Unassigned",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Board assign popover missing %q\n", want)
		}
	}
	// The project's app account is not a project member, so it is never offered.
	if strings.Contains(body, `assignee-opt:acct-bot`) {
		t.Errorf("the app account leaked into the assign popover\n%s", body)
	}
}

// current is the exact markup a marked choice carries. Asserting the pair
// adjacently (rather than "the body contains aria-current somewhere") is what
// keeps the assertion about THAT option.
func current(key, opt string) string {
	return fmt.Sprintf(`data-testid="card:%s:assignee-opt:%s" aria-current="true"`, key, opt)
}

// TestBoardAssignPopoverMarksTheCurrentAssignee asserts the card's current
// assignee is marked in its own popover — and that an unassigned card marks
// Unassigned instead.
func TestBoardAssignPopoverMarksTheCurrentAssignee(t *testing.T) {
	app := newAssignApp(t, assignFixture())
	body := get(t, app.URL+"/board")

	// DCAI-10 is Ada's: her option carries the current marker.
	if !strings.Contains(body, current("DCAI-10", "acct-ada")) {
		t.Errorf("DCAI-10's assignee (Ada) is not marked current in its popover\n%s", body)
	}
	// DCAI-11 is unassigned: the Unassigned choice is the marked one...
	if !strings.Contains(body, current("DCAI-11", "unassigned")) {
		t.Errorf("unassigned DCAI-11 does not mark Unassigned as current\n%s", body)
	}
	// ...and nobody else on that card is.
	for _, opt := range []string{"acct-ada", "acct-grace", "acct-lise"} {
		if strings.Contains(body, current("DCAI-11", opt)) {
			t.Errorf("unassigned DCAI-11 marked %s as current", opt)
		}
	}
}

// TestBoardCardKeepsAnAssigneeWhoLeftTheProject asserts a card assigned to
// someone who is no longer a project member still SHOWS them while never
// offering them: it can be reassigned away from them, never back (CONTEXT.md →
// Assignee edit).
func TestBoardCardKeepsAnAssigneeWhoLeftTheProject(t *testing.T) {
	app := newAssignApp(t, assignFixture())
	body := get(t, app.URL+"/board")

	if !strings.Contains(body, `title="Hedy Lamarr"`) {
		t.Errorf("the card lost the assignee who left the project\n%s", body)
	}
	// Her card's popover offers the project's members plus Unassigned — not her.
	// (The region starts at the menu, not the control root: the root also holds
	// the trigger, which legitimately draws her.)
	opts := between(t, body, `aria-label="Assignee for DCAI-13"`, `:assignee-opt:unassigned`)
	if strings.Contains(opts, "Hedy Lamarr") {
		t.Errorf("an ex-member was offered as an assign target\n%s", opts)
	}
	// Nothing is marked current on that card: she is not among the candidates.
	for _, opt := range []string{"acct-ada", "acct-grace", "acct-lise", "unassigned"} {
		if strings.Contains(body, current("DCAI-13", opt)) {
			t.Errorf("an ex-member's card marked %s as its current assignee", opt)
		}
	}
}

// TestBoardAssignWritesAndShowsTheRereadAssignee asserts picking a person writes
// to Jira and the re-rendered board shows the assignee the re-read returned,
// with no inline error, and that the projection carries it afterwards.
func TestBoardAssignWritesAndShowsTheRereadAssignee(t *testing.T) {
	fake := assignFixture()
	app := newAssignApp(t, fake)

	code, body := postForm(t, app.URL+"/board/assignee",
		url.Values{"key": {"DCAI-11"}, "account": {"acct-grace"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	card := between(t, body, `data-key="DCAI-11"`, `data-testid="board-card"`)
	if !strings.Contains(card, `alt="Grace Hopper"`) {
		t.Errorf("the write response does not show the re-read assignee Grace Hopper\n%s", card)
	}
	if strings.Contains(body, `data-testid="card:DCAI-11:assignee-error"`) {
		t.Errorf("a successful write must not render an inline error\n%s", body)
	}
	// The re-render marks the new assignee as the card's current one.
	if !strings.Contains(body, current("DCAI-11", "acct-grace")) {
		t.Errorf("the re-rendered popover does not mark the new assignee as current\n%s", body)
	}
	// Jira really was written, and the projection reflects the re-read.
	if got := fakeAssignee(t, fake, "DCAI-11"); got != "Grace Hopper" {
		t.Errorf("Jira-side assignee = %q, want %q", got, "Grace Hopper")
	}
	reboard := get(t, app.URL+"/board")
	fresh := between(t, reboard, `data-key="DCAI-11"`, `data-testid="board-card"`)
	if !strings.Contains(fresh, `alt="Grace Hopper"`) {
		t.Errorf("a fresh Board does not show the persisted assignee\n%s", fresh)
	}
}

// TestBoardAssignUnassignedClearsTheAssignee asserts the Unassigned choice — the
// one the priority edit deliberately does not offer — clears the assignee.
func TestBoardAssignUnassignedClearsTheAssignee(t *testing.T) {
	fake := assignFixture()
	app := newAssignApp(t, fake)

	code, body := postForm(t, app.URL+"/board/assignee",
		url.Values{"key": {"DCAI-10"}, "clear": {"1"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	card := between(t, body, `data-key="DCAI-10"`, `data-testid="board-card"`)
	if !strings.Contains(card, `data-testid="card:DCAI-10:avatar-empty"`) {
		t.Errorf("clearing the assignee did not render the unassigned circle\n%s", card)
	}
	if got := fakeAssignee(t, fake, "DCAI-10"); got != "" {
		t.Errorf("Jira-side assignee = %q, want it cleared", got)
	}
}

// TestBoardAssignFailedWriteLeavesTheCardAndShowsAnInlineError asserts a failed
// write is a rendered outcome, not an HTTP error: the card keeps its assignee,
// an inline message appears on THAT card (no global banner), and the projection
// is untouched.
func TestBoardAssignFailedWriteLeavesTheCardAndShowsAnInlineError(t *testing.T) {
	fake := assignFixture()
	fake.WriteErr = errors.New("jira says no (permissions)")
	app := newAssignApp(t, fake)

	code, body := postForm(t, app.URL+"/board/assignee",
		url.Values{"key": {"DCAI-10"}, "account": {"acct-grace"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-10:assignee-error"`) {
		t.Errorf("a failed write did not render an inline error on the card\n%s", body)
	}
	// The message sits at card level, INSIDE the card's <a> and outside the
	// board-assignee root, so it needs the marker in its own right — without it
	// clicking the error navigates to Jira (#221). The estimate's error is
	// covered by sitting inside its control's marked root; this one is not.
	if at := strings.Index(body, `data-testid="card:DCAI-10:assignee-error"`); at >= 0 {
		openedAt := strings.LastIndex(body[:at], "<p")
		if openedAt < 0 || !strings.Contains(body[openedAt:at], "data-card-control") {
			t.Errorf("the inline assignee error is not marked data-card-control, so clicking it follows the card link\n%s", body[max(0, at-200):at])
		}
	}
	card := between(t, body, `data-key="DCAI-10"`, `data-testid="board-card"`)
	if !strings.Contains(card, `alt="Ada Lovelace"`) {
		t.Errorf("a failed write changed the card's avatar\n%s", card)
	}
	// Only the edited card carries the message.
	if strings.Contains(body, `data-testid="card:DCAI-12:assignee-error"`) {
		t.Errorf("the inline error leaked onto another card\n%s", body)
	}
	if got := fakeAssignee(t, fake, "DCAI-10"); got != "Ada Lovelace" {
		t.Errorf("a failed write changed Jira: assignee = %q", got)
	}
}

// TestBoardAssignWithNoAssignerWiredFails asserts a server built without an
// Assigner reports the edit as a failure (inline error) rather than silently
// dropping it.
func TestBoardAssignWithNoAssignerWiredFails(t *testing.T) {
	app := newAssignAppWith(t, assignFixture(), nil)

	code, body := postForm(t, app.URL+"/board/assignee",
		url.Values{"key": {"DCAI-10"}, "account": {"acct-grace"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-10:assignee-error"`) {
		t.Errorf("an unwired assigner must render the inline error\n%s", body)
	}
}

// TestBoardAssignWriteThatLandedIsNotReportedAsAFailure asserts the
// sync.ErrWriteLanded case: the assignment IS a fact in Jira and only the
// projection is behind, so the board re-renders (stale, never wrong) instead of
// claiming the write failed. Reporting a landed write as a failure is the exact
// defect the drag path shipped in #195/#206 and #207 had to fix.
func TestBoardAssignWriteThatLandedIsNotReportedAsAFailure(t *testing.T) {
	landed := func(*store.Store) web.Assigner {
		return stubAssigner{err: fmt.Errorf("re-fetch issue: %w: %w", sync.ErrWriteLanded, errors.New("jira timed out"))}
	}
	app := newAssignAppWith(t, assignFixture(), landed)

	code, body := postForm(t, app.URL+"/board/assignee",
		url.Values{"key": {"DCAI-10"}, "account": {"acct-grace"}})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	if strings.Contains(body, `data-testid="card:DCAI-10:assignee-error"`) {
		t.Errorf("a landed write was reported to the user as a failure\n%s", body)
	}
	if !strings.Contains(body, `data-key="DCAI-10"`) {
		t.Errorf("a landed write did not re-render the board\n%s", body)
	}
}

// TestBoardAssignRejectsAnythingButAKnownMemberOrAnExplicitClear is the guard
// around jira.UnassignedAccountID being the EMPTY STRING: an account id that
// arrives empty would otherwise silently unassign the ticket. Clearing is a
// distinct, explicit input; everything else must be a member of this project.
func TestBoardAssignRejectsAnythingButAKnownMemberOrAnExplicitClear(t *testing.T) {
	fake := assignFixture()
	app := newAssignApp(t, fake)

	for name, vals := range map[string]url.Values{
		"no key":                {"account": {"acct-ada"}},
		"empty account":         {"key": {"DCAI-10"}, "account": {""}},
		"no account at all":     {"key": {"DCAI-10"}},
		"unknown account":       {"key": {"DCAI-10"}, "account": {"acct-nobody"}},
		"app account":           {"key": {"DCAI-10"}, "account": {"acct-bot"}},
		"clear with an account": {"key": {"DCAI-10"}, "clear": {"1"}, "account": {"acct-grace"}},
	} {
		if code, _ := postForm(t, app.URL+"/board/assignee", vals); code != http.StatusBadRequest {
			t.Errorf("POST %s (%v): status %d, want 400", name, vals, code)
		}
	}
	// Nothing was written by any of them — in particular, no silent unassign.
	if got := fakeAssignee(t, fake, "DCAI-10"); got != "Ada Lovelace" {
		t.Errorf("a rejected request still changed Jira: assignee = %q", got)
	}
}

// TestBoardAssignRespectsAnActiveAssigneeFilter asserts the rule docs/adr/0010
// set for a transition, applied unchanged here: a card reassigned to someone
// outside an active assignee filter is simply absent from the re-render.
func TestBoardAssignRespectsAnActiveAssigneeFilter(t *testing.T) {
	app := newAssignApp(t, assignFixture())

	code, body := postForm(t, app.URL+"/board/assignee", url.Values{
		"key": {"DCAI-10"}, "account": {"acct-grace"},
		// The filter params ride along exactly as the chrome's hidden inputs send them.
		"assignee": {"Ada Lovelace"},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	if strings.Contains(body, `data-key="DCAI-10"`) {
		t.Errorf("a card reassigned out of the active filter is still in the re-render\n%s", body)
	}
	// The filter itself is untouched by the edit: the selection round-trips, so
	// the next toggle still carries it (the same shape a filter change sends).
	if !strings.Contains(body, `name="assignee" value="Ada Lovelace"`) {
		t.Errorf("the assignee filter's selection did not round-trip across the edit\n%s", body)
	}
}

// TestReassigningDoesNotMakeACardActiveIn24h asserts an assignee edit is not a
// status movement: it must not pull a long-idle card into the "Active in last
// 24h" lens (docs/adr/0012 — widening that definition would ripple into Daily
// movement's classification).
func TestReassigningDoesNotMakeACardActiveIn24h(t *testing.T) {
	now := kw29Activated.Add(72 * time.Hour)
	app := newAssignApp(t, assignFixture(), web.WithClock(func() time.Time { return now }))

	// Nothing on the fixture has moved in the last 24h, so the lens is empty first.
	if before := get(t, app.URL+"/board?active-24h=1"); strings.Contains(before, `data-key="DCAI-10"`) {
		t.Fatalf("the fixture card was already active in the last 24h\n%s", before)
	}

	code, body := postForm(t, app.URL+"/board/assignee", url.Values{
		"key": {"DCAI-10"}, "account": {"acct-grace"}, "active-24h": {"1"},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /board/assignee: status %d, want 200", code)
	}
	if strings.Contains(body, `data-key="DCAI-10"`) {
		t.Errorf("a reassignment made the card match Active in last 24h\n%s", body)
	}
	if after := get(t, app.URL+"/board?active-24h=1"); strings.Contains(after, `data-key="DCAI-10"`) {
		t.Errorf("a reassignment made the card active on a later load\n%s", after)
	}
}

// TestAvatarIsNotEditableWithoutProjectMembers asserts the state between a
// deploy and the first sync: with an empty member table the avatar is simply not
// editable, rather than opening an empty popover — and the server agrees, so a
// stale page cannot write through it either.
func TestAvatarIsNotEditableWithoutProjectMembers(t *testing.T) {
	fake := assignFixture()
	fake.AssignableUsers = nil
	app := newAssignApp(t, fake)

	body := get(t, app.URL+"/board")
	if strings.Contains(body, "/board/assignee") {
		t.Errorf("the avatar is editable with no project members synced\n%s", body)
	}
	// The avatar itself still renders — read-only, not missing.
	if !strings.Contains(body, `alt="Ada Lovelace"`) {
		t.Errorf("the read-only avatar disappeared with no project members\n%s", body)
	}
	for _, vals := range []url.Values{
		{"key": {"DCAI-10"}, "account": {"acct-ada"}},
		{"key": {"DCAI-10"}, "clear": {"1"}},
	} {
		if code, _ := postForm(t, app.URL+"/board/assignee", vals); code != http.StatusBadRequest {
			t.Errorf("POST %v with no project members: status %d, want 400", vals, code)
		}
	}
}

// TestAssignEditabilityDoesNotLeakOffTheBoard asserts the flag stays a Board
// per-render decision: the Daily board and the Sprint drill-down draw the same
// card-avatar and must stay read-only.
func TestAssignEditabilityDoesNotLeakOffTheBoard(t *testing.T) {
	dailyApp, _ := dailyEstimateApp(t)
	if body := get(t, dailyApp.URL+"/daily?preset=today"); strings.Contains(body, "/board/assignee") {
		t.Errorf("the assign control leaked into the Daily board\n%s", body)
	}
	drill := get(t, drillFixtureApp(t).URL+"/sprint/cell?row=started&col=total")
	if strings.Contains(drill, "/board/assignee") {
		t.Errorf("the assign control leaked into the Sprint drill-down\n%s", drill)
	}
}

// TestAssignEditabilityDoesNotLeakIntoTheFilterChips asserts the hazard
// docs/adr/0012 names outright: the assignee filter's chips draw the same
// card-avatar partial, and if editability leaked through it the filter bar would
// turn into a row of assign buttons.
func TestAssignEditabilityDoesNotLeakIntoTheFilterChips(t *testing.T) {
	app := newAssignApp(t, assignFixture())
	body := get(t, app.URL+"/board")

	bar := between(t, body, `data-testid="board-assignee-bar"`, `data-testid="board-assignee-clear"`)
	if strings.Contains(bar, "/board/assignee") {
		t.Errorf("the assign control leaked into the assignee filter's chips\n%s", bar)
	}
	if strings.Contains(bar, "data-card-control") {
		t.Errorf("a filter chip is marked as a card control\n%s", bar)
	}
	// The chips are still there, drawing the same avatars.
	if !strings.Contains(bar, `data-testid="board-assignee:Ada Lovelace"`) {
		t.Errorf("the assignee filter lost its chips\n%s", bar)
	}
}

// TestBoardAssignFilterListAndPopoverListDifferOnPurpose pins the distinction
// docs/adr/0012 rejected unifying: the filter lists people with work on this
// board, the popover lists the project's members. Lise has nothing in the sprint
// — she is offered but not filterable.
func TestBoardAssignFilterListAndPopoverListDifferOnPurpose(t *testing.T) {
	app := newAssignApp(t, assignFixture())
	body := get(t, app.URL+"/board")

	bar := between(t, body, `data-testid="board-assignee-bar"`, `data-testid="board-assignee-clear"`)
	if strings.Contains(bar, "Lise Meitner") {
		t.Errorf("a member with no work on the board became a filter chip\n%s", bar)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-11:assignee-opt:acct-lise"`) {
		t.Errorf("a member with no work on the board is not offered by the popover\n%s", body)
	}
	// Conversely the ex-member has work, so she IS a chip — and is not offered.
	if !strings.Contains(bar, `data-testid="board-assignee:Hedy Lamarr"`) {
		t.Errorf("an assignee who left the project lost her filter chip\n%s", bar)
	}
}

// fakeAssignee reads an issue's assignee straight out of the fake Jira, so a
// test can assert what was (or was not) written on the far side of the seam.
func fakeAssignee(t *testing.T, fake *jira.FakeClient, key string) string {
	t.Helper()
	iss, err := fake.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("fake FetchIssue %s: %v", key, err)
	}
	return iss.Assignee
}
