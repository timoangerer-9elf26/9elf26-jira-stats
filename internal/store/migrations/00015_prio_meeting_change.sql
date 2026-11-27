-- +goose Up
CREATE TABLE prio_meeting_change (
    id INTEGER PRIMARY KEY,
    meeting_window INTEGER NOT NULL,
    issue_key TEXT NOT NULL,
    label TEXT NOT NULL,
    account_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);
CREATE INDEX prio_meeting_change_window ON prio_meeting_change (meeting_window, id);

-- +goose Down
DROP TABLE prio_meeting_change;
