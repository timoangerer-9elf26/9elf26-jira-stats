package store

// Projection-level tests for the Sprint-view cell drill-down (SprintCellIssues,
// #79): the issues behind one cohort × outcome cell, SELECTed as cards. The list
// must always match the cell's tally count (SprintCategoriesInWindow), reuse the
// exact cohort/outcome predicates (incl. the removal asymmetry), union the Total
// row / Total column from their constituents, and sort by workflow order.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// cellKeys returns the issue keys of a drill-down cell, in the returned order.
func cellKeys(t *testing.T, st *Store, cohort SprintCohortSel, outcome SprintOutcomeSel, from, to time.Time) []string {
	t.Helper()
	issues, err := st.SprintCellIssues(wcSprintID, from, to, cohort, outcome)
	if err != nil {
		t.Fatalf("SprintCellIssues(%d,%d): %v", cohort, outcome, err)
	}
	keys := make([]string, 0, len(issues))
	for _, is := range issues {
		keys = append(keys, is.Key)
	}
	return keys
}

func assertKeys(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

// TestSprintCellIssues drills into every cohort × outcome cell of the fixture
// from TestSprintCategoriesCohortOutcome and asserts the exact issue set behind
// each — including the removal asymmetry (AD-LEFT dropped), the Total row / Total
// column unions, and the grand-total workflow-order sort.
func TestSprintCellIssues(t *testing.T) {
	st := openTempStore(t)

	from := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	atStart := from
	afterGrace := from.Add(2 * time.Hour)
	inWindow := from.Add(48 * time.Hour)
	doneAt := from.Add(24 * time.Hour)

	if err := st.SaveSprint(jira.Sprint{ID: wcSprintID, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Started-with cohort.
	saveCategoryIssue(t, st, "SW-OPEN", "S", "In Progress",
		[]jira.ChangelogEntry{status("swo", "Ready To Do", "In Progress", atStart)},
		[]jira.SprintMembershipChange{enteredSprint("sw-o", atStart)})
	saveCategoryIssue(t, st, "SW-FIN", "M", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("swf1", "Ready To Do", "In Progress", atStart),
			status("swf2", "In Progress", "DONE (This Sprint)", doneAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("sw-f", atStart)})
	saveCategoryIssue(t, st, "SW-CANCEL", "L", "Canceled",
		[]jira.ChangelogEntry{
			status("swc1", "Ready To Do", "In Progress", atStart),
			status("swc2", "In Progress", "Canceled", inWindow),
		},
		[]jira.SprintMembershipChange{enteredSprint("sw-c", atStart)})
	saveCategoryIssue(t, st, "SW-LEFT", "S", "In Progress",
		[]jira.ChangelogEntry{status("swl", "Ready To Do", "In Progress", atStart)},
		[]jira.SprintMembershipChange{enteredSprint("sw-l", atStart), leftSprint("sw-l2", inWindow)})

	// Added cohort.
	saveCategoryIssue(t, st, "AD-OPEN", "M", "In Progress",
		[]jira.ChangelogEntry{status("ado", "Ready To Do", "In Progress", afterGrace)},
		[]jira.SprintMembershipChange{enteredSprint("ad-o", afterGrace)})
	saveCategoryIssue(t, st, "AD-FIN", "S", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("adf1", "Ready To Do", "In Progress", afterGrace),
			status("adf2", "In Progress", "DONE (This Sprint)", doneAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("ad-f", afterGrace)})
	saveCategoryIssue(t, st, "AD-CANCEL", "M", "Canceled",
		[]jira.ChangelogEntry{
			status("adc1", "Ready To Do", "In Progress", afterGrace),
			status("adc2", "In Progress", "Canceled", inWindow),
		},
		[]jira.SprintMembershipChange{enteredSprint("ad-c", afterGrace)})
	saveCategoryIssue(t, st, "AD-LEFT", "L", "In Progress",
		[]jira.ChangelogEntry{status("adl", "Ready To Do", "In Progress", afterGrace)},
		[]jira.SprintMembershipChange{enteredSprint("ad-l", afterGrace), leftSprint("ad-l2", inWindow)})

	// Per-cohort base cells.
	assertKeys(t, "started/open", cellKeys(t, st, CohortStartedWith, OutcomeOpen, from, to), []string{"SW-OPEN"})
	assertKeys(t, "started/finished", cellKeys(t, st, CohortStartedWith, OutcomeFinished, from, to), []string{"SW-FIN"})
	// Workflow-sorted: SW-LEFT ("In Progress") precedes SW-CANCEL ("Canceled").
	assertKeys(t, "started/removed", cellKeys(t, st, CohortStartedWith, OutcomeRemoved, from, to), []string{"SW-LEFT", "SW-CANCEL"})
	assertKeys(t, "added/open", cellKeys(t, st, CohortAdded, OutcomeOpen, from, to), []string{"AD-OPEN"})
	assertKeys(t, "added/finished", cellKeys(t, st, CohortAdded, OutcomeFinished, from, to), []string{"AD-FIN"})
	// Removal asymmetry: AD-LEFT (reprioritised out, not cancelled) is dropped.
	assertKeys(t, "added/removed", cellKeys(t, st, CohortAdded, OutcomeRemoved, from, to), []string{"AD-CANCEL"})

	// Total column (Open + Finished + Removed) unions per cohort, workflow-sorted.
	assertKeys(t, "started/total", cellKeys(t, st, CohortStartedWith, OutcomeTotal, from, to),
		[]string{"SW-LEFT", "SW-OPEN", "SW-FIN", "SW-CANCEL"})
	assertKeys(t, "added/total", cellKeys(t, st, CohortAdded, OutcomeTotal, from, to),
		[]string{"AD-OPEN", "AD-FIN", "AD-CANCEL"})

	// Total row (Started with + Added) unions per outcome.
	assertKeys(t, "total/open", cellKeys(t, st, CohortTotal, OutcomeOpen, from, to),
		[]string{"AD-OPEN", "SW-OPEN"})

	// Grand total: every counted ticket, sorted by workflow order then key.
	assertKeys(t, "total/total", cellKeys(t, st, CohortTotal, OutcomeTotal, from, to),
		[]string{"AD-OPEN", "SW-LEFT", "SW-OPEN", "AD-FIN", "SW-FIN", "AD-CANCEL", "SW-CANCEL"})

	// The drill list count must equal the cell's tally count for every cell.
	wc, err := st.SprintCategoriesInWindow(wcSprintID, from, to)
	if err != nil {
		t.Fatalf("SprintCategoriesInWindow: %v", err)
	}
	cohortSels := map[SprintCohortSel]SprintCohort{
		CohortStartedWith: wc.StartedWith,
		CohortAdded:       wc.Added,
		CohortTotal:       wc.Total,
	}
	for cs, co := range cohortSels {
		outcomeSels := map[SprintOutcomeSel]SizeTally{
			OutcomeOpen:     co.Open,
			OutcomeFinished: co.Finished,
			OutcomeRemoved:  co.Removed,
			OutcomeTotal:    co.Total,
		}
		for os, tally := range outcomeSels {
			want := tally.S + tally.M + tally.L + tally.NoEstimate
			got := len(cellKeys(t, st, cs, os, from, to))
			if got != want {
				t.Fatalf("count mismatch cohort=%d outcome=%d: drill=%d tally=%d", cs, os, got, want)
			}
		}
	}

	// A drill card carries the current status (for the status pill) and card fields.
	issues, err := st.SprintCellIssues(wcSprintID, from, to, CohortStartedWith, OutcomeFinished)
	if err != nil {
		t.Fatalf("SprintCellIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Status != "DONE (This Sprint)" || issues[0].Type != "Story" {
		t.Fatalf("drill card fields: %+v", issues)
	}
}
