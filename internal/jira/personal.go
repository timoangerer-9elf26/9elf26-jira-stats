package jira

import "context"

type personalClientKey struct{}

// WithPersonalClient binds the authenticated person's Jira client to a request.
func WithPersonalClient(ctx context.Context, client Client) context.Context {
	return context.WithValue(ctx, personalClientKey{}, client)
}

// PersonalClientFromContext returns the request-scoped Jira client, if any.
func PersonalClientFromContext(ctx context.Context) (Client, bool) {
	client, ok := ctx.Value(personalClientKey{}).(Client)
	return client, ok
}
