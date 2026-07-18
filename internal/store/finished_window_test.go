package store

// Projection-level tests for the Sprint view's "Finished" rollup
// (FinishedInWindow): the same Done-crossing semantics as CompletedInRange, but
// scoped to the active-sprint membership snapshot (active_sprint IS NOT NULL).

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// saveActiveSprintCrossing saves a Task/Bug/Story that crossed into the given
// Done status at `at`, with the given active-sprint membership ("" = none).
func saveActiveSprintCrossing(t *testing.T, st *Store, key, size, toStatus, activeSprint string, at time.Time) {
	t.Helper()
	if err := st.SaveIssue(jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: toStatus, StatusCategory: "Done",
		Size: size, ActiveSprint: activeSprint,
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: "In Progress", To: toStatus, Timestamp: at},
		},
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

// TestFinishedInWindowScopesToActiveSprint asserts FinishedInWindow counts only
// active-sprint crossings (excluding an otherwise-identical whole-project
// completion), and that a Ready-for-Release crossing counts (corrected Done set)
// — while the unscoped CompletedInRange still sees every crossing.
func TestFinishedInWindowScopesToActiveSprint(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	from, to := mondayWeek(t, loc, 2026, time.July, 15)
	at := time.Date(2026, time.July, 15, 14, 0, 0, 0, loc)

	saveActiveSprintCrossing(t, st, "DCAI-1", "S", "DONE (This Sprint)", "KW29", at)
	saveActiveSprintCrossing(t, st, "DCAI-2", "M", "Ready for Release", "KW29", at) // Done state
	// Same window and Done crossing, but NOT in the active sprint → excluded.
	saveActiveSprintCrossing(t, st, "DCAI-3", "L", "DONE (This Sprint)", "", at)

	got, err := st.FinishedInWindow(from, to)
	if err != nil {
		t.Fatalf("FinishedInWindow: %v", err)
	}
	assertTally(t, "finished", got, SizeTally{S: 1, M: 1, Points: 1 + 2})

	all, err := st.CompletedInRange(from, to)
	if err != nil {
		t.Fatalf("CompletedInRange: %v", err)
	}
	assertTally(t, "completed", all, SizeTally{S: 1, M: 1, L: 1, Points: 1 + 2 + 3})
}
