package web

import (
	"fmt"
	"net/http"
	"time"
)

// defaultVelocityWeeks is how many trailing ISO weeks the Velocity view shows
// when not otherwise configured. The spec calls for ~8–12 weeks of history to
// inform "how much can we plan next week."
const defaultVelocityWeeks = 10

// velocityWeek is one bar of the Velocity view: a single ISO week's completed
// points. Percent is the bar height relative to the tallest week in the window
// (0 when the whole window is empty), so bars share one scale.
type velocityWeek struct {
	Label   string // ISO week, Europe/Berlin, e.g. "KW29"
	Points  int
	Percent int
}

// velocityView is the Velocity page model: the trailing ISO weeks oldest-first
// with no gaps — every week in the window is present, empty weeks included.
// Empty is true when no week in the window has any completed points, so the
// view can show a friendly note (e.g. before the first sync populates history).
type velocityView struct {
	Weeks []velocityWeek
	Empty bool
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

// velocityView computes completed points per ISO week for the trailing
// s.velocityWeeks weeks (Europe/Berlin, Monday-start), oldest first.
//
// It reuses store.CompletedInRange once per [weekStart, weekStart+7d) window
// rather than reimplementing Done-crossing detection, and reuses weekStart for
// the Monday boundaries. Every week in the window is emitted so the series has
// no gaps; weeks with no completions carry zero points.
func (s *Server) velocityView() (velocityView, error) {
	thisMonday := weekStart(s.now().In(s.loc), s.loc)
	weeks := make([]velocityWeek, 0, s.velocityWeeks)
	maxPoints := 0
	for i := s.velocityWeeks - 1; i >= 0; i-- {
		from := thisMonday.AddDate(0, 0, -7*i)
		to := from.AddDate(0, 0, 7)
		tally, err := s.rollups.CompletedInRange(from, to)
		if err != nil {
			return velocityView{}, err
		}
		weeks = append(weeks, velocityWeek{Label: isoWeekLabel(from), Points: tally.Points})
		if tally.Points > maxPoints {
			maxPoints = tally.Points
		}
	}
	for i := range weeks {
		weeks[i].Percent = barPercent(weeks[i].Points, maxPoints)
	}
	return velocityView{Weeks: weeks, Empty: maxPoints == 0}, nil
}

// isoWeekLabel formats a week's Monday as its Europe/Berlin ISO-week label
// (e.g. "KW29"). weekStart hands us a Monday, so ISOWeek is unambiguous.
func isoWeekLabel(mondayStart time.Time) string {
	_, week := mondayStart.ISOWeek()
	return fmt.Sprintf("KW%02d", week)
}

// barPercent scales points to a 0–100 bar height relative to the tallest week.
// A zero (or empty) window yields 0 so empty bars stay flat.
func barPercent(points, max int) int {
	if max <= 0 {
		return 0
	}
	return points * 100 / max
}
