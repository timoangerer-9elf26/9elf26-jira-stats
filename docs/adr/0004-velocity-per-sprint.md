---
status: accepted
---

# Velocity becomes per-sprint, aligned with the Sprint view (#99)

Velocity is reshaped from **per-ISO-week, project-wide throughput** into
**per-sprint completed points that equal the Sprint view's Finished figure**. One
bar per sprint (trailing recent sprints, oldest-first), labelled by the sprint's
name; each bar's points are that sprint's Sprint-view **Total-row Finished** —
the same cohort-scoped (Started-with + Added), pre-finished-carry-over-excluded
Done-crossing at current size. Each bar also shows the sprint's **start–end
dates** (date only): a completed sprint as `start – completion`, the active sprint
as `start – now (ongoing)`.

## The non-obvious decisions

### Why the old model didn't line up with the Sprint view

The previous Velocity summed `CompletedInRange` over each ISO calendar week
`[Mon 00:00, next Mon)` across the **whole project**, ignoring sprint membership.
Three things made it disagree with the Sprint view's Finished number for the
"same" week:

1. **Window** — a sprint's window is `[actual sprint start, now/completion)`, not
   a Monday-aligned calendar week, even though sprints are named `KW##`.
2. **Scope** — the week rollup counted completions from *other* sprints and from
   tickets *never in a sprint*; the Sprint view counts only that sprint's cohort.
3. **Carry-overs** — the Sprint view excludes pre-finished carry-overs (ADR 0002,
   #87); the week rollup did not.

So the label `KW30` on a Velocity bar and the Sprint view for `KW30` were
measuring different populations over different spans. Making each bar reuse the
Sprint view's Finished computation removes all three gaps by construction.

### The Done set is unchanged — the finish line is NOT narrowed

We keep the full Done set `{DONE (This Sprint), Ready for Release, Released /
Deployed}` for the completion crossing here (and in the Sprint view). The Daily
view's "ignore movement inside the Done set" change (#98, ADR 0003) is
deliberately Daily-only; narrowing the finish line to `DONE (This Sprint)`
everywhere would reverse ADR 0002's deliberate inclusion of Ready for Release and
retroactively move historical completions. Rejected.

### Per-sprint bars, not per-week

Bars are now one-per-sprint rather than fixed calendar weeks. This is what makes
"velocity" answer the planning question in the same currency the Sprint view
uses. The trade-off: bars are no longer evenly spaced in wall-clock time (a
sprint can run long or short), and a week with no sprint has no bar — acceptable,
since the point is comparing sprints, not calendar weeks.

### The active sprint's end date

The active sprint has no completion instant, and the planned end date is
deliberately not trusted (see `CONTEXT.md` → Sprint). Its bar therefore shows
`start – now (ongoing)` — the running current date plus an explicit *ongoing*
marker — rather than borrowing the untrusted planned end.

## Consequences

- **Historical Velocity numbers change shape.** Bars are now sprint-scoped and
  carry-over-excluded, so figures differ from the old project-wide weekly series.
  This is the number becoming the same thing the Sprint view reports, not a
  regression.
- **Velocity depends on stored sprint lifecycle + membership history** (activation
  / completion and the sprint-membership log, ADR 0002). A sprint that predates
  that history can only be computed as deeply as the backfill reaches.
- **Velocity and the Sprint view can never silently drift** — the active sprint's
  bar is, by construction, the Sprint view's Finished points.
