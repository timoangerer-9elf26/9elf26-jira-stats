-- +goose Up
CREATE TABLE prio_meeting (
    id INTEGER PRIMARY KEY,
    ended_at TEXT NOT NULL,
    account_id TEXT NOT NULL
);

-- +goose Down
DROP TABLE prio_meeting;
