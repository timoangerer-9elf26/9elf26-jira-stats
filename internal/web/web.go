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
	LastSyncedAt() (t time.Time, ok bool, err error)
}

// Server holds the parsed templates and the rollup source, and implements
// http.Handler via its router.
type Server struct {
	rollups   Rollups
	templates *template.Template
	mux       *http.ServeMux
	now       func() time.Time
	loc       *time.Location
}

// Option configures a Server at construction.
type Option func(*Server)

// WithClock overrides the wall clock used to resolve relative date-range
// presets ("this week" etc.), so tests can pin "now" deterministically.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
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
		rollups:   rollups,
		templates: tmpl,
		mux:       http.NewServeMux(),
		now:       time.Now,
		loc:       loc,
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
	s.mux.HandleFunc("GET /completed", s.handleCompleted)
	s.mux.HandleFunc("GET /completed/results", s.handleCompletedResults)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(assetsFS)))))
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// nowView is the "Now" page/fragment model: the open board plus a
// human-readable data-freshness label.
type nowView struct {
	Board      store.OpenBoard
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
		http.Error(w, "failed to compute rollup", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

func (s *Server) nowView() (nowView, error) {
	board, err := s.rollups.OpenByStatus()
	if err != nil {
		return nowView{}, err
	}
	updated := "just now"
	switch t, ok, err := s.rollups.LastSyncedAt(); {
	case err != nil:
		return nowView{}, err
	case ok:
		updated = humanizeAgo(time.Since(t))
	}
	return nowView{Board: board, UpdatedAgo: updated}, nil
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
