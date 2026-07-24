package web_test

// Integration tests for the Board "Active in last 24h" filter (#159): the compact
// toggle in the filter chrome, the per-card latest-activity timestamp, the rolling
// [now − 24h, now) window (created-in-window OR a status change in it, intra-Done
// housekeeping ignored), the URL round-trip, and three-way composition with the
// assignee and no-estimate filters.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// boardActive24hFixture pins now = Thu 2026-07-16 10:00 Berlin, so the rolling
// window is [Wed 2026-07-15 10:00, Thu 2026-07-16 10:00). Each card exercises one
// branch of the "active" rule.
func boardActive24hFixture(t *testing.T) (*jira.FakeClient, time.Time) {
	t.Helper()
	loc := berlin(t)
	now := time.Date(2026, time.July, 16, 10, 0, 0, 0, loc)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }

	// active builds an active-sprint Task/Bug/Story with a creation instant and its
	// status changes.
	build := func(key, assignee, size, status string, created time.Time, cl ...jira.ChangelogEntry) jira.Issue {
		return jira.Issue{
			Key: key, Type: "Task", Summary: key + " summary", Status: status,
			StatusCategory: "In Progress", Size: size, Assignee: assignee,
			ActiveSprint: "KW29", CreatedAt: created, Changelog: cl,
		}
	}
	xn := func(id, from, to string, at time.Time) jira.ChangelogEntry {
		return jira.ChangelogEntry{ID: id, Field: "status", From: from, To: to, Timestamp: at}
	}

	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		// Active via an in-window status change (Ada, sized).
		build("DCAI-30", "Ada Lovelace", "M", "Review / Testing", at(12, 8),
			xn("m30", "In Progress", "Review / Testing", at(16, 8))),
		// Active via creation in the window, no moves (Grace, unsized).
		build("DCAI-31", "Grace Hopper", "", "Refinement", at(16, 7)),
		// NOT active: only in-window activity is intra-Done housekeeping; the finish
		// crossing (its last real activity) was before the window.
		build("DCAI-32", "Ada Lovelace", "L", "Released / Deployed", at(12, 8),
			xn("f32", "In Progress", "DONE (This Sprint)", at(14, 10)),
			xn("h32", "DONE (This Sprint)", "Released / Deployed", at(16, 9))),
		// NOT active: last activity was before the window.
		build("DCAI-33", "Grace Hopper", "M", "In Progress", at(12, 8),
			xn("m33", "Refinement", "In Progress", at(13, 9))),
		// Active AND unsized AND Ada — the sole survivor of the three-way intersection.
		build("DCAI-34", "Ada Lovelace", "", "In Progress", at(12, 8),
			xn("m34", "Refinement", "In Progress", at(16, 9))),
	}}, now
}

// AC2/AC4: /board renders the "Active in last 24h" toggle (default off) plus a
// subtle latest-activity timestamp on every card.
func TestBoardRendersActive24hToggleAndTimestamps(t *testing.T) {
	client, now := boardActive24hFixture(t)
	app := newTestAppAt(t, client, now)
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="board-active-24h"`,
		`data-testid="board-active-24h-toggle"`,
		"Active in last 24h",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board chrome missing active-24h toggle marker %q\n%s", want, body)
		}
	}
	// Fresh load: toggle off (no control pressed), so every card shows.
	if strings.Contains(body, `aria-pressed="true"`) {
		t.Errorf("fresh /board must have the active-24h toggle off\n%s", body)
	}
	for _, key := range []string{"DCAI-30", "DCAI-31", "DCAI-32", "DCAI-33", "DCAI-34"} {
		if !strings.Contains(body, `data-key="`+key+`"`) {
			t.Errorf("fresh /board should show all cards, missing %q", key)
		}
	}
	// Every card carries a latest-activity timestamp (AC4). Spot-check the computed
	// instants: the in-window move (16.7. 08:00), the creation (16.7. 07:00), and
	// the housekeeping card falling back to its finish crossing (14.7. 10:00), NOT
	// the 16.7. 09:00 intra-Done hop.
	for _, want := range []string{
		`data-testid="card:DCAI-30:activity"`,
		"16.7. 08:00",
		"16.7. 07:00",
		"14.7. 10:00",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected latest-activity timestamp %q on the board\n%s", want, body)
		}
	}
}

// AC3: with the toggle on, only cards active in [now − 24h, now) render; every
// column stays put; the intra-Done-housekeeping-only card is hidden.
func TestBoardActive24hHidesInactiveCards(t *testing.T) {
	client, now := boardActive24hFixture(t)
	app := newTestAppAt(t, client, now)
	body := get(t, app.URL+"/board/results?active-24h=1")

	assertAllColumnsRender(t, body) // columns stay put
	for _, want := range []string{`data-key="DCAI-30"`, `data-key="DCAI-31"`, `data-key="DCAI-34"`} {
		if !strings.Contains(body, want) {
			t.Errorf("active-24h=1 should keep active card %q\n%s", want, body)
		}
	}
	for _, absent := range []string{
		`data-key="DCAI-32"`, // only in-window move was intra-Done housekeeping
		`data-key="DCAI-33"`, // last activity before the window
	} {
		if strings.Contains(body, absent) {
			t.Errorf("active-24h=1 should hide inactive card %q", absent)
		}
	}
}

// AC5: toggle state is URL-encoded, bookmarkable (round-trips on the full page),
// and the pressed control round-trips as a hidden filter param.
func TestBoardActive24hRoundTrips(t *testing.T) {
	client, now := boardActive24hFixture(t)
	app := newTestAppAt(t, client, now)

	// The fresh toggle points at ?active-24h=1 (turn on).
	off := get(t, app.URL+"/board")
	if !strings.Contains(off, `hx-get="/board/results?active-24h=1"`) {
		t.Errorf("off toggle should turn on via ?active-24h=1\n%s", off)
	}

	on := get(t, app.URL+"/board/results?active-24h=1")
	if !strings.Contains(on, `aria-pressed="true"`) {
		t.Errorf("on toggle should be marked pressed\n%s", on)
	}
	if !strings.Contains(on, `hx-get="/board/results"`) {
		t.Errorf("on toggle should turn off via the bare path\n%s", on)
	}
	if !strings.Contains(on, `data-filterparam name="active-24h" value="1"`) {
		t.Errorf("on state should round-trip as a hidden filter param\n%s", on)
	}

	// Bookmarked full page round-trips the toggle too.
	page := get(t, app.URL+"/board?active-24h=1")
	if !strings.Contains(page, `data-filterparam name="active-24h" value="1"`) {
		t.Errorf("/board should round-trip the bookmarked toggle\n%s", page)
	}
	if strings.Contains(page, `data-key="DCAI-33"`) {
		t.Errorf("/board?active-24h=1 should hide the inactive card")
	}
}

// AC5: the active-24h toggle composes with the assignee and no-estimate filters —
// all three active is the intersection (Ada ∩ no-estimate ∩ active = only DCAI-34).
func TestBoardActive24hComposesWithAssigneeAndNoEstimate(t *testing.T) {
	client, now := boardActive24hFixture(t)
	app := newTestAppAt(t, client, now)
	body := get(t, app.URL+"/board/results?assignee="+url.QueryEscape("Ada Lovelace")+"&no-estimate=1&active-24h=1")

	assertAllColumnsRender(t, body)
	// Only DCAI-34 is Ada AND unsized AND active in the window.
	if !strings.Contains(body, `data-key="DCAI-34"`) {
		t.Errorf("Ada ∩ no-estimate ∩ active-24h should keep DCAI-34\n%s", body)
	}
	for _, absent := range []string{
		`data-key="DCAI-30"`, // Ada, active, but sized
		`data-key="DCAI-31"`, // unsized, active, but Grace
		`data-key="DCAI-32"`, // Ada, but sized and inactive
		`data-key="DCAI-33"`, // Grace, sized, inactive
	} {
		if strings.Contains(body, absent) {
			t.Errorf("three-way intersection should hide %q", absent)
		}
	}
	// All three controls round-trip so the swapped panel keeps the combined state.
	for _, want := range []string{
		`data-filterparam name="assignee" value="Ada Lovelace"`,
		`data-filterparam name="no-estimate" value="1"`,
		`data-filterparam name="active-24h" value="1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intersection should round-trip %q\n%s", want, body)
		}
	}
}
