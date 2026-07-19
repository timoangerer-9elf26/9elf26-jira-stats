-- +goose Up
-- assignee_avatar_url records the public Jira avatar image URL of the current
-- assignee (from the assignee's avatarUrls). The Board card renders it at the
-- bottom-right, falling back to computed initials when NULL/empty. It is a
-- static per-issue field present on every fetch, so a normal re-sync populates
-- it — part of the rebuildable projection, no changelog/backfill work needed.
ALTER TABLE issue ADD COLUMN assignee_avatar_url TEXT; -- public Jira avatar URL; NULL when unassigned/none

-- +goose Down
ALTER TABLE issue DROP COLUMN assignee_avatar_url;
