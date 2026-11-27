-- +goose Up
ALTER TABLE issue ADD COLUMN creator_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE status_transition ADD COLUMN author_retrieved_at TEXT NOT NULL DEFAULT '';
ALTER TABLE project_member ADD COLUMN retrieved_at TEXT NOT NULL DEFAULT '';
ALTER TABLE jira_auth_grant ADD COLUMN retrieved_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jira_auth_session ADD COLUMN retrieved_at INTEGER NOT NULL DEFAULT 0;

-- Legacy rows lack retrieval instants. Bound their age conservatively using
-- the schema version that first stored the data, or an older snapshot.
UPDATE status_transition
SET author_retrieved_at = MIN(
    COALESCE((SELECT synced_at FROM issue WHERE key = issue_key), '9999'),
    (SELECT strftime('%Y-%m-%dT%H:%M:%SZ', MIN(tstamp)) FROM goose_db_version WHERE version_id = 13 AND is_applied)
) WHERE author_account_id <> '';
UPDATE project_member
SET retrieved_at = MIN(
    COALESCE((SELECT MIN(synced_at) FROM issue), '9999'),
    (SELECT strftime('%Y-%m-%dT%H:%M:%SZ', MIN(tstamp)) FROM goose_db_version WHERE version_id = 11 AND is_applied)
);
UPDATE jira_auth_session SET retrieved_at = expires_at - 2592000;
UPDATE jira_auth_grant
SET retrieved_at = MIN(
    COALESCE((SELECT MIN(retrieved_at) FROM jira_auth_session AS session WHERE session.account_id = jira_auth_grant.account_id), 253402300799),
    (SELECT unixepoch(MIN(tstamp)) FROM goose_db_version WHERE version_id = 12 AND is_applied)
);

-- Name-only data cannot be linked reliably. A full fetch repopulates it with
-- the account IDs from Jira without resetting the projection or the audit.
UPDATE issue SET creator = NULL;
UPDATE issue SET assignee = NULL, assignee_avatar_url = NULL WHERE assignee_account_id = '';
UPDATE status_transition SET author_display_name = '' WHERE author_account_id = '';
UPDATE issue
SET comments = (
    SELECT json_group_array(json(CASE WHEN COALESCE(value ->> '$.Author.AccountID', '') = ''
        AND COALESCE(value ->> '$.Author.DisplayName', '') <> '' THEN json_set(value, '$.Author.DisplayName', '')
        ELSE value END))
    FROM json_each(CAST(issue.comments AS TEXT))
)
WHERE EXISTS (
    SELECT 1 FROM json_each(CAST(issue.comments AS TEXT))
    WHERE COALESCE(value ->> '$.Author.AccountID', '') = '' AND COALESCE(value ->> '$.Author.DisplayName', '') <> ''
);
DELETE FROM meta WHERE key = 'last_sync';

-- Scheduling instants are Unix microseconds so a daily timer can hit a
-- cycle boundary without either rounding down or delaying for another day.
CREATE TABLE jira_privacy_state (
    account_hash TEXT PRIMARY KEY,
    last_reported_at INTEGER NOT NULL,
    cycle_seconds INTEGER NOT NULL,
    closed INTEGER NOT NULL DEFAULT 0
);

-- A submitted request may be accepted even when its response is lost.
CREATE TABLE jira_privacy_attempt (
    account_hash TEXT PRIMARY KEY,
    expires_at INTEGER NOT NULL
);

CREATE TABLE jira_privacy_poll (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    retry_at INTEGER NOT NULL DEFAULT 0,
    cycle_seconds INTEGER NOT NULL DEFAULT 604800
);
INSERT INTO jira_privacy_poll (id) VALUES (1);

-- A pending profile has no retrieved_at. A verified profile supersedes names
-- and avatars in snapshots and historical authors supplied by Jira.
CREATE TABLE jira_privacy_profile (
    account_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    retrieved_at TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE jira_privacy_profile;
DROP TABLE jira_privacy_poll;
DROP TABLE jira_privacy_attempt;
DROP TABLE jira_privacy_state;
ALTER TABLE jira_auth_session DROP COLUMN retrieved_at;
ALTER TABLE jira_auth_grant DROP COLUMN retrieved_at;
ALTER TABLE project_member DROP COLUMN retrieved_at;
ALTER TABLE status_transition DROP COLUMN author_retrieved_at;
ALTER TABLE issue DROP COLUMN creator_account_id;
