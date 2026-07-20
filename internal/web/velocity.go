package web

import (
	"net/http"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

// defaultVelocitySprints is how many trailing sprints the Velocity view shows
// when not otherwise configured (~10, per the spec).
const defaultVelocitySprints = 10

// velocitySprint is one bar of the Velocity view: a single sprint's Finished
// points (equal to the Sprint view's Total-row Finished for that sprint).
// Percent is the bar height relative to the tallest sprint in the window (0 when
// the whole window is empty), so bars share one scale. Dates is the start–end
// date line (date only, Europe/Berlin): "14 Jul – 18 Jul" for a completed
// sprint, "14 Jul – now (ongoing)" for the active one.
type velocitySprint struct {
	Label   string // the sprint's name (e.g. "KW29")
	Points  int
	Percent int
	Dates   string
}

// velocityView is the Velocity page model: the trailing sprints oldest-first.
// Empty is true when no sprint in the window has any Finished points, so the
// view can show a friendly note (e.g. before the first sync populates history).
type velocityView struct {
	Sprints []velocitySprint
	Empty   bool
}

// handleVelocity renders the full standalone Velocity page.
func (s *Server) handleVelocity(w http.ResponseWriter, r *http.Request) {
	view, err := s.velocityView()
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "velocity.html", view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// velocityView computes Finished points per sprint for the trailing
// s.velocitySprints sprints, oldest-first.
//
// It reuses store.VelocitySeries — which reads each sprint's Finished from the
// SAME SprintCategoriesInWindow path as the Sprint view — so a bar can never
// drift from the Sprint view's Total-row Finished. This layer only localizes the
// window instants into the display timezone and formats the date line.
func (s *Server) velocityView() (velocityView, error) {
	bars, err := s.rollups.VelocitySeries(s.now(), s.velocitySprints)
	if err != nil {
		return velocityView{}, err
	}
	sprints := make([]velocitySprint, 0, len(bars))
	maxPoints := 0
	for _, b := range bars {
		sprints = append(sprints, velocitySprint{
			Label:  b.Name,
			Points: b.Points,
			Dates:  s.sprintDateLine(b),
		})
		if b.Points > maxPoints {
			maxPoints = b.Points
		}
	}
	for i := range sprints {
		sprints[i].Percent = barPercent(sprints[i].Points, maxPoints)
	}
	return velocityView{Sprints: sprints, Empty: maxPoints == 0}, nil
}

// sprintDateLine formats a bar's start–end dates (date only, display timezone):
// a completed sprint as "14 Jul – 18 Jul"; the active sprint as
// "14 Jul – now (ongoing)" — the running end date plus an ongoing marker, since
// the planned end date is deliberately not used (docs/adr/0004).
func (s *Server) sprintDateLine(b store.VelocityBar) string {
	start := b.Start.In(s.loc).Format("2 Jan")
	if b.Ongoing {
		return start + " – now (ongoing)"
	}
	return start + " – " + b.End.In(s.loc).Format("2 Jan")
}

// barMaxPercent caps the tallest bar's height so it leaves headroom inside the
// fixed-height plot box instead of touching/overflowing the top edge (#103).
const barMaxPercent = 90

// barPercent scales points to a 0–barMaxPercent bar height relative to the
// tallest sprint, so even the tallest bar keeps headroom above it. A zero (or
// empty) window yields 0 so empty bars stay flat.
func barPercent(points, max int) int {
	if max <= 0 {
		return 0
	}
	return points * barMaxPercent / max
}
