package web

import (
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioEpicSuggestion struct {
	TargetGroup string
	Label       string
	ChildCount  int
	Children    []prioEpicChildGroup
	Transitions []prioTicketTransition
}

type prioEpicChildGroup struct {
	Label    string
	Canceled bool
	Issues   []prioEpicChild
}

type prioEpicChild struct {
	Key, Summary, Status, Href string
}

func (s *Server) prioEpicSuggestions(issues []store.PrioIssue) map[string]*prioEpicSuggestion {
	childrenByParent := make(map[string][]store.PrioIssue)
	for _, issue := range issues {
		if issue.ParentKey != "" {
			childrenByParent[issue.ParentKey] = append(childrenByParent[issue.ParentKey], issue)
		}
	}
	suggestions := make(map[string]*prioEpicSuggestion)
	for _, issue := range issues {
		if !strings.EqualFold(issue.Type, "Epic") {
			continue
		}
		children := childrenByParent[issue.Key]
		byGroup := make(map[string][]prioEpicChild)
		for _, child := range children {
			group := prioStatusGroup(child.Status)
			if strings.EqualFold(child.Status, "Canceled") {
				group = "canceled"
			}
			byGroup[group] = append(byGroup[group], prioEpicChild{
				Key: child.Key, Summary: child.Summary, Status: child.Status, Href: s.prioRelatedIssueHref(child.Key),
			})
		}

		suggestion := &prioEpicSuggestion{ChildCount: len(children)}
		relevant := len(children) - len(byGroup["canceled"])
		switch status := prioStatusGroup(issue.Status); {
		case status == statusPlanned && len(byGroup["doing"]) > 0:
			suggestion.TargetGroup, suggestion.Label = "doing", "In Arbeit"
		case status == "doing" && relevant > 0 && len(byGroup["done"]) == relevant:
			suggestion.TargetGroup, suggestion.Label = "done", "Fertig"
		default:
			continue
		}
		for _, group := range []struct{ id, label string }{
			{statusPlanned, "Offen"}, {"doing", "In Arbeit"}, {"done", "Fertig"},
			{"canceled", "Canceled"}, {"other", "Weitere Status"},
		} {
			if children := byGroup[group.id]; len(children) > 0 || group.id == statusPlanned || group.id == "doing" || group.id == "done" {
				suggestion.Children = append(suggestion.Children, prioEpicChildGroup{
					Label: group.label, Canceled: group.id == "canceled", Issues: children,
				})
			}
		}
		suggestions[issue.Key] = suggestion
	}
	return suggestions
}

func (panel *prioTicketPanel) showEpicSuggestion(suggestion *prioEpicSuggestion) {
	panel.EpicSuggestion = suggestion
	other := panel.Transitions[:0]
	for _, transition := range panel.Transitions {
		if prioStatusGroup(transition.Target) == suggestion.TargetGroup {
			suggestion.Transitions = append(suggestion.Transitions, transition)
		} else {
			other = append(other, transition)
		}
	}
	panel.Transitions = other
}
