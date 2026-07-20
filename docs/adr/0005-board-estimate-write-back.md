---
status: accepted
---

# Editable estimate on the Board writes back to Jira

The Board's estimate pill (a ticket's size — S / M / L / no-estimate) becomes
editable: clicking it opens a small popover (S · M · L · No estimate) and picking
a value **writes that estimate back to Jira** immediately, with no confirm step.
This is the **first and only write path** in an app that has until now been a
strictly read-only projection of Jira. See `CONTEXT.md` → Estimate edit for the
term.

## The non-obvious decisions

### Jira stays the source of truth — the write goes *to Jira*, not to the projection

The SQLite store is a "pure, rebuildable projection of Jira": `SaveIssue`
unconditionally overwrites every synced field (including `size`) on each
incremental cycle, and a full resync wipes and rebuilds the whole store. So a size
change written only into the local projection would be silently clobbered on the
next sync — the projection is not a place state can *originate*.

Therefore the edit is a `PUT /rest/api/3/issue/{key}` on the size field
(`customfield_10040`, the "Estimated Time" single-select: `Small` / `Medium` /
`Large`, or `null` for no-estimate). This requires a new **write primitive** on
the Jira client, which until now exposed only GET (`FetchIssues`,
`FetchIssuesUpdatedSince`, `FetchSprints`).

**Reconciliation = optimistic pill + immediate single-issue re-fetch.** On select
we update the pill optimistically and PUT to Jira; on success we immediately
re-read *that one issue* from Jira and `SaveIssue` it, so the projection is set
**only ever from a Jira read**, never from local UI state. The pill is correct
within the same request and the read-only-projection invariant is preserved
(the projection still originates entirely from Jira). Cost is one extra GET per
edit — negligible at this scale.

- *Alternative rejected — optimistic + rely on the next sync:* write the new size
  into the local `size` column and let the ~60s incremental sync reconcile.
  Simpler, no re-fetch, but it makes the projection temporarily diverge from Jira
  (state originating locally), the exact property we want to keep out of the store.
- *Alternative rejected — write-through, no local update:* PUT to Jira and touch
  nothing locally; the pill corrects on the next sync. Purest, but the pill would
  show the old value for up to a minute after an edit the user just made.

### Last-write-wins — no optimistic-locking guard

The edit is based on whatever size the projection currently shows, which can be
stale versus Jira. We do **not** guard with an "only write if Jira still has the
value we last saw" check. This is a single-user, self-hosted dashboard; concurrent
edits to the same ticket's size are not a real risk, and a guard adds round-trips
and a conflict-resolution UX for a case that won't happen here.

### Board only, though the size pill is a shared partial

The `size-chip` partial also renders on the Daily view, the "tickets I created"
list and the Sprint drill-down. The edit affordance is deliberately **Board-only**
— the request is a board feature, and confining the write path to one surface
keeps its blast radius small. The other three usages stay read-only display; the
editability is not baked into the shared partial in a way that leaks in elsewhere.

### Failure reverts locally and shows an inline error

If the Jira write (or the follow-up re-fetch) fails — permissions, network, 4xx —
the pill reverts to its prior value and a small **inline** error is shown on that
card. No global banner. A failed write must leave both Jira and the pill in their
previous state; we never show a size the write didn't actually achieve.

### The fake Jira client gains an in-memory write

When run without real credentials the app falls back to a canned fake Jira client
(read-only today). We give it an **in-memory** write so local dev and the
`make check` smoke exercise the real edit flow rather than a disabled control.

## Consequences

- **The read-only invariant now has one documented exception.** Any future reader
  who assumes "this app never writes to Jira" is wrong for exactly this one field
  on exactly the Board. The invariant is otherwise intact: the store still
  originates all state from Jira reads.
- **The Jira API token must permit writes.** Basic-auth token inherits the service
  account's Jira permissions; a read-only account will get a 4xx, surfaced via the
  inline error path above (the edit fails safely, nothing else breaks).
- **No CSRF / auth**, consistent with the existing `POST /resync`. Single-user
  self-hosted; the new mutation carries the same (absent) protection as the one
  mutation that already exists.
- **Other views reflect the change on their next load/sync, not live.** Sprint /
  Velocity / Daily tallies compute on *current* size, so a Board edit shows up
  there when they next render or after the next sync — they are not live-updated
  from the Board.
- **The change is attributed to the API token's account in Jira**, not the human
  who clicked — the app has no per-user identity. Accepted (same limitation the
  Daily view's actor attribution already notes).
