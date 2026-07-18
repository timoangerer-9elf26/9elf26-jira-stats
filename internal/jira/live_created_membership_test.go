package jira

// Unit test for capturing the fields the store needs to synthesize membership
// for a ticket created directly into a sprint (#55): the active sprint's id and
// the issue's created instant. Such a ticket has its Sprint field set at creation
// and never changed, so its changelog carries no "Sprint" item — the id must come
// from the current Sprint field, and the entry instant from `created`.

import (
	"context"
	"testing"
	"time"
)

func TestToIssueCapturesActiveSprintIDAndCreated(t *testing.T) {
	c := &LiveClient{cfg: Config{}}
	dto := issueDTO{
		Key: "DCAI-1733",
		Fields: fieldsDTO{
			Summary:   "born in the sprint",
			IssueType: issueTypeDTO{Name: "Task"},
			Status:    statusDTO{Name: "In Progress"},
			Created:   "2026-07-21T09:00:00.000+0200",
			// Sprint field set at creation: id + name + active state, no changelog.
			Sprint: []sprintDTO{{ID: 1057, Name: "KW30", State: "active"}},
		},
	}

	iss, err := c.toIssue(context.Background(), dto)
	if err != nil {
		t.Fatalf("toIssue: %v", err)
	}

	if iss.ActiveSprintID != 1057 {
		t.Errorf("ActiveSprintID = %d, want 1057", iss.ActiveSprintID)
	}
	if iss.ActiveSprint != "KW30" {
		t.Errorf("ActiveSprint = %q, want KW30", iss.ActiveSprint)
	}
	want := time.Date(2026, time.July, 21, 7, 0, 0, 0, time.UTC) // 09:00 +0200
	if !iss.CreatedAt.UTC().Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", iss.CreatedAt.UTC(), want)
	}
	// No Sprint changelog item → no changelog-derived membership; the store
	// synthesizes the entry from ActiveSprintID + CreatedAt.
	if len(iss.SprintChanges) != 0 {
		t.Errorf("expected no changelog-derived sprint changes, got %+v", iss.SprintChanges)
	}
}
