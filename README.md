# 9elf26-jira-stats

A small self-hosted web dashboard that continuously syncs a Jira Cloud project
into a local SQLite database and presents always-current, historically-honest
sprint statistics. It exists so the team can see sprint progress and plan next
week's capacity without running manual, point-in-time summaries.

Completed work is measured by the **transition into Jira's Done category** (by
timestamp), not by current status — so releasing or redeploying a ticket never
silently removes it from the counts.

## Views

The root path `/` redirects to the **Sprint** view.

- **Sprint** (`/sprint`) — the sprint-planning view: for the current active
  sprint over its own window **`[sprint start, now)`**, a three-category
  breakdown — **Started with** (open and in the sprint at its start, carry-overs
  included), **Added** (entered the sprint after it started), and **Total** —
  each a size tally (S/M/L/no-estimate counts and points) with its finished
  figure. There is no window selector; the view always anchors on the active
  sprint's start, so a carry-over at a sprint boundary counts as Started-with,
  not Added. With no active sprint it shows a friendly empty state.
- **Velocity** (`/velocity`) — completed points per ISO calendar week (labeled
  `KW##`) for the last several weeks, to inform planning.

## Architecture in one line

A background loop syncs Jira into SQLite using a service account. Interactive
Jira edits use the signed-in person’s Atlassian authorization. The SQLite
database holds the rebuildable Jira projection, encrypted OAuth grants and
server-side sessions. Full resync preserves authentication data.

## Full resync

Every view carries a **refresh-icon button** (top-right of the nav, tooltip
"Resync full database") that rebuilds the SQLite projection from Jira on demand:
it clears the projection and re-runs the full backfill (all issues, complete
changelog history, sprints, and project members). Use it to heal data gaps or
pick up past capture/schema fixes **without shell access** — it supersedes the
old "stop the server, `rm jira-stats.db`, restart" step.

The rebuild runs in the background: clicking the icon returns immediately, the
icon spins and the label shows **Resyncing…** while it runs (a full backfill
takes ~15–30s against live Jira), and the views refresh on their own once it
completes (the label then shows how fresh the data is). A resync already in progress is a safe no-op, and
it never overlaps the periodic incremental sync. No confirmation is required —
the data is fully rebuildable.

## Configuration

All settings come from environment variables (no secrets in the repo). The
easiest way to supply them is a local `.env` file, which the app **loads
automatically on startup** — so you configure it once and never export
variables by hand:

```sh
cp .env.example .env      # then edit .env and fill in your Jira credentials
```

The real `.env` is gitignored and must never be committed. Real environment
variables still take precedence over `.env`, and a missing `.env` is fine (the
app just uses env vars / defaults). Only `.env.example` — with placeholder
values — is tracked.

| Variable         | Default              | Description                                                        |
| ---------------- | -------------------- | ------------------------------------------------------------------ |
| `JIRA_BASE_URL`  | _(none)_             | Jira Cloud site base URL. Blank → run against the canned fake Jira.|
| `JIRA_EMAIL`     | _(none)_             | Service-account email for basic auth.                              |
| `JIRA_API_TOKEN` | _(none)_             | Jira API token for the service account. Keep secret.               |
| `JIRA_PROJECT`   | `DCAI`               | Project key to sync (used only with live credentials).             |
| `JIRA_BOARD_ID`  | `8`                  | Agile board id (used only with live credentials).                  |
| `TZ`             | `Europe/Berlin`      | Timezone for date maths and ISO-week (`KW##`) labels.              |
| `SYNC_INTERVAL`  | `60s`                | Background sync interval (Go duration, e.g. `30s`, `5m`).          |
| `VELOCITY_WEEKS` | `10`                 | Trailing ISO weeks shown on the Velocity view (positive integer).  |
| `LISTEN_ADDR`    | `:8080`              | Address the HTTP server listens on.                                |
| `DB_PATH`        | `jira-stats.db`      | Path to the SQLite database file.                                  |

Live Jira requires all three service-account settings and a complete OAuth
configuration. Use `AUTH_DISABLED=true` with the canned fake Jira for local
development. Leave all three service-account settings blank or set
`JIRA_FAKE=true` to select the fake. `JIRA_FAKE=true` with complete OAuth
configuration and `AUTH_DISABLED=false` supports an isolated login preview.

### Atlassian sign-in

| Variable | Meaning |
| --- | --- |
| `JIRA_OAUTH_CLIENT_ID` | Client ID from the Atlassian developer console. |
| `JIRA_OAUTH_CLIENT_SECRET` | Client secret from the Atlassian developer console. |
| `JIRA_OAUTH_REDIRECT_URI` | Exact registered public HTTPS URL ending in `/auth/callback`. |
| `JIRA_OAUTH_CLOUD_ID` | Cloud ID of the site configured in `JIRA_BASE_URL`. |
| `JIRA_OAUTH_ALLOWED_ACCOUNT_IDS` | Comma-separated Atlassian account IDs allowed to access the dashboard. |
| `JIRA_OAUTH_TOKEN_KEY` | Stable 32-byte encryption key in standard base64. Generate with `openssl rand -base64 32`. |

Enable sharing and use resource-level grants in the Atlassian registration.
Enable the classic Jira scopes `read:jira-work`, `write:jira-work` and
`read:jira-user`. The app requests those scopes plus `offline_access`, verifies
the three Jira scopes on the configured site, and requires a refresh token.
Jira Myself supplies account ID and IANA time zone. The allowlist is checked on
every authenticated request. See the [deployment runbook](docs/runbooks/deploy.md)
for runtime prerequisites and the [Atlassian 3LO documentation](https://developer.atlassian.com/cloud/jira/platform/oauth-2-3lo-apps/).

#### Request context for meeting handlers

Use `web.JiraIdentityFromContext(r.Context())` for `AccountID` and `TimeZone`
(a `*time.Location` from Jira Myself). Use
`web.PersonalJiraFromContext(r.Context())` for the request-scoped `jira.Client`.
Both accessors return an `ok` flag. In local fake mode without sign-in, the
binary supplies the stable account ID `jira-stats-fake`, the configured display
time zone, and the same fake Jira client used for sync. Keep the client scoped
to the request.


## Building the CSS (Tailwind)

The styling uses Tailwind. The generated stylesheet
(`internal/web/assets/output.css`) is committed and embedded into the binary, so
**`go build` never needs Node**. You only need to rebuild the CSS after changing
Tailwind classes in the templates:

```sh
make css        # requires npx / @tailwindcss/cli (Node)
# equivalently:
go generate ./...
```

## Build and run locally

The app is a single static binary (pure-Go SQLite, `CGO_ENABLED=0`):

```sh
# 1. Configure once (see Configuration above):
cp .env.example .env      # edit .env with your Jira credentials

# 2. Build the binary (embeds templates, CSS, and HTMX):
make build                # -> bin/jira-stats
# or directly:
CGO_ENABLED=0 go build -o bin/jira-stats ./cmd/jira-stats

# 3. Run it — configuration is injected from .env automatically:
./bin/jira-stats
```

No manual `export` needed: the binary reads `.env` from the working directory on
startup. The example configuration serves the built-in canned fake dataset at
<http://localhost:8080/>.

### Release build for arm64 (AWS Graviton / t4g)

The app is deployed to an arm64 Graviton (`t4g`) instance, so there is a
dedicated cross-compile target that produces a `linux/arm64` release binary:

```sh
make build-arm64          # -> bin/jira-stats-linux-arm64
# or directly:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/jira-stats-linux-arm64 ./cmd/jira-stats
```

Because the SQLite driver is pure Go (`CGO_ENABLED=0`) and the templates, CSS,
and HTMX are embedded, this cross-compiles cleanly from any host with no C
toolchain and yields a single self-contained aarch64 binary. `make build`
(host-arch, `bin/jira-stats`) is unaffected and remains the default for local
work. Verify the output architecture with `file bin/jira-stats-linux-arm64`
(reports `ARM aarch64 … statically linked`).

For a quick dev loop without building a binary:

```sh
make run        # go run ./cmd/jira-stats
make test       # go test ./...
```

### Hot reload

`make dev` rebuilds and restarts the binary on every `.go`/`.html`/`.css` save,
using [air](https://github.com/air-verse/air) (config in `.air.toml`):

```sh
go install github.com/air-verse/air@latest   # once
make dev                                      # watches + rebuilds on save
```

Because the templates and CSS are **embedded** into the binary (`//go:embed`),
they are parsed once at startup — so a template edit only appears after a
rebuild, which is exactly what `make dev` triggers. Once air has rebuilt,
changes show up on the next reload. (A Tailwind class change still needs `make css` to
regenerate the committed `output.css`; air then rebuilds with it.)

## Testing

Tests drive the real handlers at the HTTP boundary, backed by a temp SQLite
database and a fake Jira — one seam covering sync, rollups, and rendering
end-to-end. Run them with `make test` or `go test ./...`.

### Smoke tests

The smoke tests (`smoke/`) are the "does it come up and respond" check: they
**build the real binary, boot it against the built-in fake Jira (no
credentials), and assert every route serves** — the three views plus the
embedded static assets. They complement the in-process integration tests by
exercising the actual compiled binary and its startup path.

They live behind the `smoke` build tag, so they never run in `make test`. Run
them explicitly:

```sh
make smoke      # go test -tags smoke -count=1 ./smoke/
```

Requirements: Go only — no running services, no Jira credentials, no network.
Each test picks a free port, starts the binary with `SYNC_INTERVAL=1s` and a
temp database, waits until it serves, checks the routes, and shuts it down.

### CI / pre-deploy gate

`make check` runs the unit/integration tests **and** the smoke tests together —
use it as the gate before shipping:

```sh
make check      # == make test && make smoke
```

CI runs the same steps on every push to `main` and every pull request
(`.github/workflows/ci.yml`): `gofmt` check, `go vet`, static build, `go test
./...`, then the smoke tests.
