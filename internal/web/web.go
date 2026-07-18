// Package web is the HTTP boundary: real net/http handlers rendering
// html/template pages, with the built Tailwind CSS and vendored htmx embedded
// and served from the binary. This is the seam every integration test drives.
package web

import (
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

//go:embed assets/output.css assets/htmx.min.js
var assetsFS embed.FS

// Rollups is the read side the web layer depends on: rollup queries over the
// synced store plus the data-freshness stamp. Keeping it an interface keeps the
// HTTP seam testable and decoupled from the concrete store.
type Rollups interface {
	OpenByStatus() (store.OpenBoard, error)
	CompletedInRange(from, to time.Time) (store.SizeTally, error)
	// WeeklyCategoriesInWindow is the Weekly view's Started-with / Added / Finished
	// breakdown for a sprint over [from, to), reconstructed from status and
	// membership history.
	WeeklyCategoriesInWindow(sprintID int, from, to time.Time) (store.WeeklyCategories, error)
	LastSyncedAt() (t time.Time, ok bool, err error)
	// ActiveSprintWindow reports the active sprint entity (name and activation
	// instant). ok is false when no sprint is active.
	ActiveSprintWindow() (store.ActiveSprint, bool, error)
	// ActiveSprintBoard is the whole active sprint as a per-status Kanban board
	// (Done columns included) for the /board data-quality view.
	ActiveSprintBoard() (store.Board, error)
	// DailyStatusChanges returns active-sprint Task/Bug/Story tickets whose status
	// changed within [from, to), filtered by assignee ("" = all,
	// store.UnassignedAssignee = unassigned, else an exact name), for the /daily view.
	DailyStatusChanges(assignee string, from, to time.Time) ([]store.DailyTicket, error)
	// ActiveSprintAssignees lists the distinct named assignees of active-sprint
	// work items, to populate the /daily assignee dropdown.
	ActiveSprintAssignees() ([]string, error)
	// IssuesCreatedInRange returns the tickets authored (by their immutable Jira
	// Creator) within [from, to), filtered by creator ("" = any,
	// store.UnassignedAssignee = none, else an exact name), for the Daily view's
	// "tickets I created" section. Deliberately NOT sprint-scoped.
	IssuesCreatedInRange(creator string, from, to time.Time) ([]store.CreatedTicket, error)
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

// Server holds the parsed templates and the rollup source, and implements
// http.Handler via its router.
type Server struct {
	rollups       Rollups
	resyncer      Resyncer
	templates     *template.Template
	mux           *http.ServeMux
	now           func() time.Time
	loc           *time.Location
	velocityWeeks int
	jiraBaseURL   string
	me            string
}

// Option configures a Server at construction.
type Option func(*Server)

// WithClock overrides the wall clock used to resolve relative windows (the
// Weekly work-week / live-sprint window, the Daily window, Velocity weeks), so
// tests can pin "now" deterministically.
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

// WithVelocityWeeks overrides how many trailing ISO weeks the Velocity view
// shows (spec: ~8–12). Non-positive values are ignored, keeping the default.
func WithVelocityWeeks(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.velocityWeeks = n
		}
	}
}

// NewServer parses the embedded templates, loads the display timezone, and
// wires the routes.
func NewServer(rollups Rollups, opts ...Option) (*Server, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	loc, err := time.LoadLocation(displayTimeZone)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", displayTimeZone, err)
	}
	s := &Server{
		rollups:       rollups,
		templates:     tmpl,
		mux:           http.NewServeMux(),
		now:           time.Now,
		loc:           loc,
		velocityWeeks: defaultVelocityWeeks,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /now/board", s.handleNowBoard)
	s.mux.HandleFunc("GET /board", s.handleBoard)
	s.mux.HandleFunc("GET /daily", s.handleDaily)
	s.mux.HandleFunc("GET /daily/results", s.handleDailyResults)
	s.mux.HandleFunc("GET /weekly", s.handleWeekly)
	s.mux.HandleFunc("GET /weekly/results", s.handleWeeklyResults)
	s.mux.HandleFunc("GET /velocity", s.handleVelocity)
	s.mux.HandleFunc("POST /resync", s.handleResync)
	s.mux.HandleFunc("GET /resync/status", s.handleResyncStatus)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(assetsFS)))))
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// nowView is the "Now" page/fragment model: the open board scoped to the active
// sprint, the active sprint's name (empty when none is known), plus a
// human-readable data-freshness label.
type nowView struct {
	Board      store.OpenBoard
	SprintName string
	UpdatedAgo string
}

// handleIndex renders the full "Now" page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.renderNow(w, "index.html")
}

// handleNowBoard renders just the self-polling board fragment (the HTMX
// refresh target).
func (s *Server) handleNowBoard(w http.ResponseWriter, r *http.Request) {
	s.renderNow(w, "now-board")
}

func (s *Server) renderNow(w http.ResponseWriter, name string) {
	view, err := s.nowView()
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
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

func (s *Server) nowView() (nowView, error) {
	board, err := s.rollups.OpenByStatus()
	if err != nil {
		return nowView{}, err
	}
	sprintName := ""
	switch sprint, ok, err := s.rollups.ActiveSprintWindow(); {
	case err != nil:
		return nowView{}, err
	case ok:
		sprintName = sprint.Name
	}
	updated := "just now"
	switch ago, ok, err := s.syncedAgo(); {
	case err != nil:
		return nowView{}, err
	case ok:
		updated = ago
	}
	return nowView{Board: board, SprintName: sprintName, UpdatedAgo: updated}, nil
}

// syncedAgo reports how long ago the projection was last synced as a compact
// "Ns ago" label. ok is false on a never-synced (empty) store. It is the single
// source of the data-freshness label shared by the Now heading and the resync
// status widget.
func (s *Server) syncedAgo() (string, bool, error) {
	t, ok, err := s.rollups.LastSyncedAt()
	if err != nil || !ok {
		return "", ok, err
	}
	return humanizeAgo(time.Since(t)), true, nil
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
func templateFuncs() template.FuncMap {
	return template.FuncMap{"dict": dict}
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
