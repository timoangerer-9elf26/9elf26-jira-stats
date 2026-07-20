package store

// Projection-level tests for the per-sprint Velocity series (VelocitySeries):
// one bar per sprint whose Finished points are read from the SAME code path as
// the Sprint view's Total-row Finished (SprintCategoriesInWindow), so the two
// views can never drift. Also covers trailing-N oldest-first ordering and the
// completed-vs-active window ([start, completion] vs [start, now)).

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// saveVelocityIssue saves a Task/Bug/Story into a specific sprint (membership +
// status crossing), so VelocitySeries and SprintCategoriesInWindow reconstruct
// it identically. enteredAt is when it joined the sprint; crossedAt (non-zero)
// is when it crossed into `current` (a Done status). activeSprintName scopes the
// per-issue snapshot.
func saveVelocityIssue(t *testing.T, st *Store, key, size, current, activeSprintName string, sprintID int, enteredAt, crossedAt time.Time) {
	t.Helper()
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	var changelog []jira.ChangelogEntry
	if !crossedAt.IsZero() {
		changelog = []jira.ChangelogEntry{{ID: key + "-x", Field: "status", From: "In Progress", To: current, Timestamp: crossedAt}}
	}
	if err := st.SaveIssue(jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: current, StatusCategory: cat,
		Size: size, ActiveSprint: activeSprintName,
		Changelog: changelog,
		SprintChanges: []jira.SprintMembershipChange{
			{EntryID: key + "-m", SprintID: sprintID, SprintName: activeSprintName, Entered: true, Timestamp: enteredAt},
		},
	}, "2026-07-15T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

// TestVelocitySeriesFinishedMatchesSprintView is the alignment guarantee: an
// active sprint's Velocity bar points EQUAL SprintCategoriesInWindow(...).
// Total.Finished points, cohort-scoped, with a pre-finished carry-over excluded
// and points at the ticket's current size.
func TestVelocitySeriesFinishedMatchesSprintView(t *testing.T) {
	st := openTempStore(t)

	from := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	afterGrace := from.Add(2 * time.Hour)
	doneAt := from.Add(24 * time.Hour)
	priorSprint := from.Add(-72 * time.Hour) // crossed Done in a PRIOR sprint

	if err := st.SaveSprint(jira.Sprint{ID: 29, Name: "KW29", State: "active", ActivatedAt: from}); err != nil {
		t.Fatalf("save sprint: %v", err)
	}

	// Started-with, finished in-window (M = 2).
	saveVelocityIssue(t, st, "SW-FIN", "M", "DONE (This Sprint)", "KW29", 29, from, doneAt)
	// Added, finished in-window (S = 1).
	saveVelocityIssue(t, st, "AD-FIN", "S", "DONE (This Sprint)", "KW29", 29, afterGrace, doneAt)
	// Pre-finished carry-over: currently Done but crossed in a prior sprint → excluded.
	saveVelocityIssue(t, st, "CARRY", "L", "Released / Deployed", "KW29", 29, from, priorSprint)

	cats, err := st.SprintCategoriesInWindow(29, from, now)
	if err != nil {
		t.Fatalf("SprintCategoriesInWindow: %v", err)
	}

	bars, err := st.VelocitySeries(now, 10)
	if err != nil {
		t.Fatalf("VelocitySeries: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected 1 bar, got %d: %+v", len(bars), bars)
	}
	bar := bars[0]
	if bar.Points != cats.Total.Finished.Points {
		t.Errorf("bar points %d != Sprint-view Total Finished %d (must share the same code path)", bar.Points, cats.Total.Finished.Points)
	}
	if bar.Points != 3 {
		t.Errorf("bar points = %d, want 3 (M+S, carry-over excluded)", bar.Points)
	}
	if bar.Name != "KW29" {
		t.Errorf("bar name = %q, want KW29", bar.Name)
	}
	if !bar.Ongoing {
		t.Errorf("active sprint bar must be Ongoing")
	}
	if !bar.End.Equal(now) {
		t.Errorf("active sprint bar End = %v, want now %v", bar.End, now)
	}
	if !bar.Start.Equal(from) {
		t.Errorf("bar Start = %v, want %v", bar.Start, from)
	}
}

// TestVelocitySeriesTrailingOldestFirst asserts only the trailing N sprints are
// returned, oldest-first, and future (never-started) sprints are skipped.
func TestVelocitySeriesTrailingOldestFirst(t *testing.T) {
	st := openTempStore(t)
	base := time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)

	// 12 started sprints, one per week, ids 1..12 in chronological order.
	for i := 1; i <= 12; i++ {
		start := base.AddDate(0, 0, 7*(i-1))
		sp := jira.Sprint{ID: i, Name: "S" + itoa(i), State: "closed", ActivatedAt: start, CompletedAt: start.Add(6 * 24 * time.Hour)}
		if i == 12 {
			sp.State = "active"
			sp.CompletedAt = time.Time{}
		}
		if err := st.SaveSprint(sp); err != nil {
			t.Fatalf("save sprint %d: %v", i, err)
		}
	}
	// A future sprint (never started) must not appear even though it carries a
	// createdDate-derived activation instant.
	if err := st.SaveSprint(jira.Sprint{ID: 99, Name: "FUTURE", State: "future", ActivatedAt: base.AddDate(0, 0, 200)}); err != nil {
		t.Fatalf("save future: %v", err)
	}

	now := base.AddDate(0, 0, 7*11).Add(3 * 24 * time.Hour)
	bars, err := st.VelocitySeries(now, 10)
	if err != nil {
		t.Fatalf("VelocitySeries: %v", err)
	}
	if len(bars) != 10 {
		t.Fatalf("expected trailing 10 bars, got %d", len(bars))
	}
	// Oldest-first: sprints 3..12 (the 12 started minus the 2 oldest).
	wantNames := []string{"S3", "S4", "S5", "S6", "S7", "S8", "S9", "S10", "S11", "S12"}
	for i, w := range wantNames {
		if bars[i].Name != w {
			t.Errorf("bar[%d] = %q, want %q (trailing 10, oldest-first)", i, bars[i].Name, w)
		}
	}
	for _, b := range bars {
		if b.Name == "FUTURE" {
			t.Errorf("future sprint must be skipped")
		}
	}
}

// TestVelocitySeriesCompletedVsActiveWindow asserts a completed sprint measures
// over [start, completion] (a crossing after completion is excluded) while the
// active sprint measures over [start, now).
func TestVelocitySeriesCompletedVsActiveWindow(t *testing.T) {
	st := openTempStore(t)

	closedStart := time.Date(2026, time.July, 6, 9, 0, 0, 0, time.UTC)
	closedEnd := time.Date(2026, time.July, 10, 17, 0, 0, 0, time.UTC)
	activeStart := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	if err := st.SaveSprint(jira.Sprint{ID: 28, Name: "KW28", State: "closed", ActivatedAt: closedStart, CompletedAt: closedEnd}); err != nil {
		t.Fatalf("save KW28: %v", err)
	}
	if err := st.SaveSprint(jira.Sprint{ID: 29, Name: "KW29", State: "active", ActivatedAt: activeStart}); err != nil {
		t.Fatalf("save KW29: %v", err)
	}

	// KW28: finished before completion (counts), plus a crossing AFTER completion
	// (must be excluded because the window ends at completion).
	saveVelocityIssue(t, st, "K28-IN", "M", "DONE (This Sprint)", "KW28", 28, closedStart, closedStart.Add(24*time.Hour))
	saveVelocityIssue(t, st, "K28-LATE", "L", "DONE (This Sprint)", "KW28", 28, closedStart, closedEnd.Add(24*time.Hour))
	// KW29 (active): finished in-window.
	saveVelocityIssue(t, st, "K29-IN", "S", "DONE (This Sprint)", "KW29", 29, activeStart, activeStart.Add(24*time.Hour))

	bars, err := st.VelocitySeries(now, 10)
	if err != nil {
		t.Fatalf("VelocitySeries: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected 2 bars, got %d: %+v", len(bars), bars)
	}
	kw28, kw29 := bars[0], bars[1]
	if kw28.Name != "KW28" || kw29.Name != "KW29" {
		t.Fatalf("order wrong: %q, %q", kw28.Name, kw29.Name)
	}
	if kw28.Ongoing {
		t.Errorf("completed sprint must not be Ongoing")
	}
	if !kw28.End.Equal(closedEnd) {
		t.Errorf("completed sprint End = %v, want completion %v", kw28.End, closedEnd)
	}
	if kw28.Points != 2 {
		t.Errorf("KW28 points = %d, want 2 (late crossing excluded by completion bound)", kw28.Points)
	}
	if !kw29.Ongoing || !kw29.End.Equal(now) {
		t.Errorf("active sprint must be Ongoing with End=now; got Ongoing=%v End=%v", kw29.Ongoing, kw29.End)
	}
	if kw29.Points != 1 {
		t.Errorf("KW29 points = %d, want 1", kw29.Points)
	}
}

// itoa is a tiny int→string for fixture sprint names (avoids importing strconv
// just for the test).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
