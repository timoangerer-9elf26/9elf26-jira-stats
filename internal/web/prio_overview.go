package web

import (
	"net/url"
	"slices"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioGroup struct {
	ID, Label string
	Rows      []prioRow
}

type prioAgendaOption struct {
	Label, Href string
	Active      bool
}

func keepPrioOverviewRow(q url.Values, issue store.PrioIssue, events prioIssueEvents) bool {
	if q.Get("parent") == "1" && issue.ParentKey != "" {
		return false
	}
	technical := slices.Contains(issue.Labels, technicalLabel)
	switch q.Get("tech") {
	case "technical":
		if !technical {
			return false
		}
	case "non-technical":
		if technical {
			return false
		}
	}
	// Local meeting activity provides the initial overview until Jira saved
	// filters are wired in. The agenda queues always use the entire projection.
	return q.Get("preset") == prioLocalPreset || events.New || events.StatusChanged ||
		events.Resolved || len(events.PriorityChanges) != 0
}

func (s *Server) populatePrioOverview(view *prioAgendaView, q url.Values, rows []prioRow, issueCount int) error {
	if q.Get("sort") == "recent" {
		latest, err := s.rollups.LatestIssueStatusChanges()
		if err != nil {
			return err
		}
		slices.SortStableFunc(rows, func(a, b prioRow) int {
			if order := latest[b.Key].Compare(latest[a.Key]); order != 0 {
				return order
			}
			return compareAgendaPriority(a, b)
		})
	} else {
		sortAgendaRows(rows, false)
	}
	groups := []prioGroup{
		{ID: statusPlanned, Label: "Geplant"},
		{ID: "doing", Label: "In Arbeit"},
		{ID: "done", Label: "Fertig"},
		{ID: "other", Label: "Weitere Status"},
	}
	for _, row := range rows {
		current := prioStatusGroup(row.Status)
		for i := range groups {
			if current == groups[i].ID {
				groups[i].Rows = append(groups[i].Rows, row)
				break
			}
		}
	}
	view.Groups = groups
	view.IssueCount, view.OverviewCount = issueCount, len(rows)
	sort := q.Get("sort")
	view.SortOptions = []prioAgendaOption{
		{Label: "Höchste Priorität", Href: prioHref(q, "sort", "priority"), Active: sort != "recent"},
		{Label: "Jüngste Statusänderung", Href: prioHref(q, "sort", "recent"), Active: sort == "recent"},
	}
	tech := q.Get("tech")
	view.TechnicalOptions = []prioAgendaOption{
		{Label: "Nicht technisch", Href: prioHref(q, "tech", "non-technical"), Active: tech == "non-technical"},
		{Label: "Technisch", Href: prioHref(q, "tech", "technical"), Active: tech == "technical"},
		{Label: "Alle", Href: prioHref(q, "tech", ""), Active: tech != "technical" && tech != "non-technical"},
	}
	view.ParentHref = prioHref(q, "parent", "1")
	view.ParentOnly = q.Get("parent") == "1"
	if view.ParentOnly {
		view.ParentHref = prioHref(q, "parent", "")
	}
	view.CloseHref = prioHref(q, "ticket", "")
	return nil
}
