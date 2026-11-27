-- +goose Up
CREATE TABLE jira_auth_flow (
    state_hash TEXT NOT NULL PRIMARY KEY,
    browser_hash TEXT NOT NULL,
    next_path TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE jira_auth_grant (
    account_id TEXT NOT NULL PRIMARY KEY,
    ciphertext BLOB NOT NULL
);

CREATE TABLE jira_auth_session (
    token_hash TEXT NOT NULL PRIMARY KEY,
    account_id TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX jira_auth_session_account ON jira_auth_session (account_id);

-- +goose Down
DROP TABLE jira_auth_session;
DROP TABLE jira_auth_grant;
DROP TABLE jira_auth_flow;
