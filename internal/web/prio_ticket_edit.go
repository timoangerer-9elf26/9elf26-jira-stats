package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioTicketForm struct {
	Name, Label, Kind, Current, Revision string
	Required, HasCurrent                 bool
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

func (s *Server) handlePrioTicketControls(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, actions, enabled := s.prioTicketClient(ctx)
	if !enabled {
		http.Error(w, "Persönliche Jira-Anmeldung erforderlich.", http.StatusForbidden)
		return
	}
	q := prioAgendaParams(r.URL.Query())
	key := q.Get("ticket")
	if !dcaiIssueKey.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	detail, found, err := s.rollups.IssueDetail(key)
	if err != nil {
		s.renderError(w)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	panel := prioTicketPanel{Row: s.makePrioRow(detail.PrioIssue, prioEdit{}), Detail: detail, CanWrite: true, Params: prioParamsList(q)}
	s.populatePrioTicketActions(ctx, actions, &panel)
	if q.Get("step") == prioEpicCheck {
		issues, err := s.rollups.PrioIssues()
		if err != nil {
			s.renderError(w)
			return
		}
		if suggestion := s.prioEpicSuggestions(issues)[key]; suggestion != nil {
			panel.showEpicSuggestion(suggestion)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "prio-ticket-controls", &panel); err != nil {
		log.Printf("web: Prio ticket controls: %v", err)
	}
}

func (s *Server) populatePrioTicketActions(ctx context.Context, actions jira.IssueActions, panel *prioTicketPanel) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var fields map[string]jira.EditField
	var fieldsErr error
	var requests sync.WaitGroup
	requests.Go(func() {
		fields, fieldsErr = actions.FetchEditFields(ctx, panel.Row.Key)
	})
	transitions, transitionsErr := actions.FetchIssueTransitions(ctx, panel.Row.Key)
	requests.Wait()
	if fieldsErr != nil {
		panel.MetadataError = "Jira-Bearbeitungsfelder konnten nicht geladen werden."
		log.Printf("web: Prio edit metadata %s: %v", panel.Row.Key, fieldsErr)
	} else {
		panel.Forms = s.prioTicketForms(panel.Detail, fields)
	}
	if transitionsErr != nil {
		panel.TransitionsUnavailable = true
		panel.MetadataError += " Jira-Statusübergänge konnten nicht geladen werden."
		log.Printf("web: Prio transition metadata %s: %v", panel.Row.Key, transitionsErr)
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
		{Name: "parent", Label: "Parent / Epic", Kind: "select", Current: detail.ParentKey},
	}
	var available []prioTicketForm
	for _, form := range forms {
		field := fields[prioJiraField(form.Name)]
		if !slices.Contains(field.Operations, "set") || (form.Name == "description" && !plain) {
			continue
		}
		form.Required = form.Required || field.Required
		if form.Kind == "select" {
			if form.Name == "parent" {
				form.Options = s.prioParentOptions(detail, field)
			} else {
				form.Options = s.prioTicketOptions(form.Name, field)
			}
			form.HasCurrent = slices.ContainsFunc(form.Options, func(option prioTicketOption) bool { return option.Value == form.Current })
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

func (s *Server) prioParentOptions(detail store.IssueDetail, field jira.EditField) []prioTicketOption {
	var options []prioTicketOption
	if !field.Required {
		options = append(options, prioTicketOption{Label: "Kein Parent"})
	}
	if len(field.AllowedValues) != 0 {
		for _, option := range field.AllowedValues {
			if option.Disabled {
				continue
			}
			value := option.Key
			if value == "" {
				value = option.Value
			}
			if !jiraIssueKey.MatchString(value) {
				value = option.ID
				if !jiraIssueID.MatchString(value) {
					continue
				}
			}
			if value == detail.Key {
				continue
			}
			label := value
			if option.Summary != "" {
				label += " · " + option.Summary
			}
			options = append(options, prioTicketOption{value, label})
		}
		return options
	}
	// Jira omitted choices. Offer projected DCAI Epics for work items and
	// preserve the current parent even if the parent itself is not projected.
	issues, err := s.rollups.PrioIssues()
	if err == nil {
		for _, candidate := range issues {
			if candidate.Key == detail.Key || !dcaiIssueKey.MatchString(candidate.Key) {
				continue
			}
			if candidate.Key == detail.ParentKey || (detail.Type != "Epic" && strings.EqualFold(candidate.Type, "Epic")) {
				label := candidate.Key
				if candidate.Summary != "" {
					label += " · " + candidate.Summary
				}
				options = append(options, prioTicketOption{candidate.Key, label})
			}
		}
	}
	if jiraIssueKey.MatchString(detail.ParentKey) && !slices.ContainsFunc(options, func(option prioTicketOption) bool { return option.Value == detail.ParentKey }) {
		options = append(options, prioTicketOption{detail.ParentKey, detail.ParentKey})
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

func prioFieldValue(field prioTicketForm, metadata jira.EditField, value string) (any, error) {
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
		// Prefer the Jira option ID from edit metadata. The legacy inline
		// endpoint sends names from a fixed list and cannot represent these IDs.
		for _, option := range metadata.AllowedValues {
			if option.Name == value && option.ID != "" && !option.Disabled {
				return map[string]string{"id": option.ID}, nil
			}
		}
		return map[string]string{"name": value}, nil
	case "size":
		// Use the field's actual option ID or value from Jira edit metadata.
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
		if jiraIssueID.MatchString(value) {
			return map[string]string{"id": value}, nil
		}
		if value != "" {
			return map[string]string{"key": value}, nil
		}
	}
	return nil, nil
}
