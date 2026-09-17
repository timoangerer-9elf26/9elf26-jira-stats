package sync

// The project-member sync step (#222): every cycle refreshes the people who can
// be assigned work on the project, so the Board's assign popover renders from
// SQLite and no read path ever calls Jira (docs/adr/0012).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// memberClient answers the member read and nothing else; every other read is
// empty, so a test's assertions are about members alone.
type memberClient struct {
	jira.Client // unused reads panic loudly if ever called

	members []jira.ProjectMember
	issues  []jira.Issue // the backfill set
	updated []jira.Issue // the incremental set
	err     error        // fails the member read alone, leaving the issue reads healthy
	calls   int
}

func (c *memberClient) FetchIssues(context.Context) ([]jira.Issue, error) { return c.issues, nil }
func (c *memberClient) FetchIssuesUpdatedSince(context.Context, time.Time) ([]jira.Issue, error) {
	return c.updated, nil
}
func (c *memberClient) FetchSprints(context.Context) ([]jira.Sprint, error) { return nil, nil }
func (c *memberClient) FetchProjectMembers(context.Context) ([]jira.ProjectMember, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.members, nil
}

func adaMember() jira.ProjectMember {
	return jira.ProjectMember{AccountID: "acct-ada", DisplayName: "Ada", AvatarURL: "/static/avatars/ada.svg"}
}

func TestBackfillRefreshesProjectMembers(t *testing.T) {
	client := &memberClient{members: []jira.ProjectMember{adaMember()}}
	store := &recordingStore{}

	if _, err := Backfill(context.Background(), client, store); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("FetchProjectMembers calls = %d, want 1", client.calls)
	}
	if len(store.members) != 1 || len(store.members[0]) != 1 || store.members[0][0] != adaMember() {
		t.Fatalf("stored members = %+v, want one replace carrying Ada", store.members)
	}
}

// An incremental cycle refreshes members too — otherwise a member list would
// only ever be correct straight after a full resync.
func TestIncrementalCycleRefreshesProjectMembers(t *testing.T) {
	client := &memberClient{members: []jira.ProjectMember{adaMember()}}
	// A non-empty store forces the incremental path rather than the cold backfill.
	store := &recordingStore{saved: []jira.Issue{{Key: "DCAI-1"}}}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("Cycle: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("FetchProjectMembers calls = %d, want 1 on an incremental cycle", client.calls)
	}
	if len(store.members) != 1 {
		t.Fatalf("member replaces = %d, want 1", len(store.members))
	}
}

// Someone removed from the project is reported by Jira's next answer simply by
// being absent from it; the syncer hands that answer straight to the store,
// which replaces wholesale.
func TestCycleHandsJirasAnswerStraightToTheStore(t *testing.T) {
	client := &memberClient{members: []jira.ProjectMember{adaMember(), {AccountID: "acct-alan", DisplayName: "Alan"}}}
	store := &recordingStore{saved: []jira.Issue{{Key: "DCAI-1"}}}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("first Cycle: %v", err)
	}
	client.members = []jira.ProjectMember{adaMember()}
	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("second Cycle: %v", err)
	}
	if len(store.members) != 2 {
		t.Fatalf("member replaces = %d, want 2", len(store.members))
	}
	if len(store.members[1]) != 1 || store.members[1][0] != adaMember() {
		t.Fatalf("second replace = %+v, want Ada alone", store.members[1])
	}
}

// A failing member fetch must NOT abort the cycle. Jira gates its user search on
// a permission separate from issue read, so a token allowed to read issues but
// not to browse users would otherwise stale the whole dashboard — issues and
// sprints included — over a list that feeds a popover.
func TestFailedMemberFetchStillSyncsIssues(t *testing.T) {
	client := &memberClient{
		issues: []jira.Issue{{Key: "DCAI-1", Type: "Task", Summary: "Still synced"}},
		err:    errors.New("jira: you do not have permission to browse users"),
	}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("Cycle aborted on a failed member fetch: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Key != "DCAI-1" {
		t.Fatalf("saved issues = %+v, want DCAI-1 synced despite the member failure", store.saved)
	}
	// The stored list is left exactly as it was — no empty write wipes it.
	if len(store.members) != 0 {
		t.Fatalf("member replaces = %+v, want none (the previous list must survive)", store.members)
	}
}

// The same on the incremental path, which is what a live dashboard runs almost
// all the time.
func TestFailedMemberFetchStillSyncsIncrementally(t *testing.T) {
	client := &memberClient{
		updated: []jira.Issue{{Key: "DCAI-2", Type: "Bug", Summary: "Changed"}},
		err:     errors.New("jira: you do not have permission to browse users"),
	}
	store := &recordingStore{saved: []jira.Issue{{Key: "DCAI-1"}}}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("incremental Cycle aborted on a failed member fetch: %v", err)
	}
	if len(store.saved) != 2 || store.saved[1].Key != "DCAI-2" {
		t.Fatalf("saved issues = %+v, want DCAI-2 synced despite the member failure", store.saved)
	}
	if len(store.members) != 0 {
		t.Fatalf("member replaces = %+v, want none (the previous list must survive)", store.members)
	}
}

// A store write that fails is equally non-fatal: the cycle carries on and the
// previously stored list stands.
func TestFailedMemberSaveStillSyncsIssues(t *testing.T) {
	client := &memberClient{
		issues:  []jira.Issue{{Key: "DCAI-1", Type: "Task", Summary: "Still synced"}},
		members: []jira.ProjectMember{adaMember()},
	}
	store := &recordingStore{memberSaveErr: errors.New("database is locked")}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err != nil {
		t.Fatalf("Cycle aborted on a failed member save: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved issues = %+v, want DCAI-1 synced despite the member save failure", store.saved)
	}
}
