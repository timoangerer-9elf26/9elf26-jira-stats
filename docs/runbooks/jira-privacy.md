# Jira personal data reporting

The live binary checks at startup and daily for accounts due for Atlassian
Personal Data Reporting. It submits up to 90 Atlassian account IDs per request
with the oldest retrieval instant for each account, in UTC RFC 3339. Scheduling
and rate-limit deadlines are stored in SQLite and survive restarts and full
resyncs. The default cycle is seven days. Atlassian can supply a different cycle
in days through `Cycle-Period` or a retry delay in seconds through `Retry-After`.
The job uses the latest advertised cycle for subsequent batches and preserves
each already reported account’s minimum interval. If a request’s outcome is
ambiguous, SQLite retains the attempt for the known cycle to avoid resending
early. The next eligible report can recover any actions from a lost response.
[Atlassian privacy guide](https://developer.atlassian.com/cloud/jira/platform/user-privacy-developer-guide/) ·
[Atlassian Cycle-Period clarification](https://community.developer.atlassian.com/t/cycle-period-header-example/26940/2)

## Stored data and retention

- OAuth identity covers Atlassian account IDs on stored grants and sessions and
  the Jira time zone encrypted within each grant. Sessions expire after 30 days
  and expired rows are pruned on login. Per-account grants enable browser
  sessions and background reporting until replaced, invalidated, or erased.
- The Jira projection covers project member names and avatars, issue assignees
  and creators, comment and tracked change authors, and account IDs in
  structured Atlassian Document Format mentions. Projection data follows Jira
  on refresh or full resync. A verified current profile replaces stale names
  or avatars in all of these projections where they are used.
- Meeting completion and the change audit retain account attribution, issue
  keys where applicable, and the original event times. Actor attribution is
  erased on closure and the chronology is retained as meeting history. Audit
  account IDs use the instant they were recorded locally when determining the
  age of the oldest retained reference.
- Closure deletes the encrypted grant, sessions, and local profile. It clears
  attributable identity from projection and meeting rows while preserving the
  tickets, change events, and comments. A SHA-256 digest of the closed account
  ID remains in SQLite for as long as this Jira source can feed the app. The
  digest suppresses later pulls, audit writes, and sign-ins for that account.
  Report cadence and interrupted attempts also use digests as scheduling keys.
  The operator-managed allowlist is access configuration. Remove a closed
  account from it as part of access administration.
- An update clears stale names, avatars, cached meeting-panel author details,
  and the encrypted grant's cached time zone. The next session lookup obtains
  its current time zone from Jira Myself. The reporting worker refreshes
  display profiles with Jira's Get user API and keeps unavailable profiles
  suppressed while it retries. The permission to browse users and groups and
  profile visibility determine what the reporting grant can retrieve. Audit
  account IDs remain stable across profile updates.
  [Jira Get user](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-users/#api-rest-api-3-user-get)
- Free-form work content such as ticket titles, descriptions, and comment text
  follows Jira's retention. The automated user lookup and erasure cover
  structured account-linked fields. Jira content that identifies someone in
  prose requires handling through the source ticket. The service keeps its
  transient per-session Jira results in process memory.

Migration `00016` inventories identifiable legacy rows. It uses saved snapshot
times, existing session timestamps, and the schema adoption dates as
conservative bounds where the old schema lacks a retrieval time. It removes
legacy name-only projection values that lack an account ID and schedules a
historical fetch to repopulate those fields with linked IDs. Apply the normal
retention and restricted access controls to database files, SQLite journals,
and backups. When restoring an older backup, carry forward the current
closure digests and reconcile affected identities before serving or syncing.

## Operator setup and production proof

1. Complete the owner-only OAuth cutover through PR #240 and its deployment
   runbook. Keep Atlassian organization sharing off during the pilot. Have the
   Atlassian app owner sign in to create a stored 3LO grant with the configured
   Jira Cloud and project access.
2. Set optional `JIRA_PRIVACY_OWNER_ACCOUNT_ID` in the service configuration
   when multiple approved accounts can sign in. The worker tries that account
   first when it has a usable stored grant, then tries other allowlisted grants.
   With the owner-only allowlist, the owner is the only candidate. The worker
   runs against live Jira with the existing OAuth settings.
3. Deploy the reporting build. After the owner signs in, wait for the next
   check or restart the service through the normal deployment procedure to
   check immediately. Use the service journal to confirm a nonzero result.

   ```sh
   journalctl -u jira-stats.service --since today --grep='jira privacy: successfully reported'
   ```

4. Record the deployment version, UTC timestamp, and aggregate account count
   as issue #241 verification evidence. A line such as
   `jira privacy: successfully reported 18 accounts` is emitted after
   Atlassian acknowledges the batch and SQLite commits the outcome. Use that
   evidence for the Atlassian Console declaration before enabling organization
   sharing. If the journal says no usable stored grant, verify that the owner
   completed the sign-in and can still browse the configured Jira project.
