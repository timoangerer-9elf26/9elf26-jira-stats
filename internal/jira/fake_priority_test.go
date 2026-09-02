package jira

// The fake client carries the priority write too (#212), so the Prio view's
// priority edit is exercisable in tests, in local dev and in the smoke suite
// without live Jira — a live control, not a dead one.

import (
	"context"
	"errors"
	"testing"
)

func TestFakeClientUpdateIssuePrioritySetsTheLevelInMemory(t *testing.T) {
	c := NewFakeClient()
	key := c.Issues[0].Key

	if err := c.UpdateIssuePriority(context.Background(), key, "Lowest"); err != nil {
		t.Fatalf("UpdateIssuePriority: %v", err)
	}
	// The reconciliation read (FetchIssue) must see the write, as live Jira would.
	got, err := c.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if got.Priority != "Lowest" {
		t.Errorf("priority after write = %q, want %q", got.Priority, "Lowest")
	}
}

func TestFakeClientUpdateIssuePriorityRejectsAnUnknownLevel(t *testing.T) {
	c := NewFakeClient()
	key := c.Issues[0].Key
	before := c.Issues[0].Priority

	for _, bad := range []string{"", "Critical", "highest"} {
		if err := c.UpdateIssuePriority(context.Background(), key, bad); err == nil {
			t.Errorf("UpdateIssuePriority(%q) succeeded, want an error", bad)
		}
	}
	if c.Issues[0].Priority != before {
		t.Errorf("a rejected write changed the priority to %q", c.Issues[0].Priority)
	}
}

func TestFakeClientUpdateIssuePriorityHonoursTheInjectedWriteError(t *testing.T) {
	c := NewFakeClient()
	c.WriteErr = errors.New("jira says no")
	key := c.Issues[0].Key
	before := c.Issues[0].Priority

	if err := c.UpdateIssuePriority(context.Background(), key, "Highest"); err == nil {
		t.Fatal("UpdateIssuePriority succeeded despite WriteErr")
	}
	if c.Issues[0].Priority != before {
		t.Errorf("a failed write changed the priority to %q", c.Issues[0].Priority)
	}
}

func TestFakeClientUpdateIssuePriorityUnknownKey(t *testing.T) {
	c := NewFakeClient()
	if err := c.UpdateIssuePriority(context.Background(), "DCAI-999999", "High"); err == nil {
		t.Fatal("UpdateIssuePriority on an unknown key succeeded, want an error")
	}
}
