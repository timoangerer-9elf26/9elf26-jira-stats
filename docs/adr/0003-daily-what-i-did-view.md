---
status: accepted
---

# Daily view becomes a "what I did since yesterday" overview

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

We reshape the Daily view from a neutral per-assignee status-change browser into
a personal morning overview centred on **me** (a single configured Jira display
name; Daily defaults to it, the dropdown still selects teammates/All). Over a
recent window it shows a **daily digest** — each moved ticket bucketed by net
movement into Finished / Advanced / Pulled back — stacked above the existing
granular per-transition log. See `CONTEXT.md` for the term definitions.

## The non-obvious decisions

### Attribution is by *current* assignee, not the actor of each move

"What I did" ideally means the status moves *I personally made*. We instead
filter on the ticket's **current** assignee, so a move made while a ticket was
mine but later reassigned is credited to the new assignee, not me (and vice
versa). We accept this misattribution because the truer anchor needs the
changelog to carry the actor (or assignee-at-instant) of each transition, which
isn't synced today. A future reader will wonder why handoffs misattribute — this
is why.

### The movement digest is active-sprint-scoped, but "tickets I created" is not

Status movements are counted only for active-sprint work items, keeping Daily on
the same sprint spine as the rest of the app. The deferred "tickets I created"
section (see below) is deliberately **not** sprint-scoped — a ticket you
authored is something you did regardless of whether it landed in the sprint.
The asymmetry is intentional, not an oversight.

### "Me" is keyed on display name for now

The identity is a configured Jira **display name**, matching what the store
already holds for assignee — zero sync/schema change. Display names can be
renamed or collide, silently breaking the match; acceptable for a single-team
internal dashboard until the sync is touched, at which point a stabler key
(`accountId`/email) should replace it.

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
