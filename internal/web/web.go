// Package web is the HTTP boundary: real net/http handlers rendering
// html/template pages, with the built Tailwind CSS and vendored htmx embedded
// and served from the binary. This is the seam every integration test drives.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// displayTimeZone is the timezone all date maths and week labels use (spec:
// Europe/Berlin, Monday-start ISO weeks). Timestamps are stored UTC underneath.
const displayTimeZone = "Europe/Berlin"

// Regenerate the committed Tailwind stylesheet (see also `make css`). Node is
// only needed to build CSS; `go build` embeds the committed output.css and
// never invokes Tailwind.
//go:generate npx @tailwindcss/cli -i assets/input.css -o assets/output.css --minify

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/output.css assets/htmx.min.js assets/avatars/*.svg
var assetsFS embed.FS

// Rollups is the read side the web layer depends on: rollup queries over the
// synced store plus the data-freshness stamp. Keeping it an interface keeps the
// HTTP seam testable and decoupled from the concrete store.
type Rollups interface {
	CompletedInRange(from, to time.Time) (store.SizeTally, error)
	// VelocitySeries returns the trailing N sprints as Velocity bars (oldest-first),
	// each bar's Finished points read from the Sprint view's SprintCategoriesInWindow
	// path, so a bar can never drift from the Sprint view. `now` bounds the active
	// sprint's open window.
	VelocitySeries(now time.Time, trailing int) ([]store.VelocityBar, error)
	// SprintCategoriesInWindow is the Sprint view's Started-with / Added / Finished
	// breakdown for a sprint over [from, to), reconstructed from status and
	// membership history.
	SprintCategoriesInWindow(sprintID int, from, to time.Time) (store.SprintCategories, error)
	// SprintCellIssues returns the issues behind one Sprint-view cohort × outcome
	// cell over [from, to) as drill-down cards (board-card fields + current status),
	// sorted by workflow order — the drill target behind each non-zero cell's
	// "N tickets" link. The list always matches the cell's count.
	SprintCellIssues(sprintID int, from, to time.Time, cohort store.SprintCohortSel, outcome store.SprintOutcomeSel) ([]store.SprintCellIssue, error)
	// LastFullResync is the instant of the last successful user-triggered full
	// resync (CONTEXT.md → Sync). ok is false until the first one — the cold-start
	// backfill does not count — so the tooltip reads "never".
	LastFullResync() (t time.Time, ok bool, err error)
	// LastSync is the incremental-sync heartbeat: the instant of the last
	// successful periodic sync cycle. ok is false before any cycle has run. A
	// frozen value means syncing has broken.
	LastSync() (t time.Time, ok bool, err error)
	// ActiveSprintWindow reports the active sprint entity (name and activation
	// instant). ok is false when no sprint is active.
	ActiveSprintWindow() (store.ActiveSprint, bool, error)
	// ActiveSprintBoard is the whole active sprint as a per-status Kanban board
	// (Done columns included) for the /board data-quality view.
	ActiveSprintBoard() (store.Board, error)
	// DailyBoard returns the /daily board's cards for [from, to): active-sprint
	// Task/Bug/Story created in the window OR moved in it, filtered by assignee
	// ("" = all, store.UnassignedAssignee = unassigned, else an exact name), each
	// placed by its status at the window END and carrying its movement facts.
	DailyBoard(assignee string, from, to time.Time) ([]store.DailyBoardCard, error)
	// ActiveSprintAssignees lists the distinct named assignees of active-sprint
	// work items, to populate the /daily assignee dropdown.
	ActiveSprintAssignees() ([]string, error)
}

// Resyncer triggers a full rebuild of the SQLite projection from Jira and
// reports whether one is running. It is the web layer's handle on the sync
// engine's resync capability (the app binds it to the running Syncer with a
// long-lived context). It is nil when the server is built without one (most
// in-process tests): the resync button still renders and the status endpoint
// still reports freshness, but a trigger is a no-op.
type Resyncer interface {
	// Resync starts a full resync in the background if none is running, returning
	// true when it started one and false when a resync was already in progress
	// (a safe no-op). It must return promptly — the rebuild runs in the background.
	Resync() bool
	// Resyncing reports whether a full resync is currently running.
	Resyncing() bool
}

// Estimator is the Board estimate edit's write path (docs/adr/0005 / #108): the
// app's only mutation of Jira. SetEstimate writes the issue's size back to Jira,
// re-reads that one issue and persists it, returning the authoritative size the
// re-fetch carried ("S"/"M"/"L" or "" for no-estimate). An error means the write
// (or its reconciliation read/save) failed, so the handler reverts the pill and
// shows an inline error, leaving Jira and the projection unchanged. It is nil
// when the server is built without one (most in-process tests): the pill still
// renders editable, but a write is reported as a failure (reverted + inline
// error), never silently dropped. The running *sync.Syncer satisfies it.
type Estimator interface {
	SetEstimate(ctx context.Context, key, size string) (string, error)
}

// Server holds the parsed templates and the rollup source, and implements
// http.Handler via its router.
type Server struct {
	rollups         Rollups
	resyncer        Resyncer
	estimator       Estimator
	templates       *template.Template
	mux             *http.ServeMux
	now             func() time.Time
	loc             *time.Location
	velocitySprints int
	jiraBaseURL     string
	me              string
	// auth gates every route behind the shared team login (#122). Nil means
	// auth is disabled and the middleware is a pass-through.
	auth *authConfig
}

// Option configures a Server at construction.
type Option func(*Server)

// WithClock overrides the wall clock used to resolve relative windows (the
// Sprint window [sprint start, now), the Daily window, the Velocity active
// sprint's [start, now)), so tests can pin "now" deterministically.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// WithLocation overrides the timezone used for all date maths and week labels
// (default Europe/Berlin), so the deployment's TZ setting is honored. A nil
// location is ignored, keeping the default.
func WithLocation(loc *time.Location) Option {
	return func(s *Server) {
		if loc != nil {
			s.loc = loc
		}
	}
}

// WithJiraBaseURL sets the Jira site base URL used to build board-card links
// (`<base>/browse/<KEY>`). The trailing slash is trimmed so the joined path
// never doubles up. When left empty (unset in config), board cards render
// without a link rather than a broken href.
func WithJiraBaseURL(base string) Option {
	return func(s *Server) { s.jiraBaseURL = strings.TrimRight(base, "/") }
}

// WithMe sets the configured identity — "me" — a Jira display name the Daily
// view revolves around: with no explicit assignee chosen, Daily defaults its
// filter to me instead of "All". Left empty (unset in config), Daily keeps the
// "All" default. See CONTEXT.md → Me and docs/adr/0003-daily-what-i-did-view.md.
func WithMe(name string) Option {
	return func(s *Server) { s.me = strings.TrimSpace(name) }
}

// WithResyncer wires the sync engine's full-resync capability into the server,
// enabling POST /resync to rebuild the projection from Jira. Left unset, the
// resync button renders but a trigger is a no-op (the status endpoint still
// reports data freshness).
func WithResyncer(r Resyncer) Option {
	return func(s *Server) { s.resyncer = r }
}

// WithEstimator wires the Board estimate edit's write path into the server,
// enabling POST /board/estimate to write a ticket's size back to Jira (see
// docs/adr/0005). Left unset, the editable pill still renders but a write is
// reported back as a failure (the pill reverts and shows an inline error).
func WithEstimator(e Estimator) Option {
	return func(s *Server) { s.estimator = e }
}

// WithAuth gates the server behind the shared team login (#122), enabling the
// /login form and session middleware for the given shared credential. Left
// unset, auth is disabled and every route is served unauthenticated (the
// local-dev / AUTH_DISABLED case); the caller (run() in main) is responsible
// for refusing to start a public deployment without it.
func WithAuth(email, password string) Option {
	return func(s *Server) { s.auth = newAuthConfig(email, password) }
}

// WithVelocitySprints overrides how many trailing sprints the Velocity view
// shows (spec: ~10). Non-positive values are ignored, keeping the default.
func WithVelocitySprints(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.velocitySprints = n
		}
	}
}

// NewServer parses the embedded templates, loads the display timezone, and
// wires the routes.
func NewServer(rollups Rollups, opts ...Option) (*Server, error) {
	loc, err := time.LoadLocation(displayTimeZone)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", displayTimeZone, err)
	}
	s := &Server{
		rollups:         rollups,
		mux:             http.NewServeMux(),
		now:             time.Now,
		loc:             loc,
		velocitySprints: defaultVelocitySprints,
	}
	for _, opt := range opts {
		opt(s)
	}
	// Templates are parsed after s is constructed because templateFuncs is now a
	// method: its authEnabled helper closes over s and reads s.auth at render
	// time (so the nav shows the logout control only when the shared login is
	// active). Parse order relative to the options does not matter.
	tmpl, err := template.New("").Funcs(s.templateFuncs()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s.templates = tmpl
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /board", s.handleBoard)
	s.mux.HandleFunc("POST /board/estimate", s.handleBoardEstimate)
	s.mux.HandleFunc("GET /daily", s.handleDaily)
	s.mux.HandleFunc("GET /daily/results", s.handleDailyResults)
	s.mux.HandleFunc("GET /sprint", s.handleSprint)
	s.mux.HandleFunc("GET /sprint/results", s.handleSprintResults)
	s.mux.HandleFunc("GET /sprint/cell", s.handleSprintCell)
	s.mux.HandleFunc("GET /velocity", s.handleVelocity)
	s.mux.HandleFunc("POST /resync", s.handleResync)
	s.mux.HandleFunc("GET /resync/status", s.handleResyncStatus)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(assetsFS)))))
	// Shared-team login (#122). Registered unconditionally; the handlers and the
	// auth middleware are pass-throughs when auth is disabled (s.auth == nil).
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("POST /login", s.handleLogin)
	s.mux.HandleFunc("GET /logout", s.handleLogout)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
}

// ServeHTTP makes Server an http.Handler, wrapping the mux in the auth
// middleware so every route is gated behind the shared login (#122). The
// middleware is a pass-through when auth is disabled.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.authMiddleware(s.mux).ServeHTTP(w, r)
}

// handleIndex redirects the root path to the Sprint view. The Now view was
// removed (#66); Sprint is the default landing page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/sprint", http.StatusFound)
}

// renderError renders the shared friendly error page for a failed rollup query,
// so a broken read shows a clear message (HTTP 500) rather than a bare status or
// a stack trace.
func (s *Server) renderError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if err := s.templates.ExecuteTemplate(w, "app-error", nil); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// agoOrNever renders an instant as a compact "Ns/Nm/Nh ago" label relative to
// the server clock, or the literal "never" when ok is false — the shared
// rendering for both tooltip timestamps (last full resync, last incremental
// sync). Using the server clock (s.now) keeps the label deterministic under a
// pinned test clock.
func (s *Server) agoOrNever(t time.Time, ok bool) string {
	if !ok {
		return "never"
	}
	return humanizeAgo(s.now().Sub(t))
}

// humanizeAgo renders an elapsed duration as a compact "Ns/Nm/Nh ago" label,
// clamping negative skew to zero.
func humanizeAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// templateFuncs exposes helpers the partials need. dict builds a map so a
// partial can receive several named values (Go templates take a single arg).
// authEnabled reports whether the shared login is active, letting the nav show
// the logout control only when there is a session to end.
func (s *Server) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"dict":        dict,
		"authEnabled": func() bool { return s.auth != nil },
	}
}

func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd argument count %d", len(pairs))
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is not a string", i)
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// mustSub returns the embedded assets rooted at the "assets" directory, so
// "/static/output.css" maps to the embedded "assets/output.css".
func mustSub(fsys embed.FS) fs.FS {
	sub, err := fs.Sub(fsys, "assets")
	if err != nil {
		panic(fmt.Sprintf("web: embedded assets missing: %v", err))
	}
	return sub
}
