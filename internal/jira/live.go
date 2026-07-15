package jira

import "context"

// Config holds the connection settings for the live Jira client. Populated from
// environment variables (see spec); unused by the walking skeleton.
type Config struct {
	BaseURL    string
	Email      string
	APIToken   string
	ProjectKey string
	BoardID    string
}

// LiveClient is the real Jira Cloud REST client. It is stubbed for the walking
// skeleton: a later ticket implements JQL search with changelog expansion and
// the per-issue changelog fallback.
type LiveClient struct {
	cfg Config
}

// NewLiveClient builds a live client from config.
func NewLiveClient(cfg Config) *LiveClient {
	return &LiveClient{cfg: cfg}
}

// FetchIssues is not implemented yet.
func (c *LiveClient) FetchIssues(ctx context.Context) ([]Issue, error) {
	return nil, ErrNotImplemented
}
