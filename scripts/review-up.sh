#!/usr/bin/env bash
#
# review-up.sh — boot an ephemeral, disposable review instance of the jira-stats
# dashboard for an agent (or human) to drive, then tear it down with
# review-down.sh. It builds the binary, picks an OS-assigned free port, launches
# the process in the background against the built-in credential-free fake Jira
# with a pinned clock (REVIEW_NOW), polls "/" until it serves, and records the
# base URL / pid / log under tmp/review/.
#
# See docs/adr/0001-agent-driven-acceptance-review-harness.md for the rationale
# and smoke/smoke_test.go for the free-port + readiness-poll pattern this mirrors.
#
# Env overrides:
#   REVIEW_NOW         RFC3339 instant to pin the web clock (default below). The
#                      default sits inside the fake backend's active sprint window.
#   READINESS_TIMEOUT  Seconds to wait for "/" to answer 200 (default 30).
set -euo pipefail

# Resolve the repo root from this script's own location so it runs from any CWD.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

STATE_DIR="$REPO_ROOT/tmp/review"
BIN="$REPO_ROOT/bin/jira-stats"
DB_PATH="$STATE_DIR/review.db"
LOG="$STATE_DIR/log"

# A fixed default that matches the fake backend's active sprint window (14:00
# Europe/Berlin on Wed 15 Jul 2026 — ISO week KW29). Override via the env var.
: "${REVIEW_NOW:=2026-07-15T12:00:00Z}"
: "${READINESS_TIMEOUT:=30}"

# REVIEW_DATASET selects the backing fixture. "dense" boots the dense/adversarial
# dataset (issue #104) that stresses every view's layout; unset (or any other
# value) keeps the canonical canned dataset, unchanged.
: "${REVIEW_DATASET:=}"
if [ "$REVIEW_DATASET" = "dense" ]; then
	echo "review-up: REVIEW_DATASET=dense"
fi

# Always start from a clean slate: stop any prior instance and clear its state so
# re-running review-up never orphans a process or reuses a stale DB/port file.
"$SCRIPT_DIR/review-down.sh"

mkdir -p "$STATE_DIR"

echo "review-up: building binary (CGO_ENABLED=0)..."
CGO_ENABLED=0 go build -o "$BIN" ./cmd/jira-stats

# Ask the OS for an unused loopback TCP port (mirrors smoke/ freePort: bind to
# 127.0.0.1:0, read back the assigned port, release it). A tiny race exists
# between release and re-bind, but each review gets a distinct port so parallel
# instances do not collide.
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
addr="127.0.0.1:${port}"
url="http://${addr}"

echo "review-up: launching on ${url} (REVIEW_NOW=${REVIEW_NOW})"
# Blank JIRA_* (present but empty) forces the fake backend and, crucially, stops
# godotenv from backfilling real credentials from a local .env — a set-but-empty
# var counts as present, so it is never overridden. SYNC_INTERVAL=1s backfills
# the fake dataset quickly.
LISTEN_ADDR="$addr" \
DB_PATH="$DB_PATH" \
SYNC_INTERVAL="1s" \
REVIEW_NOW="$REVIEW_NOW" \
REVIEW_DATASET="$REVIEW_DATASET" \
JIRA_BASE_URL="" JIRA_EMAIL="" JIRA_API_TOKEN="" \
AUTH_DISABLED="${AUTH_DISABLED:-true}" \
AUTH_EMAIL="${AUTH_EMAIL:-}" AUTH_PASSWORD="${AUTH_PASSWORD:-}" \
	"$BIN" >"$LOG" 2>&1 &
pid=$!

echo "$pid" >"$STATE_DIR/pid"
echo "$url" >"$STATE_DIR/url"

echo "review-up: waiting up to ${READINESS_TIMEOUT}s for ${url}/ to serve..."
deadline=$(( $(date +%s) + READINESS_TIMEOUT ))
until curl -fs -o /dev/null "${url}/"; do
	# Fail fast if the process died before it started serving.
	if ! kill -0 "$pid" 2>/dev/null; then
		echo "review-up: ERROR: process $pid exited before serving. Log follows:" >&2
		cat "$LOG" >&2 || true
		exit 1
	fi
	if [ "$(date +%s)" -ge "$deadline" ]; then
		echo "review-up: ERROR: timed out after ${READINESS_TIMEOUT}s waiting for ${url}/. Log follows:" >&2
		cat "$LOG" >&2 || true
		"$SCRIPT_DIR/review-down.sh" || true
		exit 1
	fi
	sleep 0.2
done

echo "review-up: ready. url=${url} pid=${pid} log=${LOG}"
echo "$url"
