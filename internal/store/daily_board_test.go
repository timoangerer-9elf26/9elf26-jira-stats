package store

// Projection-level tests for the Daily BOARD rollup (issue #112): active-sprint
// Task/Bug/Story created in the window OR moved in it, each placed by its status
// at the window END (reconstructed via statusAtSubquery) and carrying the
// movement facts a card renders.

import (
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// saveDailyCreated saves an active-sprint (unless active=false) issue created at
// `createdAt` with the given current status and no transitions.
func saveDailyCreated(t *testing.T, st *Store, key, typ, assignee, status string, active bool, createdAt time.Time) {
	t.Helper()
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: status,
		StatusCategory: "To Do", Size: "M", Assignee: assignee,
		Creator: assignee, CreatedAt: createdAt,
	}
	if active {
		iss.ActiveSprint = "KW29"
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

func dailyBoardByKey(cards []DailyBoardCard) map[string]DailyBoardCard {
	m := map[string]DailyBoardCard{}
	for _, c := range cards {
		m[c.Key] = c
	}
	return m
}

// TestDailyBoardPopulationPlacementAndOrigin exercises the whole board query:
// population (created OR moved), window-end placement, the origin facts, the
// created-here flag, the #98 intra-done drop, and the recency sort.
func TestDailyBoardPopulationPlacementAndOrigin(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 0, 0, 0, 0, loc)

	// Moved in-window: Ready to Do -> In Progress -> Review / Testing. Advanced;
	// window-end column Review / Testing; started (window-start) at Ready to Do.
	saveDaily(t, st, "DCAI-1", "Story", "alice", true,
		xition{"t1a", "Ready to Do", "In Progress", at(15, 9)},
		xition{"t1b", "In Progress", "Review / Testing", at(15, 14)})
	// Finished in-window: In Progress -> DONE. Done column.
	saveDaily(t, st, "DCAI-2", "Task", "alice", true,
		xition{"t2", "In Progress", "DONE (This Sprint)", at(15, 11)})
	// Canceled in-window: In Progress -> Canceled. Canceled column, pulled back.
	saveDaily(t, st, "DCAI-3", "Bug", "alice", true,
		xition{"t3", "In Progress", "Canceled", at(15, 8)})
	// Only intra-done moves — dropped (#98), must be absent entirely.
	saveDaily(t, st, "DCAI-4", "Task", "alice", true,
		xition{"t4a", "DONE (This Sprint)", "Ready for Release", at(15, 10)},
		xition{"t4b", "Ready for Release", "Released / Deployed", at(15, 12)})
	// Created in-window, never moved → created-here, placed in creation status.
	saveDailyCreated(t, st, "DCAI-5", "Story", "alice", "Refinement", true, at(15, 13))
	// Created BEFORE the window and never moved → not in population.
	saveDailyCreated(t, st, "DCAI-6", "Task", "alice", "Refinement", true, time.Date(2026, time.July, 10, 9, 0, 0, 0, loc))

	cards, err := st.DailyBoard([]string{"alice"}, from, to)
	if err != nil {
		t.Fatalf("daily board: %v", err)
	}
	by := dailyBoardByKey(cards)

	if _, ok := by["DCAI-4"]; ok {
		t.Errorf("DCAI-4 (only intra-done moves) must be absent (#98)")
	}
	if _, ok := by["DCAI-6"]; ok {
		t.Errorf("DCAI-6 (created before the window, unmoved) must be absent")
	}
	if len(cards) != 4 {
		t.Fatalf("want 4 cards (DCAI-1/2/3/5), got %d: %+v", len(cards), cards)
	}

	one := by["DCAI-1"]
	if one.Column != "Review / Testing" {
		t.Errorf("DCAI-1 column = %q, want Review / Testing", one.Column)
	}
	if one.StartStatus != "Ready to Do" || one.Moves != 2 || one.Movement != MovementAdvanced {
		t.Errorf("DCAI-1 origin wrong: start=%q moves=%d movement=%v", one.StartStatus, one.Moves, one.Movement)
	}
	if one.CreatedInWindow {
		t.Errorf("DCAI-1 was not created in the window")
	}
	if !one.LatestActivity.Equal(at(15, 14)) {
		t.Errorf("DCAI-1 latest activity = %v, want %v", one.LatestActivity, at(15, 14))
	}

	if two := by["DCAI-2"]; two.Column != DailyColumnDone || two.Movement != MovementFinished {
		t.Errorf("DCAI-2 want Done/Finished, got %q/%v", two.Column, two.Movement)
	}
	if three := by["DCAI-3"]; three.Column != DailyColumnCanceled || three.Movement != MovementPulledBack {
		t.Errorf("DCAI-3 want Canceled/Pulled back, got %q/%v", three.Column, three.Movement)
	}

	five := by["DCAI-5"]
	if !five.CreatedInWindow || five.Moves != 0 {
		t.Errorf("DCAI-5 should be created-here with no moves, got created=%v moves=%d", five.CreatedInWindow, five.Moves)
	}
	if five.Column != "Refinement" {
		t.Errorf("DCAI-5 (created into Refinement) column = %q, want Refinement", five.Column)
	}
	if !five.LatestActivity.Equal(at(15, 13)) {
		t.Errorf("DCAI-5 latest activity should be its creation instant, got %v", five.LatestActivity)
	}

	// Recency sort: DCAI-1 (14:00) > DCAI-5 (13:00) > DCAI-2 (11:00) > DCAI-3 (08:00).
	wantOrder := []string{"DCAI-1", "DCAI-5", "DCAI-2", "DCAI-3"}
	for i, key := range wantOrder {
		if cards[i].Key != key {
			t.Errorf("sort order[%d] = %q, want %q (full: %+v)", i, cards[i].Key, key, cards)
		}
	}
}

// TestDailyBoardSnapshotPlacement pins the window-end reconstruction: a ticket
// moved AFTER the window shows in its window-end column, not its current one.
func TestDailyBoardSnapshotPlacement(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	// Yesterday's window.
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 0, 0, 0, 0, loc)

	// Moved to In Progress IN the window (15th), then to DONE AFTER it (16th).
	saveDaily(t, st, "DCAI-1", "Story", "alice", true,
		xition{"t1a", "Ready to Do", "In Progress", at(15, 9)},
		xition{"t1b", "In Progress", "DONE (This Sprint)", at(16, 9)})

	cards, err := st.DailyBoard([]string{"alice"}, from, to)
	if err != nil {
		t.Fatalf("daily board: %v", err)
	}
	by := dailyBoardByKey(cards)
	if one, ok := by["DCAI-1"]; !ok {
		t.Fatalf("DCAI-1 should appear (it moved in-window)")
	} else if one.Column != "In Progress" {
		t.Errorf("DCAI-1 window-end column = %q, want In Progress (a snapshot, not its current Done)", one.Column)
	}
}

// TestDailyBoardCarriesAssigneeAvatarURL verifies the avatar URL is sourced by
// the Daily query itself (both arms — moved and created-in-window), not a
// secondary board fetch (#114): a moved and a created ticket each carry their
// assignee's captured avatar URL, and an unassigned ticket carries none.
func TestDailyBoardCarriesAssigneeAvatarURL(t *testing.T) {
	loc := berlin(t)
	st := openTempStore(t)
	at := func(day, hour int) time.Time { return time.Date(2026, time.July, day, hour, 0, 0, 0, loc) }
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, loc)
	to := time.Date(2026, time.July, 16, 0, 0, 0, 0, loc)

	// Moved arm: alice with an avatar.
	saveIssueWithAvatar(t, st, "DCAI-1", "Story", "alice",
		"https://jira.example/avatar/alice.png", at(15, 9))
	// Created-but-unmoved arm: bob with an avatar.
	saveCreatedWithAvatar(t, st, "DCAI-2", "Task", "bob",
		"https://jira.example/avatar/bob.png", at(15, 11))
	// Unassigned moved ticket: no avatar.
	saveIssueWithAvatar(t, st, "DCAI-3", "Bug", "", "", at(15, 10))

	cards, err := st.DailyBoard(nil, from, to)
	if err != nil {
		t.Fatalf("daily board: %v", err)
	}
	by := dailyBoardByKey(cards)
	if got := by["DCAI-1"].AssigneeAvatarURL; got != "https://jira.example/avatar/alice.png" {
		t.Errorf("DCAI-1 (moved) avatar = %q, want alice's URL", got)
	}
	if got := by["DCAI-2"].AssigneeAvatarURL; got != "https://jira.example/avatar/bob.png" {
		t.Errorf("DCAI-2 (created) avatar = %q, want bob's URL", got)
	}
	if got := by["DCAI-3"].AssigneeAvatarURL; got != "" {
		t.Errorf("DCAI-3 (unassigned) avatar = %q, want empty", got)
	}
}

// saveIssueWithAvatar saves an active-sprint issue that moved once in-window,
// carrying the given assignee avatar URL.
func saveIssueWithAvatar(t *testing.T, st *Store, key, typ, assignee, avatarURL string, at time.Time) {
	t.Helper()
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: "In Progress",
		StatusCategory: "In Progress", Size: "M", Assignee: assignee,
		AssigneeAvatarURL: avatarURL, ActiveSprint: "KW29",
		Changelog: []jira.ChangelogEntry{
			{ID: key + "-x", Field: "status", From: "Ready to Do", To: "In Progress", Timestamp: at},
		},
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}

// saveCreatedWithAvatar saves an active-sprint issue created in-window with no
// transitions, carrying the given assignee avatar URL.
func saveCreatedWithAvatar(t *testing.T, st *Store, key, typ, assignee, avatarURL string, createdAt time.Time) {
	t.Helper()
	iss := jira.Issue{
		Key: key, Type: typ, Summary: key + " summary", Status: "Ready to Do",
		StatusCategory: "To Do", Size: "M", Assignee: assignee,
		AssigneeAvatarURL: avatarURL, ActiveSprint: "KW29",
		Creator: assignee, CreatedAt: createdAt,
	}
	if err := st.SaveIssue(iss, "2026-07-16T10:00:00Z"); err != nil {
		t.Fatalf("save %s: %v", key, err)
	}
}
