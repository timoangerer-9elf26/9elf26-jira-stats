package web_test

// Integration tests for the data-card-control marker (#221) — the single
// attribute that opts a control on a board card out of both the card's link and
// (on the Board) the card's drag. The contract and its rules live in
// assets/card-control.js.
//
// These exist because the marker's failure mode is silence: a control that
// carries only half the wiring throws nothing, it just stops responding. So the
// wiring is pinned here rather than left to a manual click.

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// TestEditableControlsCarryTheCardControlMarker asserts the estimate control's
// ROOT carries the marker on both board surfaces — the Board and the Daily
// board render the same control (docs/adr/0005 amended) and both inherit the
// hazard. Asserting the attribute next to the control's testid is deliberate: a
// bare "data-card-control" search would be satisfied by the option spans'
// hx-target selector even if the root lost its marker.
func TestEditableControlsCarryTheCardControlMarker(t *testing.T) {
	boardApp := newEstimateApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	dailyApp, _ := dailyEstimateApp(t)

	for _, tc := range []struct {
		surface string
		key     string
		body    string
	}{
		{"Board", "DCAI-11", get(t, boardApp.URL+"/board")},
		{"Daily", "DCAI-40", get(t, dailyApp.URL+"/daily?preset=today")},
	} {
		want := `data-card-control data-testid="card:` + tc.key + `:estimate"`
		if !strings.Contains(tc.body, want) {
			t.Errorf("%s estimate control is missing %q\n%s", tc.surface, want, tc.body)
		}
	}
}

// TestAssignControlCarriesTheCardControlMarker asserts the Board's Assignee edit
// (#224) opts out through the SAME marker rather than arranging its own link and
// drag opt-outs. The avatar sits at the card's bottom-right, on the drag surface,
// inside the card's link: carrying only half the wiring would fail silently —
// either the popover never opens and the card drags instead, or picking a person
// navigates to Jira.
func TestAssignControlCarriesTheCardControlMarker(t *testing.T) {
	app := newAssignApp(t, assignFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/board")

	want := `data-card-control data-testid="card:DCAI-11:assignee"`
	if !strings.Contains(body, want) {
		t.Errorf("the Board assign control is missing %q\n%s", want, body)
	}
}

// TestAssignControlDoesNotStopClickPropagation asserts the rule
// assets/card-control.js states outright: the marker's link opt-out is a
// DELEGATED document listener, so a control that stops a click short of the
// document is correctly marked and still navigates away. The assign popover is
// CSS-only precisely so it never needs to — this pins that it stays that way.
func TestAssignControlDoesNotStopClickPropagation(t *testing.T) {
	app := newAssignApp(t, assignFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	body := get(t, app.URL+"/board")

	if strings.Contains(body, "stopPropagation") {
		t.Errorf("the Board markup calls stopPropagation, which silently breaks the card-control marker\n%s", body)
	}
}

// TestEveryPageLoadsTheCardControlScript asserts the script that gives the
// marker its link opt-out is loaded from the shared head, not per page — a page
// that renders a marked control without it would follow the card's link with no
// error to show for it.
func TestEveryPageLoadsTheCardControlScript(t *testing.T) {
	app := newEstimateApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))

	for _, path := range []string{"/board", "/daily", "/prio", "/sprint", "/velocity"} {
		body := get(t, app.URL+path)
		if !strings.Contains(body, `src="/static/card-control.js"`) {
			t.Errorf("%s does not load /static/card-control.js\n%s", path, body)
		}
	}
}

// TestDragScriptFiltersOnTheCardControlMarker asserts the drag script keys its
// Sortable filter off the shared marker rather than a control-specific
// selector, which is what makes marking a new control enough to keep it from
// starting a drag.
func TestDragScriptFiltersOnTheCardControlMarker(t *testing.T) {
	app := newEstimateApp(t, boardFixture())
	drag := get(t, app.URL+"/static/board-drag.js")
	if !strings.Contains(drag, `filter: "[data-card-control]"`) {
		t.Errorf("board-drag.js does not filter drags on [data-card-control]")
	}
}
