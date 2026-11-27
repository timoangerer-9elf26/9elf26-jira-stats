package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/auth"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

const (
	sessionCookieName = "__Host-jira_stats_session"
	flowCookieName    = "__Host-jira_stats_oauth"
	defaultNext       = "/sprint"
)

type jiraIdentityKey struct{}

var crossOriginProtection = http.NewCrossOriginProtection()

// JiraIdentityFromContext exposes the request's Jira AccountID and TimeZone.
// Fake review mode supplies a stable identity with the configured display zone.
func JiraIdentityFromContext(ctx context.Context) (auth.Identity, bool) {
	identity, ok := ctx.Value(jiraIdentityKey{}).(auth.Identity)
	return identity, ok
}

// PersonalJiraFromContext returns the signed-in user's Jira client. In fake
// review mode it returns the app's fake client.
func PersonalJiraFromContext(ctx context.Context) (jira.Client, bool) {
	return jira.PersonalClientFromContext(ctx)
}

const projectAccessMessage = "This Atlassian account does not have permission to browse the Jira project."

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	protected := crossOriginProtection.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			protected.ServeHTTP(w, r)
			return
		}
		if s.auth == nil {
			if s.fakeJira != nil {
				identity := auth.Identity{AccountID: "jira-stats-fake", TimeZone: s.loc}
				ctx := context.WithValue(r.Context(), jiraIdentityKey{}, identity)
				r = r.WithContext(jira.WithPersonalClient(ctx, s.fakeJira))
			}
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		identity, client, err := s.auth.Resolve(r.Context(), cookieValue(r, sessionCookieName))
		switch {
		case errors.Is(err, auth.ErrProjectAccess):
			if r.Header.Get("HX-Request") == "true" {
				query := url.Values{"next": {safeNext(safeCurrentPath(r.Header.Get("HX-Current-URL")))}, "reason": {"project-access"}}
				w.Header().Set("HX-Redirect", "/login?"+query.Encode())
				w.WriteHeader(http.StatusOK)
			} else {
				s.renderLogin(w, safeNext(r.URL.RequestURI()), projectAccessMessage, http.StatusForbidden)
			}
		case errors.Is(err, auth.ErrSignIn):
			s.redirectToLogin(w, r)
		case err != nil:
			log.Printf("web: checking Jira session: %v", err)
			http.Error(w, "Jira sign-in is temporarily unavailable. Try again shortly.", http.StatusServiceUnavailable)
		default:
			if s.fakeJira != nil {
				client = s.fakeJira
			}
			ctx := context.WithValue(r.Context(), jiraIdentityKey{}, identity)
			protected.ServeHTTP(w, r.WithContext(jira.WithPersonalClient(ctx, client)))
		}
	})
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", htmxLoginRedirect(r.Header.Get("HX-Current-URL")))
		w.WriteHeader(http.StatusOK)
		return
	}
	next := defaultNext
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		next = safeNext(r.URL.RequestURI())
	} else if page := safeCurrentPath(r.Header.Get("HX-Current-URL")); page != "" {
		next = page
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
}

func htmxLoginRedirect(currentURL string) string {
	if next := safeCurrentPath(currentURL); next != "" {
		return "/login?next=" + url.QueryEscape(next)
	}
	return "/login"
}

func safeCurrentPath(currentURL string) string {
	u, err := url.Parse(currentURL)
	if err == nil {
		if next := u.RequestURI(); u.Path != "" && safeNext(next) == next {
			return next
		}
	}
	return ""
}

func isPublicPath(path string) bool {
	switch path {
	case "/login", "/auth/start", auth.CallbackPath, "/logout", "/version":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	if r.URL.Query().Get("reason") == "project-access" {
		s.renderLogin(w, next, projectAccessMessage, http.StatusForbidden)
		return
	}
	s.renderLogin(w, next, "", http.StatusOK)
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.PostFormValue("next"))
	target, browser, err := s.auth.Start(r.Context(), next)
	if err != nil {
		log.Printf("web: starting Atlassian sign-in: %v", err)
		s.renderLogin(w, next, "Couldn’t start Atlassian sign-in. Try again.", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, authCookie(flowCookieName, browser, auth.FlowLifetime))
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	session, next, err := s.auth.Complete(r.Context(), query.Get("state"), cookieValue(r, flowCookieName),
		query.Get("code"), cookieValue(r, sessionCookieName))
	if next != "" {
		http.SetCookie(w, authCookie(flowCookieName, "", -1))
	}
	switch {
	case errors.Is(err, auth.ErrProjectAccess):
		s.renderLogin(w, safeNext(next), projectAccessMessage, http.StatusForbidden)
	case errors.Is(err, auth.ErrSignIn):
		s.renderLogin(w, safeNext(next), "Atlassian sign-in was denied or expired. Try again with an authorized account.", http.StatusUnauthorized)
	case err != nil:
		log.Printf("web: completing Atlassian sign-in: %v", err)
		s.renderLogin(w, safeNext(next), "Atlassian sign-in is temporarily unavailable. Try again.", http.StatusServiceUnavailable)
	default:
		http.SetCookie(w, authCookie(sessionCookieName, session, auth.SessionLifetime))
		http.Redirect(w, r, safeNext(next), http.StatusFound)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, defaultNext, http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := s.auth.Logout(r.Context(), cookieValue(r, sessionCookieName)); err != nil {
		log.Printf("web: revoking Jira session: %v", err)
		http.Error(w, "Couldn’t sign out. Try again.", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, authCookie(sessionCookieName, "", -1))
	http.Redirect(w, r, "/login", http.StatusFound)
}

func authCookie(name, value string, lifetime time.Duration) *http.Cookie {
	maxAge := -1
	if lifetime > 0 {
		maxAge = int(lifetime.Seconds())
	}
	return &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) renderLogin(w http.ResponseWriter, next, errMsg string, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	view := struct {
		Next  string
		Error string
	}{Next: next, Error: errMsg}
	if err := s.templates.ExecuteTemplate(w, "login.html", view); err != nil {
		log.Printf("web: rendering login: %v", err)
	}
}

func safeNext(next string) string {
	u, err := url.ParseRequestURI(next)
	if err != nil || u.IsAbs() || !strings.HasPrefix(u.Path, "/") ||
		strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n\t") {
		return defaultNext
	}
	return u.RequestURI()
}
