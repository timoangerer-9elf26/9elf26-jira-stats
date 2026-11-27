package web

import (
	"context"
	"crypto/sha256"
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

const (
	prioPresetCacheTTL   = 45 * time.Second
	prioPresetCacheLimit = 256
)

type planningPresetClient interface {
	PlanningPresets(context.Context) ([]jira.PlanningPreset, error)
	SearchIssueKeys(context.Context, string) ([]string, error)
}

type prioPresetView struct {
	PresetOptions               []prioAgendaOption
	PresetError, JiraSearchHref string
	Missing                     []prioMissingTicket
	JiraOrder                   bool
}

type prioMissingTicket struct{ Key, Href string }

type prioQueryKey struct{ account, jql [sha256.Size]byte }

type prioCacheEntry[V any] struct {
	value   V
	expires time.Time
	timer   *time.Timer
}

type prioCache[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]*prioCacheEntry[V]
}

func (c *prioCache[K, V]) get(key K, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(key, now)
}

func (c *prioCache[K, V]) getLocked(key K, now time.Time) (V, bool) {
	if entry := c.entries[key]; entry != nil && now.Before(entry.expires) {
		return entry.value, true
	}
	var zero V
	return zero, false
}

func (c *prioCache[K, V]) put(key K, value V, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, value, now)
}

func (c *prioCache[K, V]) putLocked(key K, value V, now time.Time) {
	if c.entries == nil {
		c.entries = make(map[K]*prioCacheEntry[V])
	}
	c.deleteLocked(key)
	if len(c.entries) >= prioPresetCacheLimit {
		var oldestKey K
		var oldest time.Time
		for candidate, entry := range c.entries {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldestKey, oldest = candidate, entry.expires
			}
		}
		c.deleteLocked(oldestKey)
	}
	entry := &prioCacheEntry[V]{value: value, expires: now.Add(prioPresetCacheTTL)}
	entry.timer = time.AfterFunc(prioPresetCacheTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	})
	c.entries[key] = entry
}

func (c *prioCache[K, V]) deleteLocked(key K) {
	if previous := c.entries[key]; previous != nil {
		previous.timer.Stop()
		delete(c.entries, key)
	}
}

type prioSearchCache struct {
	prioCache[prioQueryKey, []string]
	generation uint64
}

func (c *prioSearchCache) lookup(key prioQueryKey, now time.Time) ([]string, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, found := c.getLocked(key, now)
	return value, found, c.generation
}

func (c *prioSearchCache) store(key prioQueryKey, value []string, now time.Time, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation == generation {
		c.putLocked(key, value, now)
	}
}

func (c *prioSearchCache) invalidate(accountID string) {
	account := sha256.Sum256([]byte(accountID))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for query := range c.entries {
		if query.account == account {
			c.deleteLocked(query)
		}
	}
}

func (s *Server) prioPresetKeys(ctx context.Context, view *prioAgendaView, q url.Values) []string {
	if view.Preset == prioLocalPreset {
		view.JiraSearchHref = s.jiraSearchURL("project = " + jira.PrioProject)
	}
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
	account := sha256.Sum256([]byte(identity.AccountID))
	filters, cached := s.prioFiltersCache.get(account, s.now())
	if !cached {
		fetched, err := client.PlanningPresets(ctx)
		if err != nil {
			view.PresetError = "Jira-Filter konnten nicht geladen werden."
			return nil
		}
		filters = fetched
		s.prioFiltersCache.put(account, filters, s.now())
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
	filter := filters[i]
	var loc *time.Location
	if prioCutoffMarker.MatchString(filter.Description) {
		var err error
		loc, err = identity.JiraTimeZone(ctx)
		if err != nil {
			view.PresetError = "Die Jira-Zeitzone konnte nicht geladen werden."
			return nil
		}
	}
	jql, err := prioPresetJQL(filter, view.Cutoff, loc)
	if err != nil {
		view.PresetError = "Konfigurationsfehler im Jira-Filter. " + err.Error()
		return nil
	}
	view.JiraSearchHref = s.jiraSearchURL(jql)
	key := prioQueryKey{account, sha256.Sum256([]byte(jql))}
	keys, cached, generation := s.prioKeysCache.lookup(key, s.now())
	if !cached {
		keys, err = client.SearchIssueKeys(ctx, jql)
		if err != nil {
			view.PresetError = "Jira-Abfrage fehlgeschlagen."
			return nil
		}
		s.prioKeysCache.store(key, keys, s.now(), generation)
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

func (s *Server) jiraSearchURL(jql string) string {
	if s.jiraBaseURL == "" {
		return ""
	}
	return s.jiraBaseURL + "/issues/?" + url.Values{"jql": {jql}}.Encode()
}
