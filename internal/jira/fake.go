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
	// SinceCalls records the bounds FetchIssuesUpdatedSince was called with, so
	// tests can assert the incremental query was issued and how it was bounded.
	SinceCalls []time.Time
	// Err, if set, is returned by both fetch methods (for exercising error
	// paths).
	Err error
	// WriteErr, if set, is returned by UpdateIssueSize instead of applying the
	// write, so a test can exercise the Board estimate edit's failure path (the
	// pill reverts and an inline error shows, with Jira left unchanged).
	WriteErr error
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
	return &FakeClient{Issues: issues, Sprints: sprints}
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

// FetchIssue returns the current in-memory snapshot of one issue by key (or the
// configured error). It reflects any prior UpdateIssueSize write, so the Board
// estimate edit's post-write reconciliation read returns the authoritative
// value in fake mode, exactly as it would against live Jira.
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
