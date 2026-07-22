package web_test

// Integration tests for the "Daily" standup view over the HTTP seam.
//
// Fixtures are active-sprint tickets whose `status` transitions fall inside or
// outside the working-day presets (Today / Yesterday / day-before-yesterday) or a
// custom range, across assignees (incl. Unassigned). A fixed clock pins "now" so
// each preset resolves deterministically. Tests drive the real handlers and
// assert on rendered HTML.

import (
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// dailyIssue builds an active-sprint (unless active=false) Task/Bug/Story with a
// single status transition at `at`.
func dailyIssue(key, typ, assignee string, active bool, from, to string, at time.Time) jira.Issue {
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: to,
		StatusCategory: "In Progress", Size: "M", Assignee: assignee,
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: from, To: to, Timestamp: at},
		},
	}
	if active {
		iss.ActiveSprint = "KW29"
	}
	return iss
}

// dailyFixture pins the transitions relative to now = Thu 2026-07-16 10:00
// Berlin, so the presets resolve to:
//
//	Today               = [2026-07-16 00:00, 2026-07-17 00:00)
//	Yesterday           = [2026-07-15 00:00, 2026-07-16 00:00)  (Wed)
//	day-before-yesterday = [2026-07-14 00:00, 2026-07-15 00:00)  (Tue)
func dailyFixture(t *testing.T) (*jira.FakeClient, time.Time) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		// Inside Today.
		dailyIssue("DCAI-1", "Story", "alice", true, "In Progress", "Review / Testing", at(16, 8)),
		dailyIssue("DCAI-4", "Bug", "", true, "Ready to Do", "In Progress", at(16, 9)),
		// Inside Yesterday (Wed 15).
		dailyIssue("DCAI-2", "Task", "bob", true, "Refinement", "Ready to Do", at(15, 11)),
		dailyIssue("DCAI-3", "Story", "alice", true, "Refinement", "Ready to Do", at(15, 6)),
		// Inside day-before-yesterday (Tue 14).
		dailyIssue("DCAI-9", "Task", "alice", true, "Ready to Do", "In Progress", at(14, 9)),
		// Outside every preset (Mon 13).
		dailyIssue("DCAI-5", "Story", "alice", true, "Ready to Do", "In Progress", at(13, 9)),
		// Excluded by scope: recent change but not in the active sprint.
		dailyIssue("DCAI-6", "Story", "carol", false, "Ready to Do", "In Progress", at(16, 9)),
		// Excluded types.
		dailyIssue("DCAI-7", "Epic", "alice", true, "Ready to Do", "In Progress", at(16, 9)),
		dailyIssue("DCAI-8", "Sub-task", "alice", true, "Ready to Do", "In Progress", at(16, 9)),
	}}, now
}

func TestDailyAllAssigneesToday(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily") // default: All + Today

	for _, key := range []string{"DCAI-1", "DCAI-4"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Today/All should include %s\n%s", key, body)
		}
	}
	for _, key := range []string{"DCAI-2", "DCAI-3", "DCAI-5", "DCAI-6", "DCAI-7", "DCAI-8", "DCAI-9"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Today/All should NOT include %s", key)
		}
	}
	// The origin badge names where the card came from, and its Berlin timestamp
	// renders in the compact "16.7. 08:00" form.
	if !strings.Contains(body, "from In Progress") {
		t.Errorf("DCAI-1 origin badge (from <status>) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "16.7. 08:00") {
		t.Errorf("DCAI-1 latest-activity timestamp (Berlin, compact) not rendered:\n%s", body)
	}
	// Placement is by window-end status: DCAI-4 lands in In Progress, DCAI-1 in
	// Review / Testing. The In Progress column renders before Review / Testing, so
	// DCAI-4 appears before DCAI-1 in the DOM.
	assertColumnHasCard(t, body, "In Progress", "DCAI-4")
	assertColumnHasCard(t, body, "Review / Testing", "DCAI-1")
	assertOrder(t, body, `data-key="DCAI-4"`, `data-key="DCAI-1"`)
}

// assertColumnHasCard checks that the given card key appears within the board
// column of the given status name (a card placed in its window-end column).
func assertColumnHasCard(t *testing.T, body, status, key string) {
	t.Helper()
	marker := `data-status="` + status + `"`
	start := strings.Index(body, marker)
	if start == -1 {
		t.Errorf("column %q not found:\n%s", status, body)
		return
	}
	// The next column starts at the following data-status; the card must fall
	// before it.
	rest := body[start+len(marker):]
	end := strings.Index(rest, `data-testid="daily-column"`)
	if end == -1 {
		end = len(rest)
	}
	if !strings.Contains(rest[:end], `data-key="`+key+`"`) {
		t.Errorf("card %s should be in column %q:\n%s", key, status, body)
	}
}

func TestDailySpecificAssignee(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=alice&preset=today")

	if !strings.Contains(body, `data-key="DCAI-1"`) {
		t.Errorf("alice/Today should include DCAI-1\n%s", body)
	}
	for _, key := range []string{"DCAI-2", "DCAI-3", "DCAI-4"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("alice/Today should NOT include %s", key)
		}
	}
}

func TestDailyYesterdayPreset(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?preset=yesterday")

	// Yesterday (Wed 15) holds DCAI-2 and DCAI-3.
	for _, key := range []string{"DCAI-2", "DCAI-3"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Yesterday/All should include %s\n%s", key, body)
		}
	}
	// Today's and the day-before's tickets are excluded.
	for _, key := range []string{"DCAI-1", "DCAI-4", "DCAI-9", "DCAI-5"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Yesterday should exclude out-of-window %s", key)
		}
	}
}

func TestDailyDayBeforeYesterdayPreset(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?preset=day-before-yesterday")

	// day-before-yesterday (Tue 14) holds only DCAI-9.
	if !strings.Contains(body, `data-key="DCAI-9"`) {
		t.Errorf("day-before-yesterday/All should include DCAI-9\n%s", body)
	}
	for _, key := range []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4", "DCAI-5"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("day-before-yesterday should exclude %s", key)
		}
	}
}

// TestDailyCustomRange: a custom ?from=&to= drives the results and is honoured
// verbatim (here spanning Tue 14 → Wed 15, so both DCAI-9 and the Wed tickets
// appear), with no preset highlighted.
func TestDailyCustomRange(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?from=2026-07-14T00:00&to=2026-07-16T00:00")

	for _, key := range []string{"DCAI-2", "DCAI-3", "DCAI-9"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("custom range should include %s\n%s", key, body)
		}
	}
	for _, key := range []string{"DCAI-1", "DCAI-4", "DCAI-5"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("custom range should exclude out-of-range %s", key)
		}
	}
	// No preset is highlighted in custom mode.
	for _, key := range []string{"today", "yesterday", "day-before-yesterday"} {
		if presetSelected(body, key) {
			t.Errorf("custom range should highlight no preset, but %s is pressed:\n%s", key, body)
		}
	}
	// The inputs round-trip the range.
	if !strings.Contains(body, `data-testid="daily-from" value="2026-07-14T00:00"`) {
		t.Errorf("From input should round-trip the custom value:\n%s", body)
	}
}

// TestDailyInvalidCustomRange: an invalid range (From >= Until) shows the inline
// error and renders no results — no fallback.
func TestDailyInvalidCustomRange(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?from=2026-07-16T10:00&to=2026-07-16T08:00")

	if !strings.Contains(body, `data-testid="daily-range-error"`) {
		t.Errorf("invalid range should show the inline error:\n%s", body)
	}
	// No results of any kind render.
	if strings.Contains(body, `data-testid="daily-results"`) {
		t.Errorf("invalid range must render no results section")
	}
	for _, key := range []string{"DCAI-1", "DCAI-2", "DCAI-3", "DCAI-4"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("invalid range must render no cards, got %s", key)
		}
	}
}

func TestDailyUnassigned(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=__unassigned__&preset=today")

	if !strings.Contains(body, `data-key="DCAI-4"`) {
		t.Errorf("Unassigned/Today should include DCAI-4\n%s", body)
	}
	for _, key := range []string{"DCAI-1", "DCAI-2"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Unassigned/Today should NOT include assigned %s", key)
		}
	}
	// The Unassigned chip is marked selected.
	if !assigneePressed(body, "__unassigned__") {
		t.Errorf("the Unassigned chip should be pressed:\n%s", body)
	}
}

// TestDailyCardAvatars: each Daily card shows the assignee avatar the way the
// Board does (#114) — the public Jira avatar image when captured, the computed
// initials fallback alongside it, and a neutral empty circle when unassigned.
// The plain assignee-name span is gone.
func TestDailyCardAvatars(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := time.Date(2026, time.July, 16, 9, 0, 0, 0, loc)

	withAvatar := dailyIssue("DCAI-1", "Story", "Alice Smith", true, "Ready to Do", "In Progress", at)
	withAvatar.AssigneeAvatarURL = "https://jira.example/avatar/alice.png"
	unassigned := dailyIssue("DCAI-4", "Bug", "", true, "Ready to Do", "In Progress", at)

	app := newTestAppAt(t, &jira.FakeClient{
		Sprints: activeSprintKW29(),
		Issues:  []jira.Issue{withAvatar, unassigned},
	}, now)

	body := get(t, app.URL+"/daily/results?preset=today")

	// Assigned-with-avatar: the image (sourced from the Daily query) plus its
	// hidden initials fallback ("AS").
	if !strings.Contains(body, `data-testid="card:DCAI-1:avatar-img"`) ||
		!strings.Contains(body, `src="https://jira.example/avatar/alice.png"`) {
		t.Errorf("DCAI-1 should render its Jira avatar image:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="card:DCAI-1:avatar-initials"`) ||
		!strings.Contains(body, `>AS</span>`) {
		t.Errorf("DCAI-1 should carry the computed initials fallback:\n%s", body)
	}
	// Unassigned: the neutral empty circle, no initials.
	if !strings.Contains(body, `data-testid="card:DCAI-4:avatar-empty"`) {
		t.Errorf("unassigned DCAI-4 should render the neutral empty circle:\n%s", body)
	}
	if strings.Contains(body, `data-testid="card:DCAI-4:avatar-initials"`) {
		t.Errorf("unassigned DCAI-4 must not render an initials circle:\n%s", body)
	}
	// The old plain assignee-name span is gone.
	if strings.Contains(body, `data-testid="card:DCAI-1:assignee"`) {
		t.Errorf("the plain assignee-name span should be replaced by the avatar:\n%s", body)
	}
}

func TestDailyEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	// One active-sprint ticket, but its only change is well out of every preset.
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		dailyIssue("DCAI-1", "Story", "alice", true, "Ready to Do", "In Progress",
			time.Date(2026, time.July, 1, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/daily/results?preset=today")
	// The five workflow columns always render, even when the board is empty.
	if !strings.Contains(body, `data-testid="daily-board"`) {
		t.Errorf("board should still render its columns when empty:\n%s", body)
	}
	// The strip scrolls horizontally only; vertical scroll belongs to the window (#120).
	if !strings.Contains(body, "overflow-x-auto") || !strings.Contains(body, "overflow-y:hidden") {
		t.Errorf("daily board strip must scroll horizontally only (overflow-x-auto + overflow-y:hidden):\n%s", body)
	}
	for _, col := range []string{"Refinement", "Ready To Do", "In Progress", "Review / Testing", "Done"} {
		if !strings.Contains(body, `data-status="`+col+`"`) {
			t.Errorf("empty board should still render the %q column:\n%s", col, body)
		}
	}
	// Canceled has no card, so its column is not rendered.
	if strings.Contains(body, `data-status="Canceled"`) {
		t.Errorf("empty Canceled column must not render")
	}
	if strings.Contains(body, `data-key="DCAI-1"`) {
		t.Errorf("out-of-window ticket must not render")
	}
}

func TestDailyNoActiveSprintEmptyState(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	app := newTestAppAt(t, &jira.FakeClient{Issues: []jira.Issue{
		dailyIssue("DCAI-1", "Story", "alice", false, "Ready to Do", "In Progress",
			time.Date(2026, time.July, 16, 9, 0, 0, 0, loc)),
	}}, now)

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-testid="daily-no-sprint"`) {
		t.Errorf("expected friendly no-active-sprint state:\n%s", body)
	}
}

// TestDailyPanelKeepsSelectionAfterSwap is the regression guard (cf. the
// Completed picker): the swapped panel must re-render with the selected assignee
// and window marked, not reset to the defaults.
func TestDailyPanelKeepsSelectionAfterSwap(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=bob&preset=yesterday")

	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Fatalf("results endpoint must return a partial, got full document:\n%s", body)
	}
	// The controls are part of the swapped fragment.
	if !strings.Contains(body, `data-testid="daily-assignee-bar"`) {
		t.Fatalf("results fragment must include the controls so they re-render:\n%s", body)
	}
	// The selected assignee stays selected: its chip is pressed and its value is
	// carried in a hidden input so a preset/range change round-trips it.
	if !assigneePressed(body, "bob") {
		t.Errorf("bob's chip should stay pressed:\n%s", body)
	}
	if !strings.Contains(body, `<input type="hidden" name="assignee" value="bob">`) {
		t.Errorf("bob should be carried in a hidden assignee input:\n%s", body)
	}
	// The selected preset stays highlighted; the others do not.
	if !presetSelected(body, "yesterday") {
		t.Errorf("yesterday preset should stay selected:\n%s", body)
	}
	if presetSelected(body, "today") {
		t.Errorf("today preset should NOT be selected after choosing yesterday")
	}
}

// assigneePressed reports whether the assignee avatar chip for the given value is
// marked selected (aria-pressed) in the rendered controls.
func assigneePressed(html, value string) bool {
	marker := `data-testid="daily-assignee:` + value + `"`
	start := strings.Index(html, marker)
	if start == -1 {
		return false
	}
	end := strings.Index(html[start:], ">")
	if end == -1 {
		return false
	}
	return strings.Contains(html[start:start+end], `aria-pressed="true"`)
}

// presetSelected reports whether the preset button for the given key is marked
// selected (aria-pressed) in the rendered controls.
func presetSelected(html, key string) bool {
	marker := `data-testid="daily-preset:` + key + `"`
	start := strings.Index(html, marker)
	if start == -1 {
		return false
	}
	end := strings.Index(html[start:], ">")
	if end == -1 {
		return false
	}
	return strings.Contains(html[start:start+end], `aria-pressed="true"`)
}

// TestDailyDefaultsToAll: opening Daily with no assignee param selects no chip
// (zero selected = all), renders an avatar bar with one chip per active-sprint
// assignee plus a trailing Unassigned chip, and shows no Clear affordance (#136).
func TestDailyDefaultsToAll(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily") // no assignee param

	if !strings.Contains(body, `data-testid="daily-assignee-bar"`) {
		t.Fatalf("the avatar filter bar should render:\n%s", body)
	}
	if strings.Contains(body, `>Assignee</span>`) {
		t.Errorf("the avatar filter bar should not render an Assignee text label:\n%s", body)
	}
	if !strings.Contains(body, `data-testid="daily-assignee-avatars" class="flex flex-wrap items-center"`) {
		t.Errorf("avatar chips should rely on their own padding and border, with no inter-chip gap:\n%s", body)
	}
	// A chip per active-sprint assignee (alice, bob) plus the Unassigned chip.
	for _, v := range []string{"alice", "bob", "__unassigned__"} {
		if !strings.Contains(body, `data-testid="daily-assignee:`+v+`"`) {
			t.Errorf("missing avatar chip for %q:\n%s", v, body)
		}
	}
	// Every chip retains both accessible-name affordances.
	for _, name := range []string{"alice", "bob", "Unassigned"} {
		if !strings.Contains(body, `title="`+name+`" aria-label="`+name+`"`) {
			t.Errorf("avatar chip %q should retain its title and aria-label:\n%s", name, body)
		}
	}
	// Nothing is pressed by default (zero selected = all).
	for _, v := range []string{"alice", "bob", "__unassigned__"} {
		if assigneePressed(body, v) {
			t.Errorf("no chip should be pressed by default, but %q is", v)
		}
	}
	// The old single-select dropdown is gone.
	if strings.Contains(body, `<select name="assignee"`) {
		t.Errorf("the single-select assignee dropdown must be gone:\n%s", body)
	}
	// With nothing selected there is no Clear affordance.
	if strings.Contains(body, `data-testid="daily-assignee-clear"`) {
		t.Errorf("Clear should not show when nothing is selected")
	}
	// Alphabetical order: alice before bob, then the Unassigned chip last.
	assertOrder(t, body,
		`data-testid="daily-assignee:alice"`,
		`data-testid="daily-assignee:bob"`,
		`data-testid="daily-assignee:__unassigned__"`,
	)
}

// TestDailyExplicitAssigneeFilters: an explicit ?assignee=<name> scopes the board
// to that assignee and marks the chip pressed; zero selected (no assignee param)
// shows every assignee's cards and presses no chip.
func TestDailyExplicitAssigneeFilters(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// Zero selected carries no assignee param and shows every assignee's cards.
	all := get(t, app.URL+"/daily/results?preset=today")
	if assigneePressed(all, "alice") || assigneePressed(all, "bob") {
		t.Errorf("no chip should be pressed with no assignee param:\n%s", all)
	}
	for _, key := range []string{"DCAI-1", "DCAI-4"} {
		if !strings.Contains(all, `data-key="`+key+`"`) {
			t.Errorf("zero selected should include %s\n%s", key, all)
		}
	}

	// An explicit name scopes to that assignee and presses its chip.
	bob := get(t, app.URL+"/daily/results?assignee=bob&preset=today")
	if !assigneePressed(bob, "bob") {
		t.Errorf("explicit bob's chip should be pressed:\n%s", bob)
	}
	if strings.Contains(bob, `data-key="DCAI-1"`) {
		t.Errorf("explicit bob scope should exclude alice's DCAI-1")
	}
}

// TestDailyMultiAssigneeUnion: two selected chips show the UNION of their tickets
// (OR semantics), press both chips, and reveal the Clear affordance.
func TestDailyMultiAssigneeUnion(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// alice + Unassigned over Today: alice's DCAI-1 and the unassigned DCAI-4.
	body := get(t, app.URL+"/daily/results?assignee=alice&assignee=__unassigned__&preset=today")

	for _, key := range []string{"DCAI-1", "DCAI-4"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("union (alice OR unassigned) should include %s\n%s", key, body)
		}
	}
	// bob's ticket is out of Today, so it never shows regardless — but a bob-only
	// ticket must not leak in from an unselected chip. Assert both chips pressed.
	if !assigneePressed(body, "alice") || !assigneePressed(body, "__unassigned__") {
		t.Errorf("both alice and Unassigned chips should be pressed:\n%s", body)
	}
	if assigneePressed(body, "bob") {
		t.Errorf("bob's chip must not be pressed:\n%s", body)
	}
	// Clear shows once ≥1 is selected.
	if !strings.Contains(body, `data-testid="daily-assignee-clear"`) {
		t.Errorf("Clear should show when assignees are selected:\n%s", body)
	}
	if !strings.Contains(body, `rounded-md border border-slate-200 bg-white px-3 py-1.5 text-sm`) {
		t.Errorf("Clear should render as a bordered pill button:\n%s", body)
	}
	assertOrder(t, body,
		`data-testid="daily-assignee:__unassigned__"`,
		`data-testid="daily-assignee-clear"`,
	)
}

// TestDailyMultiAssigneeUnionYesterday: a Yesterday selection of alice + bob
// unions their tickets (DCAI-3 alice, DCAI-2 bob), proving OR across two named
// assignees, not just against Unassigned.
func TestDailyMultiAssigneeUnionYesterday(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=alice&assignee=bob&preset=yesterday")

	for _, key := range []string{"DCAI-2", "DCAI-3"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("union (alice OR bob) over Yesterday should include %s\n%s", key, body)
		}
	}
}

// TestDailyClearResetsToAll: the Clear affordance hx-GETs /daily/results with no
// assignee param, which resolves to zero selected (= all) — no chip pressed.
func TestDailyClearResetsToAll(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// The Clear button carries no assignee param (its hx-get is the bare path),
	// and hitting that path shows all cards with nothing pressed.
	cleared := get(t, app.URL+"/daily/results?preset=today")
	for _, v := range []string{"alice", "bob", "__unassigned__"} {
		if assigneePressed(cleared, v) {
			t.Errorf("after Clear no chip should be pressed, but %q is:\n%s", v, cleared)
		}
	}
	if strings.Contains(cleared, `data-testid="daily-assignee-clear"`) {
		t.Errorf("Clear should not show once selection is empty")
	}
	// The Clear button, when present, targets the bare results path.
	selected := get(t, app.URL+"/daily/results?assignee=alice&preset=today")
	if !strings.Contains(selected, `data-testid="daily-assignee-clear"`) ||
		!strings.Contains(selected, `hx-get="/daily/results" hx-include`) {
		t.Errorf("Clear should hx-get the bare /daily/results path:\n%s", selected)
	}
}

// TestDailyPresetPreservesAssigneeSelection: a preset button carries the current
// selection as hidden ?assignee= inputs (hx-included), and each chip's toggle URL
// preserves the range separately — so switching presets keeps the assignees.
func TestDailyPresetPreservesAssigneeSelection(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily/results?assignee=alice&assignee=bob&preset=today")

	// Both selections are carried as hidden inputs the preset buttons hx-include.
	for _, v := range []string{"alice", "bob"} {
		if !strings.Contains(body, `<input type="hidden" name="assignee" value="`+v+`">`) {
			t.Errorf("selection %q should be carried in a hidden input for the preset buttons:\n%s", v, body)
		}
	}
	// The preset buttons hx-include the assignee inputs so the selection survives.
	if !strings.Contains(body, `hx-get="/daily/results?preset=yesterday" hx-include="[name='assignee']"`) {
		t.Errorf("preset buttons must hx-include the assignee selection:\n%s", body)
	}
}

// dailyDigestFixture pins one ticket per net-movement bucket for alice, all
// inside Today (now = Thu 2026-07-16 10:00 Berlin).
func dailyDigestFixture(t *testing.T) (*jira.FakeClient, time.Time) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(hour int) time.Time { return time.Date(2026, time.July, 16, hour, 0, 0, 0, loc) }
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		dailyIssue("DCAI-10", "Story", "alice", true, "In Progress", "DONE (This Sprint)", at(9)), // finished
		dailyIssue("DCAI-11", "Task", "alice", true, "Ready to Do", "In Progress", at(8)),         // advanced
		dailyIssue("DCAI-12", "Bug", "alice", true, "Review / Testing", "In Progress", at(7)),     // pulled back
	}}, now
}

// TestDailyBoardMovementKinds: each moved ticket lands in the column of its
// window-end status with an origin badge coloured by its net-movement kind.
func TestDailyBoardMovementKinds(t *testing.T) {
	client, now := dailyDigestFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily") // defaults to All + Today

	// DCAI-10 finished (In Progress → DONE): Done column, finished-coloured badge.
	assertColumnHasCard(t, body, "Done", "DCAI-10")
	// DCAI-11 advanced (Ready to Do → In Progress): In Progress column.
	assertColumnHasCard(t, body, "In Progress", "DCAI-11")
	// DCAI-12 pulled back (Review / Testing → In Progress): In Progress column too.
	assertColumnHasCard(t, body, "In Progress", "DCAI-12")

	for _, want := range []string{
		`data-testid="card:DCAI-10:origin"`,
		"daily-origin--finished",
		"daily-origin--advanced",
		"daily-origin--pulled-back",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board movement cue missing %q:\n%s", want, body)
		}
	}
	// The origin badge names where each card came from.
	if !strings.Contains(body, "from In Progress") { // DCAI-10's origin
		t.Errorf("finished card origin (from In Progress) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "from Review / Testing") { // DCAI-12's origin
		t.Errorf("pulled-back card origin (from Review / Testing) not rendered:\n%s", body)
	}
}

// TestDailyIgnoresIntraDoneMoves pins issue #98 over the HTTP seam: a status
// move whose from AND to are both in the done set is dropped from BOTH the
// granular cards and the digest, and the surviving changes drive the net From ⟶
// To, the bucket, and whether a ticket appears at all.
func TestDailyIgnoresIntraDoneMoves(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(hour int) time.Time { return time.Date(2026, time.July, 16, hour, 0, 0, 0, loc) }

	// multiDaily builds an active-sprint ticket carrying a sequence of status
	// transitions (from, to) at ascending hours.
	multiDaily := func(key, typ string, pairs ...[3]string) jira.Issue {
		cl := make([]jira.ChangelogEntry, len(pairs))
		for i, p := range pairs {
			cl[i] = jira.ChangelogEntry{ID: key + "-" + p[2], Field: "status", From: p[0], To: p[1], Timestamp: at(9 + i)}
		}
		return jira.Issue{
			Key: key, Type: typ, Summary: key + " summary", Status: "In Progress",
			StatusCategory: "In Progress", Size: "M", Assignee: "alice",
			ActiveSprint: "KW29", Changelog: cl,
		}
	}

	client := &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		// In Progress → DONE → Released: the DONE→Released hop is intra-done, dropped;
		// net is In Progress ⟶ DONE (This Sprint), Finished.
		multiDaily("DCAI-20", "Story",
			[3]string{"In Progress", "DONE (This Sprint)", "a"},
			[3]string{"DONE (This Sprint)", "Released / Deployed", "b"}),
		// Only intra-done moves — disappears entirely.
		multiDaily("DCAI-21", "Task",
			[3]string{"DONE (This Sprint)", "Ready for Release", "a"},
			[3]string{"Ready for Release", "Released / Deployed", "b"}),
		// Reopen (done → non-done) — kept, pulled back.
		multiDaily("DCAI-22", "Bug",
			[3]string{"Ready for Release", "In Progress", "a"}),
	}}
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily") // All + Today

	// The intra-done-only ticket disappears from the board entirely (#98).
	if strings.Contains(body, `data-key="DCAI-21"`) {
		t.Errorf("DCAI-21 (only intra-done moves) must not appear anywhere on Daily:\n%s", body)
	}
	// DCAI-20 survives: its window-end status is Released / Deployed (a done
	// status), so it lands in the collapsed Done column, and its surviving move is
	// the finish crossing from In Progress.
	if !strings.Contains(body, `data-key="DCAI-20"`) {
		t.Errorf("DCAI-20 (finish crossing) should appear:\n%s", body)
	}
	assertColumnHasCard(t, body, "Done", "DCAI-20")
	if !strings.Contains(body, "from In Progress") {
		t.Errorf("DCAI-20 origin (from In Progress) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "daily-origin--finished") {
		t.Errorf("DCAI-20 should carry the finished movement colour:\n%s", body)
	}
	// The reopen (Ready for Release → In Progress) survives as a pulled-back move,
	// placed in the In Progress column.
	assertColumnHasCard(t, body, "In Progress", "DCAI-22")
	if !strings.Contains(body, "from Ready for Release") {
		t.Errorf("DCAI-22 reopen origin (from Ready for Release) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "daily-origin--pulled-back") {
		t.Errorf("DCAI-22 reopen should carry the pulled-back movement colour:\n%s", body)
	}
}

// TestDailyBoardCreatedHere: a ticket created in the window that never moved is
// placed in its creation-status column and highlighted "created here" (no origin
// "from" status, no movement colour). Also covers the Canceled column appearing.
func TestDailyBoardCreatedHere(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := time.Date(2026, time.July, 16, 8, 0, 0, 0, loc)

	// A created-today active-sprint ticket in Refinement, with no transitions.
	created := jira.Issue{
		Key: "DCAI-30", Type: "Story", Summary: "DCAI-30 summary", Status: "Refinement",
		StatusCategory: "To Do", Size: "M", Assignee: "alice", Creator: "alice",
		CreatedAt: at, ActiveSprint: "KW29",
	}
	// A moved-to-Canceled ticket so the Canceled column renders.
	canceled := dailyIssue("DCAI-31", "Bug", "alice", true, "In Progress", "Canceled",
		time.Date(2026, time.July, 16, 9, 0, 0, 0, loc))
	app := newTestAppAt(t, &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{created, canceled}}, now)

	body := get(t, app.URL+"/daily/results?assignee=alice&preset=today")

	assertColumnHasCard(t, body, "Refinement", "DCAI-30")
	if !strings.Contains(body, "created here") {
		t.Errorf("created-in-window card should read 'created here':\n%s", body)
	}
	// The Canceled column renders (rightmost) and holds the canceled card.
	assertColumnHasCard(t, body, "Canceled", "DCAI-31")
	assertOrder(t, body, `data-status="Done"`, `data-status="Canceled"`)
}

// TestDailyBoardEmptyForAssigneeWithNoWork: an assignee with no in-window work
// still gets the five columns, just empty.
func TestDailyBoardEmptyForAssigneeWithNoWork(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// carol has no active-sprint work in the window.
	body := get(t, app.URL+"/daily/results?assignee=carol&preset=today")
	if !strings.Contains(body, `data-testid="daily-board"`) {
		t.Errorf("board should render even for an empty selection:\n%s", body)
	}
	if strings.Contains(body, `data-testid="daily-card"`) {
		t.Errorf("no cards should render for carol:\n%s", body)
	}
}

// TestDailyControlsLayout: the controls bar renders the presets in chronological
// display order (weekday-named day-before → Yesterday → Today) on the far left,
// then a right-aligned group holding From, Until and the Assignee avatar bar in
// that order (the avatar bar last in the DOM). The group carries an auto
// left-margin.
func TestDailyControlsLayout(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily")

	// day-before (Tue 14 -> "Tuesday") then Yesterday then Today, left to right.
	assertOrder(t,
		body,
		`data-testid="daily-preset:day-before-yesterday"`,
		`data-testid="daily-preset:yesterday"`,
		`data-testid="daily-preset:today"`,
		`data-testid="daily-from"`,
		`data-testid="daily-to"`,
		`data-testid="daily-assignee-bar"`,
	)
	// The right-aligned group is pushed right via an auto left-margin.
	if !strings.Contains(body, `margin-left:auto`) {
		t.Errorf("the range/assignee group should be pushed right with an auto left-margin:\n%s", body)
	}
}

func TestDailyNavActive(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `data-nav="daily" aria-current="page"`) {
		t.Errorf("/daily should mark the Daily nav item active:\n%s", body)
	}
	if n := strings.Count(body, `aria-current="page"`); n != 1 {
		t.Errorf("/daily: expected exactly one active nav item, got %d", n)
	}
}
