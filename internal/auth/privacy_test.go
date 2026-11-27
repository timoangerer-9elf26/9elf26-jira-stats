package auth_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/auth"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

func TestPrivacyReportingHonorsBatchLimitOwnerAndPersistentCadence(t *testing.T) {
	const owner = "owner-account"
	const fallback = "alternate-account"
	var requests [][]store.PrivacyAccount
	var bearers []string
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		var report struct{ Accounts []store.PrivacyAccount }
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&report) != nil {
			t.Error("invalid report request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests = append(requests, report.Accounts)
		bearers = append(bearers, r.Header.Get("Authorization"))
		switch len(requests) {
		case 1:
			w.Header().Set("Cycle-Period", "10")
		case 2:
			w.Header().Set("Retry-After", "7200")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stub.Close()

	now := time.Date(2026, 11, 27, 9, 0, 0, 345000000, time.UTC)
	oldest := now.Add(-10 * 24 * time.Hour)
	dbPath := filepath.Join(t.TempDir(), "persistent.db")
	st, service := openPrivacyAuth(t, dbPath, stub.URL, fallback+","+owner)
	loginPrivacyUser(t, service, fallback)
	loginPrivacyUser(t, service, owner)
	for i := range 90 {
		id := fmt.Sprintf("person-%02d", i)
		err := st.SaveIssue(jira.Issue{Key: fmt.Sprintf("DCAI-%d", i),
			AssigneeAccountID: id, CreatorAccountID: "unknown",
			Description: []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"collection","userType":"SPECIAL"}}]}]}`),
			Comments:    []jira.Comment{{ID: "comment", Author: jira.Person{AccountID: "stub"}}},
		}, oldest.Format(time.RFC3339))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompletePrioMeeting(0, oldest.Add(-time.Hour), fallback); err != nil {
		t.Fatal(err)
	}
	reporter := auth.NewPrivacyReporter(service, owner, func() time.Time { return now })
	count, retry, err := reporter.Check(t.Context())
	if err != nil || count != 90 || !retry.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("first check count=%d retry=%v err=%v", count, retry, err)
	}
	if len(requests) != 2 || len(requests[0]) != 90 || len(requests[1]) != 2 ||
		bearers[0] != "Bearer "+owner || requests[0][0].AccountID != fallback ||
		!requests[0][0].UpdatedAt.Equal(oldest.Add(-time.Hour)) {
		t.Fatalf("first request sizes/order/bearer were unexpected: %d, %v", len(requests), bearers)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, service = openPrivacyAuth(t, dbPath, stub.URL, fallback+","+owner)
	reporter = auth.NewPrivacyReporter(service, owner, func() time.Time { return now })

	now = now.Add(time.Hour)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 0 || len(requests) != 2 {
		t.Fatalf("restarted during retry: count=%d requests=%d err=%v", count, len(requests), err)
	}
	now = now.Add(time.Hour)
	if err := st.DeleteOAuthGrant(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 || len(requests) != 3 || bearers[2] != "Bearer "+fallback {
		t.Fatalf("restarted after retry: count=%d requests=%d bearers=%v err=%v", count, len(requests), bearers, err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 0 || len(requests) != 3 {
		t.Fatalf("advertised period was ignored: count=%d requests=%d err=%v", count, len(requests), err)
	}
	now = now.Add(2 * 24 * time.Hour)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 91 || len(requests) != 5 {
		t.Fatalf("elapsed cycle: count=%d requests=%d err=%v", count, len(requests), err)
	}
}

func openPrivacyAuth(t *testing.T, path, endpoint, allowlist string) (*store.Store, *auth.Service) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	service, err := auth.New(st, auth.Config{
		ClientID: "app", ClientSecret: "secret", RedirectURI: "https://app.example/auth/callback",
		CloudID: "cloud", SiteURL: "https://site.example", AllowedAccountIDs: allowlist,
		TokenKey:    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		AuthBaseURL: endpoint, APIBaseURL: endpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, service
}

func loginPrivacyUser(t *testing.T, service *auth.Service, account string) string {
	t.Helper()
	location, binding, err := service.Start(t.Context(), "/prio")
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := service.Complete(t.Context(), authorize.Query().Get("state"), binding, account, "")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func privacyProvider(report http.HandlerFunc) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/app/report-accounts/{$}", report)
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]string
		_ = json.NewDecoder(r.Body).Decode(&fields)
		id := fields["code"]
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": id, "refresh_token": "refresh-" + id, "expires_in": 3600})
	})
	mux.HandleFunc("/oauth/token/accessible-resources", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"cloud","url":"https://site.example","scopes":["read:jira-work","write:jira-work","read:jira-user"]}]`)
	})
	mux.HandleFunc("/ex/jira/cloud/rest/api/3/mypermissions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"permissions":{"BROWSE_PROJECTS":{"havePermission":true}}}`)
	})
	mux.HandleFunc("/ex/jira/cloud/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("Authorization")[len("Bearer "):]
		_ = json.NewEncoder(w).Encode(map[string]any{"accountId": id, "timeZone": "Europe/Berlin", "active": true})
	})
	return mux
}

func TestPrivacyUpdateClearsAndRefreshesProfileWithoutChangingChronology(t *testing.T) {
	const account = "updated-account"
	now := time.Now().UTC().Truncate(time.Second)
	retrieved := now.Add(-24 * time.Hour)
	reports := 0
	mux := privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		reports++
		if reports > 1 {
			w.Header().Set("Retry-After", "7200")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"accounts":[{"accountId":"updated-account","status":"updated"}]}`)
	})
	profileReads := 0
	mux.HandleFunc("/ex/jira/cloud/rest/api/3/user", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("accountId") != account || r.Header.Get("Authorization") != "Bearer "+account {
			t.Error("profile lookup did not identify the requested account and grant")
		}
		profileReads++
		if profileReads <= 2 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"accountId":"updated-account","displayName":"Current Name","avatarUrls":{"48x48":"https://example.com/current"}}`)
	})
	myselfAvailable := false
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ex/jira/cloud/rest/api/3/myself" && reports > 0 {
			if !myselfAvailable {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"accountId":"updated-account","timeZone":"UTC","active":true}`)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer stub.Close()
	st, service := openPrivacyAuth(t, filepath.Join(t.TempDir(), "update.db"), stub.URL, account)
	session := loginPrivacyUser(t, service, account)
	old := jira.Person{AccountID: account, DisplayName: "Old Name"}
	issue := jira.Issue{Key: "DCAI-25", Summary: "Review work",
		Assignee: old.DisplayName, AssigneeAccountID: account, AssigneeAvatarURL: "https://example.com/old",
		Creator: old.DisplayName, CreatorAccountID: account,
		Comments:  []jira.Comment{{ID: "comment", Author: old, CreatedAt: retrieved}},
		Changelog: []jira.ChangelogEntry{{ID: "change", Field: "status", To: "Done", Timestamp: retrieved, Author: old}},
	}
	save := func() {
		t.Helper()
		if err := st.SaveIssue(issue, retrieved.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		if err := st.ReplaceProjectMembers([]jira.ProjectMember{{AccountID: account, DisplayName: old.DisplayName, AvatarURL: issue.AssigneeAvatarURL}}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	if err := st.CompletePrioMeeting(0, retrieved, account); err != nil {
		t.Fatal(err)
	}
	reporter := auth.NewPrivacyReporter(service, account, func() time.Time { return now })
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 {
		t.Fatalf("first check count=%d err=%v", count, err)
	}

	assertProfile := func(name, avatar string) {
		t.Helper()
		detail, found, err := st.IssueDetail(issue.Key)
		if err != nil || !found || detail.Assignee != name || detail.AssigneeAvatarURL != avatar ||
			detail.AssigneeAccountID != account || len(detail.Comments) != 1 || detail.Comments[0].Author.DisplayName != name ||
			!detail.Comments[0].CreatedAt.Equal(retrieved) {
			t.Fatalf("ticket profile: %+v found=%v err=%v", detail, found, err)
		}
		changes, err := st.IssueChangesSince(retrieved)
		if err != nil || len(changes[issue.Key]) != 1 || changes[issue.Key][0].Author.DisplayName != name ||
			changes[issue.Key][0].To != "Done" || !changes[issue.Key][0].Timestamp.Equal(retrieved) {
			t.Fatalf("history: %+v err=%v", changes, err)
		}
		last, err := st.LastPrioMeeting()
		if err != nil || last.AccountID != account || !last.EndedAt.Equal(retrieved) {
			t.Fatalf("meeting: %+v err=%v", last, err)
		}
	}
	assertProfile("", "")
	save()
	assertProfile("", "")
	if members, err := st.ProjectMembers(); err != nil || len(members) != 0 {
		t.Fatalf("stale profile is still selectable: %+v err=%v", members, err)
	}

	if err := st.RecordPrioMeetingChange(store.PrioMeetingChange{IssueKey: issue.Key, Label: "Status", AccountID: "new-account", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if count, retry, err := reporter.Check(t.Context()); err != nil || count != 0 || reports != 2 || !retry.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("rate-limited report: count=%d reports=%d retry=%v err=%v", count, reports, retry, err)
	}
	now = now.Add(time.Hour)
	if count, retry, err := reporter.Check(t.Context()); err != nil || count != 0 || reports != 2 || !retry.Equal(now.Add(time.Hour)) {
		t.Fatalf("profile retry during report backoff: count=%d reports=%d retry=%v err=%v", count, reports, retry, err)
	}
	const freshAvatar = "https://example.com/current"
	assertProfile("Current Name", freshAvatar)
	save()
	assertProfile("Current Name", freshAvatar)
	members, err := st.ProjectMembers()
	if err != nil || len(members) != 1 || members[0].DisplayName != "Current Name" || members[0].AvatarURL != freshAvatar {
		t.Fatalf("refreshed members: %+v err=%v", members, err)
	}
	identity, _, err := service.Resolve(t.Context(), session)
	if err != nil || identity.AccountID != account || identity.TimeZone != nil {
		t.Fatalf("updated account lost its valid session: account matches=%v err=%v", identity.AccountID == account, err)
	}
	if zone, err := identity.JiraTimeZone(t.Context()); err == nil || zone != nil {
		t.Fatalf("unavailable Jira timezone gave zone=%v err=%v", zone, err)
	}
	myselfAvailable = true
	if zone, err := identity.JiraTimeZone(t.Context()); err != nil || zone.String() != "UTC" {
		t.Fatalf("timezone after recovery: zone=%v err=%v", zone, err)
	}
}

func TestPrivacyReportDoesNotResendAfterAnAmbiguousResponseAndRestart(t *testing.T) {
	const account = "owner-account"
	requests := 0
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			// Atlassian acknowledged the submission, but the action body is lost.
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stub.Close()
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "attempt.db")
	st, service := openPrivacyAuth(t, path, stub.URL, account)
	loginPrivacyUser(t, service, account)
	reporter := auth.NewPrivacyReporter(service, account, func() time.Time { return now })
	if count, _, err := reporter.Check(t.Context()); err == nil || count != 0 || requests != 1 {
		t.Fatalf("incomplete response: count=%d requests=%d err=%v", count, requests, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_, service = openPrivacyAuth(t, path, stub.URL, account)
	reporter = auth.NewPrivacyReporter(service, account, func() time.Time { return now })
	now = now.Add(24 * time.Hour)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 0 || requests != 1 {
		t.Fatalf("premature retry: count=%d requests=%d err=%v", count, requests, err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 || requests != 2 {
		t.Fatalf("elapsed cycle: count=%d requests=%d err=%v", count, requests, err)
	}
}

func TestPrivacyClosedDirectiveRevokesTheReportingOwnersSession(t *testing.T) {
	const account = "owner-account"
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"accounts":[{"accountId":"owner-account","status":"closed"}]}`)
	}))
	defer stub.Close()
	st, service := openPrivacyAuth(t, filepath.Join(t.TempDir(), "closed.db"), stub.URL, account)
	session := loginPrivacyUser(t, service, account)
	started := time.Now().UTC()
	if err := st.CompletePrioMeeting(0, started, account); err != nil {
		t.Fatal(err)
	}

	reporter := auth.NewPrivacyReporter(service, account, time.Now)
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 {
		t.Fatalf("closed response: count=%d err=%v", count, err)
	}
	if _, _, err := service.Resolve(t.Context(), session); !errors.Is(err, auth.ErrSignIn) {
		t.Fatalf("closed account resolved its old browser session: %v", err)
	}
	last, err := st.LastPrioMeeting()
	if err != nil || last.AccountID != "" || !last.EndedAt.Equal(started) {
		t.Fatalf("closed account left attributable or altered history: %+v err=%v", last, err)
	}
}

func TestConcurrentPrivacyReportersDoNotSendTheSameAccount(t *testing.T) {
	const account = "owner-account"
	requests := make(chan struct{}, 2)
	release := make(chan struct{})
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stub.Close()
	path := filepath.Join(t.TempDir(), "workers.db")
	_, first := openPrivacyAuth(t, path, stub.URL, account)
	loginPrivacyUser(t, first, account)
	_, second := openPrivacyAuth(t, path, stub.URL, account)

	// Pause the first worker after it reads the due inventory so the
	// second worker can claim those same accounts and start sending.
	paused, resume := make(chan struct{}), make(chan struct{})
	calls := 0
	now := time.Now()
	firstReporter := auth.NewPrivacyReporter(first, account, func() time.Time {
		calls++
		if calls == 3 {
			close(paused)
			<-resume
		}
		return now
	})
	firstDone := make(chan error, 1)
	go func() { _, _, err := firstReporter.Check(t.Context()); firstDone <- err }()
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	select {
	case <-paused:
	case err := <-firstDone:
		t.Fatalf("first worker finished before attempting to send: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("first worker did not reach the claim")
	}
	secondReporter := auth.NewPrivacyReporter(second, account, func() time.Time { return now })
	secondDone := make(chan error, 1)
	go func() { _, _, err := secondReporter.Check(t.Context()); secondDone <- err }()
	select {
	case <-requests:
	case err := <-secondDone:
		t.Fatalf("second worker finished before sending: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second worker did not send")
	}
	close(resume)
	select {
	case <-requests:
		t.Fatal("two reporters sent the same account")
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale worker did not yield")
	}
	close(release)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyReportRefreshesUnauthorizedBearerAndSkipsRefreshOnForbidden(t *testing.T) {
	const owner = "owner-account"
	const fallback = "alternate-account"
	for _, tc := range []struct {
		name        string
		status      int
		freshStatus int
		bearers     []string
		refreshes   int
	}{
		{"401 then success", http.StatusUnauthorized, http.StatusNoContent, []string{owner, "fresh-owner"}, 1},
		{"401 then 401", http.StatusUnauthorized, http.StatusUnauthorized, []string{owner, "fresh-owner", fallback}, 1},
		{"403 then fallback", http.StatusForbidden, http.StatusNoContent, []string{owner, fallback}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bearers []string
			refreshes := 0
			mux := privacyProvider(func(w http.ResponseWriter, r *http.Request) {
				bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				bearers = append(bearers, bearer)
				switch bearer {
				case owner:
					w.WriteHeader(tc.status)
				case "fresh-owner":
					w.WriteHeader(tc.freshStatus)
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			})
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					var fields map[string]string
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if fields["grant_type"] == "refresh_token" {
						refreshes++
						if fields["refresh_token"] != "refresh-"+owner {
							t.Error("unexpected refresh grant")
						}
						fmt.Fprint(w, `{"access_token":"fresh-owner","refresh_token":"next-owner","expires_in":3600}`)
						return
					}
					id := fields["code"]
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": id, "refresh_token": "refresh-" + id, "expires_in": 3600})
					return
				}
				mux.ServeHTTP(w, r)
			}))
			defer stub.Close()
			_, service := openPrivacyAuth(t, filepath.Join(t.TempDir(), "oauth.db"), stub.URL, owner+","+fallback)
			loginPrivacyUser(t, service, owner)
			loginPrivacyUser(t, service, fallback)

			reporter := auth.NewPrivacyReporter(service, owner, time.Now)
			if count, _, err := reporter.Check(t.Context()); err != nil || count != 2 {
				t.Fatalf("report result: count=%d err=%v", count, err)
			}
			if !slices.Equal(bearers, tc.bearers) || refreshes != tc.refreshes {
				t.Fatalf("unexpected report sequence and refresh count: %v, %d", bearers, refreshes)
			}
		})
	}
}

func TestKnownPrivacyClosureSurvivesFailedProjectionAndRestart(t *testing.T) {
	const account = "owner-account"
	reports := 0
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		reports++
		fmt.Fprint(w, `{"accounts":[{"accountId":"owner-account","status":"closed"}]}`)
	}))
	defer stub.Close()
	path := filepath.Join(t.TempDir(), "inbox.db")
	st, service := openPrivacyAuth(t, path, stub.URL, account)
	session := loginPrivacyUser(t, service, account)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER block_projection BEFORE DELETE ON jira_auth_grant
		BEGIN SELECT RAISE(ABORT, 'test projection failure'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reporter := auth.NewPrivacyReporter(service, account, func() time.Time { return now })
	if count, _, err := reporter.Check(t.Context()); err == nil || count != 0 || reports != 1 {
		t.Fatalf("interrupted projection: count=%d reports=%d err=%v", count, reports, err)
	}
	if count, _, err := reporter.Check(t.Context()); err == nil || count != 0 || reports != 1 {
		t.Fatalf("failed replay: count=%d reports=%d err=%v", count, reports, err)
	}
	if _, err := db.Exec("DROP TRIGGER block_projection"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_, service = openPrivacyAuth(t, path, stub.URL, account)
	reporter = auth.NewPrivacyReporter(service, account, func() time.Time { return now })
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 || reports != 1 {
		t.Fatalf("replayed closure: count=%d reports=%d err=%v", count, reports, err)
	}
	if _, _, err := service.Resolve(t.Context(), session); !errors.Is(err, auth.ErrSignIn) {
		t.Fatalf("replayed closure left a usable session: %v", err)
	}
}

func TestPrivacyRunWakesAtPersistedAccountDeadlineAfterRestart(t *testing.T) {
	const account = "owner-account"
	requests := make(chan struct{}, 2)
	stub := httptest.NewServer(privacyProvider(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer stub.Close()
	path := filepath.Join(t.TempDir(), "deadline.db")
	st, service := openPrivacyAuth(t, path, stub.URL, account)
	loginPrivacyUser(t, service, account)
	// Make the next report due shortly after a new worker starts.
	due := time.Now().Add(2 * time.Second)
	reporter := auth.NewPrivacyReporter(service, account, func() time.Time { return due.Add(-7 * 24 * time.Hour) })
	if count, _, err := reporter.Check(t.Context()); err != nil || count != 1 {
		t.Fatalf("initial check: count=%d err=%v", count, err)
	}
	<-requests
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_, service = openPrivacyAuth(t, path, stub.URL, account)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		auth.NewPrivacyReporter(service, account, time.Now).Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-requests:
		if time.Now().Before(due) {
			t.Fatal("reported before the persisted deadline")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("reporter missed the persisted deadline")
	}
}
