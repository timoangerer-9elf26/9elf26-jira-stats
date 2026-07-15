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
- **Completed** (`/completed`) — S/M/L/no-estimate counts and points for work
  that entered the Done category within a chosen date range (presets + custom
  from/to), independent of the active sprint.
- **Velocity** (`/velocity`) — completed points per ISO calendar week (labeled
  `KW##`) for the last several weeks, to inform planning.

## Architecture in one line

A background loop syncs Jira into SQLite; all views query SQLite only. The
database is a **pure, rebuildable projection of Jira** — deleting it costs only
a re-sync, not real data.

## Configuration

All settings come from environment variables (no secrets in the repo). Copy
`.env.example` to `.env` and edit. The real `.env` is gitignored.

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
# Build the binary (embeds templates, CSS, and HTMX):
make build                                   # -> bin/jira-stats
# or directly:
CGO_ENABLED=0 go build -o bin/jira-stats ./cmd/jira-stats

# Run it (reads configuration from the environment):
./bin/jira-stats
```

With no Jira credentials set, it serves the canned fake dataset — open
<http://localhost:8080/>. For live data, set the `JIRA_*` variables (e.g. via
`.env`) and restart.

For a quick dev loop without building a binary:

```sh
make run        # go run ./cmd/jira-stats
make test       # go test ./...
```

## Testing

Tests drive the real handlers at the HTTP boundary, backed by a temp SQLite
database and a fake Jira — one seam covering sync, rollups, and rendering
end-to-end. Run them with `make test` or `go test ./...`.
