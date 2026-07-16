package store

// Projection-level tests for the "Completed" rollup (Done-crossing semantics).
//
// A completion is the transition crossing FROM a non-Done status INTO the
// explicit Done set ("DONE (This Sprint)", "Ready for Release" or
// "Released / Deployed"). Moves within the Done set do not recount; on reopen
// the latest crossing wins.
// Counts use the CURRENT size (S=1/M=2/L=3, NULL = no estimate) and only
// Task/Bug/Story types. A completion falls in a range if its instant is in
// [from, to). All range boundaries here are computed in Europe/Berlin,
// Monday-start ISO weeks; timestamps are stored UTC underneath.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// berlin is the display/filter timezone; loaded once for the boundary maths.
func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}
	return loc
}

// mondayWeek returns [Monday 00:00, next Monday 00:00) in Berlin for the ISO
// week containing the given Berlin-local date.
func mondayWeek(t *testing.T, loc *time.Location, year int, month time.Month, day int) (from, to time.Time) {
	t.Helper()
	d := time.Date(year, month, day, 12, 0, 0, 0, loc)
	// Go's Weekday: Sunday=0..Saturday=6; ISO weeks start Monday.
	offset := (int(d.Weekday()) + 6) % 7
	monday := time.Date(d.Year(), d.Month(), d.Day()-offset, 0, 0, 0, 0, loc)
	return monday, monday.AddDate(0, 0, 7)
}

// saveCompleted saves an issue plus its status transitions (all field='status').
type xition struct {
	id, from, to string
	at           time.Time
}

func saveWithTransitions(t *testing.T, st *Store, key, typ, size string, current string, xs ...xition) {
	t.Helper()
	cl := make([]jira.ChangelogEntry, len(xs))
	for i, x := range xs {
		cl[i] = jira.ChangelogEntry{ID: x.id, Field: "status", From: x.from, To: x.to, Timestamp: x.at}
	}
	// current status_category: Done if the current status is a Done status.
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	if err := st.SaveIssue(jira.Issue{
		Key: key, Type: typ, Summary: key, Status: current, StatusCategory: cat, Size: size,
		Changelog: cl,
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

func TestCompletedInRangeCountsCrossingsWithSizeAndPoints(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	// Week of Mon 2026-07-13 .. Mon 2026-07-20 (Berlin).
	from, to := mondayWeek(t, loc, 2026, time.July, 15)
	cross := time.Date(2026, time.July, 15, 14, 0, 0, 0, loc)

	// Sized crossings inside the range.
	saveWithTransitions(t, st, "DCAI-1", "Story", "S", "DONE (This Sprint)",
		xition{"a1", "Review / Testing", "DONE (This Sprint)", cross})
	saveWithTransitions(t, st, "DCAI-2", "Task", "M", "DONE (This Sprint)",
		xition{"a2", "In Progress", "DONE (This Sprint)", cross})
	saveWithTransitions(t, st, "DCAI-3", "Bug", "L", "DONE (This Sprint)",
		xition{"a3", "In Progress", "DONE (This Sprint)", cross})
	// Unsized crossing -> no-estimate bucket.
	saveWithTransitions(t, st, "DCAI-4", "Story", "", "DONE (This Sprint)",
		xition{"a4", "In Progress", "DONE (This Sprint)", cross})
	// Excluded types even though they crossed into Done in range.
	saveWithTransitions(t, st, "DCAI-5", "Epic", "L", "DONE (This Sprint)",
		xition{"a5", "In Progress", "DONE (This Sprint)", cross})
	saveWithTransitions(t, st, "DCAI-6", "Sub-task", "S", "DONE (This Sprint)",
		xition{"a6", "In Progress", "DONE (This Sprint)", cross})
	// Crossed the week before -> outside the range.
	saveWithTransitions(t, st, "DCAI-7", "Story", "M", "DONE (This Sprint)",
		xition{"a7", "In Progress", "DONE (This Sprint)", cross.AddDate(0, 0, -7)})

	got, err := st.CompletedInRange(from, to)
	if err != nil {
		t.Fatalf("CompletedInRange: %v", err)
	}
	want := SizeTally{S: 1, M: 1, L: 1, NoEstimate: 1, Points: 1 + 2 + 3}
	assertTally(t, "completed", got, want)
}

func TestCompletedInRangeWithinDoneMoveCountsOnceInOriginalWeek(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	crossWeekFrom, crossWeekTo := mondayWeek(t, loc, 2026, time.July, 15)
	releaseWeekFrom, releaseWeekTo := mondayWeek(t, loc, 2026, time.July, 22)

	cross := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)   // crossing into Done
	release := time.Date(2026, time.July, 23, 9, 0, 0, 0, loc) // within-Done move, later week

	saveWithTransitions(t, st, "DCAI-10", "Story", "L", "Released / Deployed",
		xition{"c1", "Review / Testing", "DONE (This Sprint)", cross},
		xition{"c2", "DONE (This Sprint)", "Released / Deployed", release})

	// Counted once in the ORIGINAL crossing week...
	got, err := st.CompletedInRange(crossWeekFrom, crossWeekTo)
	if err != nil {
		t.Fatalf("CompletedInRange original week: %v", err)
	}
	assertTally(t, "original crossing week", got, SizeTally{L: 1, Points: 3})

	// ...and NOT recounted in the week of the within-Done move.
	got, err = st.CompletedInRange(releaseWeekFrom, releaseWeekTo)
	if err != nil {
		t.Fatalf("CompletedInRange release week: %v", err)
	}
	assertTally(t, "within-Done move week", got, SizeTally{})
}

func TestCompletedInRangeReopenLatestCrossingWins(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	firstFrom, firstTo := mondayWeek(t, loc, 2026, time.July, 15)
	latestFrom, latestTo := mondayWeek(t, loc, 2026, time.July, 22)

	first := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc)  // first crossing
	reopen := time.Date(2026, time.July, 16, 9, 0, 0, 0, loc) // Done -> In Progress
	latest := time.Date(2026, time.July, 23, 9, 0, 0, 0, loc) // crossing again (latest)

	saveWithTransitions(t, st, "DCAI-20", "Story", "M", "DONE (This Sprint)",
		xition{"r1", "In Progress", "DONE (This Sprint)", first},
		xition{"r2", "DONE (This Sprint)", "In Progress", reopen},
		xition{"r3", "In Progress", "DONE (This Sprint)", latest})

	// The first crossing week must NOT count it (latest crossing wins).
	got, err := st.CompletedInRange(firstFrom, firstTo)
	if err != nil {
		t.Fatalf("CompletedInRange first week: %v", err)
	}
	assertTally(t, "first crossing week", got, SizeTally{})

	// The latest crossing week counts it once.
	got, err = st.CompletedInRange(latestFrom, latestTo)
	if err != nil {
		t.Fatalf("CompletedInRange latest week: %v", err)
	}
	assertTally(t, "latest crossing week", got, SizeTally{M: 1, Points: 2})
}

func TestCompletedInRangeBerlinWeekBoundary(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	prevFrom, prevTo := mondayWeek(t, loc, 2026, time.July, 8)  // week ending Mon 07-13 00:00
	thisFrom, thisTo := mondayWeek(t, loc, 2026, time.July, 15) // Mon 07-13 .. Mon 07-20

	// One minute before Monday 00:00 Berlin: belongs to the PREVIOUS week.
	justBefore := time.Date(2026, time.July, 12, 23, 59, 0, 0, loc)
	saveWithTransitions(t, st, "DCAI-30", "Story", "S", "DONE (This Sprint)",
		xition{"b1", "In Progress", "DONE (This Sprint)", justBefore})

	got, err := st.CompletedInRange(prevFrom, prevTo)
	if err != nil {
		t.Fatalf("CompletedInRange prev week: %v", err)
	}
	assertTally(t, "previous week (before Monday)", got, SizeTally{S: 1, Points: 1})

	got, err = st.CompletedInRange(thisFrom, thisTo)
	if err != nil {
		t.Fatalf("CompletedInRange this week: %v", err)
	}
	assertTally(t, "this week (from Monday)", got, SizeTally{})
}

// TestCompletedInRangeCountsCrossingIntoReadyForRelease asserts a ticket whose
// (latest) Done-crossing lands in "Ready for Release" is counted as completed —
// Ready for Release is part of the authoritative Done set, so a crossing
// straight from a non-Done status into it is a completion.
func TestCompletedInRangeCountsCrossingIntoReadyForRelease(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	from, to := mondayWeek(t, loc, 2026, time.July, 15)
	cross := time.Date(2026, time.July, 15, 14, 0, 0, 0, loc)

	saveWithTransitions(t, st, "DCAI-40", "Story", "M", "Ready for Release",
		xition{"rfr1", "Review / Testing", "Ready for Release", cross})

	got, err := st.CompletedInRange(from, to)
	if err != nil {
		t.Fatalf("CompletedInRange: %v", err)
	}
	assertTally(t, "crossed into Ready for Release", got, SizeTally{M: 1, Points: 2})
}

// TestCompletedInRangeDoneToReadyForReleaseIsWithinSet asserts a move from
// DONE (This Sprint) to Ready for Release is a within-Done-set move, not a new
// completion: the ticket is counted once at its original crossing week and not
// recounted in the week of the within-set move.
func TestCompletedInRangeDoneToReadyForReleaseIsWithinSet(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	crossWeekFrom, crossWeekTo := mondayWeek(t, loc, 2026, time.July, 15)
	moveWeekFrom, moveWeekTo := mondayWeek(t, loc, 2026, time.July, 22)

	cross := time.Date(2026, time.July, 15, 9, 0, 0, 0, loc) // crossing into Done
	move := time.Date(2026, time.July, 23, 9, 0, 0, 0, loc)  // within-Done move, later week

	saveWithTransitions(t, st, "DCAI-41", "Story", "L", "Ready for Release",
		xition{"m1", "Review / Testing", "DONE (This Sprint)", cross},
		xition{"m2", "DONE (This Sprint)", "Ready for Release", move})

	got, err := st.CompletedInRange(crossWeekFrom, crossWeekTo)
	if err != nil {
		t.Fatalf("CompletedInRange crossing week: %v", err)
	}
	assertTally(t, "original crossing week", got, SizeTally{L: 1, Points: 3})

	got, err = st.CompletedInRange(moveWeekFrom, moveWeekTo)
	if err != nil {
		t.Fatalf("CompletedInRange within-set move week: %v", err)
	}
	assertTally(t, "within-Done-set move week", got, SizeTally{})
}
