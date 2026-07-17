package store

// Projection-level tests for the "tickets I created" rollup (#44): tickets
// authored by a given Jira Creator within a [from, to) window, most-recent first,
// deliberately NOT scoped to the active sprint.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// saveCreated saves an issue with a Creator and CreatedAt (and no sprint
// membership unless activeSprint is set), so the created-in-range query can be
// exercised independently of the sprint spine.
func saveCreated(t *testing.T, st *Store, key, typ, creator string, createdAt time.Time, activeSprint bool) {
	t.Helper()
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: "Ready To Do",
		StatusCategory: "To Do", Size: "M", Creator: creator, CreatedAt: createdAt,
	}
	if activeSprint {
		iss.ActiveSprint = "KW29"
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

func TestIssuesCreatedInRangeWindowAndCreator(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)

	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 12, 0, 0, 0, loc)

	// In window, created by Ada — one in the active sprint, one NOT (must still
	// count: the section is not sprint-scoped).
	saveCreated(t, st, "DCAI-1", "Story", "Ada", at(16, 8), true)
	saveCreated(t, st, "DCAI-2", "Task", "Ada", at(15, 9), false)
	// In window, created by someone else.
	saveCreated(t, st, "DCAI-3", "Bug", "Grace", at(15, 14), true)
	// Out of window (before from).
	saveCreated(t, st, "DCAI-4", "Task", "Ada", at(10, 9), true)
	// Out of window (at/after to).
	saveCreated(t, st, "DCAI-5", "Task", "Ada", at(16, 12), true)

	// Filter to Ada: her two in-window tickets, most-recent first, regardless of
	// sprint membership.
	got, err := st.IssuesCreatedInRange("Ada", from, to)
	if err != nil {
		t.Fatalf("created Ada: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Ada: want 2 tickets, got %d: %+v", len(got), got)
	}
	if got[0].Key != "DCAI-1" || got[1].Key != "DCAI-2" {
		t.Fatalf("Ada: wrong order/keys (want DCAI-1 then DCAI-2): %+v", got)
	}
	if got[0].Creator != "Ada" || got[0].Type != "Story" {
		t.Errorf("DCAI-1 fields wrong: %+v", got[0])
	}

	// Any creator ("") includes Grace's ticket too.
	all, err := st.IssuesCreatedInRange("", from, to)
	if err != nil {
		t.Fatalf("created all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all: want 3 in-window tickets, got %d: %+v", len(all), all)
	}
}

func TestIssuesCreatedInRangeIgnoresNullCreatedAt(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 12, 0, 0, 0, loc)

	// A ticket with no created_at (zero time) must not appear.
	if err := st.SaveIssue(jira.Issue{
		Key: "DCAI-9", Type: "Task", Summary: "no created", Status: "Triage",
		StatusCategory: "To Do", Creator: "Ada",
	}, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save DCAI-9: %v", err)
	}

	got, err := st.IssuesCreatedInRange("Ada", from, to)
	if err != nil {
		t.Fatalf("created: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no tickets (created_at is NULL), got %+v", got)
	}
}
