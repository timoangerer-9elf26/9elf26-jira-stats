-- +goose Up
-- priority records the issue's standard Jira priority level (fields.priority.name:
-- Highest / High / Medium / Low / Lowest). Every DCAI issue has one, but it was
-- not part of the projection before the Prio view (#200), which sorts its table
-- Highest→Lowest. It is a static per-issue field present on every fetch, so a
-- normal re-sync populates it — part of the rebuildable projection, no
-- changelog/backfill work needed. Existing rows stay NULL until a resync.
ALTER TABLE issue ADD COLUMN priority TEXT;  -- Jira priority level; NULL when unset

-- +goose Down
ALTER TABLE issue DROP COLUMN priority;
