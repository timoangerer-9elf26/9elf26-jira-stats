package web_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	syncstd "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/auth"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

const sessionCookie = "__Host-jira_stats_session"

func newAuthApp(t *testing.T) *testApp {
	t.Helper()
	return newOAuthApp(t, nil, nil)
}

func newOAuthApp(t *testing.T, provider *httptest.Server, configure func(*auth.Config)) *testApp {
	t.Helper()
	return newOAuthAppAt(t, provider, filepath.Join(t.TempDir(), "app.db"), configure)
}

func newOAuthAppAt(t *testing.T, provider *httptest.Server, path string, configure func(*auth.Config)) *testApp {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := jira.NewFakeClient()
	if err := sync.Once(context.Background(), fake, st); err != nil {
		t.Fatal(err)
	}
	cfg := auth.Config{
		ClientID: "app-id", ClientSecret: "app-secret", CloudID: "cloud-1",
		RedirectURI: "https://app.example/auth/callback", SiteURL: "https://site.example",
		AllowedAccountIDs: "person-1", TokenKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	if provider != nil {
		cfg.AuthBaseURL, cfg.APIBaseURL = provider.URL, provider.URL
	}
	if configure != nil {
		configure(&cfg)
	}
	oauth, err := auth.New(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	syncer := sync.NewSyncer(fake, st, 0, sync.RequirePersonalWrites)
	srv, err := web.NewServer(st, web.WithOAuth(oauth), web.WithEstimator(syncer), web.WithTransitioner(syncer),
		web.WithPrioritizer(syncer), web.WithAssigner(syncer), web.WithJiraBaseURL(cfg.SiteURL))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)
	return &testApp{Server: ts, Store: st}
}

func oauthClient(app *testApp) *http.Client {
	return &http.Client{
		Transport:     app.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// noRedirectClient returns an http.Client that never follows redirects, so a
// test can inspect the 3xx Location and any Set-Cookie on the response itself.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// findCookie returns the named cookie from a response, or nil.
func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoggedOutRedirectsToLoginWithNext(t *testing.T) {
	app := newAuthApp(t)
	client := oauthClient(app)

	resp, err := client.Get(app.URL + "/sprint")
	if err != nil {
		t.Fatalf("GET /sprint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /sprint logged out: status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?next=%2Fsprint" {
		t.Fatalf("GET /sprint logged out: Location = %q, want /login?next=%%2Fsprint", loc)
	}
	if c := findCookie(resp, sessionCookie); c != nil {
		t.Fatalf("redirect should not set a session cookie, got %q", c.Value)
	}
}

func TestUnauthenticatedHTMXGetsHXRedirectNotBody(t *testing.T) {
	app := newAuthApp(t)
	client := oauthClient(app)

	// The always-on poller hits a protected route carrying the HTMX headers.
	req, _ := http.NewRequest(http.MethodGet, app.URL+"/resync/status", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://example.com/board?epic=ABC")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /resync/status (HTMX): %v", err)
	}
	defer resp.Body.Close()

	// A 200 + HX-Redirect (full-page navigation), NOT a 302 that XHR follows
	// into a login-document body swapped over the fragment.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX unauth: status %d, want 200", resp.StatusCode)
	}
	// The originating page rides along via HX-Current-URL -> ?next=.
	if got := resp.Header.Get("HX-Redirect"); got != "/login?next=%2Fboard%3Fepic%3DABC" {
		t.Fatalf("HTMX unauth: HX-Redirect = %q, want /login?next=%%2Fboard%%3Fepic%%3DABC", got)
	}
	if resp.Header.Get("Location") != "" {
		t.Fatalf("HTMX unauth: should not send a 302 Location, got %q", resp.Header.Get("Location"))
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "Sign in") {
		t.Fatalf("HTMX unauth: response body should not carry the login document")
	}
}

func TestUnauthenticatedHTMXFallsBackToBareLogin(t *testing.T) {
	app := newAuthApp(t)
	client := oauthClient(app)

	cases := map[string]string{
		"missing HX-Current-URL":     "",
		"protocol-relative (unsafe)": "http://example.com//evil.com/x",
		"off-origin path is dropped": "not a url ::::",
	}
	for name, currentURL := range cases {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, app.URL+"/resync/status", nil)
			req.Header.Set("HX-Request", "true")
			if currentURL != "" {
				req.Header.Set("HX-Current-URL", currentURL)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET /resync/status (HTMX): %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HTMX unauth: status %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("HX-Redirect"); got != "/login" {
				t.Fatalf("HTMX unauth fallback: HX-Redirect = %q, want bare /login", got)
			}
		})
	}
}

func TestUnauthenticatedNonHTMXStill302(t *testing.T) {
	app := newAuthApp(t)
	client := oauthClient(app)

	// No HX-Request header: a normal full-page navigation keeps today's 302.
	resp, err := client.Get(app.URL + "/board")
	if err != nil {
		t.Fatalf("GET /board: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("non-HTMX unauth: status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?next=%2Fboard" {
		t.Fatalf("non-HTMX unauth: Location = %q, want /login?next=%%2Fboard", loc)
	}
	if resp.Header.Get("HX-Redirect") != "" {
		t.Fatalf("non-HTMX unauth: should not send HX-Redirect, got %q", resp.Header.Get("HX-Redirect"))
	}
}

func TestAuthDisabledHTMXPassthrough(t *testing.T) {
	// Built without OAuth: the middleware is a pass-through even for HTMX
	// requests, so a protected route serves its real 200 with no HX-Redirect.
	app := newTestApp(t, jira.NewFakeClient())
	client := noRedirectClient()

	req, _ := http.NewRequest(http.MethodGet, app.URL+"/resync/status", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://example.com/board")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /resync/status (HTMX, auth disabled): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth-disabled HTMX: status %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "" {
		t.Fatalf("auth-disabled HTMX: should not send HX-Redirect, got %q", got)
	}
}

func TestStaticReachableWhileLoggedOut(t *testing.T) {
	app := newAuthApp(t)

	// The login page must be able to load the stylesheet with no session.
	css := readResponse(t, response(t)(oauthClient(app).Get(app.URL+"/static/output.css")), http.StatusOK)
	if !strings.Contains(css, "tabular-nums") {
		t.Fatalf("/static/output.css not served while logged out")
	}

	// And /login itself renders unauthenticated.
	page := readResponse(t, response(t)(oauthClient(app).Get(app.URL+"/login")), http.StatusOK)
	if !strings.Contains(page, "Sign in") {
		t.Fatalf("/login page did not render while logged out")
	}
}

func TestLogoutButtonHiddenWhenAuthDisabled(t *testing.T) {
	// Built without OAuth: there is no session to end, so the nav must not
	// offer a logout control (it would dead-end at the login pass-through).
	app := newTestApp(t, jira.NewFakeClient())

	if body := get(t, app.URL+"/sprint"); strings.Contains(body, `data-testid="logout-button"`) {
		t.Fatalf("auth-disabled /sprint rendered a logout button")
	}
}

func TestAuthDisabledBypass(t *testing.T) {
	// Built without OAuth: this is the AUTH_DISABLED / local-dev case, where
	// the middleware is a pass-through and every route is reachable directly.
	app := newTestApp(t, jira.NewFakeClient())

	resp, err := http.Get(app.URL + "/sprint")
	if err != nil {
		t.Fatalf("GET /sprint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth-disabled GET /sprint: status %d, want 200 (no login required)", resp.StatusCode)
	}
}

func response(t *testing.T) func(*http.Response, error) *http.Response {
	t.Helper()
	return func(resp *http.Response, err error) *http.Response {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
}

func readResponse(t *testing.T, resp *http.Response, want int) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("status %d, want %d: %s (%v)", resp.StatusCode, want, body, err)
	}
	return string(body)
}

func startOAuth(t *testing.T, app *testApp, next string) (*url.URL, *http.Cookie) {
	t.Helper()
	resp := response(t)(oauthClient(app).PostForm(app.URL+"/auth/start", url.Values{"next": {next}}))
	readResponse(t, resp, http.StatusFound)
	dest, err := resp.Location()
	if err != nil || len(resp.Cookies()) != 1 {
		t.Fatalf("expected authorization URL and flow cookie: %v, %v", err, resp.Cookies())
	}
	return dest, resp.Cookies()[0]
}

func callback(t *testing.T, app *testApp, state string, flow *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, app.URL+"/auth/callback?"+url.Values{"code": {"auth-code"}, "state": {state}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if flow != nil {
		req.AddCookie(flow)
	}
	return response(t)(oauthClient(app).Do(req))
}

func TestOAuthReturnsToRequestedPageWithOneTimeBrowserBoundState(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600}`)
		case "/oauth/token/accessible-resources":
			io.WriteString(w, `[{"id":"cloud-1","url":"https://site.example","scopes":["read:jira-work","write:jira-work","read:jira-user"]}]`)
		case myPermissionsPath:
			writeProjectPermission(t, w, r, validProjectPermission)
		case "/ex/jira/cloud-1/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"person-1","timeZone":"America/New_York","active":true}`)
		default:
			t.Errorf("unexpected OAuth provider call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()
	app := newOAuthApp(t, provider, nil)
	dest, flow := startOAuth(t, app, "/board?epic=ABC")
	params := dest.Query()
	if dest.Path != "/authorize" || params.Get("state") == "" || params.Get("client_id") != "app-id" ||
		params.Get("response_type") != "code" || params.Get("redirect_uri") != "https://app.example/auth/callback" ||
		params.Get("audience") != "api.atlassian.com" || params.Get("prompt") != "consent" ||
		params.Get("scope") != "read:jira-work write:jira-work read:jira-user offline_access" {
		t.Fatalf("unexpected authorization request: %s", dest)
	}
	state := params.Get("state")
	readResponse(t, callback(t, app, state, nil), http.StatusUnauthorized)
	resp := callback(t, app, state, flow)
	readResponse(t, resp, http.StatusFound)
	session := findCookie(resp, sessionCookie)
	if resp.Header.Get("Location") != "/board?epic=ABC" || session == nil || !session.Secure ||
		!session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.MaxAge <= 0 {
		t.Fatalf("unexpected login redirect or cookie attributes: %v", resp.Header)
	}
	readResponse(t, callback(t, app, state, flow), http.StatusUnauthorized)
	req, _ := http.NewRequest(http.MethodGet, app.URL+"/board", nil)
	req.AddCookie(session)
	body := readResponse(t, response(t)(oauthClient(app).Do(req)), http.StatusOK)
	if !strings.Contains(body, `data-testid="logout-button"`) {
		t.Fatal("expected logout control")
	}
	req, _ = http.NewRequest(http.MethodPost, app.URL+"/logout", nil)
	req.AddCookie(session)
	readResponse(t, response(t)(oauthClient(app).Do(req)), http.StatusFound)
	req, _ = http.NewRequest(http.MethodGet, app.URL+"/board", nil)
	req.AddCookie(session)
	readResponse(t, response(t)(oauthClient(app).Do(req)), http.StatusFound)
}

const validResources = `[{"id":"cloud-1","url":"https://site.example","scopes":["read:jira-work","write:jira-work","read:jira-user"]}]`
const validProjectPermission = `{"permissions":{"BROWSE_PROJECTS":{"havePermission":true}}}`
const deniedProjectPermission = `{"permissions":{"BROWSE_PROJECTS":{"havePermission":false}}}`
const myPermissionsPath = "/ex/jira/cloud-1/rest/api/3/mypermissions"

func writeProjectPermission(t *testing.T, w http.ResponseWriter, r *http.Request, payload string) {
	t.Helper()
	if r.Method != http.MethodGet || r.URL.Query().Get("projectKey") != "DCAI" ||
		r.URL.Query().Get("permissions") != "BROWSE_PROJECTS" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		t.Errorf("unexpected project permission request: %s %s", r.Method, r.URL)
	}
	io.WriteString(w, payload)
}

const validMyself = `{"accountId":"person-1","timeZone":"America/New_York","active":true}`

func signIn(t *testing.T, app *testApp) *http.Cookie {
	t.Helper()
	dest, flow := startOAuth(t, app, "/board")
	resp := callback(t, app, dest.Query().Get("state"), flow)
	readResponse(t, resp, http.StatusFound)
	session := findCookie(resp, sessionCookie)
	if session == nil {
		t.Fatal("login did not set a session")
	}
	return session
}

func TestInteractiveJiraWritesUseTheSignedInPersonsGrant(t *testing.T) {
	cases := []struct {
		name, route, payload, method, suffix string
		form                                 url.Values
	}{
		{"estimate", "/board/estimate", `{"fields":{"customfield_10040":{"value":"Small"}}}`, "PUT", "",
			url.Values{"size": {"S"}, "prior": {"M"}}},
		{"priority", "/prio/priority", `{"fields":{"priority":{"name":"High"}}}`, "PUT", "",
			url.Values{"priority": {"High"}, "status": {"all"}, "non-technical": {"0"}, "no-parent": {"0"}}},
		{"assignee", "/board/assignee", `{"accountId":"acct-ada"}`, "PUT", "/assignee",
			url.Values{"account": {"acct-ada"}}},
		{"status", "/board/move", `{"transition":{"id":"move-1"}}`, "POST", "/transitions",
			url.Values{"status": {"Ready To Do"}}},
	}
	for _, tc := range cases {
		for _, scope := range []string{"DCAI", "moved", "foreign", "moved after write"} {
			if scope == "foreign" && tc.name == "status" {
				continue // Board already rejects tickets absent from its active sprint.
			}
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				key := "DCAI-2"
				if scope == "foreign" {
					key = "OTHER-2"
				}
				issuePath := "/ex/jira/cloud-1/rest/api/3/issue/" + key
				var writeSeen, readSeen atomic.Bool
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/oauth/token" {
						var fields map[string]string
						if err := json.NewDecoder(r.Body).Decode(&fields); err != nil ||
							fields["grant_type"] != "authorization_code" || fields["code"] != "auth-code" ||
							fields["client_secret"] != "app-secret" || fields["client_id"] != "app-id" ||
							fields["redirect_uri"] != "https://app.example/auth/callback" {
							t.Errorf("unexpected authorization code exchange: %v (%v)", fields, err)
						}
						io.WriteString(w, `{"access_token":"personal-access","refresh_token":"refresh-1","expires_in":3600}`)
						return
					}
					if got := r.Header.Get("Authorization"); got != "Bearer personal-access" {
						t.Errorf("%s %s Authorization = %q", r.Method, r.URL.Path, got)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					switch {
					case r.URL.Path == "/oauth/token/accessible-resources":
						io.WriteString(w, validResources)
					case r.URL.Path == myPermissionsPath:
						writeProjectPermission(t, w, r, validProjectPermission)
					case r.URL.Path == "/ex/jira/cloud-1/rest/api/3/myself":
						io.WriteString(w, validMyself)
					case r.Method == tc.method && r.URL.Path == issuePath+tc.suffix:
						body, _ := io.ReadAll(r.Body)
						if string(body) != tc.payload {
							t.Errorf("Jira payload = %s, want %s", body, tc.payload)
						}
						writeSeen.Store(true)
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodGet && r.URL.Path == issuePath+"/transitions":
						fmt.Fprintf(w, `{"transitions":[{"id":"move-1","to":{"id":%q}}]}`, jira.StatusIDReadyToDo)
					case r.Method == http.MethodGet && r.URL.Path == issuePath:
						readSeen.Store(true)
						canonical, project := key, "DCAI"
						if scope == "moved" || scope == "foreign" || (scope == "moved after write" && writeSeen.Load()) {
							canonical, project = "OTHER-2", "OTHER"
						}
						fmt.Fprintf(w, `{"key":%q,"fields":{"project":{"key":%q},"summary":"Personal edit reconciled","issuetype":{"name":"Task"},"status":{"name":"Ready To Do","statusCategory":{"name":"To Do"}},"priority":{"name":"High"},"assignee":{"displayName":"Ada"},"customfield_10040":{"value":"Small"},"customfield_10020":[{"id":29,"name":"KW29","state":"active"}]}}`, canonical, project)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer provider.Close()

				app := newOAuthApp(t, provider, nil)
				session := signIn(t, app)
				form := tc.form
				form.Set("key", key)
				req, _ := http.NewRequest(http.MethodPost, app.URL+tc.route, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(session)
				resp := response(t)(oauthClient(app).Do(req))
				defer resp.Body.Close()
				if _, err := io.Copy(io.Discard, resp.Body); err != nil {
					t.Fatal(err)
				}
				wantWrite := scope == "DCAI" || scope == "moved after write"
				if writeSeen.Load() != wantWrite {
					t.Fatalf("personal Jira write = %v, want %v", writeSeen.Load(), wantWrite)
				}
				if scope == "DCAI" && (!readSeen.Load() || resp.StatusCode != http.StatusOK) {
					t.Fatalf("expected a successful personal Jira write and re-read: read=%v status=%d", readSeen.Load(), resp.StatusCode)
				}
				if scope == "moved after write" && tc.name == "status" {
					if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Board-Move") != "applied" {
						t.Fatalf("expected pending projection after successful move, got %d (%v)", resp.StatusCode, resp.Header)
					}
				}
				if _, found, err := app.Store.IssueDetail("OTHER-2"); found || err != nil {
					t.Fatalf("foreign Jira issue entered the DCAI projection: found=%v err=%v", found, err)
				}
			})
		}
	}
}

func TestOAuthRejectsInvalidSiteScopesOrAccount(t *testing.T) {
	for _, tc := range []struct{ name, resources, myself string }{
		{"wrong Cloud ID", strings.Replace(validResources, "cloud-1", "cloud-2", 1), validMyself},
		{"wrong site URL", strings.Replace(validResources, "site.example", "wrong.example", 1), validMyself},
		{"missing write scope", strings.Replace(validResources, `"write:jira-work",`, "", 1), validMyself},
		{"person outside allowlist", validResources, strings.Replace(validMyself, "person-1", "person-2", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/token":
					io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600}`)
				case "/oauth/token/accessible-resources":
					io.WriteString(w, tc.resources)
				case myPermissionsPath:
					writeProjectPermission(t, w, r, validProjectPermission)
				case "/ex/jira/cloud-1/rest/api/3/myself":
					io.WriteString(w, tc.myself)
				default:
					t.Errorf("unexpected OAuth request: %s", r.URL.Path)
				}
			}))
			defer provider.Close()

			app := newOAuthApp(t, provider, nil)
			target, flow := startOAuth(t, app, "/board")
			resp := callback(t, app, target.Query().Get("state"), flow)
			if body := readResponse(t, resp, http.StatusUnauthorized); !strings.Contains(body, `data-testid="login-error"`) {
				t.Fatal("expected the login page to explain the denial")
			}
			if findCookie(resp, sessionCookie) != nil {
				t.Fatal("a rejected grant must never create a session")
			}
		})
	}
}

func TestSessionSurvivesRestartAndResyncWithSerializedRefresh(t *testing.T) {
	var refreshes atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			var fields map[string]string
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Errorf("decode token request: %v", err)
			}
			if fields["grant_type"] == "authorization_code" {
				io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":1}`)
				return
			}
			if fields["grant_type"] != "refresh_token" || fields["refresh_token"] != "refresh-1" {
				t.Errorf("unexpected refresh parameters %v", fields)
			}
			refreshes.Add(1)
			io.WriteString(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`)
		case "/oauth/token/accessible-resources":
			io.WriteString(w, validResources)
		case myPermissionsPath:
			writeProjectPermission(t, w, r, validProjectPermission)
		case "/ex/jira/cloud-1/rest/api/3/myself":
			io.WriteString(w, validMyself)
		default:
			t.Errorf("unexpected OAuth request: %s", r.URL.Path)
		}
	}))
	defer provider.Close()

	path := filepath.Join(t.TempDir(), "app.db")
	app := newOAuthAppAt(t, provider, path, nil)
	session := signIn(t, app)
	app.Close()
	if err := app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newOAuthAppAt(t, provider, path, nil)
	// A full resync clears and backfills the projection through these same store
	// APIs. Use the real Syncer to exercise the production reset path.
	syncer := sync.NewSyncer(jira.NewFakeClient(), reopened.Store, time.Minute)
	if !syncer.TriggerResync(context.Background()) {
		t.Fatal("resync did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for syncer.Resyncing() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if syncer.Resyncing() {
		t.Fatal("resync did not finish")
	}

	var requests syncstd.WaitGroup
	for range 6 {
		requests.Go(func() {
			req, _ := http.NewRequest(http.MethodGet, reopened.URL+"/board", nil)
			req.AddCookie(session)
			resp, err := oauthClient(reopened).Do(req)
			if err != nil {
				t.Errorf("authenticated board: %v", err)
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "</html>") {
				t.Errorf("authenticated board after restart and resync: status %d, body end %q (%v)",
					resp.StatusCode, body[max(0, len(body)-320):], err)
			}
		})
	}
	requests.Wait()
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("concurrent requests refreshed %d times, want 1", got)
	}

	reopened.Close()
	if err := reopened.Store.Close(); err != nil {
		t.Fatal(err)
	}
	rotated := newOAuthAppAt(t, provider, path, nil)
	req, _ := http.NewRequest(http.MethodGet, rotated.URL+"/board", nil)
	req.AddCookie(session)
	readResponse(t, response(t)(oauthClient(rotated).Do(req)), http.StatusOK)
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("reopened DB triggered another refresh: %d exchanges", got)
	}
	rotated.Close()
	if err := rotated.Store.Close(); err != nil {
		t.Fatal(err)
	}

	// Removing the account from configuration takes effect for that same session.
	removed := newOAuthAppAt(t, provider, path, func(cfg *auth.Config) { cfg.AllowedAccountIDs = "person-2" })
	req, _ = http.NewRequest(http.MethodGet, removed.URL+"/board", nil)
	req.AddCookie(session)
	readResponse(t, response(t)(oauthClient(removed).Do(req)), http.StatusFound)
}

func TestOAuthRefreshRequiresReauthorizationWhenGrantOrAccessIsLost(t *testing.T) {
	for _, tc := range []string{"invalid grant", "site removed", "project removed"} {
		t.Run(tc, func(t *testing.T) {
			var rotatedSiteCheck, rotatedProjectCheck atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/token":
					var fields map[string]string
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Errorf("decode token request: %v", err)
					}
					switch fields["grant_type"] {
					case "authorization_code":
						io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":1}`)
					case "refresh_token":
						if tc == "invalid grant" {
							w.WriteHeader(http.StatusForbidden)
							io.WriteString(w, `{"error":"invalid_grant"}`)
							return
						}
						io.WriteString(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`)
					}
				case "/oauth/token/accessible-resources":
					if r.Header.Get("Authorization") == "Bearer rotated-access" {
						rotatedSiteCheck.Store(true)
						if tc == "site removed" {
							io.WriteString(w, "[]")
							return
						}
					}
					io.WriteString(w, validResources)
				case myPermissionsPath:
					if r.Header.Get("Authorization") == "Bearer rotated-access" {
						rotatedProjectCheck.Store(true)
						writeProjectPermission(t, w, r, deniedProjectPermission)
						return
					}
					writeProjectPermission(t, w, r, validProjectPermission)
				case "/ex/jira/cloud-1/rest/api/3/myself":
					io.WriteString(w, validMyself)
				default:
					t.Errorf("unexpected OAuth request: %s", r.URL.Path)
				}
			}))
			defer provider.Close()
			app := newOAuthApp(t, provider, nil)
			session := signIn(t, app)
			req, _ := http.NewRequest(http.MethodGet, app.URL+"/board/results", nil)
			req.Header.Set("HX-Request", "true")
			req.Header.Set("HX-Current-URL", app.URL+"/board?q=search")
			req.AddCookie(session)
			resp := response(t)(oauthClient(app).Do(req))
			readResponse(t, resp, http.StatusOK)
			want := "/login?next=%2Fboard%3Fq%3Dsearch"
			if tc == "project removed" {
				want += "&reason=project-access"
			}
			if loc := resp.Header.Get("HX-Redirect"); loc != want {
				t.Fatalf("expected a full sign-in redirect, got %q", loc)
			}
			if tc == "project removed" && (!rotatedSiteCheck.Load() || !rotatedProjectCheck.Load()) {
				t.Fatal("refresh did not verify site and project access with the new token")
			}
			if tc == "site removed" && !rotatedSiteCheck.Load() {
				t.Fatal("refresh did not verify site access with the new token")
			}
		})
	}
}

func TestOAuthRejectsAllowlistedAccountWithoutProjectAccess(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":3600}`)
		case "/oauth/token/accessible-resources":
			io.WriteString(w, validResources)
		case "/ex/jira/cloud-1/rest/api/3/myself":
			io.WriteString(w, validMyself)
		case myPermissionsPath:
			writeProjectPermission(t, w, r, deniedProjectPermission)
		default:
			t.Errorf("unexpected OAuth request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	app := newOAuthApp(t, provider, nil)
	dest, flow := startOAuth(t, app, "/prio")
	resp := callback(t, app, dest.Query().Get("state"), flow)
	body := readResponse(t, resp, http.StatusForbidden)
	if !strings.Contains(body, "does not have permission to browse") || findCookie(resp, sessionCookie) != nil {
		t.Fatal("denied Jira account must see an access error and receive no session")
	}
}
