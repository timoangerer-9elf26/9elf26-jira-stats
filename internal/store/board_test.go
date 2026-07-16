package store

// Projection-level test for the sprint Kanban board rollup. Uses the public
// SaveIssue writer to build a known active-sprint fixture, then asserts
// ActiveSprintBoard seeds the fixed workflow columns (in order, empty ones
// included) whenever an active sprint exists, places active-sprint Task/Bug/Story
// cards into their column by case-insensitive status match, INCLUDING the
// Done-category columns (the board is not filtered to open work), drops the
// board-excluded statuses (Triage, Canceled), surfaces brand-new statuses as
// extra columns after the known ones, and excludes Epics, Sub-tasks and issues
// outside the active sprint.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// seedActiveSprintKW29 stores the active-sprint ENTITY the board's existence gate
// (ActiveSprintWindow) reads. Membership stays on the issues (ActiveSprint field);
// this supplies the sprint the board is gated on.
func seedActiveSprintKW29(t *testing.T, st *Store) {
	t.Helper()
	if err := st.SaveSprint(jira.Sprint{
		ID: 29, Name: "KW29", State: "active",
		ActivatedAt: time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed active sprint: %v", err)
	}
}

// boardColumns is the fixed, ordered set of workflow columns the sprint board
// always renders when an active sprint exists (left→right). Triage and Canceled
// are intentionally excluded from the board.
var boardColumns = []string{
	"Refinement",
	"Ready To Do",
	"In Progress",
	"Review / Testing",
	"DONE (This Sprint)",
	"Ready for Release",
	"Released / Deployed",
}

// assertColumnOrder fails unless the board's columns are exactly want, in order.
func assertColumnOrder(t *testing.T, board Board, want []string) {
	t.Helper()
	got := make([]string, len(board.Columns))
	for i, c := range board.Columns {
		got[i] = c.Status
	}
	if len(got) != len(want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("column %d = %q, want %q (order %v)", i, got[i], w, got)
		}
	}
}

func TestActiveSprintBoardGroupsCardsByStatus(t *testing.T) {
	st := openTempStore(t)
	seedActiveSprintKW29(t, st)

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

	// The fixed workflow columns render in order — including the ones with no
	// cards (Ready To Do, Ready for Release) — since an active sprint exists.
	assertColumnOrder(t, board, boardColumns)

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
	// The seeded columns with no cards render empty.
	assertCards(t, "Ready To Do", cards["Ready To Do"], nil)
	assertCards(t, "Ready for Release", cards["Ready for Release"], nil)

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

// TestActiveSprintBoardOrdersReadyToDoByWorkflowCaseInsensitively asserts a card
// whose synced status differs in casing from the seeded column ("ready to do"
// vs the canonical "Ready To Do") still lands in the seeded column at position
// 2 — status matching is case-insensitive.
func TestActiveSprintBoardOrdersReadyToDoByWorkflowCaseInsensitively(t *testing.T) {
	st := openTempStore(t)
	seedActiveSprintKW29(t, st)

	active := func(key, typ, status, category string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key:            key,
			Type:           typ,
			Summary:        "summary of " + key,
			Status:         status,
			StatusCategory: category,
			ActiveSprint:   "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	active("DCAI-10", "Story", "Refinement", "To Do")
	// A lower-cased "ready to do" must still match the seeded "Ready To Do"
	// column via case-insensitive normalization.
	active("DCAI-11", "Story", "ready to do", "To Do")
	active("DCAI-12", "Task", "In Progress", "In Progress")

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}

	// The fixed columns render in order; the "Ready To Do" card lands in the
	// seeded "Ready To Do" column (position 2) despite the casing difference.
	assertColumnOrder(t, board, boardColumns)
	cards := map[string][]BoardCard{}
	for _, c := range board.Columns {
		cards[c.Status] = c.Cards
	}
	assertCards(t, "Ready To Do", cards["Ready To Do"], []BoardCard{
		{Key: "DCAI-11", Summary: "summary of DCAI-11", Size: "", Type: "Story"},
	})
}

// TestActiveSprintBoardSeedsAllColumnsWithSubsetOfStatuses asserts the fixed set
// of seven workflow columns renders in order — empty ones included — when the
// active sprint has issues in only a subset of statuses. This is the core
// contract of #18.
func TestActiveSprintBoardSeedsAllColumnsWithSubsetOfStatuses(t *testing.T) {
	st := openTempStore(t)
	save := func(key, status string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key: key, Type: "Task", Summary: "summary of " + key, Status: status,
			StatusCategory: "In Progress", ActiveSprint: "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}
	seedActiveSprintKW29(t, st)
	// Cards in only two of the seven statuses.
	save("DCAI-10", "In Progress")
	save("DCAI-11", "Refinement")

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}
	assertColumnOrder(t, board, boardColumns)
	// The five statuses with no cards render as empty columns.
	for _, c := range board.Columns {
		switch c.Status {
		case "In Progress", "Refinement":
			if len(c.Cards) != 1 {
				t.Fatalf("%s: want 1 card, got %d", c.Status, len(c.Cards))
			}
		default:
			if len(c.Cards) != 0 {
				t.Fatalf("%s: want empty column, got %d cards", c.Status, len(c.Cards))
			}
		}
	}
}

// TestActiveSprintBoardExcludesTriageAndCanceled asserts issues in the
// board-excluded statuses never render — no column, no card — even when they
// are active-sprint Task/Bug/Story issues.
func TestActiveSprintBoardExcludesTriageAndCanceled(t *testing.T) {
	st := openTempStore(t)
	save := func(key, status, category string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key: key, Type: "Task", Summary: "summary of " + key, Status: status,
			StatusCategory: category, ActiveSprint: "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}
	seedActiveSprintKW29(t, st)
	save("DCAI-10", "In Progress", "In Progress")
	save("DCAI-90", "Triage", "To Do")
	save("DCAI-91", "Canceled", "Done")

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}
	// Only the seven seeded columns; neither Triage nor Canceled appears.
	assertColumnOrder(t, board, boardColumns)
	for _, c := range board.Columns {
		for _, card := range c.Cards {
			if card.Key == "DCAI-90" || card.Key == "DCAI-91" {
				t.Fatalf("excluded issue %s leaked onto the board (column %q)", card.Key, c.Status)
			}
		}
	}
}

// TestActiveSprintBoardAppendsUnknownStatusColumn asserts a brand-new Jira
// status (neither one of the seven nor a board-excluded one) surfaces as an
// extra column AFTER the seven known ones rather than dropping the card, so the
// board stays useful for data-quality validation.
func TestActiveSprintBoardAppendsUnknownStatusColumn(t *testing.T) {
	st := openTempStore(t)
	save := func(key, status string) {
		t.Helper()
		if err := st.SaveIssue(jira.Issue{
			Key: key, Type: "Task", Summary: "summary of " + key, Status: status,
			StatusCategory: "In Progress", ActiveSprint: "KW29",
		}, "2026-07-15T10:00:00Z"); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}
	seedActiveSprintKW29(t, st)
	save("DCAI-10", "In Progress")
	save("DCAI-99", "Brand New Status")

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("ActiveSprintBoard: %v", err)
	}
	assertColumnOrder(t, board, append(append([]string{}, boardColumns...), "Brand New Status"))
	last := board.Columns[len(board.Columns)-1]
	assertCards(t, "Brand New Status", last.Cards, []BoardCard{
		{Key: "DCAI-99", Summary: "summary of DCAI-99", Size: "", Type: "Task"},
	})
}

// TestWorkflowOrderPlacesReadyForReleaseAfterDone asserts the single source of
// truth for column order (workflowOrder) lists Ready for Release immediately
// after DONE (This Sprint) and before Released / Deployed — the DCAI workflow
// order that both /board and /now render by. Guards against regressing the
// ordering fix.
func TestWorkflowOrderPlacesReadyForReleaseAfterDone(t *testing.T) {
	rankOf := func(status string) (int, bool) {
		for i, s := range workflowOrder {
			if s == status {
				return i, true
			}
		}
		return 0, false
	}
	done, ok := rankOf("DONE (This Sprint)")
	if !ok {
		t.Fatalf("workflowOrder missing DONE (This Sprint): %v", workflowOrder)
	}
	rfr, ok := rankOf("Ready for Release")
	if !ok {
		t.Fatalf("workflowOrder missing Ready for Release: %v", workflowOrder)
	}
	released, ok := rankOf("Released / Deployed")
	if !ok {
		t.Fatalf("workflowOrder missing Released / Deployed: %v", workflowOrder)
	}
	if rfr != done+1 {
		t.Errorf("Ready for Release should sit immediately after DONE (This Sprint): DONE=%d, Ready for Release=%d (order %v)", done, rfr, workflowOrder)
	}
	if released != rfr+1 {
		t.Errorf("Released / Deployed should sit immediately after Ready for Release: Ready for Release=%d, Released / Deployed=%d (order %v)", rfr, released, workflowOrder)
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
