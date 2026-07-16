#!/usr/bin/env bash
#
# review-down.sh — stop the ephemeral review instance started by review-up.sh and
# remove its temp DB and tmp/review/ state. Idempotent: a clean no-op when
# nothing is running.
#
# See docs/adr/0001-agent-driven-acceptance-review-harness.md for the rationale.
set -euo pipefail

# Resolve the repo root from this script's own location so it runs from any CWD.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." >/dev/null 2>&1 && pwd)"

STATE_DIR="$REPO_ROOT/tmp/review"
PID_FILE="$STATE_DIR/pid"

if [ -f "$PID_FILE" ]; then
	pid="$(cat "$PID_FILE" 2>/dev/null || true)"
	if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
		echo "review-down: stopping pid ${pid} (TERM)..."
		kill -TERM "$pid" 2>/dev/null || true
		# Wait up to ~5s for a clean shutdown (the binary handles SIGTERM).
		for _ in $(seq 1 50); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.1
		done
		if kill -0 "$pid" 2>/dev/null; then
			echo "review-down: pid ${pid} still alive after TERM; sending KILL." >&2
			kill -KILL "$pid" 2>/dev/null || true
		fi
	fi
fi

# Remove the temp DB (and its WAL/SHM siblings) plus all tmp/review/ state.
rm -rf "$STATE_DIR"

echo "review-down: cleaned up (tmp/review removed)."
