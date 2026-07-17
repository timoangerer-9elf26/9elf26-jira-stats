// Package jira defines the seam between the app and Jira Cloud. The Client
// interface is the single point where Jira is faked in tests; the sync engine
// depends only on this interface, never on the HTTP details of Jira.
package jira

import (
	"context"
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
	Sprint         string // current (last) sprint name on the issue, "" if none
	// ActiveSprint is the name of the ACTIVE sprint (state=="active") the issue
	// belongs to, or "" when the issue is in no active sprint (closed/future/none).
	// This is per-issue MEMBERSHIP only; the sprint's window (activation instant)
	// comes from the Sprint entity, not from planned dates carried on the issue.
	ActiveSprint string
	Assignee     string
	Changelog    []ChangelogEntry
	// SprintChanges is the issue's sprint-membership history: each entering or
	// leaving of a single sprint, derived from the "Sprint" changelog field. It
	// lets the store reconstruct which sprint(s) the issue belonged to at any past
	// instant (alongside, not replacing, the ActiveSprint snapshot above).
	SprintChanges []SprintMembershipChange
}

// SprintMembershipChange is one issue's entering or leaving of a single sprint,
// derived from a "Sprint" changelog entry. A single Jira Sprint change can add
// and/or remove several sprints at once, so it expands to one of these per
// sprint id whose membership actually changed. It is keyed on the sprint id (the
// stored sprint entity's identity); SprintName is kept for readability. EntryID
// is the stable Jira changelog entry id, deduped with SprintID on re-sync.
type SprintMembershipChange struct {
	EntryID    string    // stable Jira changelog entry id
	SprintID   int       // Jira sprint id (matches the sprint entity's id)
	SprintName string    // sprint name at the time of the change
	Entered    bool      // true = entered the sprint; false = left it
	Timestamp  time.Time // instant of the change
}

// Sprint is a board sprint as a first-class entity with its lifecycle instants —
// the trusted timestamps for windowing. Jira Cloud's Agile API exposes no
// actual-activation field (there is no activatedDate), so ActivatedAt is taken
// from Jira's startDate (the value set in the "Start sprint" dialog), the only
// available window-start anchor, falling back to createdDate for a sprint never
// started (see toSprint); CompletedAt is Jira's completeDate (the instant it was
// completed), the zero time until the sprint is completed. The planned endDate is
// deliberately NOT carried here (see docs/adr/0002 and CONTEXT.md "Sprint").
type Sprint struct {
	ID          int
	Name        string
	State       string    // "active", "closed", or "future"
	ActivatedAt time.Time // window-start instant, from startDate (createdDate fallback)
	CompletedAt time.Time // completion instant (zero until the sprint is completed)
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
	// FetchIssues walks the whole project (used for the initial backfill).
	FetchIssues(ctx context.Context) ([]Issue, error)
	// FetchIssuesUpdatedSince fetches only issues updated at or after the given
	// bound (used for cheap incremental syncs). The bound is expected to already
	// include any clock-skew overlap the caller wants.
	FetchIssuesUpdatedSince(ctx context.Context, since time.Time) ([]Issue, error)
	// FetchSprints returns the board's sprints as first-class entities, each with
	// its actual lifecycle instants (see Sprint). It is fetched on every sync so
	// the store's sprint entities track Jira.
	FetchSprints(ctx context.Context) ([]Sprint, error)
}
