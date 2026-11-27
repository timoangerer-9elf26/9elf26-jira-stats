//go:build browser

package web_test

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

func TestPrioFilterResultsInBrowser(t *testing.T) {
	cutoff := time.Date(2026, time.November, 20, 10, 0, 0, 0, time.UTC)
	fixture := prioAgendaFilterFixture(cutoff)
	app := newTestAppWith(t, fixture, cutoff.AddDate(0, 0, 7), web.WithFakeJira(agendaFilterJira{fixture}))
	cmd := exec.CommandContext(t.Context(), "node", "e2e/prio-filter-results.mjs", app.URL)
	cmd.Dir = "../.."
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
