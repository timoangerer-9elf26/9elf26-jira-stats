package web

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

func (s *Server) handlePrioMeetingComplete(w http.ResponseWriter, r *http.Request) {
	identity, ok := JiraIdentityFromContext(r.Context())
	if !ok || identity.AccountID == "" {
		http.Error(w, "Jira-Anmeldung erforderlich.", http.StatusUnauthorized)
		return
	}
	window, err := strconv.ParseInt(r.PostFormValue("meeting-window"), 10, 64)
	if err != nil || window < 0 {
		http.Error(w, "Ungültiges Meeting-Fenster.", http.StatusBadRequest)
		return
	}
	if err := s.rollups.CompletePrioMeeting(window, s.now(), identity.AccountID); err != nil {
		s.renderError(w)
		return
	}
	http.Redirect(w, r, "/prio", http.StatusSeeOther)
}

type prioMeetingChangeView struct {
	Label, AccountID, Instant, TimeLabel string
}

func (s *Server) prioMeetingChanges(window int64) ([]prioMeetingChangeView, error) {
	changes, err := s.rollups.PrioMeetingChanges(window)
	if err != nil {
		return nil, err
	}
	view := make([]prioMeetingChangeView, 0, len(changes))
	for _, change := range changes {
		view = append(view, prioMeetingChangeView{
			Label: change.IssueKey + " · " + change.Label, AccountID: change.AccountID,
			Instant:   change.OccurredAt.Format(time.RFC3339Nano),
			TimeLabel: change.OccurredAt.In(s.prioLocation).Format("02.01.2006 15:04 MST"),
		})
	}
	return view, nil
}

func (s *Server) handlePrioMeetingSummary(w http.ResponseWriter, r *http.Request) {
	window, err := strconv.ParseInt(r.URL.Query().Get("window"), 10, 64)
	if err != nil || window < 0 {
		http.Error(w, "Ungültiges Meeting-Fenster.", http.StatusBadRequest)
		return
	}
	changes, err := s.prioMeetingChanges(window)
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "prio-summary-changes", changes); err != nil {
		log.Printf("web: render Prio meeting summary: %v", err)
	}
}

func (s *Server) notePrioWrite(ctx context.Context, key, label string) {
	identity, _ := JiraIdentityFromContext(ctx)
	if identity.AccountID == "" {
		return
	}
	s.prioKeysCache.invalidate(identity.AccountID)
	if err := s.rollups.RecordPrioMeetingChange(store.PrioMeetingChange{
		IssueKey: key, Label: label, AccountID: identity.AccountID, OccurredAt: s.now(),
	}); err != nil {
		log.Printf("web: record Prio meeting change for %s: %v", key, err)
	}
}
