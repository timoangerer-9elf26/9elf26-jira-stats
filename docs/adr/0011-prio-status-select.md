---
status: accepted
---

# The Prio status filter is a single select over four categories, not overlapping toggles

> **Numbering note:** the originating ticket (#214) called this ADR 0010; that
> number was taken by [`0010-board-transitions.md`](./0010-board-transitions.md),
> so it landed as 0011.

The [Prio view's](../../CONTEXT.md#prio-view) status filter is **one `<select>`
over four categories — Planned / Doing / Done / All — of which exactly one is
active at a time**. It replaces the two independent toggles **Not done** and
**Not started** (#202, #209), whose params are deleted; old URLs carrying them
are inert and fall back to the default category.

**This supersedes the part of [`0009`](./0009-prio-view-whole-project.md) that
argued the Not done / Not started overlap was deliberate and should not be merged
away into a single status control.** The rest of 0009 — the whole-project
universe, the synced priority and labels, the default-ON filters, the
non-technical rule — still holds. The two-gear design was genuinely tried, in
production, and found wanting; that history is the point of this ADR.

## The driver: two views nobody could reach

Not started (Triage, Refinement, Ready To Do) was a **strict subset** of Not done
(those three plus In Progress, Review / Testing). Every combination of the two
therefore either widened or narrowed *around the unstarted slice*:

| Not started | Not done | Result |
| --- | --- | --- |
| on | on | not started (Not done a no-op) |
| on | off | not started |
| off | on | all open work |
| off | off | everything |

There is no row for "only work in flight" and no row for "only finished work".
The middle and the end of the workflow were **unreachable**, and no amount of
clicking got you there — the controls could only ever move the boundary, never
select an interval. 0009 read that overlap as a second gear; the gear existed,
but the gearbox was missing two gears.

A single select over categories makes each phase reachable in **one move**, which
is what a prioritisation surface is for: "what is queued", "what is being worked
on", "what is finished", "everything".

## What the categories are — and are not

| Category | Statuses |
| --- | --- |
| **Planned** (default) | Triage, Refinement, Ready To Do |
| **Doing** | In Progress, Review / Testing |
| **Done** | DONE (This Sprint), Ready for Release, Released / Deployed |
| **All** | all nine statuses, Canceled included |

- **Explicit status sets in our own code, never Jira's `status_category`.** As
  [`CONTEXT.md`](../../CONTEXT.md#ticket-status-buckets) records from live data,
  Jira files **Canceled in category `Done`** and **Triage in `To Do`**. A
  category-derived map would sweep Canceled into Done and be silently wrong. The
  price is that a new DCAI status is invisible to every category until someone
  adds it to the map — accepted, and the reason the sets sit in one small,
  commented table next to the filter rather than being computed.
- **Canceled belongs to no category.** Abandoned work is not a phase you
  prioritise; it surfaces only under All, where the point is "show me
  everything".
- **Ready for Release is a Done state** despite reading like a queue.

## The non-obvious consequences

- **This is a second, Prio-local partition of the same nine statuses**, sitting
  alongside the project-wide sprint buckets (Triage / Open ticket / Finished /
  Canceled). The two deliberately **disagree about Triage**: the sprint rollups
  exclude it as pre-sprint, while Prio puts it in **Planned**, because on a
  prioritisation surface an untriaged ticket is precisely the unprioritised work
  you came to look at. A reader who assumes one partition is the other will
  mis-read either the rollups or this view; `CONTEXT.md` states the split
  explicitly for that reason. Do not "unify" them.
- **The filter bar now has two control types.** The registry was already
  control-agnostic — each filter names the partial that renders it — so the
  select slotted in with no change to the chrome, the route or the handler. The
  select does differ in *where its value lives*: a pill encodes the state it
  flips to in its href, whereas the select **is** the element issuing the
  request, so htmx picks its value up automatically. It is therefore not marked
  `data-filterparam` (that would double-count); its non-default value is
  re-emitted as a hidden param instead, so toggling a *pill* preserves the
  chosen category.
- **Only the non-default category reaches the URL.** `planned` is omitted, so a
  bare `/prio` is still a bare URL — the same convention the pills follow with
  their `<param>=0` off-encoding. An unrecognised value falls back to Planned
  rather than erroring, which is what makes the deleted `not-done` /
  `not-started` params harmlessly inert instead of a broken link.
- **Single-select means the phases cannot be combined.** "Doing plus Done" is
  not expressible short of All. That is the trade this ADR accepts: the overlap
  problem was caused by controls that could only express unions and complements
  of one boundary, and a partition with a clear default is worth more here than
  arbitrary unions. If a genuine need for multi-select appears, the Board's
  multi-select assignee bar is the pattern to copy — but do not reintroduce
  overlapping binary toggles over the same axis.
