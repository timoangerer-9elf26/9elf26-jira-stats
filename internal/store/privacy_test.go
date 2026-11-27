package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

func TestPrivacyClosureSurvivesResyncAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "privacy.db")
	open := func() *store.Store {
		t.Helper()
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	st := open()
	ctx := t.Context()
	const id = "closed-account"
	retrieved := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	meeting := retrieved.Add(-time.Hour)
	old := jira.Person{AccountID: id, DisplayName: "Old Name"}
	issue := jira.Issue{
		Key: "DCAI-10", Summary: "Work item", Assignee: old.DisplayName, AssigneeAccountID: id,
		AssigneeAvatarURL: "https://example.com/avatar", Creator: old.DisplayName, CreatorAccountID: id,
		Description: []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"closed-account","text":"@Old Name"}}]}]}`),
		Comments:    []jira.Comment{{ID: "comment", Author: old, CreatedAt: meeting, Body: jira.PlainADF("Work comment")}},
		Changelog:   []jira.ChangelogEntry{{ID: "change", Field: "status", To: "Done", Timestamp: meeting, Author: old}},
	}
	syncProjection := func() {
		t.Helper()
		if err := st.SaveIssue(issue, retrieved.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		if err := st.ReplaceProjectMembers([]jira.ProjectMember{{AccountID: id, DisplayName: old.DisplayName}}); err != nil {
			t.Fatal(err)
		}
	}
	syncProjection()
	if err := st.SaveOAuthLogin(ctx, id, []byte("encrypted"), "session", "", retrieved.Unix(), retrieved.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := st.CompletePrioMeeting(0, meeting, id); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPrioMeetingChange(store.PrioMeetingChange{IssueKey: issue.Key, Label: "Status", AccountID: id, OccurredAt: meeting}); err != nil {
		t.Fatal(err)
	}
	due, err := st.PrivacyDue(ctx, retrieved.Add(time.Minute), 90)
	if err != nil || len(due) != 1 || due[0].AccountID != id || !due[0].UpdatedAt.Equal(meeting) {
		t.Fatalf("due accounts = %+v, err = %v", due, err)
	}
	if err := st.ApplyPrivacyReport(ctx, retrieved, 7*24*time.Hour, due, map[string]string{id: "closed"}, nil); err != nil {
		t.Fatal(err)
	}
	assertErased := func() {
		t.Helper()
		detail, found, err := st.IssueDetail(issue.Key)
		if err != nil || !found || detail.Summary != issue.Summary || detail.Assignee != "" || detail.AssigneeAccountID != "" ||
			detail.AssigneeAvatarURL != "" || len(detail.Comments) != 1 || detail.Comments[0].Author != (jira.Person{}) ||
			!detail.Comments[0].CreatedAt.Equal(meeting) || string(detail.Comments[0].Body) != string(issue.Comments[0].Body) ||
			string(detail.Description) != `{"content":[{"content":[{"attrs":{"id":"unknown"},"type":"mention"}],"type":"paragraph"}],"type":"doc","version":1}` {
			t.Fatalf("erased issue = %+v, found=%v err=%v", detail, found, err)
		}
		changes, err := st.IssueChangesSince(meeting)
		if err != nil || len(changes[issue.Key]) != 1 || changes[issue.Key][0].Author != (jira.Person{}) ||
			changes[issue.Key][0].To != "Done" || !changes[issue.Key][0].Timestamp.Equal(meeting) {
			t.Fatalf("erased changes = %+v, err=%v", changes, err)
		}
		members, err := st.ProjectMembers()
		if err != nil || len(members) != 0 {
			t.Fatalf("erased members = %+v, err=%v", members, err)
		}
		last, err := st.LastPrioMeeting()
		if err != nil || last.ID != 1 || last.AccountID != "" || !last.EndedAt.Equal(meeting) {
			t.Fatalf("erased meeting = %+v, err=%v", last, err)
		}
		audit, err := st.PrioMeetingChanges(1)
		if err != nil || len(audit) == 0 {
			t.Fatalf("erased audit = %+v, err=%v", audit, err)
		}
		for _, change := range audit {
			if change.AccountID != "" || !change.OccurredAt.Equal(meeting) {
				t.Fatalf("erased audit attribution = %+v", change)
			}
		}
		if _, err := st.OAuthGrant(ctx, id); err == nil {
			t.Fatal("closed grant survived")
		}
		if _, err := st.OAuthSessionAccount(ctx, "session", retrieved.Unix()); err == nil {
			t.Fatal("closed session survived")
		}
		due, err := st.PrivacyDue(ctx, retrieved.Add(30*24*time.Hour), 90)
		if err != nil || len(due) != 0 {
			t.Fatalf("closed account remained reportable: %v, err=%v", due, err)
		}
	}
	assertErased()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open()
	if err := st.Reset(); err != nil {
		t.Fatal(err)
	}
	syncProjection()
	if err := st.RecordPrioMeetingChange(store.PrioMeetingChange{IssueKey: issue.Key, Label: "Status", AccountID: id, OccurredAt: meeting}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOAuthLogin(ctx, id, []byte("encrypted"), "session", "", retrieved.Unix(), retrieved.Add(time.Hour).Unix()); err == nil {
		t.Fatal("closed account could sign in again")
	}
	assertErased()
}
