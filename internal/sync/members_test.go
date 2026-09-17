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
	err     error
	calls   int
}

func (c *memberClient) FetchIssues(context.Context) ([]jira.Issue, error) { return nil, nil }
func (c *memberClient) FetchIssuesUpdatedSince(context.Context, time.Time) ([]jira.Issue, error) {
	return nil, nil
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

func TestCycleFailsWhenTheMemberFetchFails(t *testing.T) {
	client := &memberClient{err: errors.New("jira down")}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.Cycle(context.Background()); err == nil {
		t.Fatal("Cycle succeeded despite a failed member fetch")
	}
}
