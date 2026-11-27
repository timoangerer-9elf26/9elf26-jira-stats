package web

import (
	"context"
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

func keepPrioOverviewRow(q url.Values, issue store.PrioIssue) bool {
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
	return true
}

func (s *Server) populatePrioOverview(ctx context.Context, view *prioAgendaView, q url.Values, rows []prioRow, issues []store.PrioIssue) error {
	keys := s.prioPresetKeys(ctx, view, q)
	if view.Preset != prioLocalPreset {
		rows = s.orderPrioPresetRows(view, rows, keys, issues)
	}
	sort := q.Get("sort")
	if sort == "recent" && len(rows) > 0 {
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
	} else if sort == "priority" || !view.JiraOrder {
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
	view.IssueCount, view.OverviewCount = len(issues), len(rows)
	view.SortOptions = []prioAgendaOption{
		{Label: "Höchste Priorität", Href: prioHref(q, "sort", "priority"), Active: sort == "priority" || (sort != "recent" && !view.JiraOrder)},
		{Label: "Jüngste Statusänderung", Href: prioHref(q, "sort", "recent"), Active: sort == "recent"},
	}
	if view.JiraOrder {
		view.SortOptions = append([]prioAgendaOption{{Label: "Jira-Reihenfolge", Href: prioHref(q, "sort", "jira"), Active: sort != "priority" && sort != "recent"}}, view.SortOptions...)
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
