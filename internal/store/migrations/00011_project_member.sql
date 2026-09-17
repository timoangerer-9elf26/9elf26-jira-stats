-- +goose Up
-- project_member is the projection's second entity beside issues and sprints: a
-- person who can be assigned work on the project (CONTEXT.md "Project member"),
-- synced from Jira's assignable-user search each cycle so the Board's assign
-- popover renders from SQLite and no read path makes a Jira call (docs/adr/0012).
-- Jira's app accounts are filtered out before they ever reach this table — only
-- people are members. Rows are REPLACED wholesale on every sync, so someone who
-- leaves the project disappears within one cycle; nothing here accumulates. A
-- sync that cannot reach Jira's user search leaves the previous list standing
-- rather than emptying the table, and never aborts the cycle (see
-- refreshProjectMembers) — nothing load-bearing reads this table.
-- Part of the rebuildable projection: Reset clears it and a full resync
-- repopulates it, exactly like the sprint table.
--
-- NB: the issue table deliberately gains NO account id from this work. An issue
-- still stores its assignee as a display name and avatar; the assignee write
-- needs only the TARGET person's id, which comes from here — so no backfill or
-- resync gates the feature (docs/adr/0012).
CREATE TABLE project_member (
    account_id   TEXT PRIMARY KEY,  -- Jira accountId, the id an assignee write targets
    display_name TEXT NOT NULL,
    avatar_url   TEXT               -- largest avatar image; NULL when the user has none
);

-- +goose Down
DROP TABLE project_member;
