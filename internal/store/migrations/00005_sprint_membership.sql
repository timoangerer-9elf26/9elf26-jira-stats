-- +goose Up
-- sprint_membership_transition is the append-only log of sprint-membership
-- changes derived from the Jira "Sprint" changelog field: each row is one issue
-- ENTERING or LEAVING one sprint at a given instant. It is to sprint membership
-- what status_transition is to status — membership at any past instant is
-- reconstructed by replaying these rows up to that instant. Keyed on the sprint
-- id (matching the sprint entity), with the name kept for readability. A single
-- Jira Sprint change can add and/or remove several sprints, so it expands to
-- multiple rows; dedup is therefore on (changelog_entry_id, sprint_id), not the
-- entry id alone. Part of the rebuildable projection: a re-sync repopulates it,
-- and the full-changelog backfill makes it retroactive.
CREATE TABLE sprint_membership_transition (
    changelog_entry_id TEXT NOT NULL,   -- stable Jira changelog entry id
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
