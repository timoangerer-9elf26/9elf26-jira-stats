# Slack + Web Approval Layer for 7AM/Sevi: Platform Capabilities and Recommended Architecture

Research date: 2026-08-26. Scope: how to build the user-facing approval/notification
layer of 7AM ("Sevi") so that Slack (the "Fernbedienung": Daily Brief, anomaly alerts,
one-decision-per-card approval cards, free-text commands, Ausgeführt-Protokoll) and the
web cockpit stay in sync on the same approval queue. Covers (1) Slack platform
capabilities as of 2026, (2) first-party guidance on human-in-the-loop approval queues
actionable from two surfaces, (3) what the Anthropic API / Claude Agent SDK offers for
propose → approve → execute flows, and (4) a concrete architecture sketch for the
approval service.

This document follows every non-obvious claim back to a primary source. **Access note:**
the doc sites `docs.slack.dev`, `api.slack.com`, `tools.slack.dev`, and
`docs.temporal.io` were blocked by this environment's egress proxy, so Slack facts were
read from Slack's official GitHub sources (the `slackapi/bolt-js` docs tree, the
`slackapi/slack-skills-plugin` first-party developer skills, SDK source in
`slackapi/node-slack-sdk` / `python-slack-sdk`) and Temporal facts from the source of
`docs.temporal.io` (`temporalio/documentation` repo). A handful of numeric limits could
only be confirmed via search snippets of the official pages; those are flagged inline.

---

## 1. Slack platform capabilities in 2026

### 1.1 SDKs and languages

- **Bolt is first-class in exactly three languages: JavaScript/TypeScript
  (`slackapi/bolt-js`), Python (`slackapi/bolt-python`), and Java
  (`slackapi/java-slack-sdk`, "Slack Developer Kit (including Bolt for Java) for any JVM
  language")** — confirmed from the `slackapi` GitHub org listing (all three actively
  maintained, last pushed Aug 2026). Lower-level official SDKs exist for Node
  (`node-slack-sdk`) and Python (`python-slack-sdk`); Deno SDKs target the "Run on
  Slack" hosted platform.
- **There is no official Slack SDK for Go.** The only Go code in the `slackapi` org is
  the `slack-cli` (a developer CLI, not a library). The de-facto Go library is the
  community-maintained [`slack-go/slack`](https://github.com/slack-go/slack) (~5k stars,
  active, supports Web API, Events API, Socket Mode, Block Kit; README warns "There is
  currently no major version released. Therefore, minor version releases may include
  backward incompatible changes").
- Practical consequence for a Go backend (the stack this repo's earlier research
  settled on, see `docs/research/htmx-go-sqlite-stack.md`): Slack interactivity is
  ultimately just signed HTTP POSTs (form-encoded `payload` JSON, verified with the
  signing secret) plus Web API calls — perfectly buildable in plain Go with
  `slack-go/slack` for types/API calls, or even stdlib-only for the small surface 7AM
  needs. Bolt adds listener routing, `ack()` helpers and the `Assistant` class, but
  none of it is required. Alternative: a thin Bolt-js/py sidecar owning only the Slack
  edge, talking to the Go approval service over HTTP — extra moving part, only worth it
  if you want the AI-app niceties (§1.4) with zero protocol work.

### 1.2 Interactive messages: buttons, ack, updating after a decision, modals

All from Bolt's official docs (read from `slackapi/bolt-js` repo,
`docs/english/concepts/`) and Slack's first-party Block Kit skill
(`slackapi/slack-skills-plugin`):

- **Block Kit approval card**: a message is `{text: fallback, blocks: [...]}` with an
  `actions` block containing `button` elements, each with an `action_id` (routes to your
  handler) and a `value` (opaque payload). Slack's own Block Kit skill ships an
  "Approval Message" template — header + section (requester/details) + divider +
  `actions` block with `Approve` (style `primary`) / `Reject` (style `danger`) buttons
  carrying `value: "request_123"`
  ([slack-skills-plugin/skills/block-kit/references/common-patterns.md](https://github.com/slackapi/slack-skills-plugin/blob/main/skills/block-kit/references/common-patterns.md)).
  This is exactly the [Ok, Sevi] [Anpassen] [Lass es] card shape. Limits: button `text`
  max 75 chars, button `value` max 2000 chars (from the official SDK type sources,
  [node-slack-sdk block-elements.ts](https://github.com/slackapi/node-slack-sdk/blob/main/packages/types/src/block-kit/block-elements.ts)
  and python-slack-sdk `block_elements`) — so put the **proposal ID** in `value`, never
  the proposal itself. Messages max 50 blocks; modals/home tabs max 100
  ([block-kit skill](https://github.com/slackapi/slack-skills-plugin/blob/main/skills/block-kit/SKILL.md)).
  `blocks.validate` is a public no-auth Web API method for validating payloads.
- **3-second acknowledgement**: every action/command/modal-submission request must be
  `ack()`ed within 3 seconds; do slow work after acking
  ([bolt-js acknowledge docs](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/acknowledge.md)).
  Design consequence: the button handler must only *record* the decision and update the
  card; the actual write to Meta/Google/Klaviyo runs async in the executor.
- **Updating the posted card after a decision** — two official mechanisms
  ([bolt-js actions docs](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/actions.md),
  [Slack "Handling user interaction"](https://api.slack.com/interactivity/handling)):
  1. `response_url` (delivered in every interaction payload): post a replacement
     message; `replace_original: true` (the default when responding with a message
     body) rewrites the card in place. The `response_url` can be used **up to 5 times
     within 30 minutes** of the interaction *(numbers confirmed only via search
     snippet of the official handling page — page unfetchable here)*.
  2. `chat.update` with the message's `channel` + `ts`: works any time, from any
     process — this is the one the **web surface** must use when a decision is made in
     the cockpit and the Slack card has to be rewritten ("✅ Ausgeführt" / "Entschieden
     im Cockpit"). So the decision record must store the Slack `channel` and `ts` of
     the card at post time (`chat.postMessage` returns them).
- **Modals for "Anpassen"**: a button press yields a `trigger_id`; call `views.open`
  with it to show an edit form (change budget amount, etc.). A `trigger_id` **expires 3
  seconds after issuance**
  ([Slack modals doc](https://docs.slack.dev/surfaces/modals/), confirmed via snippet),
  so the modal must open before any slow work. Modal submissions arrive as `view`
  events; `ack()` with `response_action: errors` does inline validation
  ([bolt-js acknowledge docs](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/acknowledge.md)).
- **Socket Mode**: an internal app can receive all events/interactions over an outbound
  WebSocket (`SocketModeReceiver`, app-level `xapp-` token) instead of exposing a public
  HTTPS endpoint
  ([bolt-js socket-mode docs](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/socket-mode.md)).
  Slack recommends it for internal apps; it removes the need for request-signature
  handling and a public URL on the EU-hosted server. `slack-go/slack` also supports it
  (README, flagged "experimental" for its event handler).

### 1.3 Scheduled messages (Daily Brief at 7:00)

`chat.scheduleMessage` exists: `post_at` is a Unix timestamp at least ~2 minutes and at
most **120 days** in the future; scheduled messages **cannot be edited via the API once
set** (per Slack's own MCP `slack_schedule_message` tool docs and the
[chat.scheduleMessage method page](https://docs.slack.dev/reference/methods/chat.schedulemessage.md);
the 120-day/no-edit constraints were read from Slack's first-party MCP tooling, not the
method page directly). **Recommendation: don't use it for the Daily Brief.** The brief
is generated fresh at 06:5x from last night's data; a server-side cron that calls
`chat.postMessage` at 07:00 is simpler and always current. `scheduleMessage` is only
interesting for pre-composed content.

### 1.4 Slack AI-app features relevant to free-text Sevi chat

Slack has grown a first-party "agents & AI apps" surface that maps well onto Sevi's
free-text channel (§7.2 of the Konzept). From
[bolt-js "Adding agent features"](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/adding-agent-features.md)
and [bolt-js "Using the Assistant class"](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/using-the-assistant-class.md):

- **Agent messaging experience is now the default** (`agent_view`): as of the
  2026-06-30 platform changelog, new apps get agent conversations in the Messages tab,
  handling `app_home_opened` and `message.im` directly; the older `assistant_view`
  (separate Chat/History tabs, `Assistant` class, `assistant_thread_started` /
  `assistant_thread_context_changed` events) is the legacy path.
- **Chat streaming**: `chat.startStream`/append/stop, wrapped by the Node SDK's
  `WebClient.chat.stream` helper and Bolt's `sayStream` utility — token-streamed
  markdown responses in Slack, with `setStatus` for "thinking…" indicators.
- **`feedback_buttons` block element**: first-class 👍/👎 on agent responses — useful
  for the Lern-Datenbank (Teil VIII) with zero custom UI.
- **Slack MCP Server**: an app can enable "Model Context Protocol" in its settings so
  an agent framework can drive Slack itself; Slack's official sample
  [`slack-samples/bolt-js-support-agent`](https://github.com/slack-samples/bolt-js-support-agent)
  ("Casey") explicitly integrates **the Claude Agent SDK** — i.e. Slack's own reference
  pattern for an AI agent app is Bolt at the edge + Claude Agent SDK behind it.
- Requires a paid workspace (or free Developer Program sandbox) for some features.

These features are additive polish for free-text chat; the approval cards themselves are
plain Block Kit and work on any plan. Note the Konzept's rule that free-text never
bypasses approval — actions extracted from chat must create the same proposal records.

### 1.5 Rate limits

From the official rate-limit docs (page unfetchable directly; numbers cross-checked via
search snippets of [docs.slack.dev/apis/web-api/rate-limits](https://docs.slack.dev/apis/web-api/rate-limits/)
— treat exact per-tier numbers as *lightly verified*):

- Web API methods are tiered per app+workspace per minute: Tier 1 ≈ 1+/min,
  Tier 2 ≈ 20+/min, Tier 3 ≈ 50+/min, Tier 4 ≈ 100+/min, each allowing short bursts.
- `chat.postMessage` has a **special tier: ~1 message per second per channel**, short
  bursts tolerated.
- Events API delivery caps at **30,000 events per workspace per app per 60 minutes**;
  beyond that Slack sends `app_rate_limited`
  ([app_rate_limited event doc](https://docs.slack.dev/reference/events/app_rate_limited/), via snippet).
- On HTTP 429 honor the `Retry-After` header (seconds)
  ([slack-api skill](https://github.com/slackapi/slack-skills-plugin/blob/main/skills/slack-api/SKILL.md)).
- **May/Sept 2025 change for non-Marketplace apps**: newly created apps not approved
  for the Slack Marketplace get `conversations.history`/`conversations.replies` limited
  to ~1 request/min with `limit` capped at 15
  ([changelog](https://api.slack.com/changelog/2025-05-terms-rate-limit-update-and-faq),
  [clarification](https://docs.slack.dev/changelog/2025/06/03/rate-limits-clarity/)).
  Slack's clarification states **"internal customer-built apps will not notice any
  changes"** — the restriction targets commercially distributed non-Marketplace apps.
  For 7AM this matters the day it becomes a multi-tenant product installed into
  *customers'* workspaces (see §1.6); it does not affect the single-workspace phase,
  and 7AM barely reads message history anyway (it posts, updates, and receives
  interactions — none of which are restricted).

For 7AM's volumes (a handful of shops × 3 channels × dozens of messages/day) none of
these limits are close; the only rule worth engineering for is 1 msg/sec/channel (queue
card posts per channel) and always honoring `Retry-After`.

### 1.6 Distribution / approval model: internal vs multi-tenant

From [Slack's distribution docs](https://docs.slack.dev/distribution) (via snippets) and
the [org-ready apps page](https://api.slack.com/enterprise/org-ready-apps):

- **Single-workspace internal app** (7AM MVP with Tattup): create the app, install it
  into your own workspace from the app config page — **no Slack review, no OAuth flow,
  no distribution step**. Tokens are the workspace's `xoxb-` bot token. Workspace
  admins may require app approval inside their own workspace (Slack's "admin-approved
  apps" setting), but that is the customer's admin, not Slack.
- **Multi-workspace (each customer's own Slack)**: requires enabling distribution and
  implementing **OAuth 2.0** to obtain a per-workspace token; "unlisted" distribution
  (share the install link) needs **no Slack review** and is the intended path for
  pilots. **Marketplace listing** requires Slack's review against their guidelines and
  is what exempts an app from the 2025 non-Marketplace rate limits when distributed
  commercially.
- Architecture consequence: even in phase 1, keep `(team_id, bot_token, channel ids)`
  in a `slack_installation` table instead of env vars, so the jump to OAuth multi-tenancy
  is a new row source, not a refactor. Bolt's docs cover OAuth/token rotation
  ([authenticating-oauth](https://github.com/slackapi/bolt-js/blob/main/docs/english/concepts/authenticating-oauth.md));
  `slack-go/slack` exposes the OAuth endpoints too.

---

## 2. First-party patterns: human-in-the-loop approval actionable from two surfaces

### 2.1 Slack's guidance

Slack's own guidance is UI-level: the approval-message template (§1.2), acknowledge
fast / work async, and use `chat.update`/`response_url` to reflect state changes in the
card. Slack deliberately treats your backend as the source of truth — a Slack message is
a *projection* of state, not the state itself. Nothing in Slack deduplicates decisions
for you: if two people (or two surfaces) act, your handler sees two interaction
payloads, and it is your job to make the second one a no-op. (Slack's Events API also
retries deliveries up to 3 times on failure, so event handling must be idempotent as
well — retry behavior widely documented; *not re-verified against the events page in
this session*.)

### 2.2 Temporal's Approval design pattern (the strongest first-party source found)

Temporal publishes a dedicated **"Approval Pattern"** design-pattern page —
human-in-the-loop workflows that block until an external decision arrives (read from
[temporalio/documentation `docs/design-patterns/approval.mdx`](https://github.com/temporalio/documentation/blob/main/docs/design-patterns/approval.mdx),
rendered at docs.temporal.io/design-patterns/approval). Its content is directly
transferable even if 7AM never runs Temporal:

- **Decision as a rich record, not a boolean**: capture `{approver, decision
  (APPROVED|REJECTED|ESCALATED), comments, timestamp}` — "capture rich approval
  context … rather than a plain boolean".
- **Block-with-timeout**: the workflow waits for the decision *or* a deadline; on
  timeout it rejects, escalates (notify + extended wait), or auto-falls-back. "Without
  a timeout, the Workflow waits indefinitely for an approval that may never arrive" is
  listed as the #1 pitfall. Maps 1:1 to the Konzept's "Später"/expiry semantics.
- **Idempotency is on you for fire-and-forget messages**: "Handle duplicate approval
  Signals safely so that re-delivery does not corrupt state; use idempotency keys."
  Temporal's message-passing encyclopedia
  ([handling-messages.mdx](https://github.com/temporalio/documentation/blob/main/docs/encyclopedia/workflow-message-passing/handling-messages.mdx))
  distinguishes:
  - **Signals** — async, fire-and-forget; the caller gets no result; deduplicate with
    your own idempotency key.
  - **Updates** — *synchronous, tracked* write requests: the caller waits and receives
    a result or error; the server **deduplicates by Update ID**; an optional
    **Validator** (non-blocking, read-only) accepts or rejects *before* anything is
    written to history — "If it rejects … the Workflow will have no indication that it
    was ever requested."
  The two-surface approval click is exactly an **Update with a validator**: validate
  ("is this proposal still pending and unexpired?"), reject with a reason if not, and
  return the outcome synchronously so the surface can render "already decided".
- **Audit for free**: every decision lives in an ordered history — the property the
  Konzept demands (Audit-Log wer/was/wann/warum) and that the approval service must
  reproduce with an append-only decision/event table.
- Also relevant: Temporal's validation that a signal and a timer "cannot truly race" —
  events are processed in one deterministic order; design the timeout path to tolerate
  a late-arriving decision. In a DB-backed service the equivalent is: expiry and
  decision must contend on the same row-level compare-and-set, never on two independent
  code paths.

### 2.3 Anthropic first-party guidance

Anthropic's tool-use guidance (Claude API docs / `claude-api` skill,
[platform.claude.com tool-use docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview)):
"For tools with side effects (sending emails, modifying databases, financial
transactions), validate inputs and **gate destructive operations behind human
approval**" — with the gate implemented either inside the tool's run function (return a
"user declined" result) or by intercepting the pending tool call before execution. The
Konzept's rule 9.1c (the model produces structured proposals only; a deterministic
execution service checks policy, approval token and schema) is the same architecture
Anthropic recommends. See §3 for the concrete mechanisms.

### 2.4 Synthesis: the two-surface pattern

No vendor doc prescribes "Slack + web on one queue" verbatim, but the three sources
converge on one shape:

1. One **decision record** per proposal in one store; both surfaces are dumb views.
2. Decisions are made by one **idempotent, validated, synchronous operation**
   (Temporal's "Update"): `decide(proposal_id, verdict, actor, surface,
   idempotency_key, proposal_revision)`.
3. First writer wins via **compare-and-set on the record's state (and revision)**; the
   loser gets a structured "already decided by X at T / superseded" answer, which the
   surface renders instead of an error.
4. Surfaces are updated by **projection**: after any state change, rewrite the Slack
   card (`chat.update` using stored channel+ts) and push/poll the web cockpit. A
   decision made on the web must update Slack and vice versa — same code path.
5. **Expiry and supersession are state transitions of the same record**, executed by
   the same guarded transition function as human decisions (Konzept 9.1b step 4: "Die
   Freigabe bindet sich an genau diesen Diff — ändert sich die Lage, verfällt sie").

---

## 3. Anthropic API / Claude Agent SDK for propose → approve → execute (today)

All from the current first-party `claude-api` skill content and the
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python) source.

### 3.1 Structured proposals (Messages API)

- **Structured outputs**: `output_config: {format: {...}}` constrains the response to a
  JSON schema (the old `output_format` parameter is deprecated); SDK helper
  `client.messages.parse()` validates against your schema
  ([structured outputs doc](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)).
  This is how Sevi emits a `Proposal{action_type, target_ids, diff, reasoning,
  expected_effect, risk, confidence, undo_plan}` object that the deterministic layer
  can trust *syntactically* (policy checks remain separate).
- **Strict tool use**: `strict: true` on a tool definition (schema with
  `additionalProperties: false` + `required`) guarantees `tool_use.input` validates
  exactly against the schema — no beta header. So "create_proposal" can be a strict
  tool and the model physically cannot emit a malformed action.

### 3.2 Approval hooks in the three agent harnesses

- **Tool Runner (Anthropic SDK, `client.beta.messages.tool_runner`)**: per-turn hooks
  support approval gates — gate inside the tool's run function (return "user declined"
  instead of executing) or inspect the pending tool call in the yielded message and
  override before it runs (`set_messages_params()` / `setMessagesParams()`).
- **Claude Agent SDK** (`claude-agent-sdk` / `@anthropic-ai/claude-agent-sdk`; Claude
  Code as a library): a first-class permission pipeline —
  `can_use_tool: (tool_name, input, context) → PermissionResultAllow(updated_input?) |
  PermissionResultDeny(message, interrupt?)` async callback, `allowed_tools` /
  `disallowed_tools` allow/deny lists, `permission_mode`, plus hooks (`PreToolUse`,
  `PermissionRequest`, `PostToolUse`, …). Evaluation order documented in the
  [permissions guide](https://platform.claude.com/docs/en/agent-sdk/permissions)
  (source: `claude-agent-sdk-python` README and `src/claude_agent_sdk/types.py`).
  Caveat: `can_use_tool` is an *in-process await* — fine for a supervised session, but
  a Freigabe-Karte that may sit unanswered for hours should not hold an agent process
  open. For 7AM, `can_use_tool` is the right hook for the *interactive free-text*
  path ("Pausiere alles über 50 EUR CPA" → deny-execute, create proposal instead);
  overnight analysis should just *emit proposals and terminate*.
- **Managed Agents (beta)**: server-side sessions support
  `permission_policy: {type: 'always_ask'}` per tool; the session emits
  `agent.tool_use` with `evaluated_permission === 'ask'`, **goes idle**, and resumes
  when you send `user.tool_confirmation {tool_use_id, result: 'allow'|'deny',
  deny_message?}`. This is a hosted long-lived pending-approval primitive — but the
  pending state lives inside an Anthropic session rather than in 7AM's own audit-able
  store, so it fits interactive sessions, not the durable approval queue.

### 3.3 The load-bearing conclusion

Every Anthropic mechanism above gates a *tool call inside a running agent*. The
Konzept's queue (proposals that live for hours, survive restarts, are audited, expire,
and are approvable from two surfaces) must be **application state, not agent state**:
the model's job ends when a schema-valid proposal row exists; execution is a separate
deterministic service triggered by the approval, exactly as Konzept 9.1b/9.1c already
specifies and as Anthropic's own "LLM produces structured proposals, execution service
checks policy/approval/schema" guidance recommends. The agent-side approval hooks are
still useful — but only for the synchronous chat surface.

---

## 4. Recommended architecture: the Approval Service

### 4.1 One decision record

```
proposal
  id                ULID ("Ok #142" is a display alias)
  shop_id, area     (meta|google|klaviyo), risk_class
  revision          int  -- bumped when the underlying situation changes
  state             (see 4.2)
  payload           action_type, target ids, current→desired diff,
                    reasoning, expected effect, risk, confidence, playbook ref
  undo              before-snapshot of affected objects + inverse action,
                    captured when the proposal is CREATED (Konzept 9.1b step 6
                    takes another snapshot immediately before executing)
  expires_at        timestamptz
  decision          verdict (approve|adjust|reject), actor_id, surface (slack|web),
                    decided_at, comment/adjustment   -- all NULL until decided
  slack_ref         channel, ts (from chat.postMessage), team_id
  execution         idempotency_key, platform request/response ids, executed_at,
                    verify_readback_ok, rolled_back_by → proposal.id
proposal_event      append-only: (proposal_id, seq, event, actor, surface, at, data)
```

`proposal_event` is the Audit-Log; `proposal` is the current-state projection of it.

### 4.2 State machine

```
DRAFT → PENDING ──(approve)──→ APPROVED → EXECUTING → EXECUTED ──(undo approved)──→ ROLLED_BACK
          │  │ (adjust: new revision, back to PENDING with re-render)
          │  ├─(reject / "Lass es")→ REJECTED
          │  ├─(expires_at passes)─→ EXPIRED
          │  └─(situation changed)─→ SUPERSEDED ──→ (optionally new PENDING proposal)
          └─(snooze / "Später")→ PENDING with pushed expires_at + re-remind
EXECUTING → FAILED (platform error / readback mismatch → alert card)
```

- Only `PENDING` accepts human verdicts. `SUPERSEDED` implements Konzept 9.1b step 4
  ("die Freigabe bindet sich an genau diesen Diff"): the watcher that detects a changed
  situation calls the same transition function, and the card is rewritten to "Lage hat
  sich geändert — neuer Vorschlag unten".
- In Auto mode the machine is identical; the "approver" is the deterministic guardrail
  engine (`actor = policy:auto-v...`), and EXECUTED items render into the
  Ausgeführt-Protokoll with their undo link. One state machine, two approvers — this
  keeps Manual/Auto per Bereich a configuration flag, not a second code path.

### 4.3 How a Slack button and a web click converge

Both surfaces call the **same internal endpoint**
`POST /proposals/{id}/decision {verdict, actor, surface, proposal_revision,
idempotency_key}`:

1. Slack: interaction handler verifies signature (or arrives via Socket Mode), `ack()`s
   within 3 s, maps `action_id` + `value`(=proposal id) + Slack user → the endpoint.
   Web: the cockpit button posts the same body with the session user.
2. The endpoint runs **one guarded transition** (Temporal-Update-style
   validate-then-commit): a single SQL
   `UPDATE proposal SET state='APPROVED', decision=... WHERE id=? AND state='PENDING'
   AND revision=?`. Rows-affected = 1 → winner: append event, enqueue execution job,
   return `{ok, new_state}`. Rows-affected = 0 → loser: read the row and return a
   structured `{conflict: already_decided|expired|superseded, by, at}` — **not an
   error**; the surface renders it ("Bereits erledigt von Timo um 09:12 im Cockpit").
   With SQLite (single writer) this compare-and-set is trivially race-free.
3. `idempotency_key` (per click; Slack retries/double-clicks reuse it) makes the
   endpoint replay-safe: same key + same verdict → return the recorded outcome.
4. **Projection fan-out** after every transition: rewrite the Slack card via
   `chat.update(channel, ts)` (buttons removed, outcome + actor + undo link shown), and
   notify the cockpit (SSE/htmx poll). Because the fan-out uses stored `slack_ref`, it
   works no matter which surface — or the expiry sweeper, or Auto mode — caused the
   transition. Fan-out is at-least-once and itself idempotent (rendering is a pure
   function of the record).
5. The **executor** consumes APPROVED jobs: re-check guardrails, snapshot, idempotent
   platform write (Konzept 9.1b steps 5–8), readback-verify, then transition
   EXECUTED/FAILED — which again triggers the projection fan-out.

### 4.4 Undo/rollback attachment

Undo is data on the record, not a special flow: the `undo` field stores the
before-snapshot and the inverse action *at proposal time*; execution refreshes the
snapshot immediately before writing. "Ein Klick zurück" on the executed card (Slack or
web) does not directly revert — it **creates a new PENDING proposal** whose payload is
the inverse action and whose `rolled_back_by` back-links the original, because the
Konzept makes rollback itself freigabepflichtig ("Rücknahme vorbereitet — aber
ebenfalls freigabepflichtig", 9.1b step 10). One mechanism, full audit trail.

### 4.5 Load-bearing decisions vs. details

**Load-bearing (hard to change later):**

1. **The decision record lives in 7AM's DB; Slack and web are projections.** Everything
   else follows from this. (Alternative rejected: keeping pending state inside an agent
   session or in Slack messages.)
2. **Single guarded transition function** (compare-and-set on state+revision) used by
   humans, expiry, supersession, and Auto mode alike; append-only event log beside it.
3. **Model emits schema-validated proposals only; deterministic executor owns writes**
   (matches both Konzept 9.1c and Anthropic guidance).
4. **Store `slack_ref` (channel, ts, team) on the record** — without it, web-side
   decisions can never update Slack cards.
5. **Multi-workspace-shaped installation storage** from day 1 (§1.6).

**Details (defer / swap freely):**

- Bolt sidecar vs. plain Go + `slack-go/slack` vs. stdlib HTTP — protocol is small
  either way; Socket Mode vs. public endpoint likewise.
- Streaming/Assistant polish for free-text chat (§1.4), `feedback_buttons`.
- Whether the executor queue is a DB table + poller, a job library, or (much later, if
  multi-step campaign launches need sagas) Temporal itself — the state machine above is
  deliberately Temporal-shaped so this remains an implementation swap.
- Card layout specifics (validate with the public `blocks.validate`), exact expiry
  durations, snooze semantics.
- `chat.scheduleMessage` vs. cron for the Daily Brief (recommend cron, §1.3).

---

## Unverified / weakly-verified claims (summary)

- Exact per-tier rate-limit numbers (1/20/50/100+ per min) and the `chat.postMessage`
  1/sec/channel figure: consistent across search snippets of the official page, but the
  page itself was unfetchable from this environment.
- `response_url` "5 uses / 30 minutes" and `trigger_id` "3 seconds": official-page
  snippets only.
- Events API retry-on-failure (3 attempts): from prior knowledge, not re-verified here.
- 120-day / no-edit constraints of `chat.scheduleMessage`: read from Slack's
  first-party MCP tool documentation rather than the method page.
- The 2026-06-30 "agent messaging experience" changelog entry is cited inside Bolt's
  own docs (read from the repo), but the changelog page itself was not fetched.
