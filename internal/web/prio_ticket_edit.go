package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioTicketForm struct {
	Name, Label, Kind, Current, Revision string
	Required                             bool
	Options                              []prioTicketOption
}

type prioTicketOption struct {
	Value, Label string
}

type prioTicketTransition struct {
	ID, TargetID, Target string
	Required             []string
	Complex              bool
}

func (s *Server) populatePrioTicketActions(ctx context.Context, panel *prioTicketPanel) {
	_, actions, enabled := s.prioTicketClient(ctx)
	panel.CanWrite = enabled && dcaiIssueKey.MatchString(panel.Row.Key)
	if !panel.CanWrite {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	fields, err := actions.FetchEditFields(ctx, panel.Row.Key)
	if err != nil {
		panel.MetadataError = "Jira-Bearbeitungsfelder konnten nicht geladen werden."
		log.Printf("web: Prio edit metadata %s: %v", panel.Row.Key, err)
	} else {
		panel.Forms = s.prioTicketForms(panel.Detail, fields)
	}
	transitions, err := actions.FetchIssueTransitions(ctx, panel.Row.Key)
	if err != nil {
		panel.MetadataError += " Jira-Statusübergänge konnten nicht geladen werden."
		log.Printf("web: Prio transition metadata %s: %v", panel.Row.Key, err)
		return
	}
	for _, transition := range transitions {
		panel.Transitions = append(panel.Transitions, prioTransitionView(transition))
	}
}

func prioTransitionView(transition jira.IssueTransition) prioTicketTransition {
	view := prioTicketTransition{ID: transition.ID, TargetID: transition.ToStatusID, Target: transition.ToStatusName,
		Complex: transition.HasScreen || len(transition.Fields) > 0}
	for key, field := range transition.Fields {
		if field.Required {
			name := field.Name
			if name == "" {
				name = key
			}
			view.Required = append(view.Required, name)
		}
	}
	sort.Strings(view.Required)
	return view
}

func (s *Server) prioTicketForms(detail store.IssueDetail, fields map[string]jira.EditField) []prioTicketForm {
	description, plain := jira.ADFText(detail.Description)
	forms := []prioTicketForm{
		{Name: "summary", Label: "Titel", Current: detail.Summary, Required: true},
		{Name: "description", Label: "Beschreibung", Kind: "textarea", Current: description, Revision: descriptionRevision(detail)},
		{Name: "priority", Label: "Priorität", Kind: "select", Current: detail.Priority},
		{Name: "assignee", Label: "Zuweisung", Kind: "select", Current: detail.AssigneeAccountID},
		{Name: "size", Label: "T-Shirt-Schätzung", Kind: "select", Current: detail.Size},
		{Name: "labels", Label: "Labels", Current: strings.Join(detail.Labels, " ")},
		{Name: "parent", Label: "Parent / Epic", Current: detail.ParentKey},
	}
	var available []prioTicketForm
	for _, form := range forms {
		field := fields[prioJiraField(form.Name)]
		if !slices.Contains(field.Operations, "set") || (form.Name == "description" && !plain) {
			continue
		}
		form.Required = form.Required || field.Required
		if form.Kind == "select" {
			form.Options = s.prioTicketOptions(form.Name, field)
			if len(form.Options) == 0 {
				continue
			}
		}
		available = append(available, form)
	}
	return available
}

func (s *Server) prioTicketOptions(name string, field jira.EditField) []prioTicketOption {
	var options []prioTicketOption
	if !field.Required && name != "priority" {
		label := "Keine Schätzung"
		if name == "assignee" {
			label = "Nicht zugewiesen"
		}
		options = append(options, prioTicketOption{Label: label})
	}
	if name == "assignee" && len(field.AllowedValues) == 0 {
		members, err := s.rollups.ProjectMembers()
		if err == nil {
			for _, member := range members {
				options = append(options, prioTicketOption{member.AccountID, member.DisplayName})
			}
		}
		return options
	}
	for _, option := range field.AllowedValues {
		if option.Disabled {
			continue
		}
		var value, label string
		switch name {
		case "priority":
			value, label = option.Name, prioLevelLabel(option.Name)
		case "assignee":
			value, label = option.AccountID, option.DisplayName
		case "size":
			value = jira.EstimateSize(option)
			label = value
		}
		if value != "" {
			options = append(options, prioTicketOption{value, label})
		}
	}
	return options
}

func prioJiraField(name string) string {
	if name == "size" {
		return jira.EstimateField
	}
	return name
}

func descriptionRevision(detail store.IssueDetail) string {
	hash := sha256.Sum256(detail.Description)
	return hex.EncodeToString(hash[:])
}

func prioFieldValue(field prioTicketForm, metadata jira.EditField, value, key string) (any, error) {
	if field.Kind == "select" && !slices.ContainsFunc(field.Options, func(option prioTicketOption) bool { return option.Value == value }) {
		return nil, errors.New("Jira bietet diesen Wert für das Feld nicht an.")
	}
	if strings.TrimSpace(value) == "" && field.Required {
		return nil, fmt.Errorf("%s ist ein Pflichtfeld.", field.Label)
	}
	switch field.Name {
	case "summary":
		return strings.TrimSpace(value), nil
	case "description":
		if strings.TrimSpace(value) != "" {
			return jira.PlainADF(value), nil
		}
	case "priority":
		for _, option := range metadata.AllowedValues {
			if option.Name == value && option.ID != "" && !option.Disabled {
				return map[string]string{"id": option.ID}, nil
			}
		}
		return map[string]string{"name": value}, nil
	case "assignee":
		if value != "" {
			return map[string]string{"accountId": value}, nil
		}
	case "size":
		for _, option := range metadata.AllowedValues {
			if jira.EstimateSize(option) == value && value != "" && !option.Disabled {
				if option.ID != "" {
					return map[string]string{"id": option.ID}, nil
				}
				return map[string]string{"value": option.Value}, nil
			}
		}
	case "labels":
		labels := []string{}
		for _, label := range strings.Fields(value) {
			if label != "" && !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		}
		return labels, nil
	case "parent":
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			if !jiraIssueKey.MatchString(value) || value == key {
				return nil, errors.New("Bitte einen gültigen Jira-Key für den Parent eingeben.")
			}
			return map[string]string{"key": value}, nil
		}
	}
	return nil, nil
}
