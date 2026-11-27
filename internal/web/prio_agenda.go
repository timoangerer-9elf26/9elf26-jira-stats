package web

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

const (
	prioOverview      = "overview"
	prioEpicCheck     = "epic-check"
	prioDone          = "done"
	prioNew           = "new"
	prioBacklog       = "backlog"
	prioDefaultPreset = "10519"
	prioLocalPreset   = "all"
	prioCutoffInput   = "2006-01-02T15:04"
)

type prioAgendaStep struct {
	ID, Label, Heading, Href string
	Active                   bool
	Rows                     []prioRow
	Total                    int
}

type prioAgendaView struct {
	CanonicalHref                          string
	PriorityChange                         string
	Groups                                 []prioGroup
	IssueCount, OverviewCount              int
	SortOptions, TechnicalOptions          []prioAgendaOption
	ParentHref, CloseHref, ShareCutoffHref string
	ParentOnly, InvalidCutoff              bool
	Current                                prioAgendaStep
	Selected                               *prioTicketPanel
	SelectedKey                            string
	BacklogSortHref, PrevHref, NextHref    string
	OldestFirst                            bool
	CutoffParams                           []filterParam
	Steps                                  []prioAgendaStep
	Step                                   string
	Cutoff                                 time.Time
	CutoffInput, CutoffSource              string
	Params                                 []filterParam
	Overview                               bool
	Preset                                 string
	Meeting                                store.PrioMeeting
	MeetingEnd, MeetingLabel               string
	CanComplete                            bool
	RecentHref, AllHref                    string
	CanCreateEpic                          bool
	TicketResult                           prioTicketResult
}

func (s *Server) prioAgenda(ctx context.Context, q url.Values, edit prioEdit) (prioAgendaView, error) {
	q = prioAgendaParams(q)
	steps := []prioAgendaStep{
		{ID: prioOverview, Label: "Übersicht", Total: 1},
		{ID: prioEpicCheck, Label: "Epic-Prüfung", Heading: "Epic-Prüfung"},
		{ID: prioDone, Label: "Fertig", Heading: "Fertig seit letztem Meeting (nur Epics)"},
		{ID: prioNew, Label: "Neu", Heading: "Neu seit Stichtag"},
		{ID: prioBacklog, Label: "Backlog", Heading: "Backlog priorisieren"},
	}
	index := 0
	for i, candidate := range steps {
		if candidate.ID == q.Get("step") {
			index = i
		}
	}
	step := steps[index].ID

	cutoff := s.now().In(s.prioLocation).AddDate(0, 0, -7)
	source := "Vor sieben Tagen"
	meeting, err := s.rollups.LastPrioMeeting()
	if err != nil {
		return prioAgendaView{}, err
	}
	if meeting.ID != 0 {
		cutoff = meeting.EndedAt
		source = "Ende des letzten Meetings"
	}
	invalidCutoff := false
	if requested, err := time.Parse(time.RFC3339Nano, q.Get("cutoff")); err == nil {
		cutoff = requested
		source = "Gewählter Stichtag"
		q.Set("cutoff", requested.UTC().Format(time.RFC3339Nano))
	} else if requested, err := time.ParseInLocation(prioCutoffInput, q.Get("cutoff"), s.prioLocation); err == nil && requested.In(s.prioLocation).Format(prioCutoffInput) == q.Get("cutoff") {
		cutoff = requested
		source = "Gewählter Stichtag"
		q.Set("cutoff", requested.UTC().Format(time.RFC3339Nano))
	} else {
		invalidCutoff = q.Has("cutoff")
		q.Del("cutoff")
	}
	q.Set("step", step)
	preset := q.Get("preset")
	if preset == "" {
		preset = prioDefaultPreset
	}
	q.Set("preset", preset)

	identity, _ := JiraIdentityFromContext(ctx)
	view := prioAgendaView{
		Meeting: meeting, CanComplete: identity.AccountID != "",
		Step: step, Cutoff: cutoff, OldestFirst: q.Get("backlog-sort") == "age",
		CutoffInput: cutoff.In(s.prioLocation).Format(prioCutoffInput), CutoffSource: source,
		Overview: step == prioOverview, Preset: preset, InvalidCutoff: invalidCutoff,
	}
	_, _, view.CanCreateEpic = s.prioTicketClient(ctx)
	view.TicketResult, _ = ctx.Value(prioTicketResultKey{}).(prioTicketResult)
	issues, err := s.rollups.PrioIssues()
	if err != nil {
		return prioAgendaView{}, err
	}
	changes, err := s.rollups.IssueChangesSince(cutoff)
	if err != nil {
		return prioAgendaView{}, err
	}
	var overviewRows []prioRow
	for _, issue := range issues {
		events := prioEvents(issue, changes[issue.Key], cutoff)
		row := s.makePrioRow(issue, edit)
		row.Markers = events.markers()
		row.SelectHref = prioHref(q, "ticket", issue.Key)
		if view.Overview && keepPrioOverviewRow(q, issue, events) {
			overviewRows = append(overviewRows, row)
		}
		if strings.EqualFold(issue.Type, "Epic") && events.DoneFromOpen {
			steps[2].Rows = append(steps[2].Rows, row)
		}
		if events.New {
			steps[3].Rows = append(steps[3].Rows, row)
		}
		if prioStatusGroup(issue.Status) == statusPlanned {
			steps[4].Rows = append(steps[4].Rows, row)
		}
	}
	for i := range steps {
		stepParams := prioAgendaParams(q)
		stepParams.Del("ticket")
		steps[i].Href = prioHref(stepParams, "step", steps[i].ID)
		steps[i].Active = i == index
		if i != 0 {
			steps[i].Total = len(steps[i].Rows)
		}
	}
	current := steps[index]
	sortAgendaRows(current.Rows, step == prioBacklog && view.OldestFirst)
	selection := q.Get("ticket")
	if selection != "" {
		view.Selected, err = s.prioTicketPanel(ctx, selection, changes[selection], cutoff, edit)
		if err != nil {
			return prioAgendaView{}, err
		}
		if view.Selected == nil && index != 0 {
			selection = ""
		}
	}
	if index != 0 && selection == "" && len(current.Rows) > 0 {
		selection = current.Rows[0].Key
		view.Selected, err = s.prioTicketPanel(ctx, selection, changes[selection], cutoff, edit)
		if err != nil {
			return prioAgendaView{}, err
		}
	}
	selectedIndex := slices.IndexFunc(current.Rows, func(row prioRow) bool { return row.Key == selection })
	if view.Selected != nil {
		view.Selected.Focused = !view.Overview
		view.Selected.Result = view.TicketResult
	}
	view.SelectedKey = selection
	if edit.Key != "" && edit.Error == "" {
		for _, issue := range issues {
			if issue.Key == edit.Key {
				view.PriorityChange = issue.Key + " · Priorität " + prioLevelLabel(issue.Priority)
				break
			}
		}
	}
	if selection != "" {
		q.Set("ticket", selection)
	} else {
		q.Del("ticket")
	}
	view.Steps, view.Current = steps, current
	if view.Overview {
		if err := s.populatePrioOverview(&view, q, overviewRows, len(issues)); err != nil {
			return prioAgendaView{}, err
		}
	}
	view.CanonicalHref = "/prio?" + q.Encode()
	view.ShareCutoffHref = prioHref(q, "cutoff", cutoff.UTC().Format(time.RFC3339Nano))
	view.RecentHref = prioHref(q, "preset", prioDefaultPreset)
	view.AllHref = prioHref(q, "preset", prioLocalPreset)
	view.BacklogSortHref = prioHref(q, "backlog-sort", "age")
	if view.OldestFirst {
		view.BacklogSortHref = prioHref(q, "backlog-sort", "")
	}
	if index > 0 {
		view.PrevHref = steps[index-1].Href
		if selectedIndex > 0 {
			view.PrevHref = current.Rows[selectedIndex-1].SelectHref
		} else if index > 1 && steps[index-1].Total == 0 && steps[index-1].ID == prioEpicCheck {
			view.PrevHref = steps[index-2].Href
		}
	}
	if index > 0 && selectedIndex+1 < len(current.Rows) {
		view.NextHref = current.Rows[selectedIndex+1].SelectHref
	} else if index == 0 && steps[1].Total == 0 {
		view.NextHref = steps[2].Href
	} else if index+1 < len(steps) {
		view.NextHref = steps[index+1].Href
	}
	for _, name := range prioAgendaParamNames {
		if value := q.Get(name); value != "" {
			param := filterParam{Name: name, Value: value}
			view.Params = append(view.Params, param)
			if name != "cutoff" {
				view.CutoffParams = append(view.CutoffParams, param)
			}
		}
	}
	if view.Selected != nil {
		view.Selected.Params = view.Params
	}
	if meeting.ID != 0 {
		view.MeetingEnd = meeting.EndedAt.Format(time.RFC3339Nano)
		view.MeetingLabel = meeting.EndedAt.In(s.prioLocation).Format("02.01.2006 15:04")
	}
	return view, nil
}

var prioAgendaParamNames = []string{"step", "cutoff", "preset", "ticket", "sort", "backlog-sort", "tech", "parent"}

func prioAgendaParams(q url.Values) url.Values {
	params := make(url.Values, len(prioAgendaParamNames))
	for _, name := range prioAgendaParamNames {
		if value := q.Get(name); value != "" {
			params.Set(name, value)
		}
	}
	return params
}

func prioHref(q url.Values, name, value string) string {
	params := make(url.Values, len(q)+1)
	for name, values := range q {
		params[name] = values
	}
	if value == "" {
		params.Del(name)
	} else {
		params.Set(name, value)
	}
	return "/prio?" + params.Encode()
}
