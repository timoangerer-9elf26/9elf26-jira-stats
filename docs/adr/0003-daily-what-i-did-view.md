---
status: accepted
---

# Daily view becomes a "what I did since yesterday" overview

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

- **"Tickets I created" is phased out to a follow-up (#44).** It needs the issue
  fetch to pull `created` + `creator` and a schema migration for two columns —
  the one part of this feature that isn't reusable existing data. The movement
  digest ships first without it.
- **The "Since yesterday" window spans the last working day (#48).** Originally it
  ran to 00:00 of the previous calendar day, so a Monday morning meant "since
  Sunday 00:00" and missed Friday's work. The window now walks back over weekends
  to the last working day, so a Monday (and Sat/Sun) reaches Friday.
- **Net-zero churn folds into Advanced.** A ticket moved out of and back to the
  same status within the window has no net movement but did see activity; it is
  bucketed as Advanced rather than given its own category.
