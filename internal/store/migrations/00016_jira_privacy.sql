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

-- Normalize legacy identities using the same rules as the live Jira decoder.
-- Keep the enclosing issue, comment, change and meeting events.
CREATE TEMP TABLE unreportable_jira_account AS
WITH ids(id) AS (
    SELECT assignee_account_id FROM issue
    UNION SELECT author_account_id FROM status_transition
    UNION SELECT account_id FROM project_member
    UNION SELECT account_id FROM jira_auth_grant
    UNION SELECT account_id FROM jira_auth_session
    UNION SELECT account_id FROM prio_meeting
    UNION SELECT account_id FROM prio_meeting_change
    UNION SELECT COALESCE(value ->> '$.Author.AccountID', '') FROM issue, json_each(CAST(comments AS TEXT))
    UNION SELECT COALESCE(node.value ->> '$.attrs.id', '')
        FROM issue, json_tree(json_array(json(CAST(description AS TEXT)), json(CAST(comments AS TEXT)))) AS node
        WHERE node.type = 'object' AND node.value ->> '$.type' = 'mention'
)
SELECT id FROM ids WHERE typeof(id) <> 'text' OR length(id) NOT BETWEEN 1 AND 128
    OR instr(id, char(0)) > 0 OR id GLOB '*[^a-zA-Z0-9:-]*'
    OR lower(id) IN ('unknown', 'stub', 'anonymous', 'unassigned', '-1');

UPDATE issue SET creator = NULL;
UPDATE issue SET assignee_account_id = '', assignee = NULL, assignee_avatar_url = NULL
WHERE assignee_account_id IN unreportable_jira_account;
UPDATE status_transition SET author_account_id = '', author_display_name = '', author_retrieved_at = ''
WHERE author_account_id IN unreportable_jira_account;
DELETE FROM project_member WHERE account_id IN unreportable_jira_account;
DELETE FROM jira_auth_session WHERE account_id IN unreportable_jira_account;
DELETE FROM jira_auth_grant WHERE account_id IN unreportable_jira_account;
UPDATE prio_meeting SET account_id = '' WHERE account_id IN unreportable_jira_account;
UPDATE prio_meeting_change SET account_id = '' WHERE account_id IN unreportable_jira_account;
UPDATE issue
SET comments = (
    SELECT json_group_array(json(CASE WHEN COALESCE(value ->> '$.Author.AccountID', '') IN unreportable_jira_account
        THEN json_set(value, '$.Author', json('{"AccountID":"","DisplayName":""}'))
        ELSE value END))
    FROM json_each(CAST(issue.comments AS TEXT))
)
WHERE EXISTS (
    SELECT 1 FROM json_each(CAST(issue.comments AS TEXT))
    WHERE COALESCE(value ->> '$.Author.AccountID', '') IN unreportable_jira_account
);
-- Replace the attributes of each unlinked ADF mention while preserving its
-- location, surrounding text and the rest of the document.
WITH RECURSIVE documents(key, field, body) AS (
    SELECT key, 'description', CAST(description AS TEXT) FROM issue WHERE description IS NOT NULL
    UNION ALL SELECT key, 'comments', CAST(comments AS TEXT) FROM issue WHERE comments IS NOT NULL
), mentions AS (
    SELECT doc.key, doc.field, node.fullkey || '.attrs' AS path,
        row_number() OVER (PARTITION BY doc.key, doc.field ORDER BY node.id) AS ordinal
    FROM documents AS doc, json_tree(doc.body) AS node
    WHERE node.type = 'object' AND node.value ->> '$.type' = 'mention'
        AND COALESCE(node.value ->> '$.attrs.userType', '') <> 'SPECIAL'
        AND COALESCE(node.value ->> '$.attrs.id', '') IN unreportable_jira_account
), redacted(key, field, body, ordinal) AS (
    SELECT doc.key, doc.field, json_set(doc.body, mention.path, json('{"id":"unknown"}')), 1
    FROM documents AS doc JOIN mentions AS mention USING (key, field) WHERE mention.ordinal = 1
    UNION ALL
    SELECT doc.key, doc.field, json_set(doc.body, mention.path, json('{"id":"unknown"}')), mention.ordinal
    FROM redacted AS doc JOIN mentions AS mention USING (key, field) WHERE mention.ordinal = doc.ordinal + 1
), final AS (
    SELECT doc.key, doc.field, doc.body FROM redacted AS doc WHERE NOT EXISTS (
        SELECT 1 FROM mentions WHERE key = doc.key AND field = doc.field AND ordinal = doc.ordinal + 1
    )
)
UPDATE issue
SET description = COALESCE((SELECT CAST(body AS BLOB) FROM final WHERE key = issue.key AND field = 'description'), description),
    comments = COALESCE((SELECT CAST(body AS BLOB) FROM final WHERE key = issue.key AND field = 'comments'), comments)
WHERE key IN (SELECT key FROM final);

DROP TABLE unreportable_jira_account;
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
    expires_at INTEGER NOT NULL,
    claim TEXT NOT NULL
);

CREATE TABLE jira_privacy_poll (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    retry_at INTEGER NOT NULL DEFAULT 0,
    claim TEXT NOT NULL DEFAULT '',
    claim_until INTEGER NOT NULL DEFAULT 0,
    cycle_seconds INTEGER NOT NULL DEFAULT 604800
);
INSERT INTO jira_privacy_poll (id) VALUES (1);

-- A known result is retained until its cadence and projection writes commit.
CREATE TABLE jira_privacy_inbox (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    reported_at INTEGER NOT NULL,
    cycle_seconds INTEGER NOT NULL,
    accounts BLOB NOT NULL,
    actions BLOB NOT NULL
);

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
DROP TABLE jira_privacy_inbox;
DROP TABLE jira_privacy_poll;
DROP TABLE jira_privacy_attempt;
DROP TABLE jira_privacy_state;
ALTER TABLE jira_auth_session DROP COLUMN retrieved_at;
ALTER TABLE jira_auth_grant DROP COLUMN retrieved_at;
ALTER TABLE project_member DROP COLUMN retrieved_at;
ALTER TABLE status_transition DROP COLUMN author_retrieved_at;
ALTER TABLE issue DROP COLUMN creator_account_id;
