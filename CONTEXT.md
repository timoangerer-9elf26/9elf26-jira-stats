# CONTEXT

Glossary of the domain language for the 9elf26-jira-stats dashboard. Terms only —
no implementation detail. See `docs/adr/` for decisions.

## Sprint

A one-week unit of planned work in the DCAI Jira project, named `KW##` (ISO
week). Treated as a first-class entity with its own **lifecycle events**, not
just a label on a ticket.

- **Sprint activation** — the instant the sprint was actually *started* in Jira
  (someone clicked "Start sprint"). The trusted "sprint started" timestamp.
- **Sprint completion** — the instant the sprint was actually *completed* in Jira
  (`completeDate`). The trusted "sprint ended" timestamp.
- **Planned dates** — the start/end dates *set* on a sprint during planning.
  Deliberately **not trusted** for windowing: they are frequently wrong. Use the
  activation and completion events instead.

## Weekly view

The sprint-planning overview (formerly "Completed"). Answers, for the active
sprint over a chosen **week window**, what happened this week. Replaces the old
date-range "how much got completed" framing.

## Week window

The time span the Weekly view measures over. Selectable between two modes:

- **Work week** — a fixed clock window: Monday 00:00 → Saturday 00:00
  (i.e. Friday end-of-day) in Europe/Berlin, derived from the current time. The
  weekend is excluded.
- **Live sprint** — sprint activation → now, for the currently active sprint.

In both modes, *membership* is the set of tickets in the active sprint; the mode
changes only the time window.

## Daily view

The morning standup overview. For a chosen **assignee** — defaulting to **me** —
over a recent window, it answers "what did this person do since yesterday". Two
stacked sections: a **daily digest** summarising the net outcome, and beneath it
the granular per-transition log. Scoped to active-sprint work items.

## Me

The single configured identity the Daily view revolves around, set in config as
a Jira **display name**. Daily defaults to *me*; every other assignee stays
selectable from the dropdown. Attribution is by a ticket's *current* assignee —
so a status move made while a ticket was mine but later reassigned away is
credited to the new assignee, not me (a known limitation, accepted until the
sync captures the actor of each transition).

## Daily digest

The Daily view's summary of what *me* (or the selected assignee) did in the
window, bucketing each moved ticket by its **net movement** — the workflow
distance from where it sat at the window start to where it sits at the window
end — into exactly one of:

- **Finished** — crossed into the Done set within the window (the same crossing
  the [Weekly view](#weekly-view-metrics) counts as Finished).
- **Advanced** — net forward in the workflow but not into Done. Net-zero churn
  (moved out and back to the same status) folds in here.
- **Pulled back** — net backward in the workflow, including a move to Canceled.

## Ticket status buckets

The DCAI workflow statuses group into buckets for the sprint rollups. Workflow
order (left→right): Triage → Refinement → Ready To Do → In Progress →
Review / Testing → DONE (This Sprint) → Ready for Release → Released / Deployed →
Canceled.

- **Triage** — pre-sprint. A Triage ticket should never enter a sprint; excluded
  from all sprint counts.
- **Open ticket** — live, committed sprint work. Exactly the four statuses:
  Refinement, Ready To Do, In Progress, Review / Testing.
- **Finished** (Done) — work completed within the sprint: DONE (This Sprint),
  Ready for Release, Released / Deployed. Ready for Release sits *after* DONE
  (This Sprint) in the flow — it is a done state, not open.
- **Canceled** — abandoned; excluded from both open and finished counts.

Open/finished are decided by these explicit buckets, **not** Jira's
`status_category`. Observed in live Jira, the category does not match the DCAI
buckets: **Canceled is category `Done`** (a category-based "finished" would
wrongly include it) and **Triage is category `To Do`** (a category-based "open"
would wrongly include it). `Ready for Release` currently has 0 issues, so its
category is unobservable from issue data. The other statuses observe as
Refinement/Ready To Do → `To Do`, In Progress/Review / Testing → `In Progress`,
DONE (This Sprint)/Released / Deployed → `Done`. This mismatch is why open and
finished must come from the explicit buckets above.

## Weekly view metrics

For the chosen week window over the active sprint, reported as three categories.
Each is a size tally (S/M/L/no-estimate counts + a points sum, S=1/M=2/L=3, at
the ticket's *current* size):

- **Started with** — tickets that were *open* and in the active sprint at the
  window start (Monday 00:00 for work week; sprint activation for live sprint).
  The capacity baseline. A snapshot: later removal from the sprint does not
  rewrite it.
- **Added during the week** — tickets that *entered* the active sprint during the
  window (scope creep), regardless of status.
- **Finished** — tickets that *crossed into* the finished bucket within the
  window, attributed to whichever category (Started with / Added) the ticket
  belongs to, plus a total across both. A ticket both added and finished in the
  window counts under Added.
