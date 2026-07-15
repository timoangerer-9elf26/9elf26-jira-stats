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
)

// Regenerate the committed Tailwind stylesheet (see also `make css`). Node is
// only needed to build CSS; `go build` embeds the committed output.css and
// never invokes Tailwind.
//go:generate npx @tailwindcss/cli -i assets/input.css -o assets/output.css --minify

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/output.css assets/htmx.min.js
var assetsFS embed.FS

// Rollups is the read side the web layer depends on: pure rollup queries over
// the synced store. Keeping it an interface keeps the HTTP seam testable and
// decoupled from the concrete store.
type Rollups interface {
	TotalOpenPoints() (int, error)
}

// Server holds the parsed templates and the rollup source, and implements
// http.Handler via its router.
type Server struct {
	rollups   Rollups
	templates *template.Template
	mux       *http.ServeMux
}

// NewServer parses the embedded templates and wires the routes.
func NewServer(rollups Rollups) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s := &Server{rollups: rollups, templates: tmpl, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(assetsFS)))))
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	points, err := s.rollups.TotalOpenPoints()
	if err != nil {
		http.Error(w, "failed to compute rollup", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", struct{ TotalOpenPoints int }{points}); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
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
