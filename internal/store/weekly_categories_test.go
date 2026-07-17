package store

// Projection-level tests for the Weekly view's three-category breakdown
// (WeeklyCategoriesInWindow): Started with / Added / Finished, reconstructed from
// the status-transition AND sprint-membership history over a fixed window.
//
// Covers the ticket's required cases:
//   - an OPEN-AT-START ticket (open + in the sprint at the window start) lands in
//     Started with
//   - a MID-WINDOW ADDED ticket (entered the sprint during the window) lands in
//     Added
//   - an ADDED-AND-FINISHED ticket counts under Added, and in Finished
//     (finished-from-added, not finished-from-started)
//   - a ticket FINISHED AFTER THE WINDOW is excluded from Finished
//   - the Total row = Started-with + Added

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// wcSprintID is the sprint the Weekly-category fixtures belong to.
const wcSprintID = 29

// enteredSprint / leftSprint build one sprint-membership changelog change.
func enteredSprint(entryID string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: wcSprintID, SprintName: "KW29", Entered: true, Timestamp: at}
}

// saveCategoryIssue saves a Task/Bug/Story with its status changelog and sprint-
// membership changes, so WeeklyCategoriesInWindow can reconstruct status and
// membership at any instant. current is the CURRENT status; size the CURRENT
// size (drives the tally).
func saveCategoryIssue(t *testing.T, st *Store, key, size, current string, changelog []jira.ChangelogEntry, sprintChanges []jira.SprintMembershipChange) {
	t.Helper()
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	if err := st.SaveIssue(jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: current, StatusCategory: cat,
		Size: size, ActiveSprint: "KW29",
		Changelog:     changelog,
		SprintChanges: sprintChanges,
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

func status(id, from, to string, at time.Time) jira.ChangelogEntry {
	return jira.ChangelogEntry{ID: id, Field: "status", From: from, To: to, Timestamp: at}
}

// TestWeeklyCategoriesInWindowSplitsStartedAddedFinished exercises all four
// required cases in one window over the active sprint.
func TestWeeklyCategoriesInWindowSplitsStartedAddedFinished(t *testing.T) {
	st := openTempStore(t)

	// A fixed window [from, to). Instants around it.
	from := time.Date(2026, time.July, 13, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	beforeStart := time.Date(2026, time.July, 12, 8, 0, 0, 0, time.UTC)
	mid := time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)
	midLater := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	afterWindow := time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC)

	if err := st.SaveSprint(jira.Sprint{ID: wcSprintID, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Open at start: member + open (In Progress) at the window start → Started with.
	saveCategoryIssue(t, st, "DCAI-100", "S", "In Progress",
		[]jira.ChangelogEntry{status("s100", "Ready To Do", "In Progress", beforeStart)},
		[]jira.SprintMembershipChange{enteredSprint("m100", beforeStart)})

	// Added mid-window: entered the sprint during the window → Added (regardless
	// of status).
	saveCategoryIssue(t, st, "DCAI-200", "M", "Ready To Do",
		nil,
		[]jira.SprintMembershipChange{enteredSprint("m200", mid)})

	// Added AND finished: entered mid-window and crossed into Done in-window →
	// counts under Added and in finished-from-added (never finished-from-started).
	saveCategoryIssue(t, st, "DCAI-300", "L", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("s300a", "Ready To Do", "In Progress", mid),
			status("s300b", "In Progress", "DONE (This Sprint)", midLater),
		},
		[]jira.SprintMembershipChange{enteredSprint("m300", mid)})

	// Started with, finished AFTER the window: in Started with, but excluded from
	// Finished (its Done crossing is at/after `to`).
	saveCategoryIssue(t, st, "DCAI-400", "S", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("s400a", "Ready To Do", "In Progress", beforeStart),
			status("s400b", "In Progress", "DONE (This Sprint)", afterWindow),
		},
		[]jira.SprintMembershipChange{enteredSprint("m400", beforeStart)})

	// Started with, finished IN the window → finished-from-started.
	saveCategoryIssue(t, st, "DCAI-500", "M", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("s500a", "Ready To Do", "In Progress", beforeStart),
			status("s500b", "In Progress", "DONE (This Sprint)", midLater),
		},
		[]jira.SprintMembershipChange{enteredSprint("m500", beforeStart)})

	wc, err := st.WeeklyCategoriesInWindow(wcSprintID, from, to)
	if err != nil {
		t.Fatalf("WeeklyCategoriesInWindow: %v", err)
	}

	assertTally(t, "started-with", wc.StartedWith, SizeTally{S: 2, M: 1, Points: 1 + 1 + 2})
	assertTally(t, "added", wc.Added, SizeTally{M: 1, L: 1, Points: 2 + 3})
	assertTally(t, "finished-from-started", wc.FinishedFromStarted, SizeTally{M: 1, Points: 2})
	assertTally(t, "finished-from-added", wc.FinishedFromAdded, SizeTally{L: 1, Points: 3})
	assertTally(t, "finished-total", wc.FinishedTotal, SizeTally{M: 1, L: 1, Points: 2 + 3})
	assertTally(t, "total", wc.Total, SizeTally{S: 2, M: 2, L: 1, Points: 9})
}
