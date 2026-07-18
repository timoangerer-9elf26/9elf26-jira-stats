package web

import (
	"net/http"
)

// resyncStatusView models the shared resync status label (the "resync-status"
// fragment). Running drives the "Resyncing…" self-polling state; when idle,
// HasSynced/SyncedAgo report how fresh the projection is.
type resyncStatusView struct {
	Running   bool
	HasSynced bool
	SyncedAgo string
}

// handleResync triggers a full resync in the background and returns promptly
// with the in-progress status fragment (never blocking for the whole backfill).
// Triggering while a resync is already running is a safe no-op — either way a
// resync is now in flight, so the response always reports the running state. A
// nil resyncer (server built without one) degrades to reporting freshness.
func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) {
	if s.resyncer != nil {
		s.resyncer.Resync()
	}
	// A resyncer that was just triggered (or already running) is in flight; with
	// no resyncer wired the button is a no-op and we just report freshness.
	s.renderResyncStatus(w, s.resyncer != nil)
}

// handleResyncStatus reports the current resync state: the "Resyncing…" label
// (which self-polls) while one runs, otherwise the data-freshness label. It is
// the poll target the running fragment hits until the resync completes.
func (s *Server) handleResyncStatus(w http.ResponseWriter, r *http.Request) {
	s.renderResyncStatus(w, s.resyncer != nil && s.resyncer.Resyncing())
}

// resyncStatus builds the status view for the given running state, folding in
// the projection's freshness (last synced) for the idle label.
func (s *Server) resyncStatus(running bool) (resyncStatusView, error) {
	view := resyncStatusView{Running: running}
	if running {
		return view, nil
	}
	ago, ok, err := s.syncedAgo()
	if err != nil {
		return resyncStatusView{}, err
	}
	view.HasSynced = ok
	view.SyncedAgo = ago
	return view, nil
}

// renderResyncStatus resolves the status view for the given running state and
// writes the resync-status fragment, degrading to the shared error state if the
// freshness read fails (consistent with the other handlers).
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
