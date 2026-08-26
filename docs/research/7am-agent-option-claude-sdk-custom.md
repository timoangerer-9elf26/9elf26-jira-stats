# 7AM Agent Option C: Claude Agent SDK vs. Custom Orchestration on the Anthropic API

Research date: 2026-08-26. Scope: the third of three agent-option documents for "7AM"
(siblings cover "Hermes" and "pi"). This one evaluates building the agent layer directly
on Anthropic's offerings — the Claude Agent SDK on one hand, the plain Anthropic
Messages API inside a self-built orchestration (the concept doc's Teil-X plan:
Python + FastAPI + Celery + Postgres + Anthropic SDK) on the other — plus the current
state of MCP servers for the five platforms 7AM touches, and the
scheduling/orchestration backbone options.

This document follows every non-obvious claim back to a primary source (official docs,
source repos, vendor announcements). Where a primary source was unreachable from this
environment (network egress proxy) or evidence is secondary, it is **flagged explicitly**.

Requirements recap (from the concept doc, Teil X / 1.4 / 1.8): seven specialist agents
(hourly Watchdog; Buyer executing only whitelisted actions against Meta/Google;
Accountant economics engine with veto; Tracker; Critic; Producer; Mailer) behind one
voice (Sevi); scheduled autonomous runs; a **deterministic guardrail engine outside the
LLM** (hard budget caps, max +25% budget steps, 48h cooldowns, max 5 changes/day,
action whitelist, kill switch); proposal→approval→execute pipeline in Slack; Postgres
for metrics/config/audit; EU self-hosted Docker; LLM budget of ~50–200 EUR/month for
1–3 shops.

---

## 1. Claude Agent SDK — state in 2026

### What it is

The Agent SDK is **Claude Code packaged as a library**: "The Agent SDK gives you the
same tools, agent loop, and context management that power Claude Code, programmable in
Python and TypeScript." It is available **only for Python and TypeScript**; to drive
the same loop from another language you run the CLI as a subprocess with `-p` and
`--output-format json` (headless mode).
([code.claude.com/docs/en/agent-sdk/overview](https://code.claude.com/docs/en/agent-sdk/overview))

Anthropic's own decision table on that page distinguishes it from the alternatives:

| You are... | Use |
|---|---|
| Building an agent without implementing the tool loop yourself | **Agent SDK** (library, runs the loop in your process) |
| Calling the API and implementing the tool loop yourself | **Client SDK** (plain `anthropic` package — the concept doc's plan) |
| Running long-running/async agents without managing sandbox/session infra | **Managed Agents** (hosted REST API, Anthropic runs the agent — *not* EU self-hosted, so out of scope for 7AM) |

### What it provides

All from the official docs
([overview](https://code.claude.com/docs/en/agent-sdk/overview),
[permissions](https://code.claude.com/docs/en/agent-sdk/permissions)):

- **Built-in tools**: Read/Write/Edit files, run commands (Bash), Glob/Grep, web
  search/fetch — a filesystem-and-shell tool surface, i.e. a *coding agent's* toolkit.
- **MCP**: connect external tools/data via Model Context Protocol servers.
- **Subagents**: spawn specialized agents for focused subtasks, with per-subagent
  depth/concurrency/spend caps.
- **Hooks**: custom code at lifecycle points; a `PreToolUse` hook "runs before every
  other step, and a hook deny applies even in `bypassPermissions` mode" — the strongest
  programmatic gate.
- **Permissions**: a six-step evaluation order (hooks → deny rules → ask rules →
  permission mode → allow rules → `canUseTool` callback). Modes include `default`,
  `dontAsk` ("anything not pre-approved ... is denied ... `canUseTool` is never
  called"), `acceptEdits`, `bypassPermissions`, `plan`, `auto`. For a locked-down
  headless agent the docs recommend pairing `allowedTools` with
  `permissionMode: "dontAsk"`. The `canUseTool` callback is the programmatic
  approval hook — in principle mappable onto a Slack approval round-trip.
- **Sessions**: "Maintain context across exchanges, resume or fork later" (session IDs
  + `resume` option).
- **Cost/usage reporting**: per-step `usage`, per-model `modelUsage`, cumulative
  `total_cost_usd` on the result message — but the docs warn these "are client-side
  estimates, not authoritative billing data"; authoritative numbers come from the
  Usage & Cost API/Console.
  ([code.claude.com/docs/en/agent-sdk/cost-tracking](https://code.claude.com/docs/en/agent-sdk/cost-tracking))
- **Prompt caching is automatic**: "The Agent SDK automatically uses prompt caching
  ... You do not need to configure caching yourself." For scheduled workloads with
  >5-minute gaps between runs, the docs explicitly note the 5-minute cache expires
  between sessions and each new session pays full input price; `ENABLE_PROMPT_CACHING_1H`
  (or `promptCacheTtl: "1h"`) requests the 1-hour TTL at a higher write rate. This is
  directly relevant to an hourly Watchdog. (same cost-tracking page)

### Fit for 7AM's scheduled ops agents — assessment

- **Designed around coding/filesystem work.** The overview defines an agent as one
  "calling tools that read files, run commands, or edit code", and the quickstart's
  first agent "finds and fixes bugs in existing code". Nothing prevents pointing it at
  MCP-only tool surfaces (ads accounts instead of files), but Sevi's tools are API
  connectors and SQL, not a filesystem — most of the built-in surface would be
  disabled (`disallowedTools`), while the Claude Code harness (system prompt, tool
  definitions) still ships with every request. *Flagged: I found no official number
  for the harness's fixed token overhead; it is cached after the first request of a
  session, but each scheduled cold start re-pays a cache write.*
- **The permission system overlaps with, but does not replace, 7AM's guardrail
  engine.** Allow/deny rules and `canUseTool` gate *which tool with which input* may
  run; they cannot evaluate "would this exceed the daily cap given what was already
  spent" — that needs DB state and belongs in 7AM's deterministic engine regardless of
  which AI layer is chosen. The docs' own warning is instructive: "Auto-approved tools
  never reach `canUseTool` ... For checks that must run on every tool call, use a
  `PreToolUse` hook." So even inside the SDK, the safe pattern is a hook that calls
  *your* guardrail engine — the SDK adds a loop, not the guardrails.
- **Licensing/auth**: "Anthropic does not allow third party developers to offer
  claude.ai login or rate limits for their products, including agents built on the
  Claude Agent SDK. Use the API key authentication methods ... instead." So a
  productized 7AM pays normal API token prices either way; the SDK gives no pricing
  advantage. ([overview](https://code.claude.com/docs/en/agent-sdk/overview))
- **Cost implication of its loop**: the SDK's value is exactly that Claude drives an
  open-ended multi-step loop — which is also its cost profile: every step re-sends
  context (mitigated by caching) and the number of steps is model-decided. For 7AM,
  most runs are *not* open-ended: Watchdog = fetch metrics → evaluate rules → maybe
  one diagnosis call; Accountant = pure deterministic math. A fixed workflow with
  1–3 targeted Messages-API calls is cheaper and more predictable than an agentic
  loop. Anthropic's own guidance in the platform docs ("Building an agent" criteria)
  says to stay at the single-call/workflow tier when the task is fully specifiable in
  advance.

**Verdict**: the Agent SDK is a strong choice for *internal* development-and-ops
tooling (e.g., an agent that maintains 7AM's own playbooks or investigates incidents
on the server), and viable for the conversational "ask Sevi anything" surface. It is
the wrong default for the production hourly/daily pipeline, where deterministic
workflows with targeted LLM calls dominate.

## 2. Plain Anthropic Messages API as the AI layer (the concept doc's plan)

Everything goes through `POST /v1/messages`; tools and output constraints are features
of this one endpoint. What the API guarantees today:

### Structured outputs — GA, and exactly what the guardrail pipeline needs

Structured outputs are **generally available** (no beta) on current models
(Opus 5/4.x, Sonnet 5/4.x, Haiku 4.5) across Claude API, Bedrock, Google Cloud,
Foundry. Two complementary features
([platform.claude.com/docs/en/build-with-claude/structured-outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)):

1. **JSON outputs** via `output_config: {format: {type: "json_schema", schema: ...}}` —
   constrains the response to a schema via constrained decoding: "Always valid: No more
   `JSON.parse()` errors ... Type safe: guaranteed field types and required fields ...
   No retries needed for schema violations." (The older `output_format` parameter is
   deprecated.)
2. **Strict tool use** via `strict: true` on a tool definition — "guarantees schema
   validation on tool names and inputs", i.e. a Buyer proposal emitted as a tool call
   is guaranteed to match the action-whitelist schema before the guardrail engine even
   sees it.

Limitations to design around: no recursive schemas, no numeric `minimum`/`maximum`
constraints (so "+25% max" **cannot** be enforced by the schema — it stays in the
deterministic engine, which matches the concept anyway), first use of a schema pays a
grammar-compilation latency, compiled grammars cached 24h.

### Tool use

Custom tools (JSON-schema functions) with a manual loop, or the SDK's beta **Tool
Runner** (`client.beta.messages.tool_runner`, `@beta_tool` in Python) which drives the
request→execute→loop cycle for tools you define, with per-turn hooks for approval
gates, error interception, and result modification — no built-in tools, no filesystem,
you host everything. This is the natural "agentic when needed" middle tier for Sevi's
conversational surface without importing the Claude Code harness.
(Anthropic SDK docs/repos: [github.com/anthropics/anthropic-sdk-python](https://github.com/anthropics/anthropic-sdk-python))

The Messages API also has an **MCP connector** (beta `mcp-client-2025-11-20`):
`mcp_servers: [{type: "url", ...}]` plus a `tools: [{type: "mcp_toolset", ...}]` entry
lets a plain API call use a remote MCP server (e.g. Google's Ads MCP) without you
proxying the tools — relevant to §3.

### Prompt caching — for the big playbook prompts

Verified pricing multipliers relative to base input
([platform.claude.com/docs/en/about-claude/pricing](https://platform.claude.com/docs/en/about-claude/pricing)):

| Cache operation | Multiplier | Duration |
|---|---|---|
| 5-minute cache write | 1.25x | 5 min |
| 1-hour cache write | 2x | 1 h |
| Cache read (hit) | **0.1x** | per preceding write |

Caching is a strict **prefix match** (render order `tools` → `system` → `messages`;
any byte change invalidates everything after it; ~1024-token minimum cacheable
prefix). For 7AM this means: freeze the per-shop playbook + KPI profile as a stable
system prefix, put volatile data (today's metrics) after the last breakpoint, and an
hourly Watchdog should use the 1-hour TTL (2x write, then 0.1x on every run that hour).
Caching multipliers stack with the Batch discount.
([platform.claude.com/docs/en/build-with-claude/prompt-caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching), pricing page)

### Batch API — for cheap nightly analysis

"The Batch API allows asynchronous processing of large volumes of requests with a
**50% discount on both input and output tokens**" — submit
`POST /v1/messages/batches`, poll until `processing_status: "ended"`, stream results
keyed by `custom_id`. Perfect fit for the nightly 7-Uhr-Bericht pipeline (per-shop
per-campaign analyses, Critic creative scoring) where a multi-hour turnaround before
07:00 is fine. (pricing page, and
[batch processing docs](https://platform.claude.com/docs/en/build-with-claude/batch-processing))

### Model tiers and pricing (verified 2026-08-26, all USD)

From the official pricing page
([platform.claude.com/docs/en/about-claude/pricing](https://platform.claude.com/docs/en/about-claude/pricing)):

| Model | Input /MTok | Output /MTok | Batch input | Batch output |
|---|---|---|---|---|
| Claude Opus 5 | $5 | $25 | $2.50 | $12.50 |
| Claude Sonnet 5 | $2 | $10 | $1 | $5 |
| Claude Sonnet 4.6 | $3 | $15 | $1.50 | $7.50 |
| Claude Haiku 4.5 | $1 | $5 | $0.50 | $2.50 |

(Sonnet 5's $2/$10 launch pricing "is now the standard price" — the scheduled Sep 2026
increase was cancelled. Note: models from 4.7 on use a tokenizer producing ~30% more
tokens for the same text.)

**Budget check against 50–200 EUR/month for 1–3 shops** (own calculation, not a
source): an hourly Watchdog on Haiku 4.5 with a cached 15k-token playbook prefix and
~3k uncached tokens in / 1k out per run ≈ $0.011/run ≈ **$8/month per shop**; a daily
brief + ~10 diagnosis/proposal calls on Sonnet 5 at ~20k in (mostly cached) / 2k out
≈ $0.3–0.5/day ≈ **$10–15/month per shop**; nightly batch analyses on Sonnet 5 at 50%
off add a few dollars. Order of magnitude **$20–40/month per shop** with disciplined
caching — comfortably inside the concept's 50–200 EUR envelope for 1–3 shops, with
headroom to use Opus 5 for the weekly retro / strategy calls. An Agent-SDK-style
open-ended loop on every run would plausibly multiply this by the number of loop steps.

## 3. MCP (Model Context Protocol) in 2026 — the five platforms

### Google Ads — concept claim VERIFIED

The concept doc claims an official Google MCP exists for reading ("offizieller
Google-MCP fürs Lesen"). **Confirmed.** Google open-sourced the **Google Ads API MCP
Server** (announced on the Google Ads Developer Blog, 2025-10-07; repo
[github.com/googleads/google-ads-mcp](https://github.com/googleads/google-ads-mcp)).
Verified from the repo: it is the official server, exposing three tools — `search`
(GAQL queries), `get_resource_metadata`, `list_accessible_customers` — i.e. reporting
and diagnostics only; the announcement states "This initial release is read-only ...
it will not make changes to your account." Requirements: a developer token with "at
least Explorer access to query production accounts", OAuth/ADC auth. The repo warns it
"will expose your data to the Agent or LLM that you connect to it" and that Google
collects usage telemetry via extra API headers. *Flag: the blog post itself
(ads-developers.googleblog.com) was blocked by this environment's egress proxy; the
read-only quote comes from search-indexed excerpts of it, the tool list from the repo
directly.* Writes (budget changes, pausing) still require the Google Ads API proper —
exactly the concept's plan (P4: "Google lesen (offizieller MCP/Reporting) und erste
Writes (Ads API)").

### Meta — an official Ads MCP server now exists (post-dates the concept doc)

Meta launched **Ads AI Connectors** — a hosted MCP server at `mcp.facebook.com/ads`
plus a CLI — announced on the Meta developers blog
([developers.facebook.com/blog/post/2026/07/16/meta-ads-mcp-server/](https://developers.facebook.com/blog/post/2026/07/16/meta-ads-mcp-server/));
secondary sources date the launch 2026-04-29 and describe ~29 tools covering campaign
management (i.e. including writes) with Business-Suite-based auth. *Flag:
developers.facebook.com is blocked by the egress proxy, so tool count, write scope,
and rate-limit claims are from secondary sources
([digitalapplied.com](https://www.digitalapplied.com/blog/official-ads-mcp-servers-meta-google-tiktok-2026-playbook),
[pasqualepillitteri.it](https://pasqualepillitteri.it/en/news/1707/official-meta-ads-mcp-claude-29-tools-2026)) —
verify against the official docs
([developers.facebook.com/documentation/ads-commerce/ads-ai-connectors/](https://developers.facebook.com/documentation/ads-commerce/ads-ai-connectors/ads-mcp-server/ads-mcp-server-overview))
before relying on it.* Even if it supports writes, see the boundary argument below:
7AM's Buyer writes should not go through an LLM-invoked MCP tool.

### Shopify — official MCP exists, but not for live shop-ops data

Shopify's official **Dev MCP server** (`@shopify/dev-mcp`,
[shopify.dev/docs/apps/build/devmcp](https://shopify.dev/docs/apps/build/devmcp)) is a
*developer-assistant* server: search Shopify docs, introspect the Admin GraphQL
schema, validate code — useful while *building* 7AM's connector, not for reading a
shop's orders. The **Storefront MCP server**
([shopify.dev/docs/apps/build/storefront-mcp](https://shopify.dev/docs/apps/build/storefront-mcp/servers/storefront))
targets shopping agents (buy-side), not merchant operations. I found **no official
Shopify MCP server exposing Admin/order/revenue data for ops agents**; the Admin
GraphQL API remains the path for 7AM's revenue truth. *Flag: absence is hard to prove;
re-check shopify.dev before committing.*

### Klaviyo — official remote MCP server exists

Klaviyo runs an official MCP server, documented at
[developers.klaviyo.com/en/docs/klaviyo_mcp_server](https://developers.klaviyo.com/en/docs/klaviyo_mcp_server),
with a hosted remote endpoint at `mcp.klaviyo.com/mcp` (OAuth). *Flag:
developers.klaviyo.com and klaviyo.com are blocked by the egress proxy; capability
details are secondary
([mcpservers.org](https://mcpservers.org/remote-mcp-servers/klaviyo),
[everboost.co.uk field guide](https://everboost.co.uk/insights/klaviyo-mcp-server/)):
strong on reporting/audits/drafting (can create campaign drafts, templates, profiles)
but as of mid-2026 reportedly cannot build or edit flows or create segments — the
Mailer's core write surface would still be the Klaviyo REST API.*

### Slack — official MCP server exists, but 7AM wants Bolt anyway

Slack (Salesforce) announced MCP support and a Slack MCP server built "in close
collaboration with Anthropic" (announced Dreamforce Oct 2025; GA reported Feb 2026;
Salesforce-hosted MCP servers GA for Enterprise editions).
([slack.dev blog](https://slack.dev/secure-data-connectivity-for-the-modern-ai-era/) —
*flag: blocked by proxy, dates from secondary sources
([salesforce.com](https://www.salesforce.com/slack/new-mcp-servers-ai-data-in-slack/),
[flagship.cc](https://flagship.cc/en/blogs/columns/slack-mcp-server-official-release))*.)
For 7AM this is mostly irrelevant: the Slack surface is an interactive *bot*
(approval cards, buttons, modals, event subscriptions), which is Bolt-SDK territory as
the concept already specifies — MCP would only matter if Sevi needed to *read* Slack
content as a data source.

### What MCP buys vs. hand-written connectors — and where the line is

MCP standardizes how an LLM discovers and calls external tools: one protocol, vendor-
maintained schemas, and for the hosted servers (Meta, Klaviyo) vendor-managed OAuth
instead of your own token plumbing. That is a real saving on the **read/diagnose
side**: Watchdog/Tracker/Critic-style questions ("query GAQL", "campaign report") come
for free and stay current as the vendor evolves the API.

For **deterministic writes it buys nothing and costs control**: an MCP tool call is
still an LLM-chosen call — the model picks the tool and the arguments. 7AM's core
safety property is that the guardrail engine, not the model, executes money-moving
actions after checking caps/cooldowns/whitelist against DB state. That engine needs a
typed, idempotent, audit-logged connector function (`set_budget(campaign_id, amount)`)
it calls *itself* after approval — not a tool surface handed to the model. The right
pattern: LLM emits a **proposal** (structured output / strict tool call), guardrail
engine validates, human approves in Slack, executor calls the platform API directly.
Official read-only MCP (Google's explicitly, by design) fits the left side of that
pipeline; hand-written connectors own the right side.

## 4. Orchestration/scheduling backbone

**Celery + Redis (concept default).** Celery is a distributed task queue — "Task
queues are used as a mechanism to distribute work across threads or machines" — with
feature-complete RabbitMQ and Redis transports, cron-style scheduling via celery beat,
and automatic client/worker retry on connection loss; current 5.6.x supports Python
3.9–3.13. ([github.com/celery/celery](https://github.com/celery/celery)) Retries are
**per-task** (you declare `retry`/`acks_late` semantics yourself); there is no durable
multi-step workflow state and no built-in audit trail — for 7AM both would live in
Postgres anyway (`proposals`, `actions_log`, `agent_runs`), which makes Celery's
thinness acceptable: tasks stay small and idempotent, Postgres is the source of truth,
and human-in-the-loop is not a "paused task" but a DB state (`proposal: pending`) that
a Slack button flips, after which the executor task is enqueued. *Flag:
docs.celeryq.dev was blocked by the proxy; the well-known Redis-broker caveat
(visibility_timeout redelivery of long-running tasks) could not be re-verified against
current docs and should be checked when configuring.*

**Temporal.** "Temporal is a durable execution platform ... The Temporal server
executes units of application logic called Workflows in a resilient manner that
automatically handles intermittent failures, and retries failed operations."
([github.com/temporalio/temporal](https://github.com/temporalio/temporal)) Workflow
state and full event history are persisted server-side, which gives replayable
audit and lets a workflow *wait days* for a human signal — the cleanest native model
for proposal→approval→execute (a Signal/Update delivers the Slack approval into a
running workflow). The cost is operational: self-hosting means running the Temporal
server + a database (PostgreSQL/MySQL/Cassandra in the official compose setups) +
UI, alongside your app
([github.com/temporalio/docker-compose](https://github.com/temporalio/docker-compose),
now superseded by
[samples-server/compose](https://github.com/temporalio/samples-server/tree/main/compose)).
For a solo-built 1–3-shop system that already commits to Postgres-as-truth, Temporal
duplicates the state layer; it becomes attractive at SaaS scale with many tenants and
long multi-step campaigns. *Flag: docs.temporal.io blocked by proxy; signals/schedules
specifics from the project's repos and prior knowledge — re-verify wording before
citing externally.*

**Plain cron + queue.** cron (or systemd timers) firing Python entrypoints that write
jobs into a Postgres table worked by a small worker loop (e.g. `SELECT ... FOR UPDATE
SKIP LOCKED`). Zero new infrastructure beyond what 7AM has; retries, backoff, and
audit are all DIY — but since 7AM's audit and state model must live in Postgres
regardless, this is less absurd than it sounds. Its real weakness vs. Celery is
concurrency management and the ecosystem (rate-limit handling, monitoring), for a
saving that is small once Redis is already in the stack. (General knowledge; no single
primary source.)

## 5. Recommended reference architecture

**Recommendation: the concept doc's plan is sound — plain Anthropic Messages API
inside your own FastAPI/Celery/Postgres orchestration; do not build the production
pipeline on the Claude Agent SDK.** The LLM sits in exactly three places; everything
else is deterministic code.

```
cron (celery beat) ── hourly/daily triggers
        │
        ▼
Celery workers (per-role tasks: watchdog, buyer, accountant, tracker, critic, ...)
        │
        ├─ Connectors (deterministic, typed, idempotent):
        │    Meta Marketing API · Google Ads API (writes) · Klaviyo REST ·
        │    Shopify Admin GraphQL · Slack Bolt
        ├─ Read-side MCP (optional, via Messages-API MCP connector):
        │    Google Ads MCP (official, read-only) · Meta Ads MCP · Klaviyo MCP
        │
        ▼
Postgres = source of truth: metrics, kpi_profiles, proposals, approvals,
           actions_log (audit), agent_runs, llm_usage
        │
        ├─ [LLM 1] Diagnosis & proposal drafting: Messages API, Sonnet 5
        │    (Haiku 4.5 for hourly Watchdog triage), structured outputs
        │    (output_config.format + strict tools), cached playbook prefix (1h TTL)
        ├─ [LLM 2] Nightly analyses & Critic scoring: Batch API (50% off)
        └─ [LLM 3] Sevi's voice & "ask the Accountant" chat: Tool Runner loop
             (or Agent SDK, if its harness earns its keep for this one surface)
        │
        ▼
Guardrail engine (pure Python, no LLM): whitelist, caps, ±25% step, cooldowns,
change budget, kill switch — validates every proposal AND re-validates at execute time
        │
        ▼
Slack Bolt: approval cards → approval row in Postgres → executor task → platform API
```

Sevi's "seven agents" are role-scoped prompt/playbook/schedule bundles over this one
pipeline — not seven independent agentic loops. The Accountant's veto is a
deterministic pre-check in the guardrail engine, with an LLM only for phrasing the
explanation.

### Key decisions to make first

1. **Where the approval state machine lives** — decide now that `proposal → approved →
   executed/failed/expired` is a Postgres state machine with idempotent executors and
   guardrail re-validation *at execution time* (approvals can be hours old), and that
   Celery carries only stateless "advance this row" jobs. This decision makes the
   Celery-vs-Temporal question low-stakes and reversible; skipping it makes every
   backbone fragile.
2. **Model tier per role + hard LLM budget enforcement** — pick the default
   (recommended: Haiku 4.5 for hourly triage, Sonnet 5 for daily
   diagnosis/briefs/batch, Opus 5 for weekly strategy), wire `llm_usage` cost caps
   with a daily cut-off from day one, and freeze prompt prefixes so the 0.1x cache
   rate actually materializes — this is what keeps 1–3 shops inside 50–200 EUR/month.
3. **The MCP boundary** — commit that MCP (Google's official read-only server first)
   is a *read/diagnose* convenience only, and every write goes through hand-written,
   whitelisted, audit-logged connector functions invoked by the guardrail engine.
   This also determines what the Buyer's "tools" are: proposal schemas, not API access.
