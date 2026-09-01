-- +goose Up
-- labels records the issue's Jira labels (fields.labels[]) as a space-delimited
-- list, e.g. 'Technical Product'. Jira labels are whitespace-free by
-- construction, so a space is an unambiguous delimiter and the list round-trips
-- through strings.Fields. Labels were not part of the projection before the Prio
-- view (#201), which renders them as pills. Like priority, this is a static
-- per-issue field present on every fetch, so a normal re-sync populates it —
-- part of the rebuildable projection, no changelog/backfill work needed.
-- Existing rows stay NULL until a resync (ADR 0009).
ALTER TABLE issue ADD COLUMN labels TEXT;  -- space-delimited Jira labels; NULL when unlabelled

-- +goose Down
ALTER TABLE issue DROP COLUMN labels;
