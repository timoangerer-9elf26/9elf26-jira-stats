package web

import (
	"net/http"
)

// resyncStatusView models the shared resync control's tooltip state (the
// "resync-status" fragment). Running drives the spinning/disabled button and the
// in-progress tooltip line and the fast (~1.5s) self-poll; when idle the fragment
// still self-polls (~30s) so the tooltip's relative times stay fresh without a
// page reload. FullResyncAgo and IncrementalAgo are the two tooltip timestamps
// (CONTEXT.md → Sync), each a relative "…ago" label or the literal "never".
type resyncStatusView struct {
	Running        bool
	FullResyncAgo  string
	IncrementalAgo string
}

// handleResync triggers a full resync in the background and returns promptly
// with the in-progress control fragment (never blocking for the whole backfill).
// Triggering while a resync is already running is a safe no-op — either way a
// resync is now in flight, so the response always reports the running state. A
// nil resyncer (server built without one) degrades to the idle fragment.
func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) {
	if s.resyncer != nil {
		s.resyncer.Resync()
	}
	// A resyncer that was just triggered (or already running) is in flight; with
	// no resyncer wired the button is a no-op and we just report the idle state.
	s.renderResyncStatus(w, s.resyncer != nil)
}

// handleResyncStatus reports the current resync state as the control fragment.
// It is the always-on poll target the fragment hits (~1.5s while running so the
// spinner clears promptly once the resync completes, ~30s when idle so the
// tooltip's relative times stay current) — both cadences are wired in the
// template off the Running flag.
func (s *Server) handleResyncStatus(w http.ResponseWriter, r *http.Request) {
	s.renderResyncStatus(w, s.resyncer != nil && s.resyncer.Resyncing())
}

// resyncStatus builds the control view for the given running state, folding in
// the two tooltip timestamps: the last full resync (or "never") and the last
// incremental-sync heartbeat.
func (s *Server) resyncStatus(running bool) (resyncStatusView, error) {
	full, fok, err := s.rollups.LastFullResync()
	if err != nil {
		return resyncStatusView{}, err
	}
	inc, iok, err := s.rollups.LastSync()
	if err != nil {
		return resyncStatusView{}, err
	}
	return resyncStatusView{
		Running:        running,
		FullResyncAgo:  s.agoOrNever(full, fok),
		IncrementalAgo: s.agoOrNever(inc, iok),
	}, nil
}

// renderResyncStatus resolves the control view for the given running state and
// writes the resync-status fragment, degrading to the shared error state if a
// timestamp read fails (consistent with the other handlers).
func (s *Server) renderResyncStatus(w http.ResponseWriter, running bool) {
	view, err := s.resyncStatus(running)
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "resync-status", view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}
