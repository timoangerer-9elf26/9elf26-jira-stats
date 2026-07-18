---
status: accepted
---

# Weekly sprint-planning view

> **Amended by #53 (2026-07):** the view is renamed **Weekly → Sprint** (`/sprint`)
> and the **week-window selector is dropped**. It now always centers on the current
> active sprint over the single window `[sprint start, now)` — i.e. the former
> "live-sprint" mode is the only mode. Started-with / Added are anchored on the
> sprint's *own* start instant (not a calendar Monday), so a carry-over pulled from
> the previous sprint at rollover counts as **Started with**, not Added. "Added
> during the week" is renamed **Added** (entered the sprint after its start). The
> "work-week" mode below is historical. The membership/lifecycle machinery this ADR
> introduced is unchanged and still underpins the view.

We repurpose the "Completed" view into a **Weekly** sprint-planning overview.
For the active sprint over a selectable week window, it reports three
categories — **Started with**, **Added during the week**, **Finished** (broken
down by the first two plus a total) — each as a size tally (S/M/L/no-estimate +
points). See `CONTEXT.md` for the term definitions. The old date-range presets
and custom from/to picker are dropped; Velocity is unaffected in shape.

## The non-obvious decisions

### Sprints become first-class entities with captured lifecycle + membership history

The started/added split is fundamentally a *sprint-membership-over-time*
question, and the window bounds need trustworthy sprint start/end. Today the
store keeps only a *current* membership snapshot (`issue.active_sprint`,
overwritten each sync) and the active sprint's *planned* dates in `meta`. So this
feature promotes the sprint to a stored entity with its **actual lifecycle
events** (activation, completion) and extends the synced changelog to capture the
**Sprint field** (currently only `status` and `Estimated Time` are tracked), so
membership at any past instant can be reconstructed the same way status already
is.

- *Alternative rejected:* approximate "added during the week" from a ticket's
  creation/first-transition date. Cheaper, no sync change — but wrong exactly
  when scope creep matters: a ticket created earlier and pulled in mid-week would
  be missed. A planning number that looks precise but misleads is worse than
  none.

### Window bounds: work-week is a clock window; live-sprint anchors on `startDate`

The **work-week** mode uses a fixed clock window (Mon 00:00 → Sat 00:00,
Europe/Berlin, from `now`); the **live-sprint** mode runs *sprint start → now*.

We originally intended the live-sprint start to be the sprint's *actual*
activation instant (`activatedDate`), distinct from the planned start. But Jira
Cloud's Agile REST API exposes **no** activation field — a sprint response carries
only `startDate`, `endDate`, `createdDate`, `completeDate` (+ id/name/state/goal).
`activatedDate` was always empty, so `activated_at` was never populated and
live-sprint silently fell back to the work-week window (bug #49). Resolution:
anchor the live-sprint start on **`startDate`** — the value set in the "Start
sprint" dialog, the only available start instant — falling back to `createdDate`
for a sprint never started. The planned `endDate` is still ignored; the sprint's
end comes from `completeDate`. A future reader will wonder why we don't read a
dedicated activation timestamp — because Jira Cloud does not provide one.

### The global "Done" set is corrected to include Ready for Release

`Ready for Release` sits *after* DONE (This Sprint) in the DCAI workflow and is a
finished state, but the code's `doneStatuses` omitted it. We correct it globally
(one authoritative Done set), so `doneStatuses` becomes `{DONE (This Sprint),
Ready for Release, Released / Deployed}`.

## Consequences

- **Velocity numbers shift.** Completion is a crossing into the Done set; adding
  Ready for Release moves some tickets' completion to that earlier crossing, so
  past weeks' figures change. This is the numbers becoming honest, not a
  regression.
- **Membership history is only as deep as the changelog backfill.** The full
  per-issue changelog re-pull makes Sprint-field history retroactive, but any
  pre-existing DB must re-backfill to gain it.
- **A consistency risk remains, filed separately:** the Now board decides
  open-ness via Jira's `status_category`, not our explicit status buckets. If
  Jira categorizes Ready for Release as not-Done, the board and Weekly/Velocity
  would disagree. The durable fix — drive open/done from the explicit buckets
  everywhere — is out of scope here and tracked as a follow-up.
- Losing the custom date-range picker removes ad-hoc "completed between X and Y"
  lookups; Velocity covers per-week trends and the planning view has a single
  clear job.
