package jira

// The fake clients must carry the same hazard the live project does: an
// assignable-user set that mixes people with app accounts (#222). A fake that
// only ever holds people would let a regression that stops filtering automation
// ship green.

import (
	"context"
	"testing"
)

func TestCannedDatasetMixesPeopleAndAppAccounts(t *testing.T) {
	assertMixedAssignableUsers(t, "canned", NewFakeClient())
}

func TestDenseDatasetMixesPeopleAndAppAccounts(t *testing.T) {
	assertMixedAssignableUsers(t, "dense", NewDenseFakeClient())
}

func assertMixedAssignableUsers(t *testing.T, dataset string, c *FakeClient) {
	t.Helper()

	var people, apps int
	for _, u := range c.AssignableUsers {
		switch u.AccountType {
		case AccountTypePerson:
			people++
		case AccountTypeApp:
			apps++
		default:
			t.Errorf("%s dataset: user %q has account type %q, want one of atlassian/app", dataset, u.DisplayName, u.AccountType)
		}
		if u.AccountID == "" {
			t.Errorf("%s dataset: user %q has no account id", dataset, u.DisplayName)
		}
	}
	if people < 2 {
		t.Errorf("%s dataset: %d people, want a realistic set of at least 2", dataset, people)
	}
	if apps < 1 {
		t.Errorf("%s dataset: no app account, so a lost filter would go unnoticed", dataset)
	}

	members, err := c.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("%s dataset: FetchProjectMembers: %v", dataset, err)
	}
	if len(members) != people {
		t.Fatalf("%s dataset: %d members from %d people (%d apps) — app accounts leaked", dataset, len(members), people, apps)
	}
	for _, m := range members {
		if m.AccountID == "" || m.DisplayName == "" {
			t.Errorf("%s dataset: member %+v is missing an identity", dataset, m)
		}
	}
}

// A zero-valued FakeClient is how most tests build one; it must answer cleanly
// rather than panic, exactly as its other reads do.
func TestZeroFakeClientHasNoProjectMembers(t *testing.T) {
	members, err := (&FakeClient{}).FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("members = %+v, want none", members)
	}
}
