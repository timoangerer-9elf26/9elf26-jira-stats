package web

import (
	"net/http"
	"strconv"
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
