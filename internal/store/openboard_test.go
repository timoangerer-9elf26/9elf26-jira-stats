package store

// Projection-level test for the "Now" board rollup. Uses the public SaveIssue
// writer to build a known open-work fixture, then asserts OpenByStatus tallies
// each open workflow status (S/M/L/no-estimate + points), orders columns by the
// workflow, aggregates a grand total, and excludes Epics, Sub-tasks and every
// status outside the explicit OPEN bucket.
//
// "Open" is a POSITIVE membership test of exactly the four open statuses
// (CONTEXT.md "Open ticket"), not "anything Jira doesn't categorize as Done".
// So Triage (Jira category "To Do"), Canceled, the three Done statuses
// (including Ready for Release), and any unknown status are all excluded — the
// board never leans on status_category.

import (
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestOpenByStatusTalliesOpenWorkByStatus(t *testing.T) {
	st := openTempStore(t)

	// save adds an issue in the active sprint (the "Now" board scope).
	save := func(key, typ, status, category, size string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key:            key,
			Type:           typ,
			Summary:        key,
			Status:         status,
			StatusCategory: category,
			Size:           size,
			ActiveSprint:   "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	// Open work spread across all four open workflow statuses, with a mix of
	// sizes and some unsized (no-estimate) issues — all in the active sprint.
	save("DCAI-10", "Story", "Refinement", "To Do", "S")
	save("DCAI-11", "Task", "Refinement", "To Do", "")
	save("DCAI-12", "Bug", "Ready to Do", "To Do", "M")
	save("DCAI-13", "Story", "In Progress", "In Progress", "L")
	save("DCAI-14", "Task", "In Progress", "In Progress", "S")
	save("DCAI-15", "Bug", "Review / Testing", "In Progress", "M")
	save("DCAI-16", "Story", "Review / Testing", "In Progress", "")
	// Excluded from the open board because they are not in the OPEN bucket, even
	// though they are active-sprint Task/Bug/Story issues:
	//   - Triage: pre-sprint. Jira's category is "To Do", so a category-based
	//     "not Done" test would WRONGLY count it as open. The explicit bucket must
	//     not.
	save("DCAI-24", "Story", "Triage", "To Do", "S")
	//   - Canceled: abandoned; excluded from both open and finished.
	save("DCAI-25", "Task", "Canceled", "Done", "M")
	//   - the three Done statuses, including Ready for Release (a done state that
	//     sits after DONE (This Sprint) in the flow).
	save("DCAI-19", "Story", "DONE (This Sprint)", "Done", "M")
	save("DCAI-23", "Bug", "Ready for Release", "Done", "L")
	save("DCAI-20", "Story", "Released / Deployed", "Done", "L")
	//   - an unknown status: not one of the four open buckets, so it never lands
	//     on the open board (positive membership, not "not Done").
	save("DCAI-21", "Task", "Blocked", "In Progress", "S")
	// Excluded by wrong type: Epic + Sub-task.
	save("DCAI-17", "Epic", "In Progress", "In Progress", "L")
	save("DCAI-18", "Sub-task", "In Progress", "In Progress", "S")
	// Open Task/Bug/Story but NOT in the active sprint: excluded by the sprint
	// scope even though they would otherwise be open work.
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-30", Type: "Story", Summary: "DCAI-30", Status: "In Progress",
		StatusCategory: "In Progress", Size: "L", // no ActiveSprint
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save DCAI-30: %v", err)
	}

	board, err := st.OpenByStatus()
	if err != nil {
		t.Fatalf("OpenByStatus: %v", err)
	}

	// Only the four open statuses appear, in workflow order.
	wantOrder := []string{"Refinement", "Ready to Do", "In Progress", "Review / Testing"}
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

	// Grand total across the open statuses only (Triage/Canceled/Done/unknown all
	// excluded).
	assertTally(t, "grand total", board.Total, SizeTally{S: 2, M: 2, L: 1, NoEstimate: 2, Points: 9})

	// The non-open statuses never surface as columns.
	for _, absent := range []string{"Triage", "Canceled", "Blocked", "DONE (This Sprint)", "Ready for Release", "Released / Deployed"} {
		if _, present := byStatus[absent]; present {
			t.Errorf("open board leaked non-open status %q (columns %v)", absent, columnStatuses(board))
		}
	}
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
