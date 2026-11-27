package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

func TestUpgradeBackfillsHistoricalIssueDetailsAndChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-meeting.db")
	seedPreMeetingProjection(t, path)

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	resolved := time.Date(2026, 7, 11, 9, 30, 0, 456000000, time.UTC)
	author := jira.Person{AccountID: "acct-ada", DisplayName: "Ada"}
	issue := jira.Issue{
		Key: "DCAI-4", Type: "Epic", Summary: "Historical issue", Status: "DONE (This Sprint)",
		ResolvedAt: resolved, Description: []byte(`{"type":"doc","version":1,"content":[]}`),
		Changelog: []jira.ChangelogEntry{
			{ID: "40/priority", Field: "priority", From: "Medium", To: "High", Timestamp: resolved, Author: author},
			{ID: "40/resolution", Field: "resolution", To: "Done", ToID: "1", Timestamp: resolved, Author: author},
			{ID: "40/status", Field: "status", From: "In Progress", To: "DONE (This Sprint)", Timestamp: resolved, Author: author},
		},
	}
	unavailable := errors.New("Jira unavailable")
	fake := &jira.FakeClient{Issues: []jira.Issue{issue}, Err: unavailable}
	syncer := sync.NewSyncer(fake, st, time.Minute)

	if err := syncer.Cycle(t.Context()); !errors.Is(err, unavailable) {
		t.Fatalf("first cycle err=%v, want %v", err, unavailable)
	}
	if detail, found, err := st.IssueDetail(issue.Key); err != nil || !found || detail.Summary != issue.Summary {
		t.Fatalf("pre-upgrade projection after failure: detail=%+v found=%v err=%v", detail, found, err)
	}
	fake.Err = nil
	if err := syncer.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if detail, found, err := st.IssueDetail(issue.Key); err != nil || !found ||
		!detail.ResolvedAt.Equal(resolved) || string(detail.Description) != string(issue.Description) {
		t.Fatalf("historical details after retry: detail=%+v found=%v err=%v", detail, found, err)
	}
	if err := syncer.Cycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.SinceCalls) != 1 {
		t.Fatalf("incremental fetches = %d, want one only after historical backfill", len(fake.SinceCalls))
	}
	if _, err := sync.Backfill(t.Context(), fake, st); err != nil {
		t.Fatal(err)
	}
	changes, err := st.IssueChangesSince(resolved)
	if err != nil || !slices.Equal(changes[issue.Key], issue.Changelog) {
		t.Fatalf("historical changes after rerun = %+v err=%v, want %+v", changes, err, issue.Changelog)
	}
	lastResync, ok, err := st.LastFullResync()
	if err != nil || !ok || lastResync.Format(time.RFC3339) != "2026-07-12T09:00:00Z" {
		t.Fatalf("last manual resync = %v recorded=%v err=%v", lastResync, ok, err)
	}
}

func seedPreMeetingProjection(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 11); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO issue (key, type, summary, status, status_category, synced_at)
		 VALUES ('DCAI-4', 'Epic', 'Historical issue', 'DONE (This Sprint)', 'Done', '2026-07-12T09:00:00Z')`,
		`INSERT INTO status_transition (changelog_entry_id, issue_key, field, from_status, to_status, transitioned_at)
		 VALUES ('40/status', 'DCAI-4', 'status', 'In Progress', 'DONE (This Sprint)', '2026-07-11T09:30:00Z')`,
		`INSERT INTO meta (key, value) VALUES
		 ('last_sync', '2026-07-12T09:00:00Z'), ('last_full_resync', '2026-07-12T09:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivacyMigrationInventoriesIdentifiableLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-privacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 15); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO issue (key, type, summary, status, status_category, synced_at, assignee, assignee_account_id, creator, comments)
		 VALUES ('DCAI-1', 'Epic', 'Existing ticket', 'Done', 'Done', '2026-01-05T09:00:00Z', 'Assignee', 'assignee-account', 'Name without ID',
		         '[{"ID":"comment-1","Author":{"AccountID":"comment-account","DisplayName":"Comment Name"}},{"ID":"comment-2","Author":{"DisplayName":"Unlinked Author"}}]'),
		        ('DCAI-2', 'Task', 'Older ticket', 'Done', 'Done', '2026-01-04T09:00:00Z', 'Unlinked Assignee', 'UNKNOWN', 'Unlinked Creator', '[{"ID":"legacy","Author":{"AccountID":"bad id","DisplayName":"Invalid author"},"Body":{"type":"doc","content":[{"type":"mention","attrs":{"text":"Legacy mention"}},{"type":"mention","attrs":{"id":"bad id","text":"Invalid mention"}},{"type":"mention","attrs":{"id":"all","userType":"SPECIAL","text":"Everyone"}}]}}]')`,
		`INSERT INTO status_transition (changelog_entry_id, issue_key, field, to_status, transitioned_at, author_account_id, author_display_name)
		 VALUES ('old-entry', 'DCAI-1', 'status', 'Done', '2025-12-20T10:00:00Z', 'transition-account', 'Transition Name'),
			        ('unknown-entry', 'DCAI-2', 'status', 'Done', '2025-12-21T10:00:00Z', '-1', 'Invalid change author')`,
		`INSERT INTO prio_meeting (ended_at, account_id) VALUES ('2026-01-01T07:00:00Z', 'meeting-account')`,
		`INSERT INTO prio_meeting_change (meeting_window, issue_key, label, account_id, occurred_at)
		 VALUES (1, 'DCAI-1', 'Status', 'audit-account', '2026-01-02T07:00:00Z')`,
		`INSERT INTO project_member (account_id, display_name) VALUES ('member-account', 'Member Name'), ('unknown', 'Unknown')`,
		`INSERT INTO jira_auth_grant (account_id, ciphertext) VALUES ('grant-account', X'AB')`,
		`INSERT INTO jira_auth_session (account_id, token_hash, expires_at) VALUES ('session-account', 's', unixepoch('2026-02-01T12:00:00Z'))`,
		`INSERT INTO meta (key, value) VALUES ('last_sync', '2026-01-05T09:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	due, err := st.PrivacyDue(t.Context(), time.Now(), 90)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"assignee-account": "2026-01-05T09:00:00Z", "comment-account": "2026-01-05T09:00:00Z",
		"transition-account": "2026-01-05T09:00:00Z", "meeting-account": "2026-01-01T07:00:00Z",
		"audit-account": "2026-01-02T07:00:00Z", "member-account": "2026-01-04T09:00:00Z",
		"session-account": "2026-01-02T12:00:00Z", "grant-account": "",
	}
	for _, account := range due {
		want, ok := expected[account.AccountID]
		if !ok || (want != "" && account.UpdatedAt.Format(time.RFC3339) != want) {
			t.Errorf("unexpected migrated inventory entry: %+v", account)
		}
		delete(expected, account.AccountID)
	}
	if len(expected) != 0 {
		t.Fatalf("missing migrated inventory entries: %v", expected)
	}
	ticket, found, err := st.IssueDetail("DCAI-1")
	if err != nil || !found || len(ticket.Comments) != 2 || ticket.Comments[0].Author.DisplayName != "Comment Name" ||
		ticket.Comments[1].ID != "comment-2" || ticket.Comments[1].Author != (jira.Person{}) {
		t.Fatalf("migrated comment authors: %+v found=%v err=%v", ticket.Comments, found, err)
	}

	old, found, err := st.IssueDetail("DCAI-2")
	if err != nil || !found || old.Assignee != "" || old.AssigneeAccountID != "" || old.Summary != "Older ticket" ||
		len(old.Comments) != 1 || old.Comments[0].Author != (jira.Person{}) {
		t.Fatalf("legacy unreportable projection: %+v found=%v err=%v", old, found, err)
	}
	wantBody := `{"type":"doc","content":[{"type":"mention","attrs":{"id":"unknown"}},{"type":"mention","attrs":{"id":"unknown"}},{"type":"mention","attrs":{"id":"all","userType":"SPECIAL","text":"Everyone"}}]}`
	if got := string(old.Comments[0].Body); got != wantBody {
		t.Fatalf("migrated legacy mentions: %s", got)
	}
	changes, err := st.IssueChangesSince(time.Time{})
	if history := changes["DCAI-2"]; err != nil || len(history) != 1 || history[0].Author != (jira.Person{}) ||
		history[0].Timestamp.Format(time.RFC3339) != "2025-12-21T10:00:00Z" {
		t.Fatalf("migrated legacy change: %+v err=%v", history, err)
	}
	members, err := st.ProjectMembers()
	if err != nil || len(members) != 1 || members[0].AccountID != "member-account" {
		t.Fatalf("migrated project members: %+v err=%v", members, err)
	}
	if _, hasWatermark, err := st.LastSync(); err != nil || hasWatermark {
		t.Fatalf("migration should schedule a historical fetch: watermark=%v err=%v", hasWatermark, err)
	}
}

func TestLiveHistoricalBackfillKeepsEventsWithoutUnreportableAuthors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board/1/sprint":
			fmt.Fprint(w, `{"isLast":true,"values":[]}`)
		case "/rest/api/3/user/assignable/search":
			fmt.Fprint(w, `[]`)
		case "/rest/api/3/search/jql":
			fmt.Fprint(w, `{"isLast":true,"issues":[{
				"key":"DCAI-4","fields":{"created":"2026-01-01T10:00:00Z",
				"creator":{"displayName":"Legacy creator"},
				"assignee":{"accountId":"UNKNOWN","displayName":"Legacy assignee","avatarUrls":{"48x48":"https://example.com/avatar"}},
				"comment":{"total":1,"comments":[{"id":"c1","author":{"displayName":"Legacy commenter"},
				"created":"2026-01-02T10:00:00Z","body":{"type":"doc","content":[]}}]}},
				"changelog":{"total":1,"histories":[{"id":"h1","author":{"displayName":"Legacy editor"},
				"created":"2026-01-03T10:00:00Z","items":[{"field":"status","fromString":"Open","toString":"Done"}]}]}
			}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	st, err := Open(filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	client := jira.NewLiveClient(jira.Config{BaseURL: server.URL, BoardID: "1", ProjectKey: "DCAI"})
	if _, err := sync.Backfill(t.Context(), client, st); err != nil {
		t.Fatal(err)
	}
	detail, found, err := st.IssueDetail("DCAI-4")
	if err != nil || !found || detail.Assignee != "" || detail.AssigneeAccountID != "" || detail.AssigneeAvatarURL != "" ||
		detail.CreatedAt.Format(time.RFC3339) != "2026-01-01T10:00:00Z" ||
		len(detail.Comments) != 1 || detail.Comments[0].Author != (jira.Person{}) ||
		detail.Comments[0].CreatedAt.Format(time.RFC3339) != "2026-01-02T10:00:00Z" {
		t.Fatalf("historical ticket: %+v found=%v err=%v", detail, found, err)
	}
	if issue := readIssue(t, st, "DCAI-4"); issue.creator != "" {
		t.Fatal("historical backfill persisted a creator without an account ID")
	}
	changes, err := st.IssueChangesSince(time.Time{})
	history := changes["DCAI-4"]
	if err != nil || len(history) != 1 || history[0].Author != (jira.Person{}) || history[0].To != "Done" ||
		history[0].Timestamp.Format(time.RFC3339) != "2026-01-03T10:00:00Z" {
		t.Fatalf("historical changes: %+v err=%v", history, err)
	}
}
