-- +goose Up
-- created_at and creator record the issue's immutable authorship: when it was
-- created in Jira (RFC3339 UTC) and the display name of its Jira Creator (the
-- immutable author, NOT the mutable Reporter). They feed the Daily view's
-- "tickets I created" section. Both are static per-issue fields present on every
-- fetch, so a normal re-sync populates them — part of the rebuildable projection,
-- no changelog/backfill work needed.
ALTER TABLE issue ADD COLUMN created_at TEXT; -- RFC3339 UTC creation instant; NULL when unknown
ALTER TABLE issue ADD COLUMN creator TEXT;    -- Jira Creator display name; NULL when unknown

-- +goose Down
ALTER TABLE issue DROP COLUMN creator;
ALTER TABLE issue DROP COLUMN created_at;
