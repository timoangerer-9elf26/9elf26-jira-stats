package store

// Projection-level test for the "Now" board rollup. Uses the public SaveIssue
// writer to build a known open-work fixture, then asserts OpenByStatus tallies
// each open workflow status (S/M/L/no-estimate + points), orders columns by the
// workflow, sorts unknown statuses last, aggregates a grand total, and excludes
// Epics, Sub-tasks and Done-category issues.

import (
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestOpenByStatusTalliesOpenWorkByStatus(t *testing.T) {
	st := openTempStore(t)

	save := func(key, typ, status, category, size string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key:            key,
			Type:           typ,
			Summary:        key,
			Status:         status,
			StatusCategory: category,
			Size:           size,
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	// Open work spread across several workflow statuses, with a mix of sizes and
	// some unsized (no-estimate) issues.
	save("DCAI-10", "Story", "Refinement", "To Do", "S")
	save("DCAI-11", "Task", "Refinement", "To Do", "")
	save("DCAI-12", "Bug", "Ready to Do", "To Do", "M")
	save("DCAI-13", "Story", "In Progress", "In Progress", "L")
	save("DCAI-14", "Task", "In Progress", "In Progress", "S")
	save("DCAI-15", "Bug", "Review / Testing", "In Progress", "M")
	save("DCAI-16", "Story", "Review / Testing", "In Progress", "")
	// An open status not in the known workflow: must sort after the known ones.
	save("DCAI-21", "Task", "Blocked", "In Progress", "S")
	// Excluded from rollups: Epic + Sub-task (wrong types), and Done-category
	// issues (not open).
	save("DCAI-17", "Epic", "In Progress", "In Progress", "L")
	save("DCAI-18", "Sub-task", "In Progress", "In Progress", "S")
	save("DCAI-19", "Story", "DONE (This Sprint)", "Done", "M")
	save("DCAI-20", "Story", "Released / Deployed", "Done", "L")

	board, err := st.OpenByStatus()
	if err != nil {
		t.Fatalf("OpenByStatus: %v", err)
	}

	wantOrder := []string{"Refinement", "Ready to Do", "In Progress", "Review / Testing", "Blocked"}
	if len(board.Columns) != len(wantOrder) {
		t.Fatalf("columns = %d %v, want %d %v", len(board.Columns), columnStatuses(board), len(wantOrder), wantOrder)
	}
	for i, want := range wantOrder {
		if got := board.Columns[i].Status; got != want {
			t.Fatalf("column %d status = %q, want %q (order %v)", i, got, want, columnStatuses(board))
		}
	}

	byStatus := map[string]SizeTally{}
	for _, c := range board.Columns {
		byStatus[c.Status] = c.SizeTally
	}
	assertTally(t, "Refinement", byStatus["Refinement"], SizeTally{S: 1, NoEstimate: 1, Points: 1})
	assertTally(t, "Ready to Do", byStatus["Ready to Do"], SizeTally{M: 1, Points: 2})
	assertTally(t, "In Progress", byStatus["In Progress"], SizeTally{S: 1, L: 1, Points: 4})
	assertTally(t, "Review / Testing", byStatus["Review / Testing"], SizeTally{M: 1, NoEstimate: 1, Points: 2})
	assertTally(t, "Blocked", byStatus["Blocked"], SizeTally{S: 1, Points: 1})

	assertTally(t, "grand total", board.Total, SizeTally{S: 3, M: 2, L: 1, NoEstimate: 2, Points: 10})
}

func columnStatuses(b OpenBoard) []string {
	out := make([]string, len(b.Columns))
	for i, c := range b.Columns {
		out[i] = c.Status
	}
	return out
}

func assertTally(t *testing.T, what string, got, want SizeTally) {
	t.Helper()
	if got != want {
		t.Fatalf("%s tally = %+v, want %+v", what, got, want)
	}
}
