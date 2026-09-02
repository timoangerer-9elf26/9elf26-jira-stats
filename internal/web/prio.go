package web

import (
	"net/http"
	"net/url"
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
	// Editable makes the priority cell an interactive write-back control (#212):
	// a popover of the five levels, each posting /prio/priority. The Prio table
	// is the sole surface that sets it, so the same partial stays read-only
	// display anywhere else — the gate the board card's Editable puts on the
	// estimate pill.
	Editable bool
	// Edited marks the row a just-landed priority write changed, so the response
	// can highlight where the row moved to. Per-response only; never persisted.
	Edited bool
	// Error is the inline message a failed priority write leaves on this row
	// ("" when none). The row itself is unmoved, since the priority did not change.
	Error string
}

// prioEdit is the outcome of a priority write the panel render reports on:
// which row it targeted and, on failure, the inline message. The zero value
// means "no edit in this response" (plain GET renders).
type prioEdit struct {
	Key   string
	Error string
}

// prioView is the /prio page model: the projection's issues that survive the
// Prio filters, as a flat table sorted priority Highest→Lowest (ties by key).
// Filters carries the resolved filter registry so the chrome can render its
// controls and re-emit their params.
type prioView struct {
	Rows    []prioRow
	Filters []prioFilter
	// Empty is true when no row renders; NoMatch distinguishes the two reasons —
	// the filters hid everything (true) vs the projection itself is empty (false).
	Empty   bool
	NoMatch bool
}

// handlePrio renders the full standalone Prio page.
func (s *Server) handlePrio(w http.ResponseWriter, r *http.Request) {
	s.renderPrio(w, r, "prio.html")
}

// handlePrioResults renders the Prio panel fragment (the HTMX swap target), so a
// later filter change can re-render the chrome and the table together without a
// full-page reload. Mirrors /board/results.
func (s *Server) handlePrioResults(w http.ResponseWriter, r *http.Request) {
	s.renderPrio(w, r, "prio-panel")
}

func (s *Server) renderPrio(w http.ResponseWriter, r *http.Request, name string) {
	s.renderPrioWith(w, r.URL.Query(), name, prioEdit{})
}

// renderPrioWith renders the named Prio template for the filter params q,
// annotating the rows with the outcome of a priority edit (if any).
func (s *Server) renderPrioWith(w http.ResponseWriter, q url.Values, name string, edit prioEdit) {
	view, err := s.prioView(q, edit)
	if err != nil {
		s.renderError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, view); err != nil {
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// prioView reads the whole projection, drops the rows the active filters hide,
// and maps the rest to table rows.
// prioView builds the Prio table for the filter params q. edit marks the row a
// priority write just targeted: highlighted when the write landed, carrying the
// inline error when it did not. Every row renders editable — the Prio table is
// the one surface the priority is editable on.
func (s *Server) prioView(q url.Values, edit prioEdit) (prioView, error) {
	issues, err := s.rollups.PrioIssues()
	if err != nil {
		return prioView{}, err
	}
	filters := prioFilters(q)
	rows := make([]prioRow, 0, len(issues))
	for _, issue := range issues {
		if !keepPrioIssue(filters, issue) {
			continue // hidden by a filter
		}
		rows = append(rows, prioRow{
			Key:      issue.Key,
			Type:     issue.Type,
			Summary:  issue.Summary,
			Status:   issue.Status,
			Priority: issue.Priority,
			Icon:     priorityIconFor(issue.Priority),
			Href:     s.jiraIssueURL(issue.Key),
			Labels:   issue.Labels,
			Editable: true, // the Prio table is the sole surface the priority is editable on (#212)
			Edited:   issue.Key == edit.Key && edit.Error == "",
			Error:    pick(issue.Key == edit.Key, edit.Error, ""),
		})
	}
	sortByPriority(rows)
	return prioView{
		Rows:    rows,
		Filters: filters,
		Empty:   len(rows) == 0,
		NoMatch: len(rows) == 0 && len(issues) > 0,
	}, nil
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
// priorityLevel is one of the five Jira levels as the Prio view draws it. Index
// in priorityLevels is the sort rank; Name is the Jira priority name, which is
// also the value the priority edit writes back (#212); Slug is the testid
// suffix of its menu choice.
type priorityLevel struct {
	Name string
	Slug string
	Icon priorityIcon
}

var priorityLevels = []priorityLevel{
	{"Highest", "highest", priorityIcon{Color: "#E11D48", Path: "M2 7l5-4 5 4M2 12l5-4 5 4"}},
	{"High", "high", priorityIcon{Color: "#F97316", Path: "M2 9.5l5-4 5 4"}},
	{"Medium", "medium", priorityIcon{Color: "#F59E0B", Path: "M2 5.5h10M2 9.5h10"}},
	{"Low", "low", priorityIcon{Color: "#0EA5E9", Path: "M2 4.5l5 4 5-4"}},
	{"Lowest", "lowest", priorityIcon{Color: "#38BDF8", Path: "M2 2l5 4 5-4M2 7l5 4 5-4"}},
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

// pick returns a when cond holds, else b.
func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
