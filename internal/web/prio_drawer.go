package web

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioMeetingCutoff struct {
	meeting store.PrioMeeting
	at      time.Time
	invalid bool
}

func (s *Server) prioCutoff(q url.Values) (prioMeetingCutoff, error) {
	meeting, err := s.rollups.LastPrioMeeting()
	if err != nil {
		return prioMeetingCutoff{}, err
	}
	cutoff := prioMeetingCutoff{meeting: meeting, at: s.now().In(s.prioLocation).AddDate(0, 0, -7)}
	if meeting.ID != 0 {
		cutoff.at = meeting.EndedAt
	}
	raw := q.Get("cutoff")
	requested, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		requested, err = time.ParseInLocation(prioCutoffInput, raw, s.prioLocation)
		if err != nil || requested.In(s.prioLocation).Format(prioCutoffInput) != raw {
			cutoff.invalid = q.Has("cutoff")
			q.Del("cutoff")
			return cutoff, nil
		}
	}
	cutoff.at = requested
	q.Set("cutoff", requested.UTC().Format(time.RFC3339Nano))
	return cutoff, nil
}

func (s *Server) handlePrioDrawer(w http.ResponseWriter, r *http.Request) {
	q := prioAgendaParams(r.URL.Query())
	q.Set("step", prioOverview)
	view := prioAgendaView{}
	if key := q.Get("ticket"); key != "" {
		cutoff, err := s.prioCutoff(q)
		if err != nil {
			s.renderError(w)
			return
		}
		changes, err := s.rollups.IssueHistorySince(key, cutoff.at)
		if err != nil {
			s.renderError(w)
			return
		}
		panel, err := s.prioTicketPanel(r.Context(), key, changes, cutoff.at, prioEdit{})
		if err != nil {
			s.renderError(w)
			return
		}
		if panel == nil {
			http.NotFound(w, r)
			return
		}
		panel.Params = prioParamsList(q)
		view.Selected = panel
		view.CloseHref = prioHref(q, "ticket", "")
	}
	w.Header().Set("HX-Push-Url", "/prio?"+q.Encode())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "prio-drawer", view); err != nil {
		log.Printf("web: Prio drawer: %v", err)
	}
}

func prioDrawerHref(href string) string {
	return "/prio/drawer?" + strings.TrimPrefix(href, "/prio?")
}

func (view prioAgendaView) DrawerCloseHref() string {
	return prioDrawerHref(view.CloseHref)
}

func (row prioRow) DrawerHref() string {
	return prioDrawerHref(row.SelectHref)
}

func prioParamsList(q url.Values) []filterParam {
	var params []filterParam
	for _, name := range prioAgendaParamNames {
		if value := q.Get(name); value != "" {
			params = append(params, filterParam{Name: name, Value: value})
		}
	}
	return params
}
