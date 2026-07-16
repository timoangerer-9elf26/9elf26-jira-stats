package jira

// Unit test for parsing the Jira changelog "Sprint" field into per-sprint
// membership-transition history. A single Sprint change carries comma-separated
// sprint IDs (from/to) and names (fromString/toString); it can add and/or remove
// several sprints at once, so it expands to one SprintMembershipChange per sprint
// id whose membership actually changed (entered or left).

import (
	"testing"
	"time"
)

func sprintChangeByID(changes []SprintMembershipChange, id int) (SprintMembershipChange, bool) {
	for _, c := range changes {
		if c.SprintID == id {
			return c, true
		}
	}
	return SprintMembershipChange{}, false
}

func TestToSprintChangesAddRemoveMove(t *testing.T) {
	histories := []historyDTO{
		// Added to a sprint from nothing.
		{ID: "5001", Created: "2026-07-13T09:00:00.000+0200", Items: []itemDTO{
			{Field: "Sprint", FieldID: sprintFieldID, From: "", To: "KW29", FromID: "", ToID: "29"},
		}},
		// Moved from one sprint to another (leaves 28, enters 30).
		{ID: "5002", Created: "2026-07-14T09:00:00.000+0200", Items: []itemDTO{
			{Field: "Sprint", FieldID: sprintFieldID, From: "KW28", To: "KW30", FromID: "28", ToID: "30"},
		}},
		// Removed from a sprint entirely.
		{ID: "5003", Created: "2026-07-15T09:00:00.000+0200", Items: []itemDTO{
			{Field: "Sprint", FieldID: sprintFieldID, From: "KW29", To: "", FromID: "29", ToID: ""},
		}},
		// A non-sprint item must be ignored here.
		{ID: "5004", Created: "2026-07-15T10:00:00.000+0200", Items: []itemDTO{
			{Field: "status", From: "In Progress", To: "Review / Testing"},
		}},
	}

	changes, err := toSprintChanges(histories)
	if err != nil {
		t.Fatalf("toSprintChanges: %v", err)
	}
	if len(changes) != 4 {
		t.Fatalf("got %d changes, want 4: %+v", len(changes), changes)
	}

	// Added 29.
	if c, ok := sprintChangeByID(changes, 29); !ok {
		t.Fatalf("missing change for sprint 29")
	} else {
		if !c.Entered {
			t.Fatalf("sprint 29 first change should be Entered")
		}
		if c.SprintName != "KW29" {
			t.Fatalf("sprint 29 name = %q, want KW29", c.SprintName)
		}
		if c.EntryID != "5001" {
			t.Fatalf("sprint 29 entry id = %q, want 5001", c.EntryID)
		}
		if want := time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC); !c.Timestamp.UTC().Equal(want) {
			t.Fatalf("sprint 29 ts = %v, want %v", c.Timestamp.UTC(), want)
		}
	}

	// Move: left 28, entered 30.
	left28, ok := sprintChangeByID(changes, 28)
	if !ok || left28.Entered {
		t.Fatalf("sprint 28 should be a Left change, got %+v ok=%v", left28, ok)
	}
	if left28.SprintName != "KW28" {
		t.Fatalf("sprint 28 name = %q, want KW28", left28.SprintName)
	}
	entered30, ok := sprintChangeByID(changes, 30)
	if !ok || !entered30.Entered {
		t.Fatalf("sprint 30 should be an Entered change, got %+v ok=%v", entered30, ok)
	}
}

func TestToSprintChangesIgnoresUnchangedSprints(t *testing.T) {
	// from {28} to {28,29}: 28 is unchanged, only 29 is entered.
	histories := []historyDTO{
		{ID: "6001", Created: "2026-07-13T09:00:00.000+0200", Items: []itemDTO{
			{Field: "Sprint", FieldID: sprintFieldID, From: "KW28", To: "KW28, KW29", FromID: "28", ToID: "28, 29"},
		}},
	}
	changes, err := toSprintChanges(histories)
	if err != nil {
		t.Fatalf("toSprintChanges: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1 (only sprint 29 entered): %+v", len(changes), changes)
	}
	if changes[0].SprintID != 29 || !changes[0].Entered {
		t.Fatalf("want Entered sprint 29, got %+v", changes[0])
	}
}
