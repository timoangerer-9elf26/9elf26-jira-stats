-- +goose Up
-- sprint_membership_transition is the log of sprint-membership changes: each row
-- is one issue ENTERING or LEAVING one sprint at a given instant. It is to sprint
-- membership what status_transition is to status — membership at any past instant
-- is reconstructed by replaying these rows up to that instant. Keyed on the
-- sprint id (matching the sprint entity), with the name kept for readability. A
-- single Jira "Sprint" change can add and/or remove several sprints, so it
-- expands to multiple rows; dedup is therefore on (changelog_entry_id, sprint_id),
-- not the entry id alone.
--
-- Most rows are derived from the Jira "Sprint" changelog field. But a ticket
-- created directly into a sprint has its Sprint field set at creation and never
-- changed, so it carries no "Sprint" changelog item; for such a ticket the sync
-- SYNTHESIZES an entering row at its created instant, keyed
-- `synthetic-created:<key>:<sprint id>` (see #55). Those synthetic rows are
-- reconciled on re-sync (removed once a real changelog entry for the sprint
-- appears), so the table is otherwise append-only. Part of the rebuildable
-- projection: a re-sync repopulates it, and the full-changelog backfill makes it
-- retroactive.
CREATE TABLE sprint_membership_transition (
    changelog_entry_id TEXT NOT NULL,   -- Jira changelog entry id, or a synthetic-created:… id (#55)
    issue_key          TEXT NOT NULL REFERENCES issue(key) ON DELETE CASCADE,
    sprint_id          INTEGER NOT NULL, -- Jira sprint id (matches sprint.id)
    sprint_name        TEXT,             -- sprint name at the time of the change
    entered            INTEGER NOT NULL, -- 1 = entered the sprint, 0 = left it
    transitioned_at    TEXT NOT NULL,    -- RFC3339 UTC instant of the change
    PRIMARY KEY (changelog_entry_id, sprint_id)
);

CREATE INDEX idx_sprint_membership_sprint ON sprint_membership_transition(sprint_id);
CREATE INDEX idx_sprint_membership_issue ON sprint_membership_transition(issue_key);

-- +goose Down
DROP TABLE sprint_membership_transition;
