package web_test

// Integration tests for the Board filter scaffolding + assignee filter (#157):
// the /board filter chrome region, the /board/results HTMX fragment, and
// URL-encoded, bookmarkable, round-tripping assignee selection that hides
// non-matching cards while every column stays rendered.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// boardAssigneeFixture is an active-sprint (KW29) mix across several assignees
// and one unassigned card, so the assignee filter can be exercised end to end.
func boardAssigneeFixture() *jira.FakeClient {
	active := func(iss jira.Issue) jira.Issue {
		iss.Type, iss.StatusCategory, iss.ActiveSprint = "Task", "In Progress", "KW29"
		return iss
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		active(jira.Issue{Key: "DCAI-10", Summary: "Ada work one", Status: "In Progress", Assignee: "Ada Lovelace", AssigneeAvatarURL: "https://avatar.example/ada/48.png"}),
		active(jira.Issue{Key: "DCAI-11", Summary: "Ada work two", Status: "Refinement", Assignee: "Ada Lovelace"}),
		active(jira.Issue{Key: "DCAI-12", Summary: "Grace work", Status: "Review / Testing", Assignee: "Grace Hopper"}),
		active(jira.Issue{Key: "DCAI-13", Summary: "Nobody's work", Status: "In Progress"}), // unassigned
	}}
}

// boardColumnDataStatuses is the fixed set of workflow-order columns the Board
// always renders (open + Done-category), regardless of any filter.
var boardColumnDataStatuses = []string{
	`data-status="Refinement"`,
	`data-status="Ready To Do"`,
	`data-status="In Progress"`,
	`data-status="Review / Testing"`,
	`data-status="DONE (This Sprint)"`,
	`data-status="Ready for Release"`,
	`data-status="Released / Deployed"`,
}

func assertAllColumnsRender(t *testing.T, body string) {
	t.Helper()
	for _, want := range boardColumnDataStatuses {
		if strings.Count(body, want) == 0 {
			t.Errorf("expected column %q to remain rendered\n", want)
		}
	}
}

// AC1: /board renders a filter chrome region with the multi-select assignee
// avatar bar (same control/semantics as Daily) — a chip per active-sprint
// assignee plus the trailing Unassigned sentinel chip.
func TestBoardRendersAssigneeFilterChrome(t *testing.T) {
	app := newBoardApp(t, boardAssigneeFixture())
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="board-filters"`,
		`data-testid="board-assignee-bar"`,
		`data-testid="board-assignee:Ada Lovelace"`,
		`data-testid="board-assignee:Grace Hopper"`,
		`data-testid="board-assignee:__unassigned__"`,
		`data-testid="board-assignee-clear"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board filter chrome missing %q\n%s", want, body)
		}
	}
	// Fresh load: nothing selected, all columns render, every card shows.
	if strings.Contains(body, `aria-pressed="true"`) {
		t.Errorf("fresh /board must have no assignee chip pre-selected\n%s", body)
	}
	assertAllColumnsRender(t, body)
	for _, key := range []string{"DCAI-10", "DCAI-11", "DCAI-12", "DCAI-13"} {
		if !strings.Contains(body, key) {
			t.Errorf("fresh /board should show all cards, missing %q", key)
		}
	}
}

// AC2: /board/results is a fragment (no full HTML document) that renders the
// filtered board and is the HTMX swap target.
func TestBoardResultsIsFragment(t *testing.T) {
	app := newBoardApp(t, boardAssigneeFixture())
	body := get(t, app.URL+"/board/results")

	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, "<html") {
		t.Errorf("/board/results must be a fragment, not a full page\n%s", body)
	}
	// It still carries the board and the filter chrome so a swap re-reflects state.
	for _, want := range []string{`data-testid="board-filters"`, `data-testid="board-card-strip"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/board/results missing %q\n%s", want, body)
		}
	}
	assertAllColumnsRender(t, body)
}

// AC3: assignee selection is URL-encoded (?assignee=..., repeatable),
// bookmarkable, round-trips (selected chips marked + hidden inputs), and the
// Unassigned sentinel works.
func TestBoardAssigneeFilterHidesNonMatchingCards(t *testing.T) {
	app := newBoardApp(t, boardAssigneeFixture())

	// Single assignee: only Ada's cards remain; other assignees' cards are hidden.
	body := get(t, app.URL+"/board/results?assignee="+url.QueryEscape("Ada Lovelace"))
	assertAllColumnsRender(t, body) // columns stay put
	for _, want := range []string{"DCAI-10", "DCAI-11"} {
		if !strings.Contains(body, want) {
			t.Errorf("assignee=Ada should keep %q\n%s", want, body)
		}
	}
	for _, absent := range []string{`data-key="DCAI-12"`, `data-key="DCAI-13"`} {
		if strings.Contains(body, absent) {
			t.Errorf("assignee=Ada should hide %q", absent)
		}
	}
	// The selected chip is marked and its selection round-trips as a hidden input.
	if !strings.Contains(body, `data-testid="board-assignee:Ada Lovelace"`) ||
		!strings.Contains(body, `aria-pressed="true"`) {
		t.Errorf("selected Ada chip should be marked pressed\n%s", body)
	}
	if !strings.Contains(body, `data-filterparam name="assignee" value="Ada Lovelace"`) {
		t.Errorf("selection should round-trip as a hidden filter param\n%s", body)
	}

	// Unassigned sentinel: only the unassigned card remains.
	un := get(t, app.URL+"/board/results?assignee=__unassigned__")
	if !strings.Contains(un, `data-key="DCAI-13"`) {
		t.Errorf("assignee=__unassigned__ should keep the unassigned card\n%s", un)
	}
	for _, absent := range []string{`data-key="DCAI-10"`, `data-key="DCAI-12"`} {
		if strings.Contains(un, absent) {
			t.Errorf("assignee=__unassigned__ should hide assigned card %q", absent)
		}
	}

	// Repeatable param: union (OR) of Ada + Grace.
	both := get(t, app.URL+"/board/results?assignee="+url.QueryEscape("Ada Lovelace")+"&assignee="+url.QueryEscape("Grace Hopper"))
	for _, want := range []string{`data-key="DCAI-10"`, `data-key="DCAI-11"`, `data-key="DCAI-12"`} {
		if !strings.Contains(both, want) {
			t.Errorf("assignee union should keep %q\n%s", want, both)
		}
	}
	if strings.Contains(both, `data-key="DCAI-13"`) {
		t.Errorf("assignee union of Ada+Grace should hide the unassigned card")
	}
}

// AC3/AC4: the full page (bookmarked URL) round-trips the selection too, not just
// the fragment endpoint.
func TestBoardPageRoundTripsAssigneeSelection(t *testing.T) {
	app := newBoardApp(t, boardAssigneeFixture())
	body := get(t, app.URL+"/board?assignee="+url.QueryEscape("Grace Hopper"))

	if !strings.Contains(body, `data-filterparam name="assignee" value="Grace Hopper"`) {
		t.Errorf("/board should round-trip the bookmarked selection\n%s", body)
	}
	if !strings.Contains(body, `data-key="DCAI-12"`) {
		t.Errorf("/board?assignee=Grace should show Grace's card\n%s", body)
	}
	if strings.Contains(body, `data-key="DCAI-10"`) {
		t.Errorf("/board?assignee=Grace should hide Ada's card")
	}
	assertAllColumnsRender(t, body)
}

// boardNoEstimateFixture is an active-sprint mix of sized and unsized cards
// across two assignees plus one unassigned card, so the no-estimate toggle and
// its intersection with the assignee filter can be exercised end to end.
func boardNoEstimateFixture() *jira.FakeClient {
	active := func(iss jira.Issue) jira.Issue {
		iss.Type, iss.StatusCategory, iss.ActiveSprint = "Task", "In Progress", "KW29"
		iss.Status = "In Progress"
		return iss
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		active(jira.Issue{Key: "DCAI-20", Summary: "Ada sized", Assignee: "Ada Lovelace", Size: "M"}),
		active(jira.Issue{Key: "DCAI-21", Summary: "Ada unsized", Assignee: "Ada Lovelace"}),   // no estimate
		active(jira.Issue{Key: "DCAI-22", Summary: "Grace unsized", Assignee: "Grace Hopper"}), // no estimate
		active(jira.Issue{Key: "DCAI-23", Summary: "Grace sized", Assignee: "Grace Hopper", Size: "L"}),
		active(jira.Issue{Key: "DCAI-24", Summary: "Nobody unsized"}), // unassigned, no estimate
	}}
}

// AC1: /board renders a compact "No estimates" toggle in the filter chrome,
// default off (not pressed) on a fresh load.
func TestBoardRendersNoEstimateToggle(t *testing.T) {
	app := newBoardApp(t, boardNoEstimateFixture())
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="board-no-estimate"`,
		`data-testid="board-no-estimate-toggle"`,
		"No estimates",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board chrome missing no-estimate toggle marker %q\n%s", want, body)
		}
	}
	// Fresh load: toggle off (no control pressed), so every card shows.
	if strings.Contains(body, `aria-pressed="true"`) {
		t.Errorf("fresh /board must have the no-estimate toggle off\n%s", body)
	}
	for _, key := range []string{"DCAI-20", "DCAI-21", "DCAI-22", "DCAI-23", "DCAI-24"} {
		if !strings.Contains(body, key) {
			t.Errorf("fresh /board should show all cards, missing %q", key)
		}
	}
}

// AC2: with the toggle on, only cards with no estimate render; every column
// stays rendered (cards hidden, not columns).
func TestBoardNoEstimateHidesSizedCards(t *testing.T) {
	app := newBoardApp(t, boardNoEstimateFixture())
	body := get(t, app.URL+"/board/results?no-estimate=1")

	assertAllColumnsRender(t, body) // columns stay put
	for _, want := range []string{`data-key="DCAI-21"`, `data-key="DCAI-22"`, `data-key="DCAI-24"`} {
		if !strings.Contains(body, want) {
			t.Errorf("no-estimate=1 should keep unsized card %q\n%s", want, body)
		}
	}
	for _, absent := range []string{`data-key="DCAI-20"`, `data-key="DCAI-23"`} {
		if strings.Contains(body, absent) {
			t.Errorf("no-estimate=1 should hide sized card %q", absent)
		}
	}
}

// AC3: toggle state is URL-encoded, bookmarkable (round-trips on the full page),
// and the pressed control round-trips as a hidden filter param.
func TestBoardNoEstimateRoundTrips(t *testing.T) {
	app := newBoardApp(t, boardNoEstimateFixture())

	// The fresh toggle points at ?no-estimate=1 (turn on); when on it points back
	// at the bare path (turn off).
	off := get(t, app.URL+"/board")
	if !strings.Contains(off, `hx-get="/board/results?no-estimate=1"`) {
		t.Errorf("off toggle should turn on via ?no-estimate=1\n%s", off)
	}

	on := get(t, app.URL+"/board/results?no-estimate=1")
	// No assignee is selected in this fixture, so the only pressed control is the
	// no-estimate toggle.
	if !strings.Contains(on, `aria-pressed="true"`) {
		t.Errorf("on toggle should be marked pressed\n%s", on)
	}
	if !strings.Contains(on, `hx-get="/board/results"`) {
		t.Errorf("on toggle should turn off via the bare path\n%s", on)
	}
	if !strings.Contains(on, `data-filterparam name="no-estimate" value="1"`) {
		t.Errorf("on state should round-trip as a hidden filter param\n%s", on)
	}

	// Bookmarked full page round-trips the toggle too.
	page := get(t, app.URL+"/board?no-estimate=1")
	if !strings.Contains(page, `data-filterparam name="no-estimate" value="1"`) {
		t.Errorf("/board should round-trip the bookmarked toggle\n%s", page)
	}
	if strings.Contains(page, `data-key="DCAI-20"`) {
		t.Errorf("/board?no-estimate=1 should hide the sized card")
	}
}

// AC4: the no-estimate toggle composes with the assignee filter — both active is
// the intersection (Ada's cards ∩ no-estimate = only Ada's unsized card).
func TestBoardNoEstimateComposesWithAssignee(t *testing.T) {
	app := newBoardApp(t, boardNoEstimateFixture())
	body := get(t, app.URL+"/board/results?assignee="+url.QueryEscape("Ada Lovelace")+"&no-estimate=1")

	assertAllColumnsRender(t, body)
	// Only Ada's unsized card survives the intersection.
	if !strings.Contains(body, `data-key="DCAI-21"`) {
		t.Errorf("assignee=Ada ∩ no-estimate should keep DCAI-21\n%s", body)
	}
	for _, absent := range []string{
		`data-key="DCAI-20"`, // Ada, but sized
		`data-key="DCAI-22"`, // unsized, but Grace
		`data-key="DCAI-23"`, // Grace, sized
		`data-key="DCAI-24"`, // unsized, but unassigned
	} {
		if strings.Contains(body, absent) {
			t.Errorf("assignee=Ada ∩ no-estimate should hide %q", absent)
		}
	}
	// Both controls round-trip so the swapped panel keeps the combined state.
	if !strings.Contains(body, `data-filterparam name="assignee" value="Ada Lovelace"`) ||
		!strings.Contains(body, `data-filterparam name="no-estimate" value="1"`) {
		t.Errorf("intersection should round-trip both filters\n%s", body)
	}
}

// The chips are server-driven toggles that point at /board/results and encode the
// resulting selection, exactly like Daily's — so no client state is needed.
func TestBoardAssigneeChipsToggleViaBoardResults(t *testing.T) {
	app := newBoardApp(t, boardAssigneeFixture())

	// Fresh load: Ada's chip adds Ada. The "+" for the space is HTML-escaped to
	// &#43; in the attribute (the browser decodes it back to "+" for htmx), exactly
	// like the Daily bar.
	body := get(t, app.URL+"/board")
	if !strings.Contains(body, `hx-get="/board/results?assignee=Ada&#43;Lovelace"`) {
		t.Errorf("fresh Ada chip should toggle to add Ada via /board/results\n%s", body)
	}

	// With Ada selected: her chip toggles back to the bare all-assignees path.
	sel := get(t, app.URL+"/board/results?assignee="+url.QueryEscape("Ada Lovelace"))
	if !strings.Contains(sel, `hx-get="/board/results"`) {
		t.Errorf("selected Ada chip should toggle back to all (bare path)\n%s", sel)
	}
	// The panel is the swap target and the chips omit their own param from the
	// include (they carry the resulting set) while preserving other filters.
	if !strings.Contains(sel, `hx-target="#board-panel"`) {
		t.Errorf("board filter form should target #board-panel\n%s", sel)
	}
}
