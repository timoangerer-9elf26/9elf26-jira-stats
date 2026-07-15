// Package jira defines the seam between the app and Jira Cloud. The Client
// interface is the single point where Jira is faked in tests; the sync engine
// depends only on this interface, never on the HTTP details of Jira.
package jira

import (
	"context"
	"errors"
	"time"
)

// Issue is a snapshot of a Jira issue plus the changelog entries needed to
// reconstruct its status history. Fields mirror the DCAI field mapping from the
// spec (size is the raw T-shirt label, empty when unestimated).
type Issue struct {
	Key            string
	Type           string // Task, Bug, Story, Epic, Sub-task
	Summary        string
	Status         string // current workflow status, e.g. "In Progress"
	StatusCategory string // Jira status category: "To Do", "In Progress", "Done"
	Size           string // T-shirt size label: "S", "M", "L", or "" (no estimate)
	Sprint         string
	Assignee       string
	Changelog      []ChangelogEntry
}

// ChangelogEntry is a single field change recorded in a Jira issue's history.
// ID is the stable Jira changelog entry id used to dedup transitions on re-sync.
type ChangelogEntry struct {
	ID        string // stable Jira changelog entry id
	Field     string // changed field, e.g. "status" or "Estimated Time"
	From      string
	To        string
	Timestamp time.Time
}

// Client fetches issues (with their changelog) from a Jira project. It is the
// only seam through which the sync engine reaches Jira, and the only thing a
// test needs to fake to exercise the whole pipeline.
type Client interface {
	FetchIssues(ctx context.Context) ([]Issue, error)
}

// ErrNotImplemented is returned by the real client until live Jira integration
// lands in a later ticket.
var ErrNotImplemented = errors.New("jira: live client not implemented yet")
