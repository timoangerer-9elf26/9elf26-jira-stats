package web

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioMarker struct {
	Kind, Label string
}

type prioIssueEvents struct {
	New, StatusChanged, Done, DoneFromOpen, Resolved bool
	PriorityChanges                                  []prioMarker
}

func prioStatusGroup(status string) string {
	for _, category := range prioStatusCategories {
		if category.Value != "all" && category.Keeps(status) {
			return category.Value
		}
	}
	return "other"
}

func prioEvents(issue store.PrioIssue, changes []jira.ChangelogEntry, cutoff time.Time) prioIssueEvents {
	events := prioIssueEvents{
		New:      issue.CreatedAt.After(cutoff),
		Resolved: issue.ResolvedAt.After(cutoff),
	}
	for _, change := range changes {
		if !change.Timestamp.After(cutoff) {
			continue
		}
		switch change.Field {
		case "status":
			events.StatusChanged = true
			from, to := prioStatusGroup(change.From), prioStatusGroup(change.To)
			if from != "done" && to == "done" {
				events.Done = true
				events.DoneFromOpen = events.DoneFromOpen || from == statusPlanned || from == "doing"
			}
		case "priority":
			direction := "prio"
			switch {
			case priorityRank(change.To) < priorityRank(change.From):
				direction = "raised"
			case priorityRank(change.To) > priorityRank(change.From):
				direction = "lowered"
			}
			events.PriorityChanges = append(events.PriorityChanges, prioMarker{direction, changeLabel(change)})
		case "resolution":
			events.Resolved = events.Resolved || (change.To != "" && !strings.EqualFold(change.To, "unresolved")) || change.ToID != ""
		}
	}
	return events
}

func (events prioIssueEvents) markers() []prioMarker {
	var markers []prioMarker
	if events.New {
		markers = append(markers, prioMarker{"new", "Neu"})
	}
	if events.StatusChanged {
		markers = append(markers, prioMarker{"status", "Status geändert"})
	}
	if events.Done {
		markers = append(markers, prioMarker{"done", "Fertig"})
	}
	if events.Resolved {
		markers = append(markers, prioMarker{"resolved", "Aufgelöst"})
	}
	return append(markers, events.PriorityChanges...)
}

func changeLabel(change jira.ChangelogEntry) string {
	from, to := change.From, change.To
	switch change.Field {
	case "priority":
		return "Prio geändert · " + prioLevelLabel(from) + " → " + prioLevelLabel(to)
	case "resolution":
		return "Auflösung · " + prioChangeValue(from) + " → " + prioChangeValue(to)
	default:
		return prioChangeValue(from) + " → " + prioChangeValue(to)
	}
}

func prioLevelLabel(level string) string {
	switch level {
	case "Highest":
		return "Höchste"
	case "High":
		return "Hoch"
	case "Medium":
		return "Mittel"
	case "Low":
		return "Niedrig"
	case "Lowest":
		return "Niedrigste"
	default:
		return prioChangeValue(level)
	}
}

func prioChangeValue(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func compareAgendaPriority(a, b prioRow) int {
	if order := cmp.Compare(priorityRank(a.Priority), priorityRank(b.Priority)); order != 0 {
		return order
	}
	// The project key suffix is Jira's issue number (DCAI-9 before DCAI-10).
	aNumber, aErr := strconv.Atoi(strings.TrimPrefix(a.Key, "DCAI-"))
	bNumber, bErr := strconv.Atoi(strings.TrimPrefix(b.Key, "DCAI-"))
	if aErr == nil && bErr == nil && aNumber != bNumber {
		return cmp.Compare(aNumber, bNumber)
	}
	if (aErr == nil) != (bErr == nil) {
		if aErr == nil {
			return -1
		}
		return 1
	}
	return cmp.Compare(a.Key, b.Key)
}

func sortAgendaRows(rows []prioRow, oldestFirst bool) {
	slices.SortStableFunc(rows, func(a, b prioRow) int {
		if oldestFirst && !a.CreatedAt.Equal(b.CreatedAt) {
			if a.CreatedAt.IsZero() {
				return 1
			}
			if b.CreatedAt.IsZero() {
				return -1
			}
			return a.CreatedAt.Compare(b.CreatedAt)
		}
		return compareAgendaPriority(a, b)
	})
}

type prioHistoryItem struct {
	Instant, TimeLabel, Author, Label string
}

func prioHistory(changes []jira.ChangelogEntry, cutoff time.Time, loc *time.Location) []prioHistoryItem {
	var history []prioHistoryItem
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		if !change.Timestamp.After(cutoff) {
			continue
		}
		author := change.Author.DisplayName
		if author == "" {
			author = change.Author.AccountID
		}
		history = append(history, prioHistoryItem{
			Instant:   change.Timestamp.Format(time.RFC3339Nano),
			TimeLabel: change.Timestamp.In(loc).Format("02.01.2006 15:04"),
			Author:    author,
			Label:     changeLabel(change),
		})
	}
	return history
}
