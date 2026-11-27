package store

import (
	"database/sql"
	"errors"
	"io/fs"
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
