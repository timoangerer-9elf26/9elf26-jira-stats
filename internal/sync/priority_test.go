package sync

// The Prio view's priority write path (#212): write the level to Jira, re-read
// that single issue, persist what the read returned — the identical shape as the
// estimate edit (docs/adr/0005) and the transition (docs/adr/0010), so a failed
// write leaves both Jira and the projection untouched.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// priorityClient records the call sequence so the test asserts
// write-then-read-then-persist rather than trusting a fake's state.
type priorityClient struct {
	jira.Client // unused reads panic loudly if ever called

	reread    jira.Issue
	writeErr  error
	rereadErr error
	calls     []string
}

func (c *priorityClient) UpdateIssuePriority(_ context.Context, key, priority string) error {
	c.calls = append(c.calls, "priority:"+key+":"+priority)
	return c.writeErr
}

func (c *priorityClient) FetchIssue(_ context.Context, key string) (jira.Issue, error) {
	c.calls = append(c.calls, "fetch:"+key)
	if c.rereadErr != nil {
		return jira.Issue{}, c.rereadErr
	}
	return c.reread, nil
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

func TestSetPriorityWritesThenRereadsThenPersists(t *testing.T) {
	client := &priorityClient{reread: jira.Issue{Key: "DCAI-1", Priority: "Highest"}}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.SetPriority(context.Background(), "DCAI-1", "Highest"); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	assertCalls(t, client.calls, []string{"priority:DCAI-1:Highest", "fetch:DCAI-1"})
	if len(store.saved) != 1 || store.saved[0].Key != "DCAI-1" || store.saved[0].Priority != "Highest" {
		t.Errorf("saved = %+v, want exactly the re-read issue", store.saved)
	}
}

// The projection is set from the Jira READ, never from what was requested.
func TestSetPriorityPersistsWhatTheReadReturned(t *testing.T) {
	client := &priorityClient{reread: jira.Issue{Key: "DCAI-1", Priority: "Medium"}}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.SetPriority(context.Background(), "DCAI-1", "Highest"); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Priority != "Medium" {
		t.Errorf("saved = %+v, want the re-read value Medium", store.saved)
	}
}

func TestSetPriorityLeavesTheProjectionUnchangedWhenTheWriteFails(t *testing.T) {
	client := &priorityClient{writeErr: errors.New("403")}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.SetPriority(context.Background(), "DCAI-1", "Highest"); err == nil {
		t.Fatal("SetPriority succeeded despite the write failing")
	}
	assertCalls(t, client.calls, []string{"priority:DCAI-1:Highest"})
	if len(store.saved) != 0 {
		t.Errorf("a failed write persisted %+v", store.saved)
	}
}

func TestSetPriorityFailsWhenTheRereadFails(t *testing.T) {
	client := &priorityClient{rereadErr: errors.New("timeout")}
	store := &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if err := s.SetPriority(context.Background(), "DCAI-1", "Low"); err == nil {
		t.Fatal("SetPriority succeeded despite the re-read failing")
	}
	if len(store.saved) != 0 {
		t.Errorf("a failed re-read persisted %+v", store.saved)
	}
}

func TestSetPriorityFailsWhenTheSaveFails(t *testing.T) {
	client := &priorityClient{reread: jira.Issue{Key: "DCAI-1", Priority: "Low"}}
	store := &recordingStore{saveErr: errors.New("disk full")}
	s := NewSyncer(client, store, time.Minute)

	if err := s.SetPriority(context.Background(), "DCAI-1", "Low"); err == nil {
		t.Fatal("SetPriority succeeded despite the save failing")
	}
}

// Over the fake client the whole path runs end to end: the fake's in-memory
// write is what the re-read returns, and that is what lands in the store.
func TestSetPriorityOverTheFakeClient(t *testing.T) {
	fake := jira.NewFakeClient()
	key := fake.Issues[0].Key
	store := &recordingStore{}
	s := NewSyncer(fake, store, time.Minute)

	if err := s.SetPriority(context.Background(), key, "Lowest"); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	if len(store.saved) != 1 || store.saved[0].Key != key || store.saved[0].Priority != "Lowest" {
		t.Errorf("saved = %+v, want %s at Lowest", store.saved, key)
	}
}
