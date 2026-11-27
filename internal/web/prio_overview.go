package web

import (
	"net/url"
	"slices"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioGroup struct {
	ID, Label string
	Rows      []prioRow
	Total     int
}

type prioAgendaOption struct {
	Label, Href, Value, Description string
	Active                          bool
}

func (s *Server) populatePrioOverview(view *prioAgendaView, q url.Values, rows []prioRow, issues []store.PrioIssue, keys []string) error {
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
	if !view.PresetLoadingResults() {
		view.Page = paginatePrioOverview(q, groups, len(rows))
	}
	view.SortOptions = []prioAgendaOption{
		{Label: "Höchste Priorität", Href: prioHref(q, "sort", "priority"), Active: sort == "priority" || (sort != "recent" && !view.JiraOrder)},
		{Label: "Jüngste Statusänderung", Href: prioHref(q, "sort", "recent"), Active: sort == "recent"},
	}
	if view.JiraOrder {
		view.SortOptions = append([]prioAgendaOption{{Label: "Jira-Reihenfolge", Href: prioHref(q, "sort", "jira"), Active: sort != "priority" && sort != "recent"}}, view.SortOptions...)
	}
	view.CloseHref = prioHref(q, "ticket", "")
	return nil
}
