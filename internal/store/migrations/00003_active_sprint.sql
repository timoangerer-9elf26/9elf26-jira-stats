-- +goose Up
-- active_sprint records the name of the ACTIVE sprint an issue belongs to (a
-- sprint entry with state=="active"), or NULL when the issue is in no active
-- sprint. It scopes the "Now" board to the current sprint. Part of the
-- rebuildable projection: a re-sync repopulates it.
ALTER TABLE issue ADD COLUMN active_sprint TEXT;

-- +goose Down
ALTER TABLE issue DROP COLUMN active_sprint;
