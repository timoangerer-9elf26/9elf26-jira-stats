---
status: accepted
---

# The Board gains standup filters (assignee, no-estimate, active-in-24h)

The Board — until now a read-only, unfiltered data-quality snapshot of the
active sprint — becomes the intended **standup surface** by gaining three
composable, URL-encoded filters: an **assignee** filter (the Daily view's exact
multi-select avatar control), a **no-estimate** toggle, and an
**active-in-last-24h** toggle. All default OFF, so `/board` still opens as the
full snapshot. This required plumbing per-card **activity data** into the Board's
projection, which previously carried no timestamps.

## Considered options

- **A jump into the Daily view** (a `/board` link to `/daily?preset=last-24h`)
  was the smaller change and avoided new Board data, but the goal is a *single*
  filtered board for the daily meeting, not a hop between two views — so we chose
  to bring activity into the Board instead.
- **Adopting Daily's movement chrome on the Board** (origin badges, movement-kind
  colour, a collapsed Done column) was rejected: it would make the filtered Board
  *become* the Daily view. The Board keeps its own presentation — cards in their
  **current** status column across the full (three-column) Done set, no movement
  badges — and stays a snapshot. Only a subtle latest-activity timestamp is added.

## The non-obvious decisions

- **"Active" reuses the Daily rule, so intra-Done moves hide a card.** A card is
  active-in-window if it was created in the window or had a status change within
  it, with moves whose *both* endpoints are in the Done set ignored (see
  `docs/adr/0003` #98). On the Board those Done statuses are *visibly separate
  columns*, so a card whose only 24h activity was a DONE (This Sprint) → Ready for
  Release housekeeping move is **hidden** when the filter is on, despite having
  changed columns. Accepted: that move is post-completion housekeeping (usually
  automated), and consistency with Daily's definition matters more than "it moved."

- **Canceled stays off-board, so the 24h lens can't show "what got dropped."**
  The Board keeps Triage and Canceled off-board. A ticket cancelled within the
  window therefore never appears under the active-in-24h filter — a gap the Daily
  view (which shows a Canceled column when non-empty) does not have. Accepted for
  now to keep this change tight and the Board's identity intact; revisit if
  standups feel the miss.

- **The Daily view is not retired.** The two views overlap heavily once the Board
  is filterable, and the long-term intent is for the Board to absorb Daily's
  role. Both coexist until the filtered Board is proven in real standups —
  retiring Daily is hard to reverse and out of scope here.
