package web

import (
	"net/url"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioAgendaFilters struct {
	Level, Jira prioAgendaOption
	SinceCutoff bool
}

func prioAgendaFiltersFor(q url.Values) prioAgendaFilters {
	sinceCutoff := q.Get("period") == "since"
	if !sinceCutoff {
		q.Del("period")
	}
	level := prioAgendaOption{Value: "epic", Label: "Nur Epics"}
	switch q.Get("level") {
	case "unparented":
		level = prioAgendaOption{Value: "unparented", Label: "Ohne Parent"}
	case "all":
		level = prioAgendaOption{Value: "all", Label: "Alle"}
	default:
		q.Del("level")
	}
	return prioAgendaFilters{Level: level, SinceCutoff: sinceCutoff}
}

func (f prioAgendaFilters) keepsLevel(issue store.PrioIssue) bool {
	return strings.EqualFold(issue.Type, "Epic") || f.Level.Value == "all" ||
		f.Level.Value == "unparented" && issue.ParentKey == ""
}

func (f prioAgendaFilters) keepsPeriod(events prioIssueEvents) bool {
	return !f.SinceCutoff || events.New || events.Resolved ||
		events.StatusLabel != "" || len(events.PriorityChanges) > 0
}

func (f prioAgendaFilters) summary() string {
	parts := []string{"Jira-Filter: " + f.Jira.Label}
	if f.SinceCutoff {
		parts = append(parts, "Nur seit Stichtag")
	}
	return strings.Join(parts, " · ")
}

func (v prioAgendaView) prioJiraFilterOption() prioAgendaOption {
	if v.Preset == prioLocalPreset {
		return prioAgendaOption{Value: prioLocalPreset, Label: "Keiner", Description: "Tickets aus dem lokalen Jira-Abbild."}
	}
	for _, option := range v.PresetOptions {
		if option.Active {
			return option
		}
	}
	return prioAgendaOption{Value: v.Preset, Label: "Filter " + v.Preset}
}
