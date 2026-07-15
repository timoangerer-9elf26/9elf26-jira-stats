-- +goose Up
-- issue holds the current snapshot of each Jira issue. It is a pure projection
-- of Jira and is fully rebuildable from a re-sync.
CREATE TABLE issue (
    key             TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    summary         TEXT NOT NULL,
    status          TEXT NOT NULL,
    status_category TEXT NOT NULL,
    size            TEXT,           -- T-shirt label: 'S','M','L'; NULL = no estimate
    sprint          TEXT,
    assignee        TEXT,
    synced_at       TEXT NOT NULL   -- RFC3339 UTC timestamp of the sync that wrote this row
);

-- status_transition is the append-only log of field changes (status and, later,
-- Estimated Time). Transition-based completion is measured from this log, not
-- from the current status. Deduped by the stable Jira changelog entry id.
CREATE TABLE status_transition (
    changelog_entry_id TEXT PRIMARY KEY,
    issue_key          TEXT NOT NULL REFERENCES issue(key) ON DELETE CASCADE,
    field              TEXT NOT NULL,
    from_status        TEXT,
    to_status          TEXT,
    transitioned_at    TEXT NOT NULL   -- RFC3339 UTC timestamp of the change
);

CREATE INDEX idx_status_transition_issue ON status_transition(issue_key);

-- +goose Down
DROP TABLE status_transition;
DROP TABLE issue;
