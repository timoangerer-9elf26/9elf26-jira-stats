package web

// Shared-team login (#122): the app is gated behind one shared credential read
// from the environment. Authentication is a login form plus opaque, in-memory
// session tokens — NOT HTTP Basic Auth and NOT signed cookies. On a successful
// login the server mints a crypto/rand token, keeps it in an in-memory set on
// the Server, and sets it as the cookie value; the middleware admits a request
// only when its cookie names a token in that set. A restart clears the set, so
// everyone re-logs-in (accepted for a low-traffic internal tool). No
// rate-limiting, no per-user accounts, no SESSION_SECRET — see the issue.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sessionCookieName is the cookie the session token rides in.
const sessionCookieName = "sofia_session"

// sessionMaxAge is how long the session cookie lives in the browser (30 days).
// The server-side set is cleared on restart regardless.
const sessionMaxAge = 30 * 24 * time.Hour

// defaultNext is where a bare login (no ?next=) lands after success.
const defaultNext = "/sprint"

// authConfig holds the shared credential and the in-memory session set. A nil
// *authConfig on the Server means auth is disabled (the middleware is a
// pass-through). All access to the session set goes through the mutex.
type authConfig struct {
	email    string
	password string

	mu       sync.Mutex
	sessions map[string]struct{}
}

// newAuthConfig builds an enabled auth config for the given shared credential.
func newAuthConfig(email, password string) *authConfig {
	return &authConfig{
		email:    email,
		password: password,
		sessions: make(map[string]struct{}),
	}
}

// credentialsMatch reports whether the submitted email+password equal the
// configured shared credential. Both fields are compared with
// crypto/subtle.ConstantTimeCompare and BOTH comparisons always run (no
// early-out), so a mismatch in one field can't leak timing about the other.
func (a *authConfig) credentialsMatch(email, password string) bool {
	emailOK := subtle.ConstantTimeCompare([]byte(email), []byte(a.email)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	return emailOK && passOK
}

// mintSession creates a new random session token, stores it in the set, and
// returns it.
func (a *authConfig) mintSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	a.mu.Lock()
	a.sessions[token] = struct{}{}
	a.mu.Unlock()
	return token, nil
}

// validSession reports whether the token names a live session.
func (a *authConfig) validSession(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	_, ok := a.sessions[token]
	a.mu.Unlock()
	return ok
}

// revokeSession removes the token from the set (a no-op if absent).
func (a *authConfig) revokeSession(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// authMiddleware wraps the mux, gating every route except the public ones
// (/login, /logout, /static/). It is a pass-through when auth is disabled
// (s.auth == nil). An unauthenticated request to a protected route is
// redirected to /login?next=<original path+query>.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil || isPublicPath(r.URL.Path) || s.requestAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		dest := "/login?next=" + url.QueryEscape(r.URL.RequestURI())
		http.Redirect(w, r, dest, http.StatusFound)
	})
}

// isPublicPath reports whether a path is reachable without a session: the login
// and logout endpoints, and the static assets that the login page itself needs.
func isPublicPath(path string) bool {
	return path == "/login" || path == "/logout" || strings.HasPrefix(path, "/static/")
}

// requestAuthenticated reports whether the request carries a valid session
// cookie.
func (s *Server) requestAuthenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return s.auth.validSession(c.Value)
}

// handleLogin serves the login form (GET) and processes a submission (POST).
// GET renders the styled page carrying the sanitized ?next= target. POST, on a
// credential match, mints a session, sets the cookie, and redirects to next;
// on a mismatch it re-renders the form with an error and HTTP 401, setting NO
// cookie. When auth is disabled the endpoint just bounces to the app.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}

	if r.Method == http.MethodGet {
		s.renderLogin(w, r, safeNext(r.URL.Query().Get("next")), "", http.StatusOK)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	email := r.PostForm.Get("email")
	password := r.PostForm.Get("password")

	if !s.auth.credentialsMatch(email, password) {
		s.renderLogin(w, r, next, "Incorrect email or password.", http.StatusUnauthorized)
		return
	}

	token, err := s.auth.mintSession()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.sessionCookie(r, token, int(sessionMaxAge.Seconds())))
	http.Redirect(w, r, next, http.StatusFound)
}

// handleLogout revokes the session named by the cookie, clears the cookie, and
// redirects to the login page.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.auth.revokeSession(c.Value)
	}
	http.SetCookie(w, s.sessionCookie(r, "", -1))
	http.Redirect(w, r, "/login", http.StatusFound)
}

// sessionCookie builds the session cookie. maxAge < 0 clears it. Secure is set
// automatically when the request arrived over HTTPS (direct TLS or via a proxy
// that set X-Forwarded-Proto: https).
func (s *Server) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   maxAge,
	}
}

// requestIsHTTPS reports whether the request reached the server over HTTPS,
// either directly (r.TLS) or through a TLS-terminating proxy that forwarded
// X-Forwarded-Proto: https.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// renderLogin renders the login page with the given next target and optional
// error message at the given status code.
func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, next, errMsg string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	view := struct {
		Next  string
		Error string
	}{Next: next, Error: errMsg}
	if err := s.templates.ExecuteTemplate(w, "login.html", view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// safeNext sanitizes a post-login redirect target to an app-local path, so the
// login flow can't be turned into an open redirect. Anything that isn't a
// single-slash-rooted local path falls back to the default landing page.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return defaultNext
	}
	return next
}
