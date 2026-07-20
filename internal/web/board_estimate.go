package web

import (
	"log"
	"net/http"
	"strings"
)

// estimateWriteError is the inline message shown on a card when a Board estimate
// write fails (permissions / network / 4xx, or the reconciliation re-fetch). It
// is deliberately generic and non-technical — the detail is logged server-side.
const estimateWriteError = "Couldn't save — try again."

// handleBoardEstimate is the Board estimate edit's write endpoint (#108,
// docs/adr/0005): the popover's choices POST here to write a ticket's size back
// to Jira. It delegates to the Estimator (write → single-issue re-fetch → persist)
// and swaps the pill to the authoritative re-read value; on any failure it reverts
// the pill to the client-sent prior value and renders an inline error on the card,
// leaving Jira and the projection unchanged (last-write-wins, no locking guard).
//
// There is no CSRF token — consistent with POST /resync (single-user, self-hosted).
func (s *Server) handleBoardEstimate(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.FormValue("key"))
	size := r.FormValue("size")
	prior := r.FormValue("prior")
	// The UI only ever offers the four choices; anything else is a malformed
	// request (never a write). prior is validated the same so a bogus revert value
	// can't be reflected back.
	if key == "" || !validEstimateSize(size) || !validEstimateSize(prior) {
		http.Error(w, "bad estimate request", http.StatusBadRequest)
		return
	}

	if s.estimator == nil {
		// No write path wired (e.g. an in-process test without a Syncer): the edit
		// can't be honored, so fail safe — revert and show the inline error.
		s.renderEstimateControl(w, key, prior, estimateWriteError)
		return
	}

	newSize, err := s.estimator.SetEstimate(r.Context(), key, size)
	if err != nil {
		log.Printf("web: board estimate write %s=%q failed: %v", key, size, err)
		s.renderEstimateControl(w, key, prior, estimateWriteError)
		return
	}
	s.renderEstimateControl(w, key, newSize, "")
}

// validEstimateSize reports whether s is one of the four values the estimate
// control offers: the T-shirt labels S/M/L, or "" for no-estimate.
func validEstimateSize(s string) bool {
	switch s {
	case "", "S", "M", "L":
		return true
	default:
		return false
	}
}

// renderEstimateControl writes the board-estimate fragment for one card: the
// interactive pill showing size (its display form), and an inline error when
// errMsg is set. It is the response htmx swaps in place of the edited pill (a 200
// so htmx swaps it; a failed write is a rendered outcome, not an HTTP error).
func (s *Server) renderEstimateControl(w http.ResponseWriter, key, size, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]any{
		"Key":     key,
		"Size":    size,
		"Display": sizeDisplay(size),
		"Error":   errMsg,
	}
	if err := s.templates.ExecuteTemplate(w, "board-estimate", data); err != nil {
		http.Error(w, "failed to render", http.StatusInternalServerError)
	}
}
