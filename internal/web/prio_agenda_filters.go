package web

import (
	"net/url"
	"slices"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioAgendaFilters struct {
	Period, Level, Technical prioAgendaOption
	Jira                     prioAgendaOption
}

func prioAgendaFiltersFor(q url.Values) prioAgendaFilters {
	period := prioAgendaOption{Value: "all", Label: "Alle"}
	if q.Get("period") == "since" {
		period = prioAgendaOption{Value: "since", Label: "Seit Stichtag"}
	} else {
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
	technical := prioAgendaOption{Value: "all", Label: "egal"}
	switch q.Get("tech") {
	case "non-technical":
		technical = prioAgendaOption{Value: "non-technical", Label: "ausblenden"}
	case "technical":
		technical = prioAgendaOption{Value: "technical", Label: "nur Technical"}
	default:
		q.Del("tech")
	}
	return prioAgendaFilters{Period: period, Level: level, Technical: technical}
}

func (f prioAgendaFilters) keeps(issue store.PrioIssue, events prioIssueEvents) bool {
	isEpic := strings.EqualFold(issue.Type, "Epic")
	if !isEpic && (f.Level.Value == "epic" ||
		f.Level.Value == "unparented" && issue.ParentKey != "") {
		return false
	}
	hasTechnical := slices.Contains(issue.Labels, technicalLabel)
	if f.Technical.Value == "technical" && !hasTechnical ||
		f.Technical.Value == "non-technical" && hasTechnical {
		return false
	}
	return f.Period.Value == "all" || events.New || events.Resolved ||
		events.StatusLabel != "" || len(events.PriorityChanges) > 0
}

func (f prioAgendaFilters) summary() string {
	parts := []string{"Zeitraum: " + f.Period.Label, "Ebene: " + f.Level.Label}
	if f.Technical.Value != "all" {
		parts = append(parts, "Technical: "+f.Technical.Label)
	}
	if f.Jira.Value != prioLocalPreset {
		parts = append(parts, "Jira: "+f.Jira.Label)
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
