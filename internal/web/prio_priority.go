package web

import (
	"log"
	"net/http"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

const priorityWriteError = "Couldn't save — try again."

// handlePrioPriority is the Prio view's priority edit (#212): POST /prio/priority
// with key + priority (one of jira.Priorities, by name) and the active filters'
// hidden inputs. It writes through the Prioritizer, then renders the WHOLE
// prio-panel from the store — success and failure alike, one response shape —
// so the table re-sorts around the change (a re-ranked row moves in the same
// response) with the edited row highlighted, or, on failure, the row unmoved
// with an inline error. The filter params ride in the form body exactly as a
// filter toggle sends them, so the panel comes back with the same filters
// applied; without them it renders at its defaults, like a bare /prio.
func (s *Server) handlePrioPriority(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.FormValue("key"))
	priority := r.FormValue("priority")
	if key == "" || !jira.ValidPriority(priority) {
		http.Error(w, "bad priority request", http.StatusBadRequest)
		return
	}

	edit := prioEdit{Key: key}
	if s.prioritizer == nil {
		edit.Error = priorityWriteError
	} else if err := s.prioritizer.SetPriority(r.Context(), key, priority); err != nil {
		log.Printf("web: prio priority write %s=%q failed: %v", key, priority, err)
		edit.Error = priorityWriteError
	}
	// r.Form holds the filter params the hidden [data-filterparam] inputs sent
	// (plus key/priority, which no filter reads).
	s.renderPrioWith(w, r.Form, "prio-panel", edit)
}
