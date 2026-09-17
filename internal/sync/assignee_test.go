package sync

// The Board's assignee write path (#223, docs/adr/0012): write the account id to
// Jira, re-read that single issue, persist what the read returned — the shape
// docs/adr/0005 set and the estimate, transition and priority writes all follow.
// A failed write leaves Jira and the projection untouched; a failure AFTER the
// write landed is reported as such, because by then Jira has already changed.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// assigneeClient records the call sequence, so the test asserts
// write-then-read-then-persist rather than trusting a fake's state.
type assigneeClient struct {
	jira.Client // unused reads panic loudly if ever called

	reread    jira.Issue
	writeErr  error
	rereadErr error
	calls     []string
}

func (c *assigneeClient) UpdateIssueAssignee(_ context.Context, key, accountID string) error {
	c.calls = append(c.calls, "assignee:"+key+":"+accountID)
	return c.writeErr
}

func (c *assigneeClient) FetchIssue(_ context.Context, key string) (jira.Issue, error) {
	c.calls = append(c.calls, "fetch:"+key)
	if c.rereadErr != nil {
		return jira.Issue{}, c.rereadErr
	}
	return c.reread, nil
}

func TestSetAssigneeWritesThenRereadsThenPersists(t *testing.T) {
	client := &assigneeClient{reread: jira.Issue{Key: "DCAI-1", Assignee: "Grace", AssigneeAvatarURL: "/static/avatars/grace.svg"}}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace")
	if err != nil {
		t.Fatalf("SetAssignee: %v", err)
	}
	if got != "Grace" {
		t.Errorf("assignee = %q, want the re-read value Grace", got)
	}
	assertCalls(t, client.calls, []string{"assignee:DCAI-1:acct-grace", "fetch:DCAI-1"})
	if len(store.saved) != 1 || store.saved[0].Key != "DCAI-1" || store.saved[0].Assignee != "Grace" {
		t.Errorf("saved = %+v, want exactly the re-read issue", store.saved)
	}
	if store.saved[0].AssigneeAvatarURL != "/static/avatars/grace.svg" {
		t.Errorf("saved avatar = %q, want the re-read one", store.saved[0].AssigneeAvatarURL)
	}
}

// The projection is set from the Jira READ, never from the account id that was
// requested — so the avatar only ever shows an assignee the write achieved.
func TestSetAssigneePersistsWhatTheReadReturned(t *testing.T) {
	client := &assigneeClient{reread: jira.Issue{Key: "DCAI-1", Assignee: "Ada"}}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace")
	if err != nil {
		t.Fatalf("SetAssignee: %v", err)
	}
	if got != "Ada" {
		t.Errorf("assignee = %q, want the re-read value Ada", got)
	}
	if len(store.saved) != 1 || store.saved[0].Assignee != "Ada" {
		t.Errorf("saved = %+v, want the re-read value Ada", store.saved)
	}
}

// Clearing is its own case: it must reach Jira as a clear and come back through
// the same re-read, not be assumed to work because assigning does.
func TestSetAssigneeClearsTheAssignee(t *testing.T) {
	client := &assigneeClient{reread: jira.Issue{Key: "DCAI-1"}}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetAssignee(context.Background(), "DCAI-1", jira.UnassignedAccountID)
	if err != nil {
		t.Fatalf("SetAssignee(clear): %v", err)
	}
	if got != "" {
		t.Errorf("assignee = %q, want empty after a clear", got)
	}
	assertCalls(t, client.calls, []string{"assignee:DCAI-1:", "fetch:DCAI-1"})
	if len(store.saved) != 1 || store.saved[0].Assignee != "" {
		t.Errorf("saved = %+v, want the cleared issue", store.saved)
	}
}

func TestSetAssigneeLeavesTheProjectionUnchangedWhenTheWriteFails(t *testing.T) {
	client := &assigneeClient{writeErr: errors.New("403")}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if _, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace"); err == nil {
		t.Fatal("SetAssignee succeeded despite the write failing")
	}
	assertCalls(t, client.calls, []string{"assignee:DCAI-1:acct-grace"})
	if len(store.saved) != 0 {
		t.Errorf("a failed write persisted %+v", store.saved)
	}
}

// A failed write is NOT marked as landed: nothing changed in Jira, so the caller
// may report it as a plain failure.
func TestSetAssigneeFailedWriteIsNotMarkedAsLanded(t *testing.T) {
	client := &assigneeClient{writeErr: errors.New("403")}
	s := NewSyncer(client, &recordingStore{}, time.Minute)

	_, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace")
	if errors.Is(err, ErrWriteLanded) {
		t.Fatalf("err = %v, marked as landed though the write itself failed", err)
	}
}

// By the time the re-read runs, Jira HAS changed. Reporting that as a failed
// write is the defect #207 had to fix on the drag path, so the caller is given a
// way to tell the two apart.
func TestSetAssigneeRereadFailureIsDistinguishableFromAFailedWrite(t *testing.T) {
	boom := errors.New("timeout")
	client := &assigneeClient{rereadErr: boom}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	_, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace")
	if err == nil {
		t.Fatal("SetAssignee succeeded despite the re-read failing")
	}
	if !errors.Is(err, ErrWriteLanded) {
		t.Errorf("err = %v, want it marked with ErrWriteLanded", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying cause still reachable", err)
	}
	if len(store.saved) != 0 {
		t.Errorf("a failed re-read persisted %+v", store.saved)
	}
}

// The persist is on the far side of the write too, so it carries the same mark.
func TestSetAssigneeSaveFailureIsMarkedAsLanded(t *testing.T) {
	client := &assigneeClient{reread: jira.Issue{Key: "DCAI-1", Assignee: "Grace"}}
	store := &recordingStore{saveErr: errors.New("disk full")}
	s := NewSyncer(client, store, time.Minute)

	_, err := s.SetAssignee(context.Background(), "DCAI-1", "acct-grace")
	if err == nil {
		t.Fatal("SetAssignee succeeded despite the save failing")
	}
	if !errors.Is(err, ErrWriteLanded) {
		t.Errorf("err = %v, want it marked with ErrWriteLanded", err)
	}
}

// Over the fake client the whole path runs end to end: the fake's in-memory
// write is what the re-read returns, and that is what lands in the store.
func TestSetAssigneeOverTheFakeClient(t *testing.T) {
	fake := jira.NewFakeClient()
	key := fake.Issues[0].Key
	members, err := fake.FetchProjectMembers(context.Background())
	if err != nil {
		t.Fatalf("FetchProjectMembers: %v", err)
	}
	store := &recordingStore{}
	s := NewSyncer(fake, store, time.Minute)

	got, err := s.SetAssignee(context.Background(), key, members[0].AccountID)
	if err != nil {
		t.Fatalf("SetAssignee: %v", err)
	}
	if got != members[0].DisplayName {
		t.Errorf("assignee = %q, want %q", got, members[0].DisplayName)
	}
	if len(store.saved) != 1 || store.saved[0].Assignee != members[0].DisplayName {
		t.Errorf("saved = %+v, want %s assigned to %s", store.saved, key, members[0].DisplayName)
	}

	cleared, err := s.SetAssignee(context.Background(), key, jira.UnassignedAccountID)
	if err != nil {
		t.Fatalf("SetAssignee(clear): %v", err)
	}
	if cleared != "" {
		t.Errorf("assignee = %q, want empty after a clear", cleared)
	}
	if len(store.saved) != 2 || store.saved[1].Assignee != "" || store.saved[1].AssigneeAvatarURL != "" {
		t.Errorf("saved = %+v, want %s unassigned", store.saved, key)
	}
}

// A failing fake write exercises the failure branch end to end: Jira unchanged,
// no re-read, nothing persisted.
func TestSetAssigneeOverTheFakeClientWhenTheWriteFails(t *testing.T) {
	fake := jira.NewFakeClient()
	key := fake.Issues[0].Key
	before := fake.Issues[0].Assignee
	fake.WriteErr = errors.New("403")
	store := &recordingStore{}
	s := NewSyncer(fake, store, time.Minute)

	if _, err := s.SetAssignee(context.Background(), key, "acct-ada"); err == nil {
		t.Fatal("SetAssignee succeeded despite the fake write failing")
	}
	if fake.Issues[0].Assignee != before {
		t.Errorf("the failed write changed Jira: assignee = %q, was %q", fake.Issues[0].Assignee, before)
	}
	if len(store.saved) != 0 {
		t.Errorf("a failed write persisted %+v", store.saved)
	}
}
