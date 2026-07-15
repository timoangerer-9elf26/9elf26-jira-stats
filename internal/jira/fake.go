package jira

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed canned_issues.json
var cannedIssuesJSON []byte

// FakeClient is an in-memory Client backed by a fixed set of issues. It is the
// reusable test double for the whole pipeline: sync it into a store and drive
// the real handlers against the result.
type FakeClient struct {
	Issues []Issue
	// Err, if set, is returned by FetchIssues instead of the issues (for
	// exercising error paths).
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

func cannedIssues() ([]Issue, error) {
	var issues []Issue
	if err := json.Unmarshal(cannedIssuesJSON, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}
