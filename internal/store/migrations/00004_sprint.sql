-- +goose Up
-- sprint promotes the board sprint from a label carried on issues to a
-- first-class entity with its ACTUAL lifecycle instants: activated_at (Jira's
-- activatedDate, the trusted "sprint started" timestamp) and completed_at
-- (Jira's completeDate, the trusted "sprint ended" timestamp). Both are stored
-- RFC3339 UTC and are NULL until the event happens (a future sprint has no
-- activation; an active sprint no completion). The PLANNED start/end dates are
-- deliberately NOT stored — they are not trusted for windowing (see
-- docs/adr/0002 and CONTEXT.md "Sprint"). This entity supersedes the
-- active_sprint_* window that migration 00003/the meta table used to carry.
-- Part of the rebuildable projection: a re-sync repopulates it.
CREATE TABLE sprint (
    id           INTEGER PRIMARY KEY,  -- Jira sprint id
    name         TEXT NOT NULL,
    state        TEXT NOT NULL,        -- 'active', 'closed', or 'future'
    activated_at TEXT,                 -- RFC3339 UTC activation instant; NULL until started
    completed_at TEXT                  -- RFC3339 UTC completion instant; NULL until completed
);

-- +goose Down
DROP TABLE sprint;
