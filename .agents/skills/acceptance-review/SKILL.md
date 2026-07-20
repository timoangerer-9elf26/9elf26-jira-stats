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
2. **Scope the surface.** From the diff, list the routes/views/endpoints the change touches (e.g. `/board`, `/weekly`, `/daily`, a fragment endpoint, a store rollup surfaced in HTML). Each AC maps to something observable there.
3. **Boot a clean instance.** `make review-up`, then read `tmp/review/url`. It runs the fake backend with a pinned `REVIEW_NOW`, so date-bearing views are deterministic. (Never point acceptance review at live Jira.)
   - **Layout that scales with data → boot the dense dataset.** When the touched view's layout scales with its data (charts, tables, lists, boards — anything whose rows/bars/cards/columns grow with the input), boot the **dense/adversarial dataset** instead of the canonical one: `REVIEW_DATASET=dense make review-up`. The canonical fixture is tuned for numeric determinism (few rows, mostly-zero values) and is the *least* layout-stressing input possible — it hides collisions, wraps and overflow. The dense dataset (see `internal/jira/densereview.go`) deliberately covers **zero / one / many / max / longest-string** across all four views under the same pinned clock: ~10 Velocity bars with a tall outlier and an ongoing active bar, every Sprint cohort×outcome cell populated (incl. an excluded carry-over and a long drill-down), every Board column filled with long-titled cards, and a dense Daily digest with created tickets and a dropped intra-done sequence. Use the canonical dataset only when the AC is about exact numbers.
4. **Drive, per AC.** Use the right channel for each:
   - **UI** via Playwright MCP: navigate to `<url><route>`, then use **two verification channels** — they assert different things and both are required for any AC touching UI:
     - The **accessibility-tree snapshot** asserts **structure and content** (elements present, order, text, roles). It is the control channel for finding/asserting elements — but it says *nothing* about how the page actually rendered.
     - A **screenshot** asserts **visual correctness** (alignment, overlap, clipping, wrapped/truncated text, color, spacing, overall layout). The accessibility snapshot cannot catch any of these. So for a UI AC the screenshot is a **mandatory verification surface, not a human-facing artifact**: you must **open and look at the image yourself** and judge the rendered pixels against the AC — capturing the file is not enough.
     - **Framing matters.** `fullPage` captures vertical scroll but **not horizontal overflow** — on a horizontally-scrolling or wide layout (e.g. the kanban `/board`) it silently clips exactly the off-frame columns a change may have touched. For such layouts, **widen the viewport** (`browser_resize` to a width past the content, e.g. 2560+) or scroll-and-stitch so the *entire changed surface* is in frame. Before citing a screenshot as evidence, **confirm it actually shows the changed surface** — if the reordered/added/fixed element isn't visible in the image, the screenshot proves nothing and must be re-framed.
     - **Capture ≥2 viewport widths, including a narrow one (~375px).** A layout that is clean at desktop width often collides, wraps or overflows once columns pack narrow — that is exactly where the value spread and long strings in the dense dataset bite. Take each screenshot at a narrow width (`browser_resize` to ~375px) **and** at least one wider width. For horizontally-scrolling views (e.g. Board), still widen or scroll-and-stitch so the whole surface is framed at the wide width, and separately capture the narrow width to expose card wrap and column crowding.
     - **Run the visual checklist against every screenshot and record the result.** For each image, explicitly judge and write down:
       - **Baseline / alignment** — do repeated elements (bars, cards, columns, table cells) share one baseline, or does one push out of line (e.g. a two-line label lifting one bar above its row)?
       - **Text wrap / truncation / overflow** — do long summaries, assignee names, sprint names or date lines wrap, clip or spill their container?
       - **Collision with container edges (headroom)** — does the tallest/widest element (e.g. the outlier Velocity bar) touch or overrun the container top/edge?
       - **Empty / single / dense states** — does the view hold up across the zero, one and many cases the dense dataset provides?
     - Also capture **console/network** as supporting evidence.
   - **API/HTTP** directly (curl/fetch) for fragment endpoints, status codes, and content that's easier to assert as text (e.g. an HTMX partial, a JSON-less HTML fragment, exact counts).
   - Exercise the *actual* behavior the AC describes (e.g. "selecting Last week highlights Last week" → drive the control and observe the swapped DOM, don't just check the endpoint returns 200).
5. **Judge each AC** pass / fail / unclear against what you observed, quoting the AC.
   - **A visual/layout AC cannot be signed off from unit tests.** Unit tests assert numbers and structure; they say nothing about how the page rendered. If a layout AC is only backed by a passing unit test, it is **not verified**. And if the live data can't exercise the realistic shape the AC is about (a tall bar, a full board, a long list), that is a **fixture gap to close — boot the dense dataset (`REVIEW_DATASET=dense`)** — never a reason to defer the check to a unit test or to a "realistic case" left untested.
6. **Capture evidence** under `tmp/review/` (screenshots, snapshots, HTTP responses) **and inline the decisive bits into your findings** — a bare file path is not evidence to a reader who doesn't share the filesystem. Inline the snapshot excerpt / the rendered text. For a screenshot, inline a **description of what the image actually shows** and your visual judgment against the AC (which you can only write if you opened and looked at it) — never a bare path.
7. **Tear down.** `make review-down` (always, even if a step failed — wrap in cleanup).

## Report

A per-AC checklist:

- `[x] AC 1 — PASS` — what you drove, and the observed evidence (inlined).
- `[ ] AC 2 — FAIL` — the AC, what you expected, what you actually observed (inlined), and where it diverged.

End with an overall verdict (all ACs pass / N failing) and, for any FAIL, a concrete repro (route + action + observed result). Do not pass an AC you couldn't actually observe — mark it unclear and say why.
