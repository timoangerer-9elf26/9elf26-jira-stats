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

// FakeClient is an in-memory Client backed by a fixed set of issues. It is the
// reusable test double for the whole pipeline: sync it into a store and drive
// the real handlers against the result.
type FakeClient struct {
	// Issues is returned by FetchIssues (the backfill set).
	Issues []Issue
	// Updated is returned by FetchIssuesUpdatedSince (the incremental set); a
	// test typically sets it between cycles to simulate a newly-changed issue.
	Updated []Issue
	// SinceCalls records the bounds FetchIssuesUpdatedSince was called with, so
	// tests can assert the incremental query was issued and how it was bounded.
	SinceCalls []time.Time
	// Err, if set, is returned by both fetch methods (for exercising error
	// paths).
	Err error
}

// NewFakeClient returns a FakeClient loaded with the canned DCAI dataset.
func NewFakeClient() *FakeClient {
	issues, err := cannedIssues()
	if err != nil {
		// The canned data is embedded and checked by a test; a parse failure
		// here is a programmer error, not a runtime condition.
		panic(fmt.Sprintf("jira: invalid canned dataset: %v", err))
	}
	return &FakeClient{Issues: issues}
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

func cannedIssues() ([]Issue, error) {
	var issues []Issue
	if err := json.Unmarshal(cannedIssuesJSON, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}
