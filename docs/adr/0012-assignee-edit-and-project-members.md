---
status: accepted
---

# Assigning on the Board makes the projection carry people, not just issues

The Board card's assignee avatar becomes editable: clicking it offers the
project's people plus Unassigned, and picking one writes the assignee back to
Jira. This is the **fourth write path** (after `docs/adr/0005`'s estimate edit,
`docs/adr/0010`'s Board transition and the Prio view's priority edit) and it
copies 0005's write → re-read → persist shape exactly.

The write is not what needed deciding. **Where the list of people comes from
is** — and answering it made the projection carry a second entity, the
[Project member](../CONTEXT.md#project-member), which is the part a future
reader will actually wonder about.

## The non-obvious decisions

### The candidate list is the project's people, not the board's

The obvious list is already lying around: the assignee filter's chips come from
`store.ActiveSprintAssignees()`, the distinct assignees of active-sprint work.
Reusing it would have cost nothing and kept one list on the Board.

Rejected, because it makes the one move you most want to make impossible:
**assigning a ticket to someone who has nothing on the board yet**. A list
derived from current assignments can only ever shuffle work between people who
already have some — it cannot bring anyone in. On a surface whose whole purpose
is the standup, "give this to Lisa, she's free" is not an edge case.

The list is short enough to just show: seven people. So it is a plain list with
no search box — a typeahead over seven names is a keystroke tax. If the project
ever grows past what a popover can hold, that is the moment to add one.

So the list is Jira's assignable users for the project
(`GET /rest/api/3/user/assignable/search`), filtered to `accountType ==
"atlassian"` — the **4 app accounts** on this project (`Jira Coding Agent`,
`Jira Delivery Agent`, `Jira Triage Agent`, `Nelf OpenClaw`) are excluded.
They are automation; a card popover offering them is 11 targets where 7 will do,
and assigning standup work to a bot reads as a mis-click. This is a product
decision, not a technical one — if agents start *owning* tickets rather than
acting on them, drop the filter.

### The assignee filter deliberately does NOT switch to this list

The Board now has two lists of faces with different membership: the filter's
chips (people with work on this board) and the assign popover (people on the
project). Unifying them was considered and rejected in both directions.

They answer different questions. The filter asks *"whose work is this?"* about
cards that exist — a chip for someone with nothing this sprint matches zero
cards and is permanent clutter on the standup surface. The popover asks *"who
should own this?"* across the team, where the whole point is reaching past the
board. The visible inconsistency is the price, knowingly paid.

### The people are SYNCED, not fetched in the request path

Until now every render read only SQLite; the app makes no Jira call in any read
path (`FetchTransitions` is the lone exception and it lives inside a write). A
user list is naturally a live lookup, and either obvious placement breaks that:
fetching per board render puts a Jira round-trip on the most-loaded page *and*
on every filter toggle, while fetching lazily on popover-open makes a slow or
failing Jira hang the control mid-standup.

Instead the syncer refreshes the project's members into the projection each
cycle, and the Board renders them from SQLite like everything else. A directory
that changes a few times a year can be up to one sync interval stale; nobody
will notice. A full resync rebuilds the member table like every other table, so
the projection stays pure and rebuildable.

This is the trade: **a migration and a sync step for a list of seven names**,
bought so that the read-path invariant survives. That is the decision worth
recording here.

### Issues still do not carry an account id

The obvious-looking move — parse `accountId` onto the issue, add a column,
resync — is unnecessary and was not done. The write needs only the **target**
person's id, and that comes from the member table. The issue keeps storing an
assignee as a display name and an avatar URL, exactly as before, and no
backfill or resync is required before the feature works.

### Two hazards this control inherits from the card it sits on

Neither is really a decision; both are recorded because their failure mode is
"nothing happened" rather than an error, which is the kind that survives review.

A board card is simultaneously a **link** and, on the drag surface, a **drag
source**, so any control placed on it must opt out of both. Opting out of only
one fails silently: the popover never opens and the card drags instead. The
estimate control already carries both opt-outs; consolidating them behind a
single marker is the prefactor that precedes this work.

The avatar partial renders the Board's cards *and* the assignee filter's chips.
Editability is therefore a per-render flag on the Board, as the estimate edit's
is — if it leaks through that shared partial, the filter bar turns into a row of
assign buttons.

## Consequences

- **Four write paths now, not three.** Every count in `CONTEXT.md` that says
  "three ways the dashboard writes to Jira" was updated with this change.
- **The projection has a second entity.** "The store is a projection of Jira
  issues" is now too narrow — it projects issues, sprints and people.
- **A reassignment can make a card leave the view it was made in.** With an
  assignee filter active, a card reassigned to someone outside that filter is
  absent from the re-render — the rule `docs/adr/0010` already set for a
  transition that moves a card out of an active filter, applied unchanged.
- **A reassignment is not a [Daily movement](../CONTEXT.md#daily-movement)** and
  will not make a card match the Board's *Active in last 24h* filter, which is
  defined on status movement. Widening that definition would ripple into Daily
  movement's classification and was not attempted.
- **The API token's account must be allowed to assign issues.** A permission
  failure surfaces as a failed write and changes nothing — the same fail-safe
  posture as the other three writes.
- **Attribution is still to the service account**, not the human who clicked;
  the app has no per-user identity (0005's limitation, now covering a fourth
  field).
- **Between a deploy and the first sync the member table is empty**, so the
  avatar is simply not editable. It self-heals within one cycle.
- **Same last-write-wins posture, same absent CSRF/auth** as 0005; not
  re-litigated here.
