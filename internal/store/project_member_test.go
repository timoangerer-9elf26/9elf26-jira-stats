package store

// Round-trip test for the project-member table (#222): the people who can be
// assigned work on the project, persisted so the assign popover renders from
// SQLite and no read path asks Jira (docs/adr/0012).

import (
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestProjectMembersRoundTripAndReplaceWholesale(t *testing.T) {
	st := openTempStore(t)

	if members, err := st.ProjectMembers(); err != nil {
		t.Fatalf("ProjectMembers on a fresh store: %v", err)
	} else if len(members) != 0 {
		t.Fatalf("fresh store has members %+v, want none", members)
	}

	if err := st.ReplaceProjectMembers([]jira.ProjectMember{
		{AccountID: "acct-grace", DisplayName: "Grace", AvatarURL: "/static/avatars/grace.svg"},
		{AccountID: "acct-ada", DisplayName: "Ada", AvatarURL: "/static/avatars/ada.svg"},
		{AccountID: "acct-lise", DisplayName: "Lise"},
	}); err != nil {
		t.Fatalf("ReplaceProjectMembers: %v", err)
	}

	members, err := st.ProjectMembers()
	if err != nil {
		t.Fatalf("ProjectMembers: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %+v, want 3", members)
	}
	// Ordered by display name, so the popover's list is stable between renders.
	assertEq(t, "first member", members[0].DisplayName, "Ada")
	assertEq(t, "second member", members[1].DisplayName, "Grace")
	assertEq(t, "third member", members[2].DisplayName, "Lise")
	assertEq(t, "Ada account id", members[0].AccountID, "acct-ada")
	assertEq(t, "Ada avatar", members[0].AvatarURL, "/static/avatars/ada.svg")
	// A member without an avatar reads back as the empty string, not an error.
	assertEq(t, "Lise avatar", members[2].AvatarURL, "")

	// Replacing is wholesale: someone off the project is gone, a newcomer is in,
	// and a renamed member carries the new name — all without duplicate rows.
	if err := st.ReplaceProjectMembers([]jira.ProjectMember{
		{AccountID: "acct-ada", DisplayName: "Ada Lovelace", AvatarURL: "/static/avatars/ada.svg"},
		{AccountID: "acct-alan", DisplayName: "Alan"},
	}); err != nil {
		t.Fatalf("second ReplaceProjectMembers: %v", err)
	}
	members, err = st.ProjectMembers()
	if err != nil {
		t.Fatalf("ProjectMembers after replace: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want exactly the 2 replacements", members)
	}
	assertEq(t, "renamed member", members[0].DisplayName, "Ada Lovelace")
	assertEq(t, "newcomer", members[1].DisplayName, "Alan")
	if got := countRows(t, st, "SELECT COUNT(*) FROM project_member"); got != 2 {
		t.Errorf("project_member rows = %d, want 2", got)
	}
}

// An empty answer from Jira empties the table rather than leaving stale people
// behind — the table mirrors Jira, it does not accumulate.
func TestReplaceProjectMembersWithNoneEmptiesTheTable(t *testing.T) {
	st := openTempStore(t)
	if err := st.ReplaceProjectMembers([]jira.ProjectMember{{AccountID: "acct-ada", DisplayName: "Ada"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.ReplaceProjectMembers(nil); err != nil {
		t.Fatalf("ReplaceProjectMembers(nil): %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM project_member"); got != 0 {
		t.Errorf("project_member rows = %d, want 0", got)
	}
}

// Reset is the full resync's wipe; the member table is part of the rebuildable
// projection like every other table.
func TestResetClearsProjectMembers(t *testing.T) {
	st := openTempStore(t)
	if err := st.ReplaceProjectMembers([]jira.ProjectMember{{AccountID: "acct-ada", DisplayName: "Ada"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM project_member"); got != 0 {
		t.Errorf("project_member rows = %d after Reset, want 0", got)
	}
}
