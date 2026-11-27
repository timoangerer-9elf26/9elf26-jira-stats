package web

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

const prioPresetCacheTTL = 45 * time.Second

type planningPresetClient interface {
	PlanningPresets(context.Context) ([]jira.PlanningPreset, error)
	SearchIssueKeys(context.Context, string) ([]string, error)
}

type prioPresetView struct {
	PresetOptions []prioAgendaOption
	PresetError   string
	Missing       []prioMissingTicket
	JiraOrder     bool
}

type prioMissingTicket struct{ Key, Href string }

type prioQueryKey struct{ account, jql string }

type prioCacheEntry[V any] struct {
	value   V
	expires time.Time
}

type prioCache[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]prioCacheEntry[V]
}

func (c *prioCache[K, V]) get(key K, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	return entry.value, now.Before(entry.expires)
}

func (c *prioCache[K, V]) put(key K, value V, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[K]prioCacheEntry[V])
	}
	for oldKey, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, oldKey)
		}
	}
	c.entries[key] = prioCacheEntry[V]{value, now.Add(prioPresetCacheTTL)}
}

func (s *Server) prioPresetKeys(ctx context.Context, view *prioAgendaView, q url.Values) []string {
	identity, _ := JiraIdentityFromContext(ctx)
	personal, _ := PersonalJiraFromContext(ctx)
	client, ok := personal.(planningPresetClient)
	if !ok || identity.AccountID == "" {
		if view.Preset != prioLocalPreset {
			view.PresetError = "Jira-Filter sind derzeit nicht verfügbar."
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filters, cached := s.prioFiltersCache.get(identity.AccountID, s.now())
	if !cached {
		fetched, err := client.PlanningPresets(ctx)
		if err != nil {
			view.PresetError = "Jira-Filter konnten nicht geladen werden."
			return nil
		}
		filters = fetched
		s.prioFiltersCache.put(identity.AccountID, filters, s.now())
	}
	for _, filter := range filters {
		if filter.ID != prioDefaultPreset {
			view.PresetOptions = append(view.PresetOptions, prioAgendaOption{
				Label: filter.Name, Href: prioHref(q, "preset", filter.ID), Active: filter.ID == view.Preset,
			})
		}
	}
	if view.Preset == prioLocalPreset {
		return nil
	}
	i := slices.IndexFunc(filters, func(filter jira.PlanningPreset) bool { return filter.ID == view.Preset })
	if i < 0 {
		view.PresetError = "Dieser Jira-Filter ist für dein Konto nicht verfügbar."
		return nil
	}
	jql, err := prioPresetJQL(filters[i], view.Cutoff, identity.TimeZone)
	if err != nil {
		view.PresetError = "Konfigurationsfehler im Jira-Filter. " + err.Error()
		return nil
	}
	key := prioQueryKey{identity.AccountID, jql}
	keys, cached := s.prioKeysCache.get(key, s.now())
	if !cached {
		keys, err = client.SearchIssueKeys(ctx, jql)
		if err != nil {
			view.PresetError = "Jira-Abfrage fehlgeschlagen."
			return nil
		}
		s.prioKeysCache.put(key, keys, s.now())
	}
	view.JiraOrder = true
	return keys
}

func (s *Server) orderPrioPresetRows(view *prioAgendaView, rows []prioRow, keys []string, issues []store.PrioIssue) []prioRow {
	if len(keys) == 0 {
		return nil
	}
	known := make(map[string]bool, len(issues))
	for _, issue := range issues {
		known[issue.Key] = true
	}
	byKey := make(map[string]prioRow, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}
	ordered := make([]prioRow, 0, len(rows))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		number, dcai := strings.CutPrefix(key, "DCAI-")
		if _, err := strconv.ParseUint(number, 10, 64); !dcai || err != nil || seen[key] {
			continue
		}
		seen[key] = true
		if row, found := byKey[key]; found {
			ordered = append(ordered, row)
		} else if !known[key] {
			view.Missing = append(view.Missing, prioMissingTicket{Key: key, Href: s.jiraIssueURL(key)})
		}
	}
	return ordered
}

var (
	prioCutoffMarker = regexp.MustCompile(`\bstichtag:[ \t]*"([^"\r\n]+)"`)
	// Keep quoted strings, including escapes, as whole tokens. An AFTER inside
	// a string cannot be a history predicate.
	prioJQLTokens = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[\pL\pN_$]+|[^\s]`)
)

func prioPresetJQL(filter jira.PlanningPreset, cutoff time.Time, loc *time.Location) (string, error) {
	if strings.TrimSpace(filter.JQL) == "" {
		return "", errors.New("Die gespeicherte Abfrage fehlt.")
	}
	markers := prioCutoffMarker.FindAllStringSubmatch(filter.Description, 2)
	if len(markers) == 0 {
		return filter.JQL, nil
	}
	if len(markers) > 1 {
		return "", errors.New("Die Filterbeschreibung enthält mehrere Stichtag-Marker.")
	}
	if loc == nil {
		return "", errors.New("Die Jira-Zeitzone fehlt.")
	}
	literal := markers[0][1]
	date := cutoff.In(loc).Format("2006-01-02 15:04")
	var rewritten strings.Builder
	end := 0
	after := false
	for _, span := range prioJQLTokens.FindAllStringIndex(filter.JQL, -1) {
		start, stop := span[0], span[1]
		token := filter.JQL[start:stop]
		if after && (token == `"`+literal+`"` || token == "'"+literal+"'") {
			rewritten.WriteString(filter.JQL[end : start+1])
			rewritten.WriteString(date)
			end = stop - 1
		}
		after = strings.EqualFold(token, "AFTER")
	}
	if end == 0 {
		return "", errors.New("Zum Stichtag-Marker fehlt ein passender AFTER-Datumswert.")
	}
	rewritten.WriteString(filter.JQL[end:])
	return rewritten.String(), nil
}
