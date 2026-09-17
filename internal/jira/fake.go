package jira

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed canned_issues.json
var cannedIssuesJSON []byte

//go:embed canned_sprints.json
var cannedSprintsJSON []byte

//go:embed canned_members.json
var cannedMembersJSON []byte

// FakeClient is an in-memory Client backed by a fixed set of issues. It is the
// reusable test double for the whole pipeline: sync it into a store and drive
// the real handlers against the result.
type FakeClient struct {
	// Issues is returned by FetchIssues (the backfill set).
	Issues []Issue
	// Updated is returned by FetchIssuesUpdatedSince (the incremental set); a
	// test typically sets it between cycles to simulate a newly-changed issue.
	Updated []Issue
	// Sprints is returned by FetchSprints (the board's sprint entities); a test
	// sets it to an active/closed/future mix to exercise the sprint lifecycle.
	Sprints []Sprint
	// AssignableUsers is the project's assignable-user set: the raw candidates
	// FetchProjectMembers filters to people. It deliberately mixes people with
	// app accounts, exactly as the live project does, so a regression that stops
	// excluding automation fails a test instead of shipping.
	AssignableUsers []AssignableUser
	// SinceCalls records the bounds FetchIssuesUpdatedSince was called with, so
	// tests can assert the incremental query was issued and how it was bounded.
	SinceCalls []time.Time
	// Err, if set, is returned by both fetch methods (for exercising error
	// paths).
	Err error
	// WriteErr, if set, is returned by every write (UpdateIssueSize,
	// UpdateIssuePriority, UpdateIssueAssignee, TransitionIssue) instead of
	// applying it, so a test can
	// exercise a write path's failure branch (the control reverts / shows an
	// inline error, with Jira left unchanged).
	WriteErr error
	// Transitions is the transition set offered for every issue. It defaults to
	// the live DCAI workflow (DCAITransitions), so the fake carries the same
	// ambiguity the real one does — including the transition labelled "Done"
	// that lands in Ready for release, matching no Board column by name.
	Transitions []Transition
	// TransitionCalls records every performed transition, so a test can assert
	// WHICH transition a status write chose (docs/adr/0010).
	TransitionCalls []TransitionCall
}

// TransitionCall is one recorded TransitionIssue invocation.
type TransitionCall struct {
	Key          string
	TransitionID string
}

// NewFakeClient returns a FakeClient loaded with the canned DCAI dataset (issues
// plus the board's sprint entities).
func NewFakeClient() *FakeClient {
	issues, err := cannedIssues()
	if err != nil {
		// The canned data is embedded and checked by a test; a parse failure
		// here is a programmer error, not a runtime condition.
		panic(fmt.Sprintf("jira: invalid canned dataset: %v", err))
	}
	sprints, err := cannedSprints()
	if err != nil {
		panic(fmt.Sprintf("jira: invalid canned sprints: %v", err))
	}
	members, err := cannedMembers()
	if err != nil {
		panic(fmt.Sprintf("jira: invalid canned members: %v", err))
	}
	return &FakeClient{Issues: issues, Sprints: sprints, AssignableUsers: members, Transitions: DCAITransitions()}
}

// FetchIssues returns the canned issues (or the configured error).
func (c *FakeClient) FetchIssues(ctx context.Context) ([]Issue, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	return c.Issues, nil
}

// FetchIssuesUpdatedSince records the bound and returns the configured Updated
// set (or the configured error).
func (c *FakeClient) FetchIssuesUpdatedSince(ctx context.Context, since time.Time) ([]Issue, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	c.SinceCalls = append(c.SinceCalls, since)
	return c.Updated, nil
}

// FetchSprints returns the configured sprint entities (or the configured error).
func (c *FakeClient) FetchSprints(ctx context.Context) ([]Sprint, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	return c.Sprints, nil
}

// FetchProjectMembers returns the people among the configured assignable users
// (or the configured error), filtered through the same projectMembers rule the
// live client uses — so the app accounts in the fake's dataset never reach the
// projection.
func (c *FakeClient) FetchProjectMembers(ctx context.Context) ([]ProjectMember, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	return projectMembers(c.AssignableUsers), nil
}

// FetchIssue returns the current in-memory snapshot of one issue by key (or the
// configured error). It reflects every prior write this fake applied, so each
// edit's post-write reconciliation read returns the authoritative value in fake
// mode, exactly as it would against live Jira.
func (c *FakeClient) FetchIssue(ctx context.Context, key string) (Issue, error) {
	if c.Err != nil {
		return Issue{}, c.Err
	}
	for _, iss := range c.Issues {
		if iss.Key == key {
			return iss, nil
		}
	}
	return Issue{}, fmt.Errorf("fake jira: issue %q not found", key)
}

// UpdateIssueSize applies the size write in memory (or returns WriteErr), so
// local dev and the smoke suite exercise the real edit flow rather than a
// disabled control. size is the T-shirt label "S"/"M"/"L" or "" (no-estimate);
// the fake stores that label directly (unlike live Jira's single-select), so the
// write is a straight field set on the matching issue.
func (c *FakeClient) UpdateIssueSize(ctx context.Context, key, size string) error {
	if c.WriteErr != nil {
		return c.WriteErr
	}
	switch size {
	case "", "S", "M", "L":
	default:
		return fmt.Errorf("fake jira: unknown size %q (want S, M, L or empty)", size)
	}
	for i := range c.Issues {
		if c.Issues[i].Key == key {
			c.Issues[i].Size = size
			return nil
		}
	}
	return fmt.Errorf("fake jira: issue %q not found", key)
}

// UpdateIssuePriority applies the priority write in memory (or returns
// WriteErr), so the Prio view's priority edit is a live control in local dev
// and the smoke suite (#212). Like live Jira it accepts exactly the five level
// names (jira.Priorities) and nothing else — there is no "clear priority".
func (c *FakeClient) UpdateIssuePriority(ctx context.Context, key, priority string) error {
	if c.WriteErr != nil {
		return c.WriteErr
	}
	if !ValidPriority(priority) {
		return fmt.Errorf("fake jira: unknown priority %q (want one of %v)", priority, Priorities)
	}
	for i := range c.Issues {
		if c.Issues[i].Key == key {
			c.Issues[i].Priority = priority
			return nil
		}
	}
	return fmt.Errorf("fake jira: issue %q not found", key)
}

// UpdateIssueAssignee applies the assignee write in memory (or returns
// WriteErr), so the Board's assign popover is a working control in local dev and
// the smoke suite (#223). It takes an ACCOUNT ID like live Jira does and
// resolves it against the whole assignable-user set, rejecting an id that
// belongs to nobody — which is what live Jira answers a 400 to. An accountID of
// UnassignedAccountID clears the assignee.
//
// Note it resolves against AssignableUsers and not projectMembers: keeping app
// accounts off the popover is a product rule that lives in FetchProjectMembers
// alone (docs/adr/0012 — "if agents start owning tickets, drop the filter"), and
// live Jira would assign one happily. A fake that refused would be stricter than
// production, which is the one direction a fake must never be.
//
// An issue stores its assignee as a display name and an avatar URL, so the
// resolved user's are what land on it.
func (c *FakeClient) UpdateIssueAssignee(ctx context.Context, key, accountID string) error {
	if c.WriteErr != nil {
		return c.WriteErr
	}
	var assignee AssignableUser
	if accountID != UnassignedAccountID {
		known := false
		for _, u := range c.AssignableUsers {
			if u.AccountID == accountID {
				assignee, known = u, true
				break
			}
		}
		if !known {
			return fmt.Errorf("fake jira: %q is not an assignable account on this project", accountID)
		}
	}
	for i := range c.Issues {
		if c.Issues[i].Key == key {
			c.Issues[i].Assignee = assignee.DisplayName
			c.Issues[i].AssigneeAvatarURL = assignee.AvatarURL
			return nil
		}
	}
	return fmt.Errorf("fake jira: issue %q not found", key)
}

// FetchTransitions offers the same set for every issue, mirroring the live DCAI
// workflow (which is effectively all-to-all).
func (c *FakeClient) FetchTransitions(ctx context.Context, key string) ([]Transition, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	// A zero-valued FakeClient (built as a struct literal, as many tests do)
	// still offers the DCAI set; an explicitly empty non-nil slice means "Jira
	// offers this issue nothing", the case a caller must fail cleanly on.
	if c.Transitions == nil {
		return DCAITransitions(), nil
	}
	return c.Transitions, nil
}

// TransitionIssue performs a transition in memory: it moves the issue into the
// transition's target status, so a subsequent FetchIssue (the write path's
// reconciliation read) returns the moved issue. An unknown transition id is an
// error, as it is in Jira. WriteErr injects a failed write.
func (c *FakeClient) TransitionIssue(ctx context.Context, key, transitionID string) error {
	if c.WriteErr != nil {
		return c.WriteErr
	}
	offered, err := c.FetchTransitions(ctx, key)
	if err != nil {
		return err
	}
	var target Transition
	for _, tr := range offered {
		if tr.ID == transitionID {
			target = tr
			break
		}
	}
	if target.ID == "" {
		return fmt.Errorf("fake jira: unknown transition %q", transitionID)
	}
	for i := range c.Issues {
		if c.Issues[i].Key == key {
			c.Issues[i].Status = target.ToStatusName
			c.Issues[i].StatusCategory = target.ToStatusCategory
			c.TransitionCalls = append(c.TransitionCalls, TransitionCall{Key: key, TransitionID: transitionID})
			return nil
		}
	}
	return fmt.Errorf("fake jira: issue %q not found", key)
}

func cannedIssues() ([]Issue, error) {
	var issues []Issue
	if err := json.Unmarshal(cannedIssuesJSON, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func cannedSprints() ([]Sprint, error) {
	var sprints []Sprint
	if err := json.Unmarshal(cannedSprintsJSON, &sprints); err != nil {
		return nil, err
	}
	return sprints, nil
}

func cannedMembers() ([]AssignableUser, error) {
	var users []AssignableUser
	if err := json.Unmarshal(cannedMembersJSON, &users); err != nil {
		return nil, err
	}
	return users, nil
}
