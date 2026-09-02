---
status: accepted
---

# The Prio view spans the whole project and introduces synced priority + labels

The [Prio view](../../CONTEXT.md#prio-view) is a prioritisation table whose
universe is **every issue in the DCAI project** — any sprint, any status — not
the active sprint. This is a deliberate departure from every other view (Board,
Daily, Sprint, Velocity are all active-sprint-scoped): prioritisation means
looking across the *whole* backlog, including Triage tickets that no
sprint-scoped view shows. To render it, **priority** and **labels** — standard
Jira fields (`fields.priority.name`, `fields.labels[]`) that were never part of
the projection — become first-class synced fields on the `issue` row.

## Considered options

- **Scoping Prio to the active sprint** (like every other view) was rejected: the
  active sprint excludes Triage and the backlog, which is exactly the work a
  prioritiser needs to triage. A prioritisation surface that can't see un-sprinted
  work defeats its own purpose.
- **Deriving "technical" from something already synced** (e.g. issue type, epic)
  instead of syncing labels was rejected: the team's technical/non-technical
  split is expressed *only* by the Jira label `Technical`, so there is no existing
  projected field to key off. Syncing labels is unavoidable for the feature.

## The non-obvious consequences

- **The universe is dominated by done work.** ~1,259 of ~1,400 issues are
  Released / Deployed. So the Prio filters — unlike the Board's, which all default
  OFF — **default ON**: the view opens on the narrowed, prioritisable slice
  rather than its raw universe, and leans on filters to widen back out. A future
  reader comparing filter defaults across views should not "fix" this to match
  the Board; it is deliberate (see
  [Prio filters](../../CONTEXT.md#prio-filters)).

- **Existing rows are blank until a full resync.** Because `priority` and `labels`
  are additive projection columns, historical tickets carry them only after a
  full resync repopulates the projection. This is acceptable — the projection is
  rebuildable from Jira by definition (`docs/adr` context; see the `issue` table)
  — but it means the columns read empty immediately after the migration ships,
  before the first resync.

- **"Non-technical" is the absence of `Technical`, not the presence of
  `Product`.** A sibling `Product` label exists and looks like Technical's
  counterpart, but the Non-Technical filter is defined as *not carrying
  `Technical`*, so tickets with neither label still count as non-technical. This
  keeps the filter a single-label check and avoids coupling to a second taxonomy.
