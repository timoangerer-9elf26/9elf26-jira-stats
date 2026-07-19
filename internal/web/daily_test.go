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
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
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
	// From → To change and its Berlin timestamp render.
	if !strings.Contains(body, "In Progress → Review / Testing") {
		t.Errorf("DCAI-1 change (From → To) not rendered:\n%s", body)
	}
	if !strings.Contains(body, "08:00") {
		t.Errorf("DCAI-1 change timestamp (Berlin) not rendered:\n%s", body)
	}
	// Cards sorted most-recent-first: DCAI-4 (09:00) before DCAI-1 (08:00).
	assertOrder(t, body, `data-key="DCAI-4"`, `data-key="DCAI-1"`)
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

	body := get(t, app.URL+"/daily/results?assignee=all&preset=yesterday")

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

	body := get(t, app.URL+"/daily/results?assignee=all&preset=day-before-yesterday")

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

	body := get(t, app.URL+"/daily/results?assignee=all&from=2026-07-14T00:00&to=2026-07-16T00:00")

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

	body := get(t, app.URL+"/daily/results?assignee=all&from=2026-07-16T10:00&to=2026-07-16T08:00")

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

	body := get(t, app.URL+"/daily/results?assignee=unassigned&preset=today")

	if !strings.Contains(body, `data-key="DCAI-4"`) {
		t.Errorf("Unassigned/Today should include DCAI-4\n%s", body)
	}
	for _, key := range []string{"DCAI-1", "DCAI-2"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("Unassigned/Today should NOT include assigned %s", key)
		}
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

	body := get(t, app.URL+"/daily/results?assignee=all&preset=today")
	if !strings.Contains(body, `data-testid="daily-empty"`) {
		t.Errorf("expected friendly empty state when nothing matches:\n%s", body)
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
	if !strings.Contains(body, `data-testid="daily-assignee"`) {
		t.Fatalf("results fragment must include the controls so they re-render:\n%s", body)
	}
	// The selected assignee stays selected.
	if !strings.Contains(body, `value="bob" selected`) {
		t.Errorf("bob should stay selected in the assignee dropdown:\n%s", body)
	}
	// The selected preset stays highlighted; the others do not.
	if !presetSelected(body, "yesterday") {
		t.Errorf("yesterday preset should stay selected:\n%s", body)
	}
	if presetSelected(body, "today") {
		t.Errorf("today preset should NOT be selected after choosing yesterday")
	}
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

// TestDailyDefaultsToMe: opening Daily with no assignee param selects the
// configured "me" (a Jira display name) — the dropdown marks me selected and
// the results are scoped to me, not "All".
func TestDailyDefaultsToMe(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("alice"))

	body := get(t, app.URL+"/daily") // no assignee param

	if !strings.Contains(body, `value="alice" selected`) {
		t.Errorf("me (alice) should be selected by default:\n%s", body)
	}
	if strings.Contains(body, `value="all" selected`) {
		t.Errorf("All must not be selected when me is the default")
	}
	// Scoped to alice: her DCAI-1 shows; bob's DCAI-2 and the unassigned DCAI-4 do not.
	if !strings.Contains(body, `data-key="DCAI-1"`) {
		t.Errorf("default me scope should include alice's DCAI-1:\n%s", body)
	}
	for _, key := range []string{"DCAI-2", "DCAI-4"} {
		if strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("default me scope should exclude %s (not alice)", key)
		}
	}
}

// TestDailyMeDefaultOverridable: explicitly choosing another assignee or "All"
// overrides the me default.
func TestDailyMeDefaultOverridable(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("alice"))

	all := get(t, app.URL+"/daily/results?assignee=all&preset=today")
	if !strings.Contains(all, `value="all" selected`) {
		t.Errorf("explicit All should be selected, overriding me:\n%s", all)
	}
	for _, key := range []string{"DCAI-1", "DCAI-4"} {
		if !strings.Contains(all, `data-key="`+key+`"`) {
			t.Errorf("explicit All should include %s\n%s", key, all)
		}
	}

	bob := get(t, app.URL+"/daily/results?assignee=bob&preset=today")
	if !strings.Contains(bob, `value="bob" selected`) {
		t.Errorf("explicit bob should be selected, overriding me:\n%s", bob)
	}
	if strings.Contains(bob, `data-key="DCAI-1"`) {
		t.Errorf("explicit bob scope should exclude alice's DCAI-1")
	}
}

// TestDailyNoMeConfiguredFallsBackToAll: with no me configured, opening Daily
// with no assignee param keeps the current "All" default (no crash).
func TestDailyNoMeConfiguredFallsBackToAll(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now) // no WithMe

	body := get(t, app.URL+"/daily")
	if !strings.Contains(body, `value="all" selected`) {
		t.Errorf("with no me configured, All should be the default selection:\n%s", body)
	}
	for _, key := range []string{"DCAI-1", "DCAI-4"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("All default should include %s\n%s", key, body)
		}
	}
}

// TestDailyMeNotOnActiveSprintStillSelected: a configured me who has no active-
// sprint work isn't among the sprint assignees, but the default must still show
// me selected in the dropdown (reflecting the actual scope) rather than silently
// falling back to All. Covers the not-on-sprint branch of dailyView.
func TestDailyMeNotOnActiveSprintStillSelected(t *testing.T) {
	client, now := dailyFixture(t)
	// carol only has DCAI-6, which is not in the active sprint, so carol is not
	// among the active-sprint assignees.
	app := newTestAppAt(t, client, now, web.WithMe("carol"))

	body := get(t, app.URL+"/daily") // no assignee param

	if !strings.Contains(body, `value="carol" selected`) {
		t.Errorf("me (carol) should be selected even though not on the active sprint:\n%s", body)
	}
	if strings.Contains(body, `value="all" selected`) {
		t.Errorf("All must not be selected when me is the default")
	}
	// carol's only ticket is out of the active-sprint scope, so no cards show.
	if strings.Contains(body, `data-key="DCAI-6"`) {
		t.Errorf("carol's non-sprint DCAI-6 must not appear in the sprint-scoped Daily")
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

// TestDailyDigest: the digest section renders above the granular log, groups
// each moved ticket under its net-movement bucket, and its headline counts match
// the groupings.
func TestDailyDigest(t *testing.T) {
	client, now := dailyDigestFixture(t)
	app := newTestAppAt(t, client, now, web.WithMe("alice"))

	body := get(t, app.URL+"/daily") // defaults to alice + Today

	// Headline: total plus a per-bucket breakdown that matches the groupings.
	if !strings.Contains(body, "moved 3 — 1 finished, 1 advanced, 1 pulled back") {
		t.Errorf("digest headline missing/incorrect:\n%s", body)
	}
	// Each ticket lands under its bucket with its net From ⟶ To movement.
	for _, want := range []string{
		`data-testid="digest-bucket:finished"`,
		`data-testid="digest-bucket:advanced"`,
		`data-testid="digest-bucket:pulled-back"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("digest bucket missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "In Progress ⟶ DONE (This Sprint)") {
		t.Errorf("digest net movement for the finished ticket not rendered:\n%s", body)
	}
	if !strings.Contains(body, "Review / Testing ⟶ In Progress") {
		t.Errorf("digest net movement for the pulled-back ticket not rendered:\n%s", body)
	}
	// The digest renders above the granular per-transition log.
	assertOrder(t, body, `data-testid="daily-digest"`, `data-testid="daily-results"`)
}

// TestDailyDigestOmitsEmptyBuckets: a selection whose tickets are all one bucket
// shows only that bucket, and the headline lists only the non-empty bucket.
func TestDailyDigestOmitsEmptyBuckets(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// All-assignees Today in the base fixture are all Advanced (DCAI-1/4).
	body := get(t, app.URL+"/daily/results?assignee=all&preset=today")

	if !strings.Contains(body, "moved 2 — 2 advanced") {
		t.Errorf("headline should list only the non-empty bucket:\n%s", body)
	}
	if strings.Contains(body, `data-testid="digest-bucket:finished"`) ||
		strings.Contains(body, `data-testid="digest-bucket:pulled-back"`) {
		t.Errorf("empty buckets must not render:\n%s", body)
	}
}

// TestDailyDigestAbsentWhenEmpty: with no in-window changes there is no digest.
func TestDailyDigestAbsentWhenEmpty(t *testing.T) {
	client, now := dailyFixture(t)
	app := newTestAppAt(t, client, now)

	// carol has no active-sprint work in the window.
	body := get(t, app.URL+"/daily/results?assignee=carol&preset=today")
	if strings.Contains(body, `data-testid="daily-digest"`) {
		t.Errorf("digest must not render for an empty selection:\n%s", body)
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
