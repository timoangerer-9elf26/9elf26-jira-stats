package web_test

// Integration tests for the shared-team login (#122), driven through the HTTP
// seam: a real server built WithAuth, exercised with real requests. They cover
// the ACs — logged-out redirect, successful login (cookie attributes + access +
// next), wrong password rejected with no cookie, logout revokes, /static
// reachable logged out, and the AUTH_DISABLED (no-WithAuth) bypass.

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

const (
	testAuthEmail    = "team@example.com"
	testAuthPassword = "s3cret-pw"
	sessionCookie    = "sofia_session"
)

// newAuthApp builds a test app gated behind the shared login.
func newAuthApp(t *testing.T) *testApp {
	t.Helper()
	return newTestApp(t, jira.NewFakeClient(), web.WithAuth(testAuthEmail, testAuthPassword))
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
	client := noRedirectClient()

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
	client := noRedirectClient()

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
	client := noRedirectClient()

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
	client := noRedirectClient()

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
	// Built WITHOUT WithAuth: the middleware is a pass-through even for HTMX
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

func TestSuccessfulLoginSetsCookieAndRedirects(t *testing.T) {
	app := newAuthApp(t)
	client := noRedirectClient()

	form := url.Values{"email": {testAuthEmail}, "password": {testAuthPassword}, "next": {"/velocity"}}
	resp, err := client.PostForm(app.URL+"/login", form)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("POST /login: status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/velocity" {
		t.Fatalf("POST /login: Location = %q, want /velocity (the next target)", loc)
	}
	c := findCookie(resp, sessionCookie)
	if c == nil {
		t.Fatalf("POST /login: no session cookie set")
	}
	if c.Value == "" {
		t.Fatalf("POST /login: session cookie is empty")
	}
	if !c.HttpOnly {
		t.Errorf("session cookie should be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Secure {
		t.Errorf("session cookie should not be Secure over plain HTTP")
	}
	if c.MaxAge <= 0 {
		t.Errorf("session cookie Max-Age = %d, want a positive lifetime", c.MaxAge)
	}

	// The minted session grants access to a protected route.
	req, _ := http.NewRequest(http.MethodGet, app.URL+"/velocity", nil)
	req.AddCookie(c)
	got, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /velocity with session: %v", err)
	}
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("GET /velocity with session: status %d, want 200", got.StatusCode)
	}
}

func TestLoginSetsSecureCookieOverForwardedHTTPS(t *testing.T) {
	app := newAuthApp(t)
	client := noRedirectClient()

	form := url.Values{"email": {testAuthEmail}, "password": {testAuthPassword}}
	req, _ := http.NewRequest(http.MethodPost, app.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()

	c := findCookie(resp, sessionCookie)
	if c == nil {
		t.Fatalf("POST /login: no session cookie set")
	}
	if !c.Secure {
		t.Errorf("session cookie should be Secure when X-Forwarded-Proto=https")
	}
	// Default next when none supplied.
	if loc := resp.Header.Get("Location"); loc != "/sprint" {
		t.Fatalf("POST /login: Location = %q, want /sprint default", loc)
	}
}

func TestWrongPasswordRejectedNoCookie(t *testing.T) {
	app := newAuthApp(t)
	client := noRedirectClient()

	form := url.Values{"email": {testAuthEmail}, "password": {"wrong"}}
	resp, err := client.PostForm(app.URL+"/login", form)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", resp.StatusCode)
	}
	if c := findCookie(resp, sessionCookie); c != nil {
		t.Fatalf("wrong password should set no session cookie, got %q", c.Value)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "login-error") {
		t.Fatalf("wrong password should re-render the login page with an error")
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	app := newAuthApp(t)

	// A cookie jar keeps the session across requests, like a browser.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	form := url.Values{"email": {testAuthEmail}, "password": {testAuthPassword}}
	if _, err := client.PostForm(app.URL+"/login", form); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Grab the live session cookie for a post-logout replay check.
	base, _ := url.Parse(app.URL)
	var session *http.Cookie
	for _, c := range jar.Cookies(base) {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("expected a session cookie after login")
	}

	// Access works while logged in.
	if resp, err := client.Get(app.URL + "/sprint"); err != nil {
		t.Fatalf("GET /sprint: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("logged in GET /sprint: status %d, want 200", resp.StatusCode)
		}
	}

	// Logout redirects to /login and clears the cookie in the browser.
	resp, err := client.Get(app.URL + "/logout")
	if err != nil {
		t.Fatalf("GET /logout: %v", err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("logout: Location = %q, want /login", loc)
	}

	// Replaying the old session token must no longer grant access — it was
	// revoked server-side, not merely cleared in the browser.
	req, _ := http.NewRequest(http.MethodGet, app.URL+"/sprint", nil)
	req.AddCookie(session)
	replay, err := client.Do(req)
	if err != nil {
		t.Fatalf("replay GET /sprint: %v", err)
	}
	replay.Body.Close()
	if replay.StatusCode != http.StatusFound {
		t.Fatalf("revoked session GET /sprint: status %d, want 302 redirect", replay.StatusCode)
	}
	if loc := replay.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("revoked session GET /sprint: Location = %q, want a /login redirect", loc)
	}
}

func TestStaticReachableWhileLoggedOut(t *testing.T) {
	app := newAuthApp(t)

	// The login page must be able to load the stylesheet with no session.
	css := get(t, app.URL+"/static/output.css")
	if !strings.Contains(css, "tabular-nums") {
		t.Fatalf("/static/output.css not served while logged out")
	}

	// And /login itself renders unauthenticated.
	page := get(t, app.URL+"/login")
	if !strings.Contains(page, "Sign in") {
		t.Fatalf("/login page did not render while logged out")
	}
}

func TestLogoutButtonShownWhenAuthEnabled(t *testing.T) {
	app := newAuthApp(t)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	form := url.Values{"email": {testAuthEmail}, "password": {testAuthPassword}}
	if _, err := client.PostForm(app.URL+"/login", form); err != nil {
		t.Fatalf("login: %v", err)
	}

	resp, err := client.Get(app.URL + "/sprint")
	if err != nil {
		t.Fatalf("GET /sprint: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `data-testid="logout-button"`) {
		t.Fatalf("logged-in /sprint did not render the logout button")
	}
	if !strings.Contains(string(body), `action="/logout"`) {
		t.Fatalf("logout control did not post to /logout")
	}
}

func TestLogoutButtonHiddenWhenAuthDisabled(t *testing.T) {
	// Built WITHOUT WithAuth: there is no session to end, so the nav must not
	// offer a logout control (it would dead-end at the login pass-through).
	app := newTestApp(t, jira.NewFakeClient())

	if body := get(t, app.URL+"/sprint"); strings.Contains(body, `data-testid="logout-button"`) {
		t.Fatalf("auth-disabled /sprint rendered a logout button")
	}
}

func TestPostLogoutRevokesSession(t *testing.T) {
	app := newAuthApp(t)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	form := url.Values{"email": {testAuthEmail}, "password": {testAuthPassword}}
	if _, err := client.PostForm(app.URL+"/login", form); err != nil {
		t.Fatalf("login: %v", err)
	}

	base, _ := url.Parse(app.URL)
	var session *http.Cookie
	for _, c := range jar.Cookies(base) {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("expected a session cookie after login")
	}

	// The nav button POSTs to /logout; it must redirect to /login and revoke.
	resp, err := client.PostForm(app.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("POST /logout: Location = %q, want /login", loc)
	}

	req, _ := http.NewRequest(http.MethodGet, app.URL+"/sprint", nil)
	req.AddCookie(session)
	replay, err := client.Do(req)
	if err != nil {
		t.Fatalf("replay GET /sprint: %v", err)
	}
	replay.Body.Close()
	if replay.StatusCode != http.StatusFound {
		t.Fatalf("revoked session GET /sprint: status %d, want 302 redirect", replay.StatusCode)
	}
}

func TestAuthDisabledBypass(t *testing.T) {
	// Built WITHOUT WithAuth: this is the AUTH_DISABLED / local-dev case, where
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
