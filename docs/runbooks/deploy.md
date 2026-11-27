# jira-stats deployment runbook

How to operate the public deployment of jira-stats at
**https://jira-stats.in.9elf26.ai**. Companion to the design spec (issue #124)
and the cost research in `docs/research/aws-hosting-internal-tooling.md`.

## What's running where

| Piece | Value |
|---|---|
| AWS account | `9elf26-internal-tooling` — `214519213070` (Infrastructure OU) |
| Region | `eu-central-1` |
| Instance | `i-0220fc1a6bee863d6` — `t4g.micro` (arm64, AL2023), Elastic IP `63.185.210.121` |
| App | `jira-stats.service` (systemd), binary `/opt/jira-stats/jira-stats`, binds `127.0.0.1:8080` |
| Reverse proxy | stock Caddy (`caddy.service`), `/etc/caddy/Caddyfile`, TLS via Let's Encrypt (HTTP-01, auto-renew) |
| DNS | `jira-stats.in.9elf26.ai` A → `63.185.210.121` in the delegated zone `in.9elf26.ai` (`Z087691143G63LUY1OIN`) |
| Secrets | The instance role reads SSM SecureString values at startup. Runtime copies include `JIRA_API_TOKEN`, `JIRA_OAUTH_CLIENT_ID`, `JIRA_OAUTH_CLIENT_SECRET`, and `JIRA_OAUTH_TOKEN_KEY`. |
| Non-secret config | `/etc/jira-stats/config.env` |

Operator access is via **SSM Session Manager** (no SSH / no public port 22):

```sh
aws ssm start-session --target i-0220fc1a6bee863d6 --region eu-central-1
```

(assume `OrganizationAccountAccessRole` in `214519213070` first, or use your
Identity Center login for the tooling account).

## Atlassian OAuth cutover

Register `https://jira-stats.in.9elf26.ai/auth/callback` in the shared Atlassian
3LO app and configure resource-level grants. Allow browser access to
`auth.atlassian.com` and server HTTPS egress to `auth.atlassian.com`,
`api.atlassian.com`, and the service-account Jira site. Caddy terminates HTTPS
and passes the public host to the app. The browser receives Secure, HttpOnly,
host-only cookies and uses a top-level callback navigation.

Provision all six [OAuth settings](../../README.md#atlassian-sign-in) in the
systemd service environment before deploying the OAuth build. Set the Cloud ID,
callback, and comma-separated allowlist in deployment configuration. The
`jira-stats Atlassian OAuth (3LO)` item in the 1Password vault `Agent Credentials`
is the source and backup for the OAuth client ID, client secret, and token
encryption key. Store identical runtime copies in the production SSM
SecureStrings read by the instance’s secret loader at startup. Preserve the
encryption key across restarts and keep it separate from the database and its
backups. The app uses one running process per SQLite database to serialize
refreshes by account. The host needs IANA time zone data (`tzdata`) for Jira
user time zones and `Europe/Berlin`. Keep the SQLite path writable and persistent
across releases. Avoid logging callback query strings, which carry the code
and state.

Migration `00012` creates the Auth tables on startup. Existing shared-login
sessions require a new Atlassian sign-in. If OAuth configuration is missing or
invalid, the live binary exits at startup. The existing Jira service account
continues to run the background sync and full resync.

To recover an unavailable or compromised client secret, rotate it in the
Atlassian developer console and update both 1Password and SSM. If the runtime
encryption key is missing, restore the same key from the named 1Password item.
If the original key is lost, generate a replacement, update both copies, and
clear the unreadable grants and their sessions in maintenance with the service
stopped. Users then sign in again to create grants encrypted with the new key.

For reporting setup, retention, and the proof required before sharing the app,
follow [Jira privacy reporting](jira-privacy.md).

## Redeploy a new build (single step)

Ship the new binary and restart. Schema migrations run on boot. Preserve the
SQLite database across deployments so browser sessions can continue.

```sh
./scripts/deploy.sh
```

This builds `bin/jira-stats-linux-arm64` (`make build-arm64`), uploads it to the
artifact bucket, and — over SSM — swaps `/opt/jira-stats/jira-stats` and runs
`systemctl restart jira-stats`. Run it from the repo root with management-account
AWS credentials. **Gate:** run `make check` (tests + smoke) before deploying — a
broken build must never reach the instance.

To roll back, check out the previous commit and re-run `./scripts/deploy.sh`.

## Live acceptance checklist

Run once after any infrastructure change. Executed 2026-07-22 against the live
instance — all passed:

- [x] **Public HTTPS, trusted cert** — `curl https://jira-stats.in.9elf26.ai/`
      succeeds with no `-k` (Let's Encrypt cert, issuer `acme-v02.api.letsencrypt.org`,
      valid Jul 22 → Oct 20 2026, auto-renewing).
- [x] **HTTP → HTTPS redirect** — `http://jira-stats.in.9elf26.ai/sprint` → `308`
      to the HTTPS URL.
- [x] **Login gate challenges** — unauthenticated `GET /sprint` → `302` →
      `/login?next=%2Fsprint`.
- [x] **Login gate admits + Secure cookie** — `POST /login` (admin@9elf26.com) →
      `302` → `/sprint`, `Set-Cookie: sofia_session=…; HttpOnly; Secure; SameSite=Lax`
      (the `Secure` flag confirms `X-Forwarded-Proto: https` is honored behind Caddy).
- [x] **Views load** — authenticated `/sprint`, `/velocity`, `/daily`, `/board`
      all return `200`.
- [x] **Full resync completes** — `POST /resync` → `200`; backfilled 1407 DCAI
      issues from live Jira; `/resync/status` shows "Last full resync" updating.
- [x] **Survives reboot** — after `aws ec2 reboot-instances`, `jira-stats.service`
      auto-starts (`active`/`enabled`) and re-syncs the projection unattended.

Quick re-check of the public surface (no reboot):

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://jira-stats.in.9elf26.ai/sprint   # 302
```

## Add another internal app

The instance and Caddy are sized/structured to host more small internal tools.
Adding app #2 is incremental — no new box:

1. **DNS** — add an `A` record `<app>.in.9elf26.ai` → `63.185.210.121` in the
   delegated zone `Z087691143G63LUY1OIN` (tooling account).
2. **App** — drop its binary on the instance and add a systemd unit modelled on
   `jira-stats.service` (its own user, its own local `127.0.0.1:<port>`, secrets
   from its own SSM parameters).
3. **Caddy** — append one block to `/etc/caddy/Caddyfile` and reload:

   ```
   <app>.in.9elf26.ai {
       reverse_proxy 127.0.0.1:<port>
   }
   ```

   ```sh
   systemctl reload caddy
   ```

Caddy issues/renews that hostname's certificate automatically.
