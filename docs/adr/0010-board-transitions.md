---
status: accepted
---

# Board transitions: the dashboard writes workflow history, not just estimates

> **Numbering note:** the originating ticket (#194) called this ADR 0009; that
> number was taken by [`0009-prio-view-whole-project.md`](./0009-prio-view-whole-project.md)
> while this work was in flight, so it landed as 0010.

The app gains a **status write path**: a [Board transition](../CONTEXT.md#board-transition)
moves a ticket into a different workflow status in Jira. This ticket adds only
the seam (`jira.Client.FetchTransitions` / `TransitionIssue`, `jira.TransitionTo`,
`sync.Syncer.SetStatus`); the drag-and-drop UI that uses it follows in #195.

**This supersedes `docs/adr/0005`'s framing of the estimate edit as the app's
"first and only write path".** There are now two writes to Jira. Everything else
0005 decided still holds — and this one deliberately copies its shape.

## The decision that actually needed making

Until now the dashboard **measured** the workflow. It now also **changes** it,
and the two feed each other: a status transition is exactly the event the
[Sprint view metrics](../CONTEXT.md#sprint-view-metrics) count as Finished, that
[Velocity](../CONTEXT.md#velocity) charts, and that
[Daily movement](../CONTEXT.md#daily-movement) classifies. So a mis-drop is not a
cosmetic slip: it writes **real workflow history** that shows up in tomorrow's
standup numbers, in the sprint's Finished count, and in that sprint's velocity
bar — and the app's own metrics will faithfully report the mistake.

Accepted anyway, because the Board is explicitly becoming the **standup surface**
(`docs/adr/0008`), and moving a ticket is the thing a standup does. Mitigation is
a **narrow legal-move set, not a confirm step**: only statuses Jira actually
offers a transition into are droppable, and a request for anything else fails
with `jira.ErrNoTransition` rather than performing some other transition. A
confirm dialog on every drag would tax the correct 95% of moves to soften the
wrong 5%, on a surface whose whole point is speed; Jira itself is the undo.

## The non-obvious decisions

### A transition is resolved by its TARGET STATUS ID, never by name

Jira has no "set status" operation: you read the transitions offered for an issue
and post the id of one. The DCAI workflow (verified live 2026-09-01 from two
different source statuses, which offered identical sets) is effectively
**all-to-all** — all nine statuses are reachable from any status — and it
contains a trap: transition **id 31 is labelled `Done`** and lands in status
**`Ready for release` (10016)**, not in `DONE (This Sprint)` (10064, transition
id 5). Matching on the transition *name* would therefore land a "move to Done"
drop in the wrong column, silently.

So the seam takes a **status id** (`jira.StatusID*`, a table of the nine DCAI
status ids) and picks the offered transition whose `to.id` matches.
`jira.TransitionTo` consults `Name` for nothing but error messages.

> **Correction to a fact recorded earlier.** The Board's `Ready for Release`
> column was believed unreachable ("no transition offers it", which was the
> stated reason it holds zero issues). That was an artefact of looking at
> transition *names*: it **is** reachable, under the label `Done`. The zero
> occupancy has some other cause (nobody uses it). #195 should treat Ready for
> Release as a legal drop target like any other column.

### The write shape is copied verbatim from the estimate edit

`SetStatus` does **write → single-issue re-read → persist**: perform the
transition in Jira, re-`FetchIssue` that one key, `SaveIssue` what the read
returned. The projection is therefore only ever set from a Jira read, so it stays
the pure, rebuildable projection `docs/adr/0005` protects — and a failed write
(no legal transition, 4xx, network) leaves **both** Jira and the projection
untouched. Same last-write-wins posture, same absent CSRF/auth, same
attribution-to-the-API-token limitation as 0005; none of that is re-litigated
here.

### The fake Jira client carries the real workflow's ambiguity

`jira.DCAITransitions()` is the live transition set, decoy `Done` label included,
and backs both the fake client and the selection tests. A fake that offered a
clean, unambiguous set would let a name-matching regression pass every test —
the same class of gap that let a wrong DTO shape ship before (#77). A
struct-literal `FakeClient` gets the DCAI set by default; an explicitly empty
(non-nil) `Transitions` models "Jira offers this issue nothing".

### It authorises a second vendored JavaScript dependency

#195 needs drag-and-drop. This repo has, to date, exactly **one** script tag
(htmx, vendored and embedded — no bundler, no npm at runtime, Node only for
Tailwind). Hand-rolling HTML5 drag-and-drop across the Board's columns is more
fiddly and worse on touch than it looks, so a **second small, vendored,
embedded** drag library is authorised here rather than argued in the middle of a
UI ticket. Constraints: vendored into the repo and `go:embed`ed like htmx (no CDN,
no build step, the single-static-binary property is non-negotiable), small, and
used only by the Board.

## Consequences

- **"The dashboard can't change Jira" is now wrong twice over** — estimates and
  status. The read-only *projection* invariant is still intact: the store
  originates nothing; every write goes to Jira and comes back through a read.
- **The API token's account must be allowed to transition issues**, not just edit
  fields. A permission failure surfaces as a failed write and changes nothing.
- **Transitions the app writes are indistinguishable from any other** in the
  changelog, so they flow into every metric — by design, and the reason the
  mis-drop risk above is real rather than theoretical.
- **The status-id table is site-specific.** The ids are stable for this Jira site
  but would need re-capturing for another project; they sit in one place
  (`internal/jira/transition.go`) next to the custom-field ids that already have
  the same property.
