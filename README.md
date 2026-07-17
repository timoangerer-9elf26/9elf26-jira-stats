# 9elf26-jira-stats

A small self-hosted web dashboard that continuously syncs a Jira Cloud project
into a local SQLite database and presents always-current, historically-honest
sprint statistics. It exists so the team can see sprint progress and plan next
week's capacity without running manual, point-in-time summaries.

Completed work is measured by the **transition into Jira's Done category** (by
timestamp), not by current status — so releasing or redeploying a ticket never
silently removes it from the counts.

## Views

- **Now** (`/`) — a live board of open work by workflow status, each column
  showing S / M / L / no-estimate counts and total points, plus a grand total.
  Self-refreshes every ~30s.
- **Weekly** (`/weekly`) — the sprint-planning view: for the active sprint over
  a chosen **week window**, the finished-this-week size tally (S/M/L/no-estimate
  counts and points). The window selector offers **Work week** (Mon 00:00 →
  Sat 00:00 Europe/Berlin, the default) and **Live sprint** (sprint activation →
  now). With no active sprint it shows a friendly empty state.
- **Velocity** (`/velocity`) — completed points per ISO calendar week (labeled
  `KW##`) for the last several weeks, to inform planning.

## Architecture in one line

A background loop syncs Jira into SQLite; all views query SQLite only. The
database is a **pure, rebuildable projection of Jira** — deleting it costs only
a re-sync, not real data.

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
| `DAILY_ME`       | _(none)_             | Your Jira **display name**; the Daily view defaults to it. Blank → "All".|
| `LISTEN_ADDR`    | `:8080`              | Address the HTTP server listens on.                                |
| `DB_PATH`        | `jira-stats.db`      | Path to the SQLite database file.                                  |

If `JIRA_BASE_URL`, `JIRA_EMAIL`, and `JIRA_API_TOKEN` are not all set, the app
falls back to a built-in **canned fake Jira** so you can run and explore the UI
locally without live access.

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
startup. Open <http://localhost:8080/>. With no Jira credentials set (blank
`JIRA_*`), it serves the built-in canned fake dataset instead of live data.

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
rebuild, which is exactly what `make dev` triggers. Combined with the Now
board's 30s browser poll, changes show up shortly after you save; other views
update on the next reload. (A Tailwind class change still needs `make css` to
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
