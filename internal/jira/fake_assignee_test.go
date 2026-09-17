package jira

// The fake client's in-memory assignee write (#223). Local dev and the smoke
// suite run against this fake, so the assign control must be a real control
// there rather than a disabled one — and its failure branch must be reachable,
// which is what WriteErr is for.

import (
	"context"
	"errors"
	"testing"
)

// assigneeFake is a fake whose first issue starts out assigned, so both
// directions of the write (reassign, clear) have somewhere to move from.
func assigneeFake(t *testing.T) (*FakeClient, string) {
	t.Helper()
	c := NewFakeClient()
	if len(c.Issues) == 0 {
		t.Fatal("canned dataset has no issues")
	}
	return c, c.Issues[0].Key
}

func TestFakeUpdateIssueAssigneeAssignsAProjectMember(t *testing.T) {
	c, key := assigneeFake(t)
	members, err := c.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}
	target := members[0]

	if err := c.UpdateIssueAssignee(context.Background(), key, target.AccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee: %v", err)
	}

	// The write is only real if the re-read the syncer does next sees it.
	iss, err := c.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if iss.Assignee != target.DisplayName {
		t.Errorf("assignee = %q, want %q", iss.Assignee, target.DisplayName)
	}
	if iss.AssigneeAvatarURL != target.AvatarURL {
		t.Errorf("avatar = %q, want %q", iss.AssigneeAvatarURL, target.AvatarURL)
	}
}

// Clearing is its own case: an unassigned ticket is a legitimate state
// (CONTEXT.md -> Assignee edit), not a side effect of assigning.
func TestFakeUpdateIssueAssigneeClearsTheAssignee(t *testing.T) {
	c, key := assigneeFake(t)
	members, _ := c.FetchProjectMembers(context.Background())
	if err := c.UpdateIssueAssignee(context.Background(), key, members[0].AccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee: %v", err)
	}

	if err := c.UpdateIssueAssignee(context.Background(), key, UnassignedAccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee(clear): %v", err)
	}

	iss, err := c.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if iss.Assignee != "" || iss.AssigneeAvatarURL != "" {
		t.Errorf("issue still carries %q / %q after a clear", iss.Assignee, iss.AssigneeAvatarURL)
	}
}

func TestFakeUpdateIssueAssigneeFailsOnDemand(t *testing.T) {
	c, key := assigneeFake(t)
	before, _ := c.FetchIssue(context.Background(), key)
	boom := errors.New("403")
	c.WriteErr = boom

	err := c.UpdateIssueAssignee(context.Background(), key, "acct-ada")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the configured WriteErr", err)
	}

	after, _ := c.FetchIssue(context.Background(), key)
	if after.Assignee != before.Assignee {
		t.Errorf("a failed write changed the assignee to %q", after.Assignee)
	}
}

// An account id that is not a member of the project is what live Jira answers a
// 400 to; the fake refuses it too rather than inventing a person.
func TestFakeUpdateIssueAssigneeRejectsAnUnknownAccount(t *testing.T) {
	c, key := assigneeFake(t)
	if err := c.UpdateIssueAssignee(context.Background(), key, "acct-nobody"); err == nil {
		t.Fatal("UpdateIssueAssignee accepted an account id that belongs to nobody")
	}
}

// Keeping app accounts off the popover is FetchProjectMembers' job, not the
// write's (docs/adr/0012). Live Jira would assign one, so the fake does too —
// a fake that refused would be stricter than production and could fail a caller
// that production accepts.
func TestFakeUpdateIssueAssigneeDoesNotPoliceAppAccounts(t *testing.T) {
	c, key := assigneeFake(t)
	var app AssignableUser
	for _, u := range c.AssignableUsers {
		if u.AccountType == AccountTypeApp {
			app = u
			break
		}
	}
	if app.AccountID == "" {
		t.Fatal("canned dataset has no app account")
	}
	if err := c.UpdateIssueAssignee(context.Background(), key, app.AccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee(%q): %v", app.AccountID, err)
	}
	iss, _ := c.FetchIssue(context.Background(), key)
	if iss.Assignee != app.DisplayName {
		t.Errorf("assignee = %q, want %q", iss.Assignee, app.DisplayName)
	}
}

func TestFakeUpdateIssueAssigneeRejectsAnUnknownIssue(t *testing.T) {
	c, _ := assigneeFake(t)
	if err := c.UpdateIssueAssignee(context.Background(), "DCAI-NOPE", "acct-ada"); err == nil {
		t.Fatal("UpdateIssueAssignee accepted an issue that does not exist")
	}
}

// The dense review dataset drives the same control in review mode, so its
// members must be assignable there too.
func TestDenseFakeUpdateIssueAssigneeAssignsAMember(t *testing.T) {
	c := NewDenseFakeClient()
	members, err := c.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}
	key := c.Issues[0].Key
	if err := c.UpdateIssueAssignee(context.Background(), key, members[0].AccountID); err != nil {
		t.Fatalf("UpdateIssueAssignee: %v", err)
	}
	iss, _ := c.FetchIssue(context.Background(), key)
	if iss.Assignee != members[0].DisplayName {
		t.Errorf("assignee = %q, want %q", iss.Assignee, members[0].DisplayName)
	}
}
