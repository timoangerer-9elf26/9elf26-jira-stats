---
status: accepted
---

# Agent-driven acceptance-review harness and loop

To close the requirements → implementation → review loop with *live* verification
(not just diff-reading), we add an ephemeral, per-review harness that boots the
real binary against the built-in credential-free fake backend, plus a distinct
`acceptance-review` skill that drives the running app (browser UI **and** HTTP
API) to confirm each acceptance criterion holds. This keeps the existing
`code-review` (static diff: Standards + Spec) unchanged and separate.

## Shape

- **Ephemeral, per-review boot.** A launcher (`scripts/review-up.sh` /
  `review-down.sh`, thin `make review-up` / `review-down` wrappers) builds the
  binary, picks an **OS-assigned free port** (`127.0.0.1:0`, as `smoke/` already
  does), background-launches with blank `JIRA_*` (fake backend) and a temp
  `DB_PATH`, polls `/` until healthy, and writes `tmp/review/{url,pid,log}`.
  Teardown kills the pid. Every review gets a clean, disposable instance; no
  shared mutable state, no long-lived process.
- **Two independent review passes, separately dispatched.** `requesting-code-review`
  → `code-review` (cheap, frequent, static). A new `requesting-acceptance-review`
  → `acceptance-review` (drives the running app, AC-by-AC). Decoupled because
  code review runs far more often than a full live acceptance pass, and each
  should be invokable on its own with its own crafted context.
- **Loop:** requirement (GitHub issue + ACs) → implement on a branch (commit refs
  the issue) → implementer self-verify (lightweight: one changed view, one
  screenshot, no console/network errors) → fresh sub-agent reviews (same worktree,
  own harness instance) → fix loop → PR → green `make check` → squash-merge.
- Evidence lands in gitignored `tmp/review/`; the key screenshot/observations are
  **inlined** into the review output (a bare path is only evidence to someone
  sharing the filesystem).

## Considered options — the two non-obvious choices

- **Driver: Playwright MCP (`@playwright/mcp`), not the already-installed
  `playwright-cli`.** `playwright-cli` would work today with zero setup and covers
  a11y-snapshot + screenshot + console + network. We chose the MCP server anyway:
  it is the ecosystem-standard, gives the agent first-class browser tools rather
  than shelled-out CLI calls, and is the better long-term surface. The harness is
  driver-agnostic, so this choice is contained to the review agent's config.
- **Exposing an injectable clock in the shipping binary.** The date-bearing views
  (Completed presets, Velocity `KW##` weeks, Daily window) compute ranges from
  `now` **server-side**, so the canned fake dataset otherwise produces different
  numbers — and drifting screenshots — day to day. `web.WithClock` already exists
  as a seam but is unreachable from the binary. We wire a new `REVIEW_NOW`
  (RFC3339) env var to it so the launcher can pin a fixed instant. This is the
  precondition for trustworthy live/visual review of this app's primary views.

## Consequences

- The production binary gains a `REVIEW_NOW` env var. A future reader will
  reasonably ask "why does the shipping app accept a fake now?" — the answer is
  deterministic review, and this ADR is that answer. It is inert unless set.
- Adopting Playwright MCP adds install/registration setup (transport choice:
  stdio vs HTTP) that `playwright-cli` would not have required.
- If visual-diff baselines are later added, they must be generated in one fixed
  environment (see `docs/research/agent-review-loop-infra.md`), since OS/font
  rendering differs across worktree hosts.
