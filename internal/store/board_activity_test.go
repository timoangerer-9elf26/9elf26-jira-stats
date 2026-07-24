package store

// Tests for the Board's per-card latest-activity computation (#159): the shared
// latestActivity primitive and its wiring into ActiveSprintBoard. "Active" reuses
// the Daily rule — created-in-window OR a status change in it, with intra-Done
// housekeeping moves ignored — surfaced here as a single latest-activity instant
// the active-in-24h filter compares against a rolling window.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// TestLatestActivityRule pins the shared primitive both views use: the latest
// non-intra-Done status change, or the creation instant when none survives.
func TestLatestActivityRule(t *testing.T) {
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, time.UTC) }
	created := at(10, 8)

	tests := []struct {
		name    string
		changes []DailyStatusChange
		created time.Time
		want    time.Time
	}{
		{
			name:    "no changes falls back to creation instant",
			changes: nil,
			created: created,
			want:    created,
		},
		{
			name: "latest surviving change wins over creation",
			changes: []DailyStatusChange{
				{From: "Ready To Do", To: "In Progress", TransitionedAt: at(15, 9)},
				{From: "In Progress", To: "Review / Testing", TransitionedAt: at(16, 8)},
			},
			created: created,
			want:    at(16, 8),
		},
		{
			name: "intra-Done housekeeping is ignored; earlier crossing wins",
			changes: []DailyStatusChange{
				{From: "Review / Testing", To: "DONE (This Sprint)", TransitionedAt: at(15, 10)},
				{From: "DONE (This Sprint)", To: "Released / Deployed", TransitionedAt: at(16, 11)},
			},
			created: created,
			want:    at(15, 10), // the finish crossing, not the housekeeping hop
		},
		{
			name: "only intra-Done moves falls back to creation instant",
			changes: []DailyStatusChange{
				{From: "DONE (This Sprint)", To: "Ready for Release", TransitionedAt: at(16, 11)},
			},
			created: created,
			want:    created,
		},
		{
			name:    "no changes and no creation instant is the zero instant",
			changes: nil,
			created: time.Time{},
			want:    time.Time{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := latestActivity(tc.changes, tc.created); !got.Equal(tc.want) {
				t.Errorf("latestActivity = %v, want %v", got, tc.want)
			}
		})
	}
}

// saveBoardActivity saves an active-sprint issue with a creation instant and its
// status transitions, for the ActiveSprintBoard latest-activity wiring test.
func saveBoardActivity(t *testing.T, st *Store, key, current string, created time.Time, xs ...xition) {
	t.Helper()
	cl := make([]jira.ChangelogEntry, len(xs))
	for i, x := range xs {
		cl[i] = jira.ChangelogEntry{ID: x.id, Field: "status", From: x.from, To: x.to, Timestamp: x.at}
	}
	cat := "In Progress"
	switch current {
	case "DONE (This Sprint)", "Ready for Release", "Released / Deployed":
		cat = "Done"
	}
	iss := jira.Issue{
		Key: key, Type: "Story", Summary: key, Status: current, StatusCategory: cat,
		ActiveSprint: "KW29", CreatedAt: created, Changelog: cl,
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

// TestActiveSprintBoardLatestActivity verifies every board card carries the
// latest-activity instant computed by the shared Daily rule.
func TestActiveSprintBoardLatestActivity(t *testing.T) {
	st := openTempStore(t)
	seedActiveSprintKW29(t, st)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, time.UTC) }

	// Moved card: latest activity is its last non-intra-Done change.
	saveBoardActivity(t, st, "DCAI-1", "Review / Testing", at(13, 8),
		xition{"m1", "Ready To Do", "In Progress", at(14, 9)},
		xition{"m2", "In Progress", "Review / Testing", at(15, 12)})
	// Created-but-unmoved card: latest activity is its creation instant.
	saveBoardActivity(t, st, "DCAI-2", "Refinement", at(16, 7))
	// Finished then housekeeping: latest activity is the finish crossing, not the
	// intra-Done hop (the #98/#159 intra-Done rule).
	saveBoardActivity(t, st, "DCAI-3", "Released / Deployed", at(12, 8),
		xition{"h1", "In Progress", "DONE (This Sprint)", at(14, 10)},
		xition{"h2", "DONE (This Sprint)", "Released / Deployed", at(16, 18)})

	board, err := st.ActiveSprintBoard()
	if err != nil {
		t.Fatalf("board: %v", err)
	}

	got := map[string]time.Time{}
	for _, col := range board.Columns {
		for _, c := range col.Cards {
			got[c.Key] = c.LatestActivity
		}
	}
	want := map[string]time.Time{
		"DCAI-1": at(15, 12),
		"DCAI-2": at(16, 7),
		"DCAI-3": at(14, 10),
	}
	for key, w := range want {
		if g, ok := got[key]; !ok {
			t.Errorf("%s missing from board", key)
		} else if !g.Equal(w) {
			t.Errorf("%s LatestActivity = %v, want %v", key, g, w)
		}
	}
}
