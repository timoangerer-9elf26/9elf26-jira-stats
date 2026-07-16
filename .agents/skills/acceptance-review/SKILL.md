---
name: acceptance-review
description: Drive the running app to confirm each acceptance criterion actually holds — live verification, not diff-reading. Boots the ephemeral review launcher, drives the touched routes via the Playwright MCP browser driver (UI) and direct HTTP (API), checks each AC against observed behavior, and reports pass/fail with inlined evidence. Use to acceptance-test a change against its issue's ACs; distinct from code-review (which reads the static diff).
---

# Acceptance review

Confirm each **acceptance criterion** of a change holds in the *running* app, by driving it — not by reading the diff. This is the live half of the review loop; `code-review` (Standards + Spec on the static diff) is the other half and stays separate.

## Prerequisites

- The **Playwright MCP** driver is registered (browser tools available — see `.mcp.json` for Claude Code; `~/.codex/config.toml` for Codex). If the tools aren't present, say so and stop — this skill can't run without them.
- The **review launcher** from `docs/adr/0001-agent-driven-acceptance-review-harness.md`: `make review-up` / `make review-down`.

## Inputs

- The **issue** whose acceptance criteria you're verifying (e.g. `gh issue view <n> --json title,body`).
- The **base..head** SHAs of the change, to know which routes/behaviors it touches (`git diff --stat <base>..<head>`).

## Process

1. **Collect the ACs.** Read the issue's acceptance-criteria checklist verbatim. List them as the checklist you will verify — one finding per AC.
2. **Scope the surface.** From the diff, list the routes/views/endpoints the change touches (e.g. `/board`, `/completed`, `/daily`, a fragment endpoint, a store rollup surfaced in HTML). Each AC maps to something observable there.
3. **Boot a clean instance.** `make review-up`, then read `tmp/review/url`. It runs the fake backend with a pinned `REVIEW_NOW`, so date-bearing views are deterministic. (Never point acceptance review at live Jira.)
4. **Drive, per AC.** Use the right channel for each:
   - **UI** via Playwright MCP: navigate to `<url><route>`, take an **accessibility-tree snapshot** as the control channel (find/assert elements), and capture a **screenshot** + **console/network** as evidence. Prefer the snapshot for assertions; the screenshot is for the human reader.
   - **API/HTTP** directly (curl/fetch) for fragment endpoints, status codes, and content that's easier to assert as text (e.g. an HTMX partial, a JSON-less HTML fragment, exact counts).
   - Exercise the *actual* behavior the AC describes (e.g. "selecting Last week highlights Last week" → drive the control and observe the swapped DOM, don't just check the endpoint returns 200).
5. **Judge each AC** pass / fail / unclear against what you observed, quoting the AC.
6. **Capture evidence** under `tmp/review/` (screenshots, snapshots, HTTP responses) **and inline the decisive bits into your findings** — a bare file path is not evidence to a reader who doesn't share the filesystem. Inline the snapshot excerpt / the rendered text / a described screenshot.
7. **Tear down.** `make review-down` (always, even if a step failed — wrap in cleanup).

## Report

A per-AC checklist:

- `[x] AC 1 — PASS` — what you drove, and the observed evidence (inlined).
- `[ ] AC 2 — FAIL` — the AC, what you expected, what you actually observed (inlined), and where it diverged.

End with an overall verdict (all ACs pass / N failing) and, for any FAIL, a concrete repro (route + action + observed result). Do not pass an AC you couldn't actually observe — mark it unclear and say why.
