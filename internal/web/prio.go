package web

import (
	"net/http"
	"sort"
)

// prioRow is one row of the Prio table: the issue's display fields plus a
// resolved Jira link. Href is empty when no Jira base URL is configured, so the
// name renders unlinked rather than as a broken link — matching the board card.
type prioRow struct {
	Key      string
	Type     string // Epic, Task, Bug or Story (drives the shared type badge)
	Summary  string
	Status   string       // plain status text
	Priority string       // Jira level: Highest…Lowest ("" when the issue carries none)
	Icon     priorityIcon // the level's icon; zero when Priority is unknown/empty
	Href     string       // "<base>/browse/<KEY>", or "" when unconfigured
	// Labels are the issue's Jira labels, in Jira's order; empty when it carries
	// none, in which case the Labels cell renders empty. Kept as separate strings
	// rather than one joined display string so a label stays individually
	// matchable.
	Labels []string
}

// prioView is the /prio page model: every issue in the projection as a flat
// table, sorted priority Highest→Lowest (ties by key). Empty drives the friendly
// "no tickets" state (an unsynced or genuinely empty projection). Labels and the
// Prio filters land in later slices; this renders Type · Name · Priority · Status.
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
			Key:      issue.Key,
			Type:     issue.Type,
			Summary:  issue.Summary,
			Status:   issue.Status,
			Priority: issue.Priority,
			Icon:     priorityIconFor(issue.Priority),
			Href:     s.jiraIssueURL(issue.Key),
			Labels:   issue.Labels,
		})
	}
	sortByPriority(rows)
	return prioView{Rows: rows, Empty: len(rows) == 0}, nil
}

// priorityIcon is how one priority level draws in the table: a stroke colour and
// the SVG path data for its chevrons, on a 14x14 viewBox. It only needs to
// RESEMBLE Jira's severity icon, not match it. The colour is a hex applied
// inline, matching the Board's epic pill (epicColorHex) — the committed
// stylesheet is built by scanning the templates only, so a Tailwind class named
// here would be stripped from it.
type priorityIcon struct {
	Color string // stroke colour, as a CSS hex
	Path  string // SVG path data (several chevrons ride in one path as sub-paths)
}

// priorityLevels is the single ordered table of Jira's five priority levels:
// most important first, so a level's index IS its sort rank, and each level's
// icon lives next to it. Adding or renaming a level is a one-line edit here —
// nothing else in the app enumerates the levels.
var priorityLevels = []struct {
	Name string
	Icon priorityIcon
}{
	{"Highest", priorityIcon{Color: "#E11D48", Path: "M2 7l5-4 5 4M2 12l5-4 5 4"}},
	{"High", priorityIcon{Color: "#F97316", Path: "M2 9.5l5-4 5 4"}},
	{"Medium", priorityIcon{Color: "#F59E0B", Path: "M2 5.5h10M2 9.5h10"}},
	{"Low", priorityIcon{Color: "#0EA5E9", Path: "M2 4.5l5 4 5-4"}},
	{"Lowest", priorityIcon{Color: "#38BDF8", Path: "M2 2l5 4 5-4M2 7l5 4 5-4"}},
}

// priorityRank orders the five levels most-important-first. An unknown or
// missing level (which DCAI issues do not have, but a row synced before priority
// joined the projection can) ranks last.
func priorityRank(priority string) int {
	for i, level := range priorityLevels {
		if level.Name == priority {
			return i
		}
	}
	return len(priorityLevels)
}

// priorityIconFor returns the level's glyph, or the zero icon (which the table
// renders as no glyph at all) for an unknown or missing level.
func priorityIconFor(priority string) priorityIcon {
	for _, level := range priorityLevels {
		if level.Name == priority {
			return level.Icon
		}
	}
	return priorityIcon{}
}

// sortByPriority orders the Prio table Highest→Lowest, breaking ties by issue
// key so the list does not reshuffle between loads. Sorting lives here rather
// than in the store, mirroring how the Board filters in the web layer.
func sortByPriority(rows []prioRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := priorityRank(rows[i].Priority), priorityRank(rows[j].Priority)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Key < rows[j].Key
	})
}
