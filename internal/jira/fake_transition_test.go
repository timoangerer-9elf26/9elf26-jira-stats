package jira

// The fake client carries the transition seam too, so the whole write path is
// exercisable in tests and in local dev without live Jira (#194).

import (
	"context"
	"errors"
	"testing"
)

func TestFakeClientOffersTheDCAITransitions(t *testing.T) {
	c := NewFakeClient()
	key := c.Issues[0].Key

	trs, err := c.FetchTransitions(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchTransitions: %v", err)
	}
	if len(trs) != len(DCAITransitions()) {
		t.Fatalf("got %d transitions, want %d", len(trs), len(DCAITransitions()))
	}
}

func TestFakeClientTransitionMovesTheIssue(t *testing.T) {
	c := NewFakeClient()
	key := c.Issues[0].Key

	trs, err := c.FetchTransitions(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchTransitions: %v", err)
	}
	tr, err := TransitionTo(trs, StatusIDInProgress)
	if err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if err := c.TransitionIssue(context.Background(), key, tr.ID); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}

	iss, err := c.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if iss.Status != "In Progress" {
		t.Errorf("status = %q, want %q", iss.Status, "In Progress")
	}
	if iss.StatusCategory != "In Progress" {
		t.Errorf("status category = %q, want %q", iss.StatusCategory, "In Progress")
	}
	if len(c.TransitionCalls) != 1 || c.TransitionCalls[0].Key != key || c.TransitionCalls[0].TransitionID != tr.ID {
		t.Errorf("TransitionCalls = %+v, want one call for %s/%s", c.TransitionCalls, key, tr.ID)
	}
}

func TestFakeClientRejectsAnUnknownTransition(t *testing.T) {
	c := NewFakeClient()
	if err := c.TransitionIssue(context.Background(), c.Issues[0].Key, "nope"); err == nil {
		t.Fatal("TransitionIssue succeeded on an unknown transition id")
	}
}

func TestFakeClientHonoursTheInjectedWriteError(t *testing.T) {
	c := NewFakeClient()
	boom := errors.New("boom")
	c.WriteErr = boom
	before := c.Issues[0].Status

	if err := c.TransitionIssue(context.Background(), c.Issues[0].Key, "21"); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if c.Issues[0].Status != before {
		t.Errorf("status = %q after a failed write, want it unchanged (%q)", c.Issues[0].Status, before)
	}
}
