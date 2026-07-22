---
status: accepted
---

# Daily view becomes a "what I did since yesterday" overview

> **Amended (2026-07): the Daily view becomes a board.** The digest + granular
> per-transition log are **replaced by a single board** (workflow columns
> Refinement → Ready To Do → In Progress → Review / Testing → **Done** (one
> column collapsing the whole Done set) → **Canceled** (rightmost, only when
> non-empty); the five workflow columns always render). It shows every
> active-sprint work item **created in the window or moved within it**; each card
> sits in the column of its **status at the window end** (reconstructed at that
> instant) — so the board is live for *Today* and a point-in-time snapshot for
> older windows. Cards are the Board card (key, type, title, editable estimate,
> assignee avatar) plus in-window movement: the latest move's timestamp and an
> **origin badge** (`from <window-start status>` + move count) coloured by
> movement kind (Finished / Advanced / Pulled back, unchanged incl. the #98
> intra-Done drop); a card created in-window is highlighted and reads *created
> here* when it never moved.
>
> Two decisions worth recording. **Placement is by status at window-end, not
> current status** — a ticket that moved after the window still shows where it
> sat when the window closed, keeping a historical "Yesterday" board a truthful
> snapshot rather than a picture of now; a created-but-unmoved ticket's
> window-end status is simply its creation status. **The board replaces the
> digest entirely** rather than sitting above it: movement kind is on every card
> and totals are the column counts, so a separate summary would be redundant —
> the one-line headline is dropped. A new **Last 24h** rolling preset
> (`[now − 24h, now)`) is added to the right of Today (presets read
> earliest→now); it is not weekend-adjusted. Default stays Today. Estimate
> editing now works on this board too (see `docs/adr/0005`).
>
> **Amended by #90 (2026-07):** the two fixed windows (Last 24h, Since yesterday)
> are replaced by a **selectable date-time range**: a custom **From / Until**
> (date + time, Europe/Berlin) plus three **working-day presets** — **Today**,
> **Yesterday**, and the working day before that (labelled by its weekday name) —
> each spanning one whole calendar day. Working days are Mon–Fri. **Yesterday** and
> the **day-before** preset walk back over weekends to the most recent working days
> (reusing the #48 skip logic — so on a Monday, Yesterday = Friday and the third =
> Thursday), while **Today** is literal and is *disabled* on a weekend. The default
> is Today, falling back to the most recent enabled preset when Today is disabled.
> Weekend exclusion applies to the **presets only** — a custom range is honoured
> verbatim (weekends included). An invalid custom range (From ≥ Until or malformed)
> shows an inline error rather than silently falling back. State is carried as
> `?preset=today|yesterday|day-before-yesterday` or `?from=&to=`.
>
> Two choices worth recording. **The Yesterday preset keeps the word "Yesterday"
> even when it resolves to Friday** (a Monday) — the label names the *slot*, not the
> date, so each button carries a hover title with the concrete date to disambiguate;
> naming it "Friday" was rejected because the slot's meaning (the previous working
> day) is what a standup wants. **Only the Today preset can be disabled** — walking
> Yesterday/day-before back over weekends keeps them always-valid, so a Monday still
> offers three useful days rather than greying two out.
>
> **Amended by #95 (2026-07):** the first label choice above is **reversed**. The
> Yesterday button reads **"Yesterday"** only when the preset resolves to the actual
> calendar yesterday; when it walks back over a weekend (Monday and Sunday) it reads
> the **full weekday name** of the day it maps to (e.g. "Friday"), matching how the
> day-before button is already labelled. So Tuesday–Friday and Saturday show
> "Yesterday", while Monday and Sunday show "Friday". The concrete-date hover title
> stays in both cases, and the range each preset drives plus its selected/disabled
> behaviour are unchanged — only the visible label differs.
>
> **Amended by #98 (2026-07):** the Daily view **ignores movement inside the Done
> set**. An in-window `status` transition is dropped when *both* its from- and
> to-status are Done statuses (DONE (This Sprint) → Ready for Release, Ready for
> Release → Released / Deployed) — post-completion housekeeping the standup doesn't
> care about. Kept: the finish crossing (non-Done → Done) and a **reopen** (Done →
> non-Done, shown as a pull-back). Applied to the whole Daily view (digest buckets
> and granular cards); movement bucket, net From ⟶ To and the ticket's presence are
> recomputed from the surviving changes, so a ticket whose only in-window moves were
> inside Done disappears, and In Progress → Done → Released shows In Progress ⟶ DONE
> (This Sprint). This is **Daily-only** — the Sprint view and Velocity keep the full
> Done set `{DONE (This Sprint), Ready for Release, Released / Deployed}`; narrowing
> the finish line globally was considered and rejected (it would reverse ADR 0002's
> deliberate inclusion of Ready for Release and shift historical metrics).
>
> **Amended by #135 (2026-07): the "me" concept is removed.** The app is deployed
> for a whole team, so a single configured "me" (the `WithMe` option / `DAILY_ME`
> env var) no longer makes sense. The Daily assignee filter now **defaults to all
> assignees** instead of a configured person; "All" is the absence of an
> `assignee` param (the dropdown still selects individual teammates and the
> Unassigned sentinel). Everything below that describes a default centred on a
> configured person is historical context, superseded by this all-assignees
> default.

The original decision (historical; see the #135 amendment above) reshaped the
Daily view from a neutral per-assignee status-change browser into a personal
morning overview centred on a single configured Jira display name that Daily
defaulted to (the dropdown still selecting teammates/All). Over a recent window
it showed a **daily digest** — each moved ticket bucketed by net movement into
Finished / Advanced / Pulled back — stacked above the granular per-transition
log. See `CONTEXT.md` for the term definitions.

## The non-obvious decisions

### Attribution is by *current* assignee, not the actor of each move

"What was done" ideally means the status moves a person *personally made*. We
instead filter on the ticket's **current** assignee, so a move made while a
ticket was assigned to one person but later reassigned is credited to the new
assignee, not the previous one. We accept this misattribution because the truer
anchor needs the changelog to carry the actor (or assignee-at-instant) of each
transition, which isn't synced today. A future reader will wonder why handoffs
misattribute — this is why.

### The movement digest is active-sprint-scoped, but authored tickets are not

Status movements are counted only for active-sprint work items, keeping Daily on
the same sprint spine as the rest of the app. The deferred authored-tickets
section (see below) was deliberately **not** sprint-scoped — a ticket someone
authored is work they did regardless of whether it landed in the sprint. The
asymmetry is intentional, not an oversight.

### The assignee filter is keyed on display name

The assignee filter matches on the Jira **display name**, matching what the
store already holds for assignee — zero sync/schema change. Display names can be
renamed or collide, silently breaking the match; acceptable for a single-team
internal dashboard until the sync is touched, at which point a stabler key
(`accountId`/email) should replace it. (The original per-user "me" default that
this keying served was removed by #135; see the amendment above.)

## Consequences

- **"Tickets I created" shipped in #44.** It added `created` + `creator` to the
  issue fetch and a schema migration for the two columns (the one part of this
  feature that wasn't reusable existing data), plus a store query for tickets a
  display name authored in the window; its count feeds the digest headline
  ("moved 5 — …, created 2"). The section is NOT sprint-scoped (see above).
  Identity still keys on display name — the stabler `accountId`/email key noted
  above remains deferred even though the sync was touched.
- **The "Since yesterday" window spans the last working day (#48).** Originally it
  ran to 00:00 of the previous calendar day, so a Monday morning meant "since
  Sunday 00:00" and missed Friday's work. The window now walks back over weekends
  to the last working day, so a Monday (and Sat/Sun) reaches Friday.
- **Net-zero churn folds into Advanced.** A ticket moved out of and back to the
  same status within the window has no net movement but did see activity; it is
  bucketed as Advanced rather than given its own category.
