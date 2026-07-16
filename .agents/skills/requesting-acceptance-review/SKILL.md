---
name: requesting-acceptance-review
description: Dispatch a fresh sub-agent with crafted context to run an acceptance-review of a change against its issue's acceptance criteria on the running app. Separate from requesting-code-review so live acceptance review is invokable independently of (and less often than) the frequent static code review. Use before merging a change whose behavior is worth confirming end-to-end.
---

# Requesting acceptance review

Hand a **fresh sub-agent** everything it needs to run `acceptance-review` on a change, so the live verification happens in a clean context and comes back as a verdict — not a pile of transcript.

Use this when a change is worth confirming *in the running app* (UI/API behavior), not just diff-reading. It's deliberately separate from `requesting-code-review`: static code review runs on almost every change; acceptance review runs on the changes where observed behavior matters (a new view, a fixed interaction, a rollup users read).

## Gather the crafted context

Assemble a **self-contained** brief — the sub-agent starts fresh and sees only what you pass:

- **Issue + acceptance criteria** — the issue number and its ACs quoted verbatim (`gh issue view <n> --json title,body`).
- **base/head SHAs** — the merge base and the branch tip, plus a `git diff --stat <base>..<head>` so the sub-agent knows the touched surface.
- **Touched routes/behaviors** — the concrete list (e.g. `/completed` preset swap, `/board` columns).
- **How to run** — boot with `make review-up`, read `tmp/review/url`, drive via the Playwright MCP driver + direct HTTP, tear down with `make review-down`.

## Dispatch

Spawn a sub-agent (a fresh Agent/Task) whose instructions are: "Follow the `acceptance-review` skill for issue #N. Here are the ACs, the base..head diff, and the touched routes. Boot the launcher, verify each AC against observed behavior, inline the decisive evidence, and return a per-AC pass/fail verdict." Require it to return the checklist verdict (not raw logs), with inlined evidence for each AC and a repro for any failure.

Keep it independent of `requesting-code-review`: they can run on the same change, but this one is opt-in per change, and its result is the *behavioral* sign-off, distinct from the Standards/Spec diff review.

## Report

Relay the sub-agent's per-AC verdict and overall pass/fail to the user. On any FAIL, surface the repro so it can be fixed before merge. Don't merge on the strength of a "probably fine" — an AC is verified only if the sub-agent observed it.
