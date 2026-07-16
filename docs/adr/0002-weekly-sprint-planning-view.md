---
status: accepted
---

# Weekly sprint-planning view

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

### Window bounds ignore the sprint's *planned* dates; use actual lifecycle events

The planned start/end dates set on a sprint are not trusted (frequently wrong).
The **work-week** mode uses a fixed clock window (Mon 00:00 → Sat 00:00,
Europe/Berlin, from `now`); the **live-sprint** mode uses *sprint activation →
now*. Neither reads the planned dates. A future reader will wonder why we don't
just use Jira's sprint dates — this is why.

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
