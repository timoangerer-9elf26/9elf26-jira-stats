package web

import (
	"context"
	"net/url"
	"slices"
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
	prioPresetView
	CanonicalHref                          string
	MeetingChanges                         []prioMeetingChangeView
	Groups                                 []prioGroup
	Page                                   prioOverviewPage
	IssueCount, OverviewCount              int
	SortOptions                            []prioAgendaOption
	Filters                                prioAgendaFilters
	FilterParams                           []filterParam
	FilterSummary, FilterHref, ResetHref   string
	CloseHref, ShareCutoffHref             string
	InvalidCutoff                          bool
	Current                                prioAgendaStep
	Selected                               *prioTicketPanel
	SelectedKey                            string
	BacklogSortHref, PrevHref, NextHref    string
	OldestFirst                            bool
	CutoffParams                           []filterParam
	Steps                                  []prioAgendaStep
	Step                                   string
	Cutoff                                 time.Time
	CutoffInput, CutoffSource, CutoffLabel string
	Params                                 []filterParam
	Overview                               bool
	Preset                                 string
	Meeting                                store.PrioMeeting
	MeetingEnd, MeetingLabel               string
	CanComplete                            bool
	AllHref                                string
	CanCreateEpic                          bool
	TicketResult                           prioTicketResult
}

func (s *Server) prioAgenda(ctx context.Context, q url.Values, edit prioEdit) (prioAgendaView, error) {
	q = prioAgendaParams(q)
	steps := []prioAgendaStep{
		{ID: prioOverview, Label: "Übersicht", Total: 1},
		{ID: prioEpicCheck, Label: "Epic-Prüfung", Heading: "Epic-Prüfung"},
		{ID: prioDone, Label: "Fertig", Heading: "Fertig seit Stichtag"},
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

	cutoffView, err := s.prioCutoff(q)
	if err != nil {
		return prioAgendaView{}, err
	}
	meeting, cutoff := cutoffView.meeting, cutoffView.at
	if step != prioOverview {
		q.Del("page")
	}
	q.Set("step", step)
	filters := prioAgendaFiltersFor(q)
	preset := q.Get("preset")
	if preset == "" {
		preset = prioLocalPreset
	}
	q.Set("preset", preset)

	identity, _ := JiraIdentityFromContext(ctx)
	view := prioAgendaView{
		Meeting: meeting, CanComplete: identity.AccountID != "",
		Step: step, Cutoff: cutoff, OldestFirst: q.Get("backlog-sort") == "age",
		CutoffInput: cutoff.In(s.prioLocation).Format(prioCutoffInput), CutoffSource: cutoffView.source,
		CutoffLabel: cutoff.In(s.prioLocation).Format("02.01.2006 15:04 MST"),
		Overview:    step == prioOverview, Preset: preset, InvalidCutoff: cutoffView.invalid, Filters: filters,
	}
	view.MeetingChanges, err = s.prioMeetingChanges(meeting.ID)
	if err != nil {
		return prioAgendaView{}, err
	}
	_, _, view.CanCreateEpic = s.prioTicketClient(ctx)
	view.TicketResult, _ = ctx.Value(prioTicketResultKey{}).(prioTicketResult)
	presetLoad, loadingPreset := ctx.Value(prioPresetLoadKey{}).(prioPresetLoad)
	view.TicketResult.Preserve = loadingPreset
	var keys []string
	if view.Overview || preset != prioLocalPreset {
		keys = s.prioPresetKeys(ctx, &view, q)
	}
	issues, err := s.rollups.PrioIssues()
	if err != nil {
		return prioAgendaView{}, err
	}
	changes, err := s.rollups.IssueChangesSince(cutoff)
	if err != nil {
		return prioAgendaView{}, err
	}
	presetMatches := make(map[string]bool, len(keys))
	for _, key := range keys {
		presetMatches[key] = true
	}
	view.Filters.Jira = view.prioJiraFilterOption()
	view.FilterSummary = view.Filters.summary()
	suggestions := s.prioEpicSuggestions(issues)
	var overviewRows []prioRow
	var reviewedEpic prioRow
	selection := q.Get("ticket")
	for _, issue := range issues {
		events := prioEvents(issue, changes[issue.Key], cutoff)
		row := s.makePrioRow(issue, edit)
		row.Markers = events.markers()
		if step == prioEpicCheck && view.TicketResult.Change != "" && issue.Key == selection {
			reviewedEpic = row
		}
		if suggestions[issue.Key] != nil {
			steps[1].Rows = append(steps[1].Rows, row)
		}
		if !filters.keeps(issue, events) {
			continue
		}
		// The overview orders the Jira matches and also reports keys still missing locally.
		if view.Overview {
			overviewRows = append(overviewRows, row)
		}
		if preset != prioLocalPreset && !presetMatches[issue.Key] {
			continue
		}
		if events.DoneFromOpen {
			steps[2].Rows = append(steps[2].Rows, row)
		}
		if events.New {
			steps[3].Rows = append(steps[3].Rows, row)
		}
		if prioStatusGroup(issue.Status) == statusPlanned {
			steps[4].Rows = append(steps[4].Rows, row)
		}
	}
	if reviewedEpic.Key != "" && len(steps[index].Rows) == 0 {
		index++
		step = steps[index].ID
		view.Step = step
		q.Set("step", step)
		if !slices.ContainsFunc(steps[index].Rows, func(row prioRow) bool { return row.Key == selection }) {
			selection = ""
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
	for i := range current.Rows {
		current.Rows[i].SelectHref = prioHref(q, "ticket", current.Rows[i].Key)
	}
	keepSelection := step != prioEpicCheck && (view.PresetLoadingResults() || presetLoad.keepTicket ||
		view.TicketResult.Change != "" || view.TicketResult.Message != "")
	if !view.Overview && !keepSelection && !slices.ContainsFunc(current.Rows, func(row prioRow) bool { return row.Key == selection }) {
		selection = ""
		if reviewedEpic.Key != "" {
			next := slices.IndexFunc(current.Rows, func(row prioRow) bool { return compareAgendaPriority(reviewedEpic, row) < 0 })
			if next >= 0 {
				selection = current.Rows[next].Key
			}
		}
	}
	if !view.Overview && selection == "" && len(current.Rows) > 0 {
		selection = current.Rows[0].Key
	}
	if selection != "" {
		view.Selected, err = s.prioTicketPanel(ctx, selection, changes[selection], cutoff, edit)
		if err != nil {
			return prioAgendaView{}, err
		}
	}
	selectedIndex := slices.IndexFunc(current.Rows, func(row prioRow) bool { return row.Key == selection })
	if view.Selected != nil {
		view.Selected.Focused = !view.Overview
		view.Selected.PreserveControls = loadingPreset
		view.Selected.Result = view.TicketResult
		if step == prioEpicCheck && suggestions[selection] != nil {
			view.Selected.showEpicSuggestion(suggestions[selection])
		}
	}
	view.SelectedKey = selection
	if selection != "" {
		q.Set("ticket", selection)
	} else {
		q.Del("ticket")
	}
	view.Steps, view.Current = steps, current
	if view.Overview {
		if err := s.populatePrioOverview(&view, q, overviewRows, issues, keys); err != nil {
			return prioAgendaView{}, err
		}
	}
	view.CanonicalHref = "/prio?" + q.Encode()
	view.ShareCutoffHref = prioHref(q, "cutoff", cutoff.UTC().Format(time.RFC3339Nano))
	view.AllHref = prioHref(q, "preset", prioLocalPreset)
	overviewParams := prioAgendaParams(q)
	overviewParams.Del("ticket")
	view.FilterHref = prioHref(overviewParams, "step", prioOverview) + "#prio-filter-controls"
	resetParams := prioAgendaParams(q)
	for _, name := range prioAgendaFilterNames {
		resetParams.Del(name)
	}
	view.ResetHref = prioHref(resetParams, "step", prioOverview) + "#prio-filter-controls"
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
	view.Params = prioParamsList(q)
	for _, param := range view.Params {
		if param.Name != "page" && !slices.Contains(prioAgendaFilterNames, param.Name) {
			view.FilterParams = append(view.FilterParams, param)
		}
		if param.Name != "page" && param.Name != "cutoff" {
			view.CutoffParams = append(view.CutoffParams, param)
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

var prioAgendaFilterNames = []string{"period", "level", "tech", "preset"}
var prioAgendaParamNames = append([]string{"step", "cutoff", "ticket", "sort", "backlog-sort", "page"}, prioAgendaFilterNames...)

func prioAgendaParams(q url.Values) url.Values {
	params := make(url.Values, len(prioAgendaParamNames))
	for _, name := range prioAgendaParamNames {
		if value := q.Get(name); value != "" {
			params.Set(name, value)
		}
	}
	// Old agenda URLs used tech/parent and the original table used these
	// toggle names. The short parameters take precedence when both are present.
	if !params.Has("tech") && q.Has(nonTechnicalParam) && q.Get(nonTechnicalParam) != nonTechnicalOff {
		params.Set("tech", "non-technical")
	}
	if !params.Has("level") {
		switch {
		case q.Get("parent-filter") == "1", q.Get("parent") == "1",
			q.Has(noParentParam) && q.Get(noParentParam) != noParentOff:
			params.Set("level", "unparented")
		case q.Get("parent-filter") == "0", q.Get("parent") == "0", q.Get(noParentParam) == noParentOff:
			params.Set("level", "all")
		}
	}
	return params
}

func prioHref(q url.Values, name, value string) string {
	params := make(url.Values, len(q)+1)
	for name, values := range q {
		params[name] = values
	}
	if name != "ticket" && name != "page" {
		params.Del("page")
	}
	if value == "" {
		params.Del(name)
	} else {
		params.Set(name, value)
	}
	return "/prio?" + params.Encode()
}
