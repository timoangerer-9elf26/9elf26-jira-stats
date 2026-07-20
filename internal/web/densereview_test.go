package web_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// denseNow is the pinned review clock the dense dataset is built against
// (REVIEW_NOW=2026-07-15T12:00:00Z — 14:00 Europe/Berlin, Wed KW29).
var denseNow = time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

// newDenseApp boots the dense/adversarial review dataset (issue #104) under the
// pinned clock, with the Daily "me" identity the dataset expects.
func newDenseApp(t *testing.T) *testApp {
	t.Helper()
	return newTestAppAt(t, jira.NewDenseFakeClient(), denseNow, web.WithMe(jira.DenseMe))
}

// TestDenseDatasetStressesAllViews is the guard for the dense review dataset: it
// asserts that, under the pinned clock, the fixture keeps every view densely
// populated in the shape the layout-stress review protocol depends on. It exists
// so a future edit to densereview.go that silently empties a cohort cell, drops a
// Board column, or breaks the intra-done drop fails loudly rather than passing a
// clean-but-hollow acceptance review.
func TestDenseDatasetStressesAllViews(t *testing.T) {
	app := newDenseApp(t)

	t.Run("velocity has ~10 bars with a spread, a tall outlier and an ongoing active bar", func(t *testing.T) {
		body := get(t, app.URL+"/velocity")
		// The active sprint renders the ongoing marker (the #103 date-line-wrap surface).
		if !strings.Contains(body, "now (ongoing)") {
			t.Error("velocity: missing the active-sprint \"now (ongoing)\" label")
		}
		// A spread from a flat zero bar to the tall KW23 outlier.
		for _, want := range []string{"KW21: 0 points", "24 points", "KW29: 20 points"} {
			if !strings.Contains(body, want) {
				t.Errorf("velocity: missing %q", want)
			}
		}
		// Ten distinct sprint bars in the window (KW20..KW29).
		for i := 20; i <= 29; i++ {
			if !strings.Contains(body, "KW"+strconv.Itoa(i)) {
				t.Errorf("velocity: missing bar KW%d", i)
			}
		}
	})

	t.Run("sprint has every cohort x outcome cell populated and excludes the carry-over", func(t *testing.T) {
		body := get(t, app.URL+"/sprint")
		// Every non-total cohort×outcome cell carries a non-zero points headline.
		for _, cell := range []string{
			"sprint-cell:started:open:points\">12",
			"sprint-cell:started:finished:points\">17",
			"sprint-cell:started:removed:points\">5",
			"sprint-cell:added:open:points\">2",
			"sprint-cell:added:finished:points\">3",
			"sprint-cell:added:removed:points\">1",
			"sprint-cell:total:total:points\">40",
		} {
			if !strings.Contains(body, cell) {
				t.Errorf("sprint: expected populated cell %q", cell)
			}
		}
		// The pre-finished carry-over (#87) is excluded from the table entirely.
		total := get(t, app.URL+"/sprint/cell?row=total&col=total")
		if strings.Contains(total, "DCAI-D15") {
			t.Error("sprint: pre-finished carry-over DCAI-D15 must be excluded from every cell")
		}
		// The Started/Finished drill-down is a long list (9 cards).
		fin := get(t, app.URL+"/sprint/cell?row=started&col=finished")
		if n := strings.Count(fin, "data-testid=\"card:"); n < 8 {
			t.Errorf("sprint: expected a long Started/Finished drill-down, got %d cards", n)
		}
	})

	t.Run("board populates every workflow column and drops off-board cards", func(t *testing.T) {
		body := get(t, app.URL+"/board")
		for _, status := range []string{
			"Refinement", "Ready To Do", "In Progress", "Review / Testing",
			"DONE (This Sprint)", "Ready for Release", "Released / Deployed",
		} {
			if !strings.Contains(body, "data-status=\""+status+"\"") {
				t.Errorf("board: missing column %q", status)
			}
		}
		// The carry-over shows on the Board (an active-sprint member) even though the
		// Sprint table excludes it.
		if !strings.Contains(body, "card:DCAI-D15:key") {
			t.Error("board: expected the carry-over DCAI-D15 to render on the board")
		}
		// Cancelled / left-sprint tickets are off the board.
		for _, absent := range []string{"card:DCAI-D13:key", "card:DCAI-D14:key", "card:DCAI-D18:key"} {
			if strings.Contains(body, absent) {
				t.Errorf("board: %q should not render (cancelled or left the sprint)", absent)
			}
		}
	})

	t.Run("daily has a dense digest, created tickets and drops the intra-done sequence", func(t *testing.T) {
		body := get(t, app.URL+"/daily")
		if !strings.Contains(body, "moved 5 — 2 finished, 2 advanced, 1 pulled back, created 2") {
			t.Error("daily: expected the dense digest headline")
		}
		// The intra-done-only ticket (#98) is dropped from the Daily view.
		if strings.Contains(body, "DCAI-D19") {
			t.Error("daily: intra-done-only ticket DCAI-D19 must be dropped (#98)")
		}
		// Created tickets (authored today by me) are surfaced.
		for _, created := range []string{"DCAI-D20", "DCAI-D21"} {
			if !strings.Contains(body, created) {
				t.Errorf("daily: missing created ticket %q", created)
			}
		}
		// Several distinct assignees make the Assignee control non-trivial.
		for _, name := range []string{jira.DenseMe, "Bo", "Carla Mendez-Ortiz", "Devraj Subramaniam", "Ekaterina Vasilyeva"} {
			if !strings.Contains(body, name) {
				t.Errorf("daily: missing assignee option %q", name)
			}
		}
	})
}
