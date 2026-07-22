# jira-stats deployment runbook

How to operate the public deployment of jira-stats at
**https://jira-stats.in.9elf26.ai**. Companion to the design spec (issue #124)
and the cost research in `docs/research/aws-hosting-internal-tooling.md`.

## What's running where

| Piece | Value |
|---|---|
| AWS account | `9elf26-internal-tooling` — `214519213070` (Infrastructure OU) |
| Region | `eu-central-1` |
| Instance | `i-0220fc1a6bee863d6` — `t4g.small` (arm64, AL2023), Elastic IP `63.185.210.121` |
| App | `jira-stats.service` (systemd), binary `/opt/jira-stats/jira-stats`, binds `127.0.0.1:8080` |
| Reverse proxy | stock Caddy (`caddy.service`), `/etc/caddy/Caddyfile`, TLS via Let's Encrypt (HTTP-01, auto-renew) |
| DNS | `jira-stats.in.9elf26.ai` A → `63.185.210.121` in the delegated zone `in.9elf26.ai` (`Z087691143G63LUY1OIN`) |
| Secrets | `/jira-stats/JIRA_API_TOKEN`, `/jira-stats/AUTH_PASSWORD` (SSM SecureString), read at start by the instance role |
| Non-secret config | `/etc/jira-stats/config.env` |

Operator access is via **SSM Session Manager** (no SSH / no public port 22):

```sh
aws ssm start-session --target i-0220fc1a6bee863d6 --region eu-central-1
```

(assume `OrganizationAccountAccessRole` in `214519213070` first, or use your
Identity Center login for the tooling account).

## Redeploy a new build (single step)

The SQLite store is a rebuildable projection of Jira, so there is no data
migration — ship the new binary and restart; the app re-syncs on boot.

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
