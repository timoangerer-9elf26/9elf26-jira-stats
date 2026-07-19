package store

// Projection-level tests for the Sprint view's cohort × outcome breakdown
// (SprintCategoriesInWindow): rows Started with / Added / Total × columns
// Open / Finished / Removed / Total, reconstructed from the status-transition AND
// sprint-membership history over a fixed window.
//
// Covers each outcome bucket for both cohorts, including the removal asymmetry:
//   - Finished = crossed Done within [sprint start, now)
//   - Removed = not finished AND (cancelled OR no longer a member); for Added,
//     ONLY cancellation counts — an added-then-reprioritised-out ticket is dropped
//     entirely (not counted in any cell)
//   - Open = the remainder (still a member, not cancelled, not finished)
//   - Total = Open + Finished + Removed; the Total row = Started-with + Added

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// wcSprintID is the sprint the Sprint-category fixtures belong to.
const wcSprintID = 29

// enteredSprint / leftSprint build one sprint-membership changelog change.
func enteredSprint(entryID string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: wcSprintID, SprintName: "KW29", Entered: true, Timestamp: at}
}

func leftSprint(entryID string, at time.Time) jira.SprintMembershipChange {
	return jira.SprintMembershipChange{EntryID: entryID, SprintID: wcSprintID, SprintName: "KW29", Entered: false, Timestamp: at}
}

// saveCategoryIssue saves a Task/Bug/Story with its status changelog and sprint-
// membership changes, so SprintCategoriesInWindow can reconstruct status and
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

// TestSprintCategoriesCohortOutcome exercises every outcome bucket (Open,
// Finished, Removed) for BOTH cohorts (Started with, Added) in one window,
// including the removal asymmetry: a Started-with ticket reprioritised out counts
// under Removed, while an Added ticket reprioritised out is dropped entirely.
func TestSprintCategoriesCohortOutcome(t *testing.T) {
	st := openTempStore(t)

	// Window [from, to). Sprint starts at `from`; the grace window ends at +1h.
	from := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	atStart := from                       // within the grace window → Started-with cohort
	afterGrace := from.Add(2 * time.Hour) // after the grace window → Added cohort
	inWindow := from.Add(48 * time.Hour)  // a Done crossing / a leave, inside the window
	doneAt := from.Add(24 * time.Hour)    // a Done crossing inside the window

	if err := st.SaveSprint(jira.Sprint{ID: wcSprintID, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// --- Started-with cohort (member at the grace-window end) ---

	// Still a member, not finished, not cancelled → Open.
	saveCategoryIssue(t, st, "SW-OPEN", "S", "In Progress",
		[]jira.ChangelogEntry{status("swo", "Ready To Do", "In Progress", atStart)},
		[]jira.SprintMembershipChange{enteredSprint("sw-o", atStart)})

	// Crossed Done in-window → Finished.
	saveCategoryIssue(t, st, "SW-FIN", "M", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("swf1", "Ready To Do", "In Progress", atStart),
			status("swf2", "In Progress", "DONE (This Sprint)", doneAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("sw-f", atStart)})

	// Cancelled (still a member) → Removed.
	saveCategoryIssue(t, st, "SW-CANCEL", "L", "Canceled",
		[]jira.ChangelogEntry{
			status("swc1", "Ready To Do", "In Progress", atStart),
			status("swc2", "In Progress", "Canceled", inWindow),
		},
		[]jira.SprintMembershipChange{enteredSprint("sw-c", atStart)})

	// Reprioritised out (left the sprint), not cancelled, not finished → Removed
	// (the Started-with cohort KEEPS its reprioritised-out tickets).
	saveCategoryIssue(t, st, "SW-LEFT", "S", "In Progress",
		[]jira.ChangelogEntry{status("swl", "Ready To Do", "In Progress", atStart)},
		[]jira.SprintMembershipChange{enteredSprint("sw-l", atStart), leftSprint("sw-l2", inWindow)})

	// --- Added cohort (first entry after the grace window) ---

	// Still a member, not finished, not cancelled → Open.
	saveCategoryIssue(t, st, "AD-OPEN", "M", "In Progress",
		[]jira.ChangelogEntry{status("ado", "Ready To Do", "In Progress", afterGrace)},
		[]jira.SprintMembershipChange{enteredSprint("ad-o", afterGrace)})

	// Crossed Done in-window → Finished.
	saveCategoryIssue(t, st, "AD-FIN", "S", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("adf1", "Ready To Do", "In Progress", afterGrace),
			status("adf2", "In Progress", "DONE (This Sprint)", doneAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("ad-f", afterGrace)})

	// Cancelled → Removed (cancellation DOES reach Removed for Added).
	saveCategoryIssue(t, st, "AD-CANCEL", "M", "Canceled",
		[]jira.ChangelogEntry{
			status("adc1", "Ready To Do", "In Progress", afterGrace),
			status("adc2", "In Progress", "Canceled", inWindow),
		},
		[]jira.SprintMembershipChange{enteredSprint("ad-c", afterGrace)})

	// Reprioritised out (left the sprint), not cancelled, not finished → DROPPED
	// entirely (an added-then-removed ticket is not counted in any cell).
	saveCategoryIssue(t, st, "AD-LEFT", "L", "In Progress",
		[]jira.ChangelogEntry{status("adl", "Ready To Do", "In Progress", afterGrace)},
		[]jira.SprintMembershipChange{enteredSprint("ad-l", afterGrace), leftSprint("ad-l2", inWindow)})

	wc, err := st.SprintCategoriesInWindow(wcSprintID, from, to)
	if err != nil {
		t.Fatalf("SprintCategoriesInWindow: %v", err)
	}

	// Started-with cohort.
	assertTally(t, "started/open", wc.StartedWith.Open, SizeTally{S: 1, Points: 1})
	assertTally(t, "started/finished", wc.StartedWith.Finished, SizeTally{M: 1, Points: 2})
	assertTally(t, "started/removed", wc.StartedWith.Removed, SizeTally{S: 1, L: 1, Points: 1 + 3})
	assertTally(t, "started/total", wc.StartedWith.Total, SizeTally{S: 2, M: 1, L: 1, Points: 1 + 2 + 1 + 3})

	// Added cohort — AD-LEFT dropped entirely.
	assertTally(t, "added/open", wc.Added.Open, SizeTally{M: 1, Points: 2})
	assertTally(t, "added/finished", wc.Added.Finished, SizeTally{S: 1, Points: 1})
	assertTally(t, "added/removed", wc.Added.Removed, SizeTally{M: 1, Points: 2})
	assertTally(t, "added/total", wc.Added.Total, SizeTally{S: 1, M: 2, Points: 1 + 2 + 2})

	// Total row = column-wise Started-with + Added.
	assertTally(t, "total/open", wc.Total.Open, SizeTally{S: 1, M: 1, Points: 3})
	assertTally(t, "total/finished", wc.Total.Finished, SizeTally{S: 1, M: 1, Points: 3})
	assertTally(t, "total/removed", wc.Total.Removed, SizeTally{S: 1, M: 1, L: 1, Points: 1 + 2 + 3})
	assertTally(t, "total/total", wc.Total.Total, SizeTally{S: 3, M: 3, L: 1, Points: 12})
}

// TestSprintCategoriesPreFinishedCarryOver pins the #87 exclusion: a
// pre-finished carry-over — a member CURRENTLY in the finished bucket whose Done
// crossing happened in a PRIOR sprint (before the window) — is excluded from the
// whole cohort × outcome table and from every cell drill-down. The test is by
// CURRENT state, so a carry-over reopened this sprint re-enters the counts (Open,
// or Finished if re-finished within the window). Covers three cases:
//   (a) lingering carry-over still done at entry → in NO cell / drill-down
//   (b) reopened carry-over → Open
//   (c) reopened-and-re-finished carry-over → Finished
func TestSprintCategoriesPreFinishedCarryOver(t *testing.T) {
	st := openTempStore(t)

	from := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	atStart := from                          // within the grace window → Started-with
	priorSprint := from.Add(-72 * time.Hour) // a Done crossing BEFORE the window
	reopenAt := from.Add(48 * time.Hour)     // reopened inside the window
	refinAt := from.Add(60 * time.Hour)      // re-finished inside the window

	if err := st.SaveSprint(jira.Sprint{ID: wcSprintID, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// (a) Lingering carry-over: crossed Done in a prior sprint, still done at
	// entry, no movement since. Currently done, did NOT cross in-window → excluded
	// from every cell.
	saveCategoryIssue(t, st, "CO-LINGER", "M", "Ready for Release",
		[]jira.ChangelogEntry{status("li1", "In Progress", "DONE (This Sprint)", priorSprint)},
		[]jira.SprintMembershipChange{enteredSprint("co-li", atStart)})

	// (b) Reopened carry-over: crossed Done in a prior sprint, then reopened this
	// sprint. Currently NOT done → back in the counts as Open.
	saveCategoryIssue(t, st, "CO-REOPEN", "S", "In Progress",
		[]jira.ChangelogEntry{
			status("re1", "In Progress", "DONE (This Sprint)", priorSprint),
			status("re2", "DONE (This Sprint)", "In Progress", reopenAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("co-re", atStart)})

	// (c) Reopened-and-re-finished carry-over: prior-sprint completion, reopened,
	// then re-crossed Done inside the window → a genuine in-window completion →
	// Finished.
	saveCategoryIssue(t, st, "CO-REFIN", "L", "DONE (This Sprint)",
		[]jira.ChangelogEntry{
			status("rf1", "In Progress", "DONE (This Sprint)", priorSprint),
			status("rf2", "DONE (This Sprint)", "In Progress", reopenAt),
			status("rf3", "In Progress", "DONE (This Sprint)", refinAt),
		},
		[]jira.SprintMembershipChange{enteredSprint("co-rf", atStart)})

	wc, err := st.SprintCategoriesInWindow(wcSprintID, from, to)
	if err != nil {
		t.Fatalf("SprintCategoriesInWindow: %v", err)
	}

	// CO-LINGER excluded everywhere; CO-REOPEN → Open; CO-REFIN → Finished.
	assertTally(t, "started/open", wc.StartedWith.Open, SizeTally{S: 1, Points: 1})
	assertTally(t, "started/finished", wc.StartedWith.Finished, SizeTally{L: 1, Points: 3})
	assertTally(t, "started/removed", wc.StartedWith.Removed, SizeTally{})
	assertTally(t, "started/total", wc.StartedWith.Total, SizeTally{S: 1, L: 1, Points: 4})
	assertTally(t, "total/total", wc.Total.Total, SizeTally{S: 1, L: 1, Points: 4})

	// Drill-downs stay in lock-step: CO-LINGER appears in no cell, including Total.
	assertKeys(t, "co/open", cellKeys(t, st, CohortStartedWith, OutcomeOpen, from, to), []string{"CO-REOPEN"})
	assertKeys(t, "co/finished", cellKeys(t, st, CohortStartedWith, OutcomeFinished, from, to), []string{"CO-REFIN"})
	assertKeys(t, "co/removed", cellKeys(t, st, CohortStartedWith, OutcomeRemoved, from, to), nil)
	// Total: CO-REOPEN (In Progress) precedes CO-REFIN (DONE) by workflow order.
	assertKeys(t, "co/total", cellKeys(t, st, CohortTotal, OutcomeTotal, from, to), []string{"CO-REOPEN", "CO-REFIN"})
}

// TestSprintCategoriesGraceWindow pins the one-hour grace window (#65): the
// Started-with / Added split anchors on `sprint start + 1h`, not the start
// instant. A ticket that is a member at the end of the grace window is Started
// with (including one that joined within the first hour, or was created directly
// into the sprint); a ticket whose FIRST membership entry falls after the grace
// window is Added. The old "open at the start" status gate is gone, so a member
// with no status history still counts as Started with.
func TestSprintCategoriesGraceWindow(t *testing.T) {
	st := openTempStore(t)

	// Window [from, to). The sprint starts at `from`; the grace window ends one
	// hour later.
	from := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	graceEnd := from.Add(time.Hour)         // 10:00 — the anchor
	inGrace := from.Add(30 * time.Minute)   // 09:30 — within the first hour
	afterGrace := graceEnd.Add(time.Second) // 10:00:01 — just past the grace window
	beforeStart := from.Add(-2 * time.Hour) // present before the start

	if err := st.SaveSprint(jira.Sprint{ID: wcSprintID, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Joined within the first hour → Started with.
	saveCategoryIssue(t, st, "DCAI-INGRACE", "S", "In Progress",
		[]jira.ChangelogEntry{status("g-ig", "Ready To Do", "In Progress", inGrace)},
		[]jira.SprintMembershipChange{enteredSprint("m-ig", inGrace)})

	// Member exactly at the grace-window end → Started with (the boundary is
	// inclusive: member AT sprint start + 1h counts).
	saveCategoryIssue(t, st, "DCAI-BOUNDARY", "M", "In Progress",
		[]jira.ChangelogEntry{status("g-b", "Ready To Do", "In Progress", graceEnd)},
		[]jira.SprintMembershipChange{enteredSprint("m-b", graceEnd)})

	// Member at the grace end but with NO status history → still Started with (the
	// open-at-start gate is removed).
	saveCategoryIssue(t, st, "DCAI-NOHIST", "S", "In Progress",
		nil,
		[]jira.SprintMembershipChange{enteredSprint("m-nh", beforeStart)})

	// First membership entry after the grace window → Added.
	saveCategoryIssue(t, st, "DCAI-AFTER", "L", "Ready To Do",
		nil,
		[]jira.SprintMembershipChange{enteredSprint("m-af", afterGrace)})

	wc, err := st.SprintCategoriesInWindow(wcSprintID, from, to)
	if err != nil {
		t.Fatalf("SprintCategoriesInWindow: %v", err)
	}

	// All four are still open members → they land in each cohort's Open (and so
	// its Total) column. Started with = INGRACE(S) + BOUNDARY(M) + NOHIST(S).
	assertTally(t, "started/open", wc.StartedWith.Open, SizeTally{S: 2, M: 1, Points: 1 + 2 + 1})
	assertTally(t, "started/total", wc.StartedWith.Total, SizeTally{S: 2, M: 1, Points: 1 + 2 + 1})
	// Added = AFTER(L) only.
	assertTally(t, "added/open", wc.Added.Open, SizeTally{L: 1, Points: 3})
	assertTally(t, "added/total", wc.Added.Total, SizeTally{L: 1, Points: 3})
	assertTally(t, "total/total", wc.Total.Total, SizeTally{S: 2, M: 1, L: 1, Points: 7})
}
