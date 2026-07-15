package store

// Projection-level test for the sprint Kanban board rollup. Uses the public
// SaveIssue writer to build a known active-sprint fixture, then asserts
// ActiveSprintBoard groups active-sprint Task/Bug/Story issues into one column
// per workflow status (workflow order, unknown last), INCLUDING the
// Done-category columns (the board is not filtered to open work), and excludes
// Epics, Sub-tasks and issues outside the active sprint.

import (
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestActiveSprintBoardGroupsCardsByStatus(t *testing.T) {
	st := openTempStore(t)

	// active adds an issue in the active sprint (the board's scope).
	active := func(key, typ, status, category, size string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key:            key,
			Type:           typ,
			Summary:        "summary of " + key,
			Status:         status,
			StatusCategory: category,
			Size:           size,
			ActiveSprint:   "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	active("DCAI-10", "Story", "Refinement", "To Do", "S")
	active("DCAI-11", "Task", "In Progress", "In Progress", "")
	active("DCAI-12", "Bug", "Review / Testing", "In Progress", "M")
	// Done-category work IS shown on the board (unlike the Now view).
	active("DCAI-13", "Story", "DONE (This Sprint)", "Done", "L")
	active("DCAI-14", "Task", "Released / Deployed", "Done", "M")
	// Excluded types even in the active sprint.
	active("DCAI-15", "Epic", "In Progress", "In Progress", "L")
	active("DCAI-16", "Sub-task", "In Progress", "In Progress", "S")
	// Excluded by sprint scope: Task/Bug/Story NOT in the active sprint.
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-30", Type: "Story", Summary: "out of sprint", Status: "In Progress",
		StatusCategory: "In Progress", Size: "L", // no ActiveSprint
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save DCAI-30: %v", err)
	}

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}

	// Columns are the statuses present, in workflow order, Done columns included.
	wantOrder := []string{"Refinement", "In Progress", "Review / Testing", "DONE (This Sprint)", "Released / Deployed"}
	gotOrder := make([]string, len(board.Columns))
	for i, c := range board.Columns {
		gotOrder[i] = c.Status
	}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("columns = %v, want %v", gotOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if gotOrder[i] != want {
			t.Fatalf("column %d = %q, want %q (order %v)", i, gotOrder[i], want, gotOrder)
		}
	}

	// Cards land in the right column and carry key/summary/size/type.
	cards := map[string][]BoardCard{}
	for _, c := range board.Columns {
		cards[c.Status] = c.Cards
	}
	assertCards(t, "Refinement", cards["Refinement"], []BoardCard{
		{Key: "DCAI-10", Summary: "summary of DCAI-10", Size: "S", Type: "Story"},
	})
	assertCards(t, "In Progress", cards["In Progress"], []BoardCard{
		{Key: "DCAI-11", Summary: "summary of DCAI-11", Size: "", Type: "Task"},
	})
	assertCards(t, "Review / Testing", cards["Review / Testing"], []BoardCard{
		{Key: "DCAI-12", Summary: "summary of DCAI-12", Size: "M", Type: "Bug"},
	})
	assertCards(t, "DONE (This Sprint)", cards["DONE (This Sprint)"], []BoardCard{
		{Key: "DCAI-13", Summary: "summary of DCAI-13", Size: "L", Type: "Story"},
	})
	assertCards(t, "Released / Deployed", cards["Released / Deployed"], []BoardCard{
		{Key: "DCAI-14", Summary: "summary of DCAI-14", Size: "M", Type: "Task"},
	})

	// The out-of-sprint issue never appears on any card.
	for _, c := range board.Columns {
		for _, card := range c.Cards {
			if card.Key == "DCAI-30" {
				t.Fatalf("out-of-sprint issue DCAI-30 leaked onto the board")
			}
		}
	}
}

// TestActiveSprintBoardEmptyWhenNoActiveSprint asserts a store with only
// out-of-sprint work yields no columns (the web layer renders the empty state).
func TestActiveSprintBoardEmptyWhenNoActiveSprint(t *testing.T) {
	st := openTempStore(t)
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-30", Type: "Story", Summary: "x", Status: "In Progress",
		StatusCategory: "In Progress", Size: "L",
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save: %v", err)
	}
	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}
	if len(board.Columns) != 0 {
		t.Fatalf("expected no columns without an active sprint, got %d", len(board.Columns))
	}
}

func assertCards(t *testing.T, status string, got, want []BoardCard) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d cards, want %d (%+v)", status, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s card %d = %+v, want %+v", status, i, got[i], want[i])
		}
	}
}
