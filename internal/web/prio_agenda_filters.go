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

func (f prioAgendaFilters) keeps(issue store.PrioIssue, events prioIssueEvents) bool {
	isEpic := strings.EqualFold(issue.Type, "Epic")
	if !isEpic && (f.Level.Value == "epic" ||
		f.Level.Value == "unparented" && issue.ParentKey != "") {
		return false
	}
	return !f.SinceCutoff || events.New || events.Resolved ||
		events.StatusLabel != "" || len(events.PriorityChanges) > 0
}

func (f prioAgendaFilters) summary() string {
	parts := []string{"Jira-Filter: " + f.Jira.Label, "Ebene: " + f.Level.Label}
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
	label := "Filter " + v.Preset
	if v.Preset == prioDefaultPreset {
		label = "Seit letztem Prio-Meeting"
	}
	return prioAgendaOption{Value: v.Preset, Label: label}
}
