# CONTEXT

Glossary of the domain language for the 9elf26-jira-stats dashboard. Terms only —
no implementation detail. See `docs/adr/` for decisions.

## Sync

How the dashboard's projection is kept in step with Jira. Two distinct kinds,
which must not be conflated (avoid the phrase "incremental resync"):

- **Full resync** — the manual, user-triggered wipe-and-rebuild of the whole
  projection from Jira (the resync button). Records its own completion instant;
  before the first one it has *never* happened. The cold-start backfill on an
  empty store is **not** a full resync.
- **Incremental sync** — the automatic periodic sync cycle that pulls only the
  issues changed since the last successful cycle. Its **heartbeat** — the last
  successful cycle — is the trusted "sync is alive" signal: a frozen heartbeat
  means syncing has broken, independent of whether any ticket happened to change.

## Sprint

A one-week unit of planned work in the DCAI Jira project, named `KW##` (ISO
week). Treated as a first-class entity with its own **lifecycle events**, not
just a label on a ticket.

- **Sprint activation** — the instant the sprint *started*, used as the
  sprint window start. Jira Cloud exposes no dedicated activation field, so
  this is anchored on the sprint's `startDate` (see `docs/adr/0002`).
- **Sprint completion** — the instant the sprint was actually *completed* in Jira.
  The trusted "sprint ended" timestamp.
- **Planned end date** — the end date *set* on a sprint during planning.
  Deliberately **not trusted**: it is frequently wrong. Use the completion event
  for the sprint's end instead.

## Sprint view

The sprint-planning overview (formerly "Weekly"; earlier "Completed"). Answers,
for the current active sprint over its own window `[sprint start, now)`, what has
happened this sprint. There is no week-window selector: the view always anchors
on the active sprint's start (see `docs/adr/0002`).

## Sprint window

The time span the Sprint view measures over: **`[sprint start, now)`** for the
current active sprint. The start is the sprint's activation instant (its
`startDate`; see [Sprint](#sprint)); the end is now. This is the single window —
anchoring Started-with / Added on the sprint's own start (not a calendar Monday)
is what keeps a carry-over at a sprint boundary in Started-with. *Membership* is
reconstructed from the sprint-membership history at the window bounds.

## Daily view

The morning standup overview. For a chosen **assignee** — defaulting to **me** —
over a **selectable date-time range**, it answers "what did this person do". Two
stacked sections: a **daily digest** summarising the net outcome, and beneath it
the granular per-transition log. Scoped to active-sprint work items.

The range is chosen either with a custom **From / Until** (date + time) or with
one of three **working-day presets** — **Today**, **Yesterday**, and the working
day before that (labelled by its weekday name) — each spanning one whole calendar
day. Working days are Monday–Friday: the Yesterday / day-before presets **walk
back over weekends** to the most recent working days (so on a Monday, Yesterday is
Friday), while the Today preset is literal and is disabled on a weekend. Weekend
exclusion applies to the presets only — a custom range is honoured verbatim.

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
  the [Sprint view](#sprint-view-metrics) counts as Finished).
- **Advanced** — net forward in the workflow but not into Done. Net-zero churn
  (moved out and back to the same status) folds in here.
- **Pulled back** — net backward in the workflow, including a move to Canceled.

**Movement *inside* the Done set is ignored** on the Daily view. A transition
whose *both* endpoints are Done statuses (e.g. DONE (This Sprint) → Ready for
Release, Ready for Release → Released / Deployed) is post-completion housekeeping
and is dropped — from the digest and the granular log. The finish crossing
(into Done) and a **reopen** out of Done are still shown; a ticket whose only
in-window moves were inside Done disappears from Daily entirely. This is
Daily-only — the [Sprint view metrics](#sprint-view-metrics) and
[Velocity](#velocity) keep the full Done set (see `docs/adr/0003`).

## Velocity

Completed work per **sprint**, one bar per sprint (trailing recent sprints,
oldest-first, labelled by the **sprint's name**). Each bar's points are that
sprint's [Sprint view](#sprint-view-metrics) **Total-row Finished** — the same
cohort-scoped, carry-over-excluded Done-crossing at current size — so Velocity
and the Sprint view always agree. A completed sprint measures over
`[sprint start, sprint completion]`; the active sprint over `[sprint start, now)`
and is shown as *ongoing*. This replaces the earlier per-ISO-week, project-wide
throughput, which never lined up with the Sprint view (see `docs/adr/0004`).

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

## Sprint view metrics

For the active sprint over its window `[sprint start, now)`, reported as a
**cohort × outcome** table: rows **Started with / Added / Total**, columns
**Open · Finished · Removed · Total**. Every cell is a size tally (S/M/L/no-estimate
counts + a points sum, S=1/M=2/L=3, at the ticket's *current* size), and
`Total = Open + Finished + Removed`.

**Pre-finished carry-overs are excluded from the whole table** (every cohort,
every outcome, every Total) and from every cell drill-down — see
[Pre-finished carry-over](#pre-finished-carry-over). They were finished in a
prior sprint and only linger because they are not yet Released; they are not this
sprint's work (see `docs/adr/0002`).

The **cohorts** (rows):

- **Started with** — the active-sprint members at the end of the **grace window**
  (`sprint start + 1h`), regardless of status, **except pre-finished carry-overs**
  (excluded). The capacity baseline, including still-unfinished carry-overs
  (tickets pulled from the previous sprint at rollover), tickets created directly
  into the sprint, and tickets moved in during the first hour. There is no
  open-at-start gate — a member with no status history still counts.
- **Added** — tickets whose **first** membership entry falls **after** the grace
  window (genuine scope creep), regardless of status, **except pre-finished
  carry-overs** (excluded).
- **Total** — the column-wise sum of Started with + Added.

The **outcomes** (columns), over the window `[sprint start, now)`:

- **Finished** — crossed into the finished bucket within the window.
- **Removed** — *not* finished and (cancelled **or** no longer a member).
- **Open** — the remainder (still a member, not cancelled, not finished).
- **Total** — Open + Finished + Removed.

Removal is **asymmetric**. A **Started-with** ticket that is cancelled *or*
reprioritised out of the sprint counts under **Removed** — the baseline keeps it.
An **Added** ticket only reaches Removed when **cancelled**; one merely
reprioritised out again is **dropped entirely** (it appears in no cell), so the
Added row counts only scope creep that actually stuck or was explicitly killed.

## Pre-finished carry-over

A sprint member that is **currently in the finished bucket** yet **did not cross
into it within this sprint's window** — i.e. its completion happened in a *prior*
sprint and it lingers in the active sprint only because it isn't `Released /
Deployed` yet. It is **not** this sprint's work, so it is excluded from the whole
[Sprint view metrics](#sprint-view-metrics) table and every cell drill-down.

The test is by *current* state, not status-at-start: a carry-over that is
**reopened** and worked this sprint leaves the finished bucket and correctly
re-enters the counts (as Open, or as Finished if it is re-finished within the
window). Scope is the Sprint view only — the Board still shows these tickets in
their status columns. [Velocity](#velocity) shares the Sprint view's Finished
computation, so it excludes pre-finished carry-overs identically (their
completion belongs to the prior sprint).

## Estimate edit

The one place a user can **change** Jira from the dashboard: on the **Board**,
the estimate pill (the ticket's size — S / M / L / no-estimate) is editable.
Picking a value **writes it back to Jira** as the ticket's estimate, immediately,
with no confirm step. Everywhere else the size is read-only display.

This is the sole exception to the dashboard being a read-only projection of Jira.
Jira stays the **source of truth**: the edit is a write *to Jira*, not to the
local projection — the projection only ever reflects what a Jira read returns, so
after a successful write the changed ticket is re-read from Jira and the pill
shows that authoritative value. A failed write leaves Jira (and the pill)
unchanged. Editing is **Board-only**: the same size pill on the Daily view, the
"tickets I created" list and the Sprint drill-down stays read-only (see
`docs/adr/0005`).
