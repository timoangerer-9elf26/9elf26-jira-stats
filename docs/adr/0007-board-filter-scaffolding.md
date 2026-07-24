---
status: accepted
---

# Reusable Board filter scaffolding

The [Board](../../CONTEXT.md) gains filters (#157 assignee, then #158 no-estimate,
#159 active-in-24h). Rather than wire each filter ad hoc, the first ticket builds
**reusable filter scaffolding** so the follow-ons add a filter without reworking
the chrome, the fragment, or the URL plumbing.

## Context

The Daily view already has URL-encoded, HTMX-swapped controls (a range selector
and an assignee avatar bar), but its plumbing is hand-wired to those two specific
controls — the assignee chips hard-code `hx-include="[name='preset'],[name='from'],[name='to']"`,
naming their siblings. Copying that shape onto the Board would make each new
Board filter edit the shared chrome and every other filter's include list — the
opposite of the "add a filter without reworking the plumbing" goal (#157 AC5).

The Board must also keep **all columns rendered** while filtering: a filter hides
cards, it never removes a column. So filtering happens over the projected board
cards, not by re-querying a narrower board.

## Decision

A filter is a value (`boardFilter`) resolved from the request query, carrying
three things and nothing view-specific:

- **`Control` + `Data`** — the name of its chrome partial and the model to render
  it with. The chrome region ranges over the filters and dispatches by name via a
  `render` template func (html/template can't take a dynamic `{{template}}` name).
- **`Params`** — its current URL params, re-emitted as hidden inputs marked
  `data-filterparam`, so a change to *any* control round-trips *every* filter.
- **`Keep(card) bool`** — a predicate; a card is shown only if it passes every
  filter (`keepCard` ANDs them). Columns are untouched.

The filters live in one ordered registry, `Server.boardFilters(query)`. The
plumbing is filter-agnostic:

- `GET /board` renders the full page; `GET /board/results` renders the same
  `board-panel` fragment (chrome + column headers + card strip). The filter form
  targets `#board-panel` and swaps `innerHTML`, so a filter change re-renders the
  controls, the header counts and the cards together — mirroring `/daily/results`.
- Sibling preservation is **generic, by attribute**: a control that replaces its
  own param (the assignee chips encode the resulting set in their URL)
  `hx-include`s `[data-filterparam]:not([name='<own>'])` — every other filter, and
  only the others, with no sibling named. A new filter is picked up automatically.

The assignee avatar bar itself is extracted into a shared `assignee-bar` partial
that both the Daily controls and the Board filter render, so the two bars are
literally the same control (same chips, same server-driven toggle, same
Unassigned sentinel), parameterised only by testid prefix, Clear target and the
include selector.

**Adding a filter (#158/#159)** is therefore: write a constructor returning a
`boardFilter` (parse params → build its control model, its `Params`, its `Keep`),
append it to `boardFilters`, and ship its control partial. No route, handler,
fragment, or URL-plumbing change.

## Consequences

- Filtering is in the web layer over `store.BoardCard`, not in a store query.
  Fine for assignee (a field on the card) and the no-estimate toggle; the
  active-in-24h filter (#159) may need the card to carry a last-activity instant,
  which is a data addition on the card, not a change to this scaffolding.
- The Daily view now depends on the shared `assignee-bar` partial. Its bespoke
  range plumbing is left as-is (not migrated onto this registry) — the scaffolding
  is introduced where the new filters land, not retrofitted across the app.
- The `render` func executes a named partial to trusted HTML; it is only ever fed
  code-controlled template names, never user input.
