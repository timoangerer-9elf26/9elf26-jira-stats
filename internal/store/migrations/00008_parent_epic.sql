-- +goose Up
-- parent_key links an issue to its parent (in DCAI, a team-managed project, the
-- parent of a Task/Bug/Story is its Epic); epic_color records an Epic's Jira
-- "Issue color" (customfield_10017, e.g. 'purple'/'dark_teal'). The Board card
-- shows the parent epic's name as a pill coloured by the epic's colour, resolving
-- both by joining a child's parent_key to the epic issue's own row. Both are
-- static per-issue fields present on every fetch, so a normal re-sync populates
-- them — part of the rebuildable projection, no changelog/backfill work needed.
ALTER TABLE issue ADD COLUMN parent_key TEXT;  -- parent issue key (the Epic for a board card); NULL when none
ALTER TABLE issue ADD COLUMN epic_color TEXT;  -- Epic's Jira Issue color; NULL when unset or not an epic

-- +goose Down
ALTER TABLE issue DROP COLUMN epic_color;
ALTER TABLE issue DROP COLUMN parent_key;
