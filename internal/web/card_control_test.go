package web_test

// Integration tests for the card-control marker (#221). A board card is both a
// link to Jira and, on the Board's drag surface, a drag source, so a control
// placed on a card has to opt out of both. data-card-control is the single
// attribute that buys both opt-outs: assets/card-control.js cancels the card
// link, and board-drag.js hands the same selector to Sortable's filter.
//
// These assertions exist because the failure mode is silent — a control missing
// one half of the marker throws nothing, it just stops responding — so the wiring
// is worth pinning rather than leaving to a manual click.

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// TestEditableControlsCarryTheCardControlMarker asserts every surface that
// renders an editable control on a card marks it, and loads the script that
// gives the marker its link opt-out. Both board surfaces (Board and Daily)
// render the same estimate control.
func TestEditableControlsCarryTheCardControlMarker(t *testing.T) {
	boardApp := newEstimateApp(t, boardFixture(), web.WithJiraBaseURL("https://9elf26.atlassian.net/"))
	dailyApp, _ := dailyEstimateApp(t)

	for _, tc := range []struct {
		surface string
		body    string
	}{
		{"Board", get(t, boardApp.URL+"/board")},
		{"Daily", get(t, dailyApp.URL+"/daily?preset=today")},
	} {
		if !strings.Contains(tc.body, "data-card-control") {
			t.Errorf("%s renders an editable control with no data-card-control marker", tc.surface)
		}
		if !strings.Contains(tc.body, `src="/static/card-control.js"`) {
			t.Errorf("%s does not load /static/card-control.js, so a marked control would still follow the card link", tc.surface)
		}
	}
}

// TestDragScriptFiltersOnTheCardControlMarker asserts the drag script keys its
// Sortable filter off the shared marker rather than a control-specific selector,
// which is what makes marking a new control enough to keep it from starting a
// drag.
func TestDragScriptFiltersOnTheCardControlMarker(t *testing.T) {
	app := newEstimateApp(t, boardFixture())
	script := get(t, app.URL+"/static/card-control.js")
	if !strings.Contains(script, "[data-card-control]") {
		t.Errorf("card-control.js does not act on [data-card-control]")
	}

	drag := get(t, app.URL+"/static/board-drag.js")
	if !strings.Contains(drag, `filter: "[data-card-control]"`) {
		t.Errorf("board-drag.js does not filter drags on [data-card-control]")
	}
}
