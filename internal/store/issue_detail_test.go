package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func openIssueStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "issues.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestIssueDetailAfterBackfillAndAssigneeEdit(t *testing.T) {
	fake := jira.NewFakeClient()
	issue := &fake.Issues[0]
	issue.Description = json.RawMessage(`{"type":"doc","version":1,"content":[]}`)
	issue.Comments = []jira.Comment{
		{ID: "11", Body: json.RawMessage(`{"type":"doc","version":1,"content":[]}`), Author: jira.Person{AccountID: "acct-ada", DisplayName: "Ada"}},
		{ID: "12", Author: jira.Person{AccountID: "acct-grace", DisplayName: "Grace"}},
	}
	issue.Links = []jira.IssueLink{{Key: "DCAI-4", Summary: "Mid-sprint chore", Relation: "blocks"}}
	issue.Labels = []string{"Technical"}
	issue.Priority = "High"
	issue.ResolvedAt = time.Date(2026, 7, 14, 9, 0, 0, 456000000, time.UTC)

	st := openIssueStore(t)
	if _, err := sync.Backfill(t.Context(), fake, st); err != nil {
		t.Fatal(err)
	}
	check := func(wantAssignee, wantAccountID string) {
		t.Helper()
		detail, found, err := st.IssueDetail("DCAI-1")
		if err != nil || !found {
			t.Fatalf("IssueDetail found=%v err=%v", found, err)
		}
		if string(detail.Description) != string(issue.Description) || !reflect.DeepEqual(detail.Comments, issue.Comments) || !slices.Equal(detail.Links, issue.Links) {
			t.Errorf("description, comments or links missing from %+v", detail)
		}
		if detail.Assignee != wantAssignee || detail.AssigneeAccountID != wantAccountID || detail.ParentKey != "DCAI-6" ||
			detail.Sprint != "KW29" || detail.Priority != "High" || !slices.Equal(detail.Labels, []string{"Technical"}) ||
			!detail.CreatedAt.Equal(issue.CreatedAt) || !detail.ResolvedAt.Equal(issue.ResolvedAt) {
			t.Errorf("detail = %+v", detail)
		}
	}

	check("Ada", "acct-ada")
	syncer := sync.NewSyncer(fake, st, time.Minute)
	if _, err := syncer.SetAssignee(t.Context(), "DCAI-1", "acct-grace"); err != nil {
		t.Fatal(err)
	}
	check("Grace", "acct-grace")

	if _, found, err := st.IssueDetail("DCAI-404"); found || err != nil {
		t.Fatalf("missing IssueDetail found=%v err=%v", found, err)
	}
}

func TestIssueChangesSinceRetainsDoneAndPriorityAfterReopen(t *testing.T) {
	st := openIssueStore(t)
	done := time.Date(2026, 11, 22, 17, 0, 0, 456000000, time.UTC)
	priorityChange := jira.ChangelogEntry{
		ID: "54/priority", Field: "priority", From: "Medium", To: "High", FromID: "3", ToID: "2",
		Timestamp: done, Author: jira.Person{AccountID: "acct-ada", DisplayName: "Ada"},
	}
	doneChange := jira.ChangelogEntry{
		ID: "54/status", Field: "status", From: "In Progress", To: "DONE (This Sprint)",
		Timestamp: done, Author: priorityChange.Author,
	}
	reopened := jira.ChangelogEntry{
		ID: "55/status", Field: "status", From: "DONE (This Sprint)", To: "In Progress",
		Timestamp: done.Add(200 * time.Millisecond), Author: jira.Person{AccountID: "acct-grace", DisplayName: "Grace"},
	}
	issue := jira.Issue{
		Key: "DCAI-54", Status: doneChange.To, Priority: "High", ResolvedAt: done,
		Changelog: []jira.ChangelogEntry{
			{ID: "53/status", Field: "status", Timestamp: done.Add(-time.Millisecond)},
			{ID: "54/estimated_time", Field: "Estimated Time", Timestamp: done},
			doneChange, priorityChange,
		},
	}
	fake := &jira.FakeClient{Issues: []jira.Issue{issue}}
	syncer := sync.NewSyncer(fake, st, time.Minute)
	if err := syncer.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}

	issue.Status = reopened.To
	issue.ResolvedAt = time.Time{} // Jira clears resolutiondate on this reopen.
	issue.Changelog = append(issue.Changelog, reopened)
	fake.Updated = []jira.Issue{issue}
	if err := syncer.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}

	changes, err := st.IssueChangesSince(done)
	if err != nil {
		t.Fatal(err)
	}
	want := []jira.ChangelogEntry{priorityChange, doneChange, reopened}
	if !slices.Equal(changes[issue.Key], want) {
		t.Errorf("changes after reopen = %+v, want %+v", changes, want)
	}

	rows, err := st.PrioIssues()
	if err != nil || len(rows) != 1 || rows[0].Status != "In Progress" || !rows[0].ResolvedAt.IsZero() {
		t.Fatalf("current projection after reopen = %+v, err=%v", rows, err)
	}
}

func TestRefreshIssueProjectsPersonalJiraRead(t *testing.T) {
	st := openIssueStore(t)
	service := jira.NewFakeClient()
	syncer := sync.NewSyncer(service, st, time.Minute)
	if err := syncer.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}

	person := jira.NewFakeClient()
	person.Issues[0].Summary = "Edited using a personal Jira account"
	person.Issues[0].Comments = []jira.Comment{
		{ID: "1", Author: jira.Person{AccountID: "acct-ada", DisplayName: "Ada"}},
	}
	if _, err := syncer.RefreshIssue(t.Context(), person, "DCAI-1"); err != nil {
		t.Fatal(err)
	}
	detail, found, err := st.IssueDetail("DCAI-1")
	if err != nil || !found || detail.Summary != "Edited using a personal Jira account" ||
		len(detail.Comments) != 1 || detail.Comments[0].Author.AccountID != "acct-ada" {
		t.Fatalf("refreshed detail=%+v found=%v err=%v", detail, found, err)
	}
}

type blockingBackfillJira struct {
	*jira.FakeClient
	started, release chan struct{}
}

func (c blockingBackfillJira) FetchIssues(ctx context.Context) ([]jira.Issue, error) {
	close(c.started)
	select {
	case <-c.release:
		return c.FakeClient.FetchIssues(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRefreshIssueRespectsDeadlineDuringBackfill(t *testing.T) {
	st := openIssueStore(t)
	jiraClient := blockingBackfillJira{
		FakeClient: &jira.FakeClient{Issues: []jira.Issue{{Key: "DCAI-1", Status: "Refinement"}}},
		started:    make(chan struct{}), release: make(chan struct{}),
	}
	syncer := sync.NewSyncer(jiraClient, st, time.Minute)
	backfill := make(chan error, 1)
	go func() { backfill <- syncer.Cycle(t.Context()) }()
	defer func() {
		close(jiraClient.release)
		if err := <-backfill; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-jiraClient.started:
	case <-time.After(3 * time.Second):
		t.Fatal("backfill did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	refresh := make(chan error, 1)
	go func() {
		_, err := syncer.RefreshIssue(ctx, jiraClient, "DCAI-1")
		refresh <- err
	}()
	select {
	case err := <-refresh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refresh error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh waited for backfill beyond its deadline")
	}
}
