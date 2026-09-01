package web

import "net/http"

// prioRow is one row of the Prio table: the issue's display fields plus a
// resolved Jira link. Href is empty when no Jira base URL is configured, so the
// name renders unlinked rather than as a broken link — matching the board card.
type prioRow struct {
	Key     string
	Type    string // Epic, Task, Bug or Story (drives the shared type badge)
	Summary string
	Status  string // plain status text
	Href    string // "<base>/browse/<KEY>", or "" when unconfigured
}

// prioView is the /prio page model: every issue in the projection as a flat,
// key-ordered table. Empty drives the friendly "no tickets" state (an unsynced
// or genuinely empty projection). Priority, labels and the Prio filters land in
// later slices; this skeleton renders Type · Name · Status for everything.
type prioView struct {
	Rows  []prioRow
	Empty bool
}

// handlePrio renders the full standalone Prio page.
func (s *Server) handlePrio(w http.ResponseWriter, r *http.Request) {
	s.renderPrio(w, "prio.html")
}

// handlePrioResults renders the Prio panel fragment (the HTMX swap target), so a
// later filter change can re-render the chrome and the table together without a
// full-page reload. Mirrors /board/results.
func (s *Server) handlePrioResults(w http.ResponseWriter, r *http.Request) {
	s.renderPrio(w, "prio-panel")
}

func (s *Server) renderPrio(w http.ResponseWriter, name string) {
	view, err := s.prioView()
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// prioView reads the whole projection and maps it to table rows, preserving the
// store's issue-key order.
func (s *Server) prioView() (prioView, error) {
	issues, err := s.rollups.PrioIssues()
	if err != nil {
		return prioView{}, err
	}
	rows := make([]prioRow, 0, len(issues))
	for _, issue := range issues {
		rows = append(rows, prioRow{
			Key:     issue.Key,
			Type:    issue.Type,
			Summary: issue.Summary,
			Status:  issue.Status,
			Href:    s.jiraIssueURL(issue.Key),
		})
	}
	return prioView{Rows: rows, Empty: len(rows) == 0}, nil
}
