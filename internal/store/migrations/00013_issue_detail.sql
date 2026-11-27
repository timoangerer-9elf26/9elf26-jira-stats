-- +goose Up
ALTER TABLE issue ADD COLUMN description BLOB;
ALTER TABLE issue ADD COLUMN comments BLOB;
ALTER TABLE issue ADD COLUMN links BLOB;
ALTER TABLE issue ADD COLUMN assignee_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE issue ADD COLUMN resolved_at TEXT;

ALTER TABLE status_transition ADD COLUMN from_id TEXT NOT NULL DEFAULT '';
ALTER TABLE status_transition ADD COLUMN to_id TEXT NOT NULL DEFAULT '';
ALTER TABLE status_transition ADD COLUMN author_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE status_transition ADD COLUMN author_display_name TEXT NOT NULL DEFAULT '';
-- Keep the second-resolution timestamp used by the sprint and daily queries.
ALTER TABLE status_transition ADD COLUMN subsecond_ns INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_issue_meeting_changes ON status_transition(transitioned_at, subsecond_ns)
    WHERE field IN ('status', 'priority', 'resolution');

-- Invalidate the incremental watermark so the next sync fills historical
-- details and changes across the project. Keep the existing projection readable.
DELETE FROM meta WHERE key = 'last_sync';

-- +goose Down
DROP INDEX idx_issue_meeting_changes;
ALTER TABLE status_transition DROP COLUMN subsecond_ns;
ALTER TABLE status_transition DROP COLUMN author_display_name;
ALTER TABLE status_transition DROP COLUMN author_account_id;
ALTER TABLE status_transition DROP COLUMN to_id;
ALTER TABLE status_transition DROP COLUMN from_id;
ALTER TABLE issue DROP COLUMN resolved_at;
ALTER TABLE issue DROP COLUMN assignee_account_id;
ALTER TABLE issue DROP COLUMN links;
ALTER TABLE issue DROP COLUMN comments;
ALTER TABLE issue DROP COLUMN description;
