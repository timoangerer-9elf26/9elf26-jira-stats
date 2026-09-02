package sync

// The Board transition write path (docs/adr/0010, #194): write the transition to
// Jira, re-read that single issue, persist what the read returned — the same
// shape as the estimate edit (docs/adr/0005), so a failed write leaves both Jira
// and the projection untouched.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// --- test doubles ---

type recordingStore struct {
	saved   []jira.Issue
	saveErr error
}

func (s *recordingStore) SaveIssue(iss jira.Issue, syncedAt string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, iss)
	return nil
}
func (s *recordingStore) SaveSprint(jira.Sprint) error       { return nil }
func (s *recordingStore) IssueCount() (int, error)           { return len(s.saved), nil }
func (s *recordingStore) LastSync() (time.Time, bool, error) { return time.Time{}, false, nil }
func (s *recordingStore) SetLastSync(time.Time) error        { return nil }
func (s *recordingStore) SetLastFullResync(time.Time) error  { return nil }
func (s *recordingStore) Reset() error                       { return nil }

// transitionClient is a jira.Client that records the call sequence, so the test
// can assert write-then-read-then-persist rather than trusting the fake's state.
type transitionClient struct {
	jira.Client // unused reads panic loudly if ever called

	offered    []jira.Transition
	reread     jira.Issue
	transitErr error
	rereadErr  error
	fetchErr   error
	calls      []string
}

func (c *transitionClient) FetchTransitions(_ context.Context, key string) ([]jira.Transition, error) {
	c.calls = append(c.calls, "transitions:"+key)
	if c.fetchErr != nil {
		return nil, c.fetchErr
	}
	return c.offered, nil
}

func (c *transitionClient) TransitionIssue(_ context.Context, key, transitionID string) error {
	c.calls = append(c.calls, "transition:"+key+":"+transitionID)
	return c.transitErr
}

func (c *transitionClient) FetchIssue(_ context.Context, key string) (jira.Issue, error) {
	c.calls = append(c.calls, "fetch:"+key)
	if c.rereadErr != nil {
		return jira.Issue{}, c.rereadErr
	}
	return c.reread, nil
}

func dcaiClient() *transitionClient {
	return &transitionClient{
		offered: jira.DCAITransitions(),
		reread:  jira.Issue{Key: "DCAI-1", Status: "In Progress", StatusCategory: "In Progress"},
	}
}

// --- tests ---

func TestSetStatusWritesThenRereadsThenPersists(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDInProgress)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got != "In Progress" {
		t.Errorf("returned status = %q, want %q", got, "In Progress")
	}
	want := []string{"transitions:DCAI-1", "transition:DCAI-1:21", "fetch:DCAI-1"}
	assertCalls(t, client.calls, want)
	if len(store.saved) != 1 || store.saved[0].Key != client.reread.Key || store.saved[0].Status != client.reread.Status {
		t.Errorf("saved = %+v, want exactly the re-read issue", store.saved)
	}
}

// The projection is set from the Jira READ, never from what was requested: if
// Jira's re-read disagrees with the requested status, the read wins.
func TestSetStatusPersistsWhatTheReadReturned(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	client.reread = jira.Issue{Key: "DCAI-1", Status: "Review / Testing", StatusCategory: "In Progress"}
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDInProgress)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got != "Review / Testing" {
		t.Errorf("returned status = %q, want the re-read value %q", got, "Review / Testing")
	}
	if len(store.saved) != 1 || store.saved[0].Status != "Review / Testing" {
		t.Errorf("saved = %+v, want the re-read status", store.saved)
	}
}

func TestSetStatusResolvesByTargetStatusIDNotName(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	s := NewSyncer(client, store, time.Minute)

	if _, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDDoneThisSprint); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	// Transition 5 lands in DONE (This Sprint); transition 31 is the decoy named
	// "Done" that lands in Ready for release.
	if got := client.calls[1]; got != "transition:DCAI-1:5" {
		t.Errorf("performed %q, want the transition landing in DONE (This Sprint) (id 5)", got)
	}
}

func TestSetStatusRefusesAStatusJiraDoesNotOffer(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	client.offered = nil // Jira offers this issue nothing
	s := NewSyncer(client, store, time.Minute)

	if _, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDReadyForRelease); !errors.Is(err, jira.ErrNoTransition) {
		t.Fatalf("error = %v, want ErrNoTransition", err)
	}
	if len(client.calls) != 1 {
		t.Errorf("calls = %v, want only the transitions lookup (no write)", client.calls)
	}
	if len(store.saved) != 0 {
		t.Errorf("saved = %+v, want the projection untouched", store.saved)
	}
}

func TestSetStatusLeavesTheProjectionUnchangedWhenTheWriteFails(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	client.transitErr = errors.New("403 from Jira")
	s := NewSyncer(client, store, time.Minute)

	if _, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDInProgress); err == nil {
		t.Fatal("SetStatus succeeded on a failed write")
	}
	if len(store.saved) != 0 {
		t.Errorf("saved = %+v, want the projection untouched", store.saved)
	}
	for _, c := range client.calls {
		if c == "fetch:DCAI-1" {
			t.Error("re-read the issue after a failed write")
		}
	}
}

func TestSetStatusFailsWhenTheRereadFails(t *testing.T) {
	client, store := dcaiClient(), &recordingStore{}
	client.rereadErr = errors.New("network")
	s := NewSyncer(client, store, time.Minute)

	if _, err := s.SetStatus(context.Background(), "DCAI-1", jira.StatusIDInProgress); err == nil {
		t.Fatal("SetStatus succeeded despite a failed re-read")
	}
	if len(store.saved) != 0 {
		t.Errorf("saved = %+v, want nothing persisted without a Jira read", store.saved)
	}
}

// The whole path runs on the built-in fake, no live Jira required.
func TestSetStatusOverTheFakeClient(t *testing.T) {
	client := jira.NewFakeClient()
	store := &recordingStore{}
	key := client.Issues[0].Key
	s := NewSyncer(client, store, time.Minute)

	got, err := s.SetStatus(context.Background(), key, jira.StatusIDReviewTesting)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got != "Review / Testing" {
		t.Errorf("returned status = %q, want %q", got, "Review / Testing")
	}
	if len(store.saved) != 1 || store.saved[0].Status != "Review / Testing" {
		t.Errorf("saved = %+v, want the moved issue", store.saved)
	}
}
