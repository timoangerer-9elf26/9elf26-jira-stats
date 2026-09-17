package store

// Integration test for the project-member sync (#222): a jira.FakeClient whose
// assignable-user set mixes people with app accounts is driven through the real
// sync engine into a temp store, and the projection is asserted directly.
//
// Covers the ticket's required cases:
//   - members are persisted and readable from the store
//   - app accounts never become members
//   - someone removed from the project stops being a member after the next cycle
//   - a full resync rebuilds the member list like every other table

import (
	"context"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func memberNames(t *testing.T, st *Store) []string {
	t.Helper()
	members, err := st.ProjectMembers()
	if err != nil {
		t.Fatalf("ProjectMembers: %v", err)
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.DisplayName)
	}
	return names
}

func assertNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func TestSyncPersistsProjectMembersAndExcludesAppAccounts(t *testing.T) {
	fake := &jira.FakeClient{
		AssignableUsers: []jira.AssignableUser{
			{AccountID: "acct-grace", DisplayName: "Grace", AvatarURL: "/static/avatars/grace.svg", AccountType: jira.AccountTypePerson},
			{AccountID: "acct-bot", DisplayName: "Jira Coding Agent", AccountType: jira.AccountTypeApp},
			{AccountID: "acct-ada", DisplayName: "Ada", AvatarURL: "/static/avatars/ada.svg", AccountType: jira.AccountTypePerson},
		},
	}
	st := openTempStore(t)
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	assertNames(t, "members after backfill", memberNames(t, st), []string{"Ada", "Grace"})

	members, err := st.ProjectMembers()
	if err != nil {
		t.Fatalf("ProjectMembers: %v", err)
	}
	assertEq(t, "Ada account id", members[0].AccountID, "acct-ada")
	assertEq(t, "Ada avatar", members[0].AvatarURL, "/static/avatars/ada.svg")

	// Re-syncing replaces rather than duplicating.
	if _, err := sync.Backfill(context.Background(), fake, st); err != nil {
		t.Fatalf("re-backfill: %v", err)
	}
	if got := countRows(t, st, "SELECT COUNT(*) FROM project_member"); got != 2 {
		t.Fatalf("project_member rows = %d after re-sync, want 2", got)
	}
}

// Leaving the project is Jira simply no longer listing the person; one cycle
// later they are no longer a member (CONTEXT.md "Project member").
func TestSyncCycleDropsSomeoneRemovedFromTheProject(t *testing.T) {
	ada := jira.AssignableUser{AccountID: "acct-ada", DisplayName: "Ada", AccountType: jira.AccountTypePerson}
	alan := jira.AssignableUser{AccountID: "acct-alan", DisplayName: "Alan", AccountType: jira.AccountTypePerson}
	fake := &jira.FakeClient{
		// An issue keeps the store warm, so the second cycle takes the incremental
		// path — the one a live dashboard runs almost all the time.
		Issues:          []jira.Issue{{Key: "DCAI-1", Type: "Task", Summary: "Warm", Status: "In Progress", StatusCategory: "In Progress"}},
		AssignableUsers: []jira.AssignableUser{ada, alan},
	}
	st := openTempStore(t)
	syncer := sync.NewSyncer(fake, st, time.Minute)

	if err := syncer.Cycle(context.Background()); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	assertNames(t, "members after first cycle", memberNames(t, st), []string{"Ada", "Alan"})

	fake.AssignableUsers = []jira.AssignableUser{ada}
	if err := syncer.Cycle(context.Background()); err != nil {
		t.Fatalf("second cycle: %v", err)
	}
	assertNames(t, "members after Alan left", memberNames(t, st), []string{"Ada"})
}
