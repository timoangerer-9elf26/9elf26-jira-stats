package web

import (
	"context"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

type prioTicketPanel struct {
	Row              prioRow
	Detail           store.IssueDetail
	History          []prioHistoryItem
	Description      string
	PlainDescription bool
	ParentHref       string
	Links            []prioTicketLink
	Comments         []prioTicketComment
	CanWrite         bool
	MetadataError    string
	Forms            []prioTicketForm
	Transitions      []prioTicketTransition
	EpicSuggestion   *prioEpicSuggestion
	Params           []filterParam
	Focused          bool
	Result           prioTicketResult
}

func (s *Server) prioTicketPanel(ctx context.Context, key string, changes []jira.ChangelogEntry, cutoff time.Time, edit prioEdit) (*prioTicketPanel, error) {
	detail, found, err := s.rollups.IssueDetail(key)
	if err != nil || !found {
		return nil, err
	}
	row := s.makePrioRow(detail.PrioIssue, edit)
	row.Editable = false
	row.Markers = prioEvents(detail.PrioIssue, changes, cutoff).markers()
	panel := &prioTicketPanel{Row: row, Detail: detail, History: prioHistory(changes, cutoff, s.prioLocation)}
	s.populatePrioTicket(panel)
	s.populatePrioTicketActions(ctx, panel)
	return panel, nil
}

type prioTicketLink struct {
	Key, Relation, Summary, Href string
}

type prioTicketComment struct {
	Text, Author, Instant, TimeLabel string
	Complex                          bool
}

func (s *Server) populatePrioTicket(panel *prioTicketPanel) {
	panel.Description, panel.PlainDescription = jira.ADFText(panel.Detail.Description)
	panel.ParentHref = s.prioRelatedIssueHref(panel.Detail.ParentKey)
	for _, link := range panel.Detail.Links {
		panel.Links = append(panel.Links, prioTicketLink{link.Key, link.Relation, link.Summary, s.prioRelatedIssueHref(link.Key)})
	}
	for _, comment := range panel.Detail.Comments {
		text, plain := jira.ADFText(comment.Body)
		view := prioTicketComment{Text: text, Author: comment.Author.DisplayName, Complex: !plain}
		if view.Author == "" {
			view.Author = comment.Author.AccountID
		}
		if !comment.CreatedAt.IsZero() {
			view.Instant = comment.CreatedAt.Format(time.RFC3339Nano)
			view.TimeLabel = comment.CreatedAt.In(s.prioLocation).Format("02.01.2006 15:04")
		}
		panel.Comments = append(panel.Comments, view)
	}
}

func (s *Server) prioRelatedIssueHref(key string) string {
	if !jiraIssueKey.MatchString(key) {
		return ""
	}
	return s.jiraIssueURL(key)
}
