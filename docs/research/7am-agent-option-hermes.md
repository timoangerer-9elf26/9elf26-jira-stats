# 7AM agent option: a "Hermes agent setup"

Research date: 2026-08-26. Scope: the user floated a "Hermes agent setup" as one option for the
7AM/Sevi agent component (7 specialist roles orchestrated by one voice, cron-driven autonomous runs,
deterministic guardrails outside the LLM, Slack human-in-the-loop, audit logging, EU self-hosting).
Three questions: (1) what "Hermes" actually refers to in the 2026 agent ecosystem, established from
primary sources (the projects' own repos/docs, not blog write-ups); (2) whether any Hermes option
fits 7AM or would fight it; (3) if "Hermes" is partly a model family, what running an open-weight
Hermes model self-hosted in the EU would mean vs. the Anthropic API.

Everything below is cited to the owning repo/docs where possible. Several relevant pages
(hermes.nousresearch.com, polaranalytics.com, huggingface.co, arxiv.org) are blocked by this
session's egress proxy; claims that could only be confirmed via search snippets of those pages are
**flagged** as such. The web is also full of SEO-spam "Hermes Agent 2026 guide" blogs — none of
those were used as evidence.

---

## 0. TL;DR

"Hermes" in 2026 means three related things: (a) **`NousResearch/hermes-agent`**, an MIT-licensed,
extremely popular open-source generalist agent harness (the most likely referent of "Hermes agent
setup"); (b) the **Hermes open-weight model family** (Hermes 4 / 4.3) from the same lab; and (c)
**"Polar/Hermes"** — already named as a *competitor* in the 7AM Konzept v3.1 (p. 37) — which turns
out to be Polar Analytics' commercial productization of exactly that open-source hermes-agent for
e-commerce. The framework is a genuinely capable harness (native Anthropic/Claude support, built-in
cron, Slack approval buttons, deterministic pre-tool-call blocking hooks) but it is a fast-moving
0.x *personal/single-operator* agent, not a multi-tenant product backend. Verdict in §5: a real
contender for an internal pilot / v0, not a safe foundation for the customer-facing product without
accepting significant churn and building the domain layer yourself anyway.

---

## 1. What "Hermes" refers to

1. **`NousResearch/hermes-agent`** — "The agent that grows with you." Open-source agent
   framework/harness by Nous Research. Created 2025-07-22; ~236,770 stars, ~47,900 forks, ~36,000
   open issues+PRs as of 2026-08-26 (GitHub API). Topics include `anthropic`, `claude`, `openai`.
   ([github.com/NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent))
   This is by far the most plausible referent of "Hermes agent setup" — in 2026 it is one of the
   most-starred agent projects on GitHub, and dozens of unrelated tools list "Hermes Agent"
   alongside Claude Code/Codex/OpenClaw as a peer harness (GitHub topic search, 2026-08-26).
   Nous Research was reported in funding talks at a $1.5B valuation on the back of it
   ([TechCrunch, 2026-07-13](https://techcrunch.com/2026/07/13/hermes-agent-maker-nous-research-in-talks-for-new-funding-at-1-5b-valuation/) — secondary source, flagged).

2. **The Hermes model family** — open-weight instruction/reasoning models from the same lab:
   Hermes 4 (14B / 70B / 405B, released Aug 2025, technical report
   [arXiv:2508.18255](https://arxiv.org/abs/2508.18255)) and later Hermes 4.3. Details in §4.

3. **"Polar/Hermes"** — Konzept v3.1 p. 37 lists "Polar/Hermes" among "Weitere KI-Agenten am
   Markt". Polar Analytics markets "Hermes: Autonomous AI Agents for Ecommerce"
   ([polaranalytics.com/hermes-ecommerce-ai-agent](https://www.polaranalytics.com/hermes-ecommerce-ai-agent))
   and describes it as **the open-source Nous Research hermes-agent run against Polar's e-commerce
   data layer** — agents that "monitor, brief, investigate and propose the next move, and act only
   when you approve", across connected tools "from Amazon Seller Central to Meta Ads", MIT-licensed
   by Nous Research, with Polar recommending white-glove deployment.
   **Flagged:** polaranalytics.com is egress-blocked here; this characterization comes from search
   snippets of Polar's own pages and should be re-verified, but it is consistent across their
   /hermes-ecommerce-ai-agent and /ai pages.

   This is a notable finding on its own: a direct competitor named in the Konzept is *already*
   shipping "hermes-agent + e-commerce data + approval-gated actions" — i.e., roughly the
   architecture the user is asking about. That both validates the pattern and weakens it as a
   differentiator.

There is no other credible major "Hermes" agent framework; everything else found under the name is
either a wrapper/companion for the Nous agent (dashboards, switchers, memory add-ons) or unrelated
small projects.

---

## 2. `NousResearch/hermes-agent`: the facts from its own repo

### 2.1 What it is

A Python agent harness with a CLI/TUI, a desktop app, and a long-running **gateway** process that
fronts messaging platforms and runs scheduled jobs. Its signature feature set is a self-improving
loop: "creates skills from experience, improves them during use, nudges itself to persist knowledge,
searches its own past conversations, and builds a deepening model of who you are across sessions"
([README](https://github.com/NousResearch/hermes-agent/blob/main/README.md)). It is framed
throughout as a *personal* agent for one operator (memory of "who you are", DM pairing, personality
files `SOUL.md`), not as a product backend.

### 2.2 License and maintenance

- **MIT License**, copyright 2025 Nous Research
  ([LICENSE](https://github.com/NousResearch/hermes-agent/blob/main/LICENSE)).
- Very actively maintained but **pre-1.0 and churning hard**: v0.20.5 released 2026-08-19; five
  patch releases in the first three weeks of August 2026; the v0.20.5 notes say it "rolls up ~323
  PRs merged since v0.20.4"
  ([releases](https://github.com/NousResearch/hermes-agent/releases)). That pace means features and
  config shapes move under you; the repo even ships migration guides *from* competing harnesses
  and its own frequent config migrations.

### 2.3 Model support — not tied to Hermes models

Model-agnostic, and **Anthropic/Claude is a first-class native provider**, not just via OpenRouter:

- "Anthropic is not just 'via OpenRouter' anymore. When provider resolution selects `anthropic`,
  Hermes uses `api_mode = anthropic_messages`, the native Anthropic Messages API"
  ([developer-guide/provider-runtime.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/provider-runtime.md)).
- `export ANTHROPIC_API_KEY=*** && hermes chat --provider anthropic --model claude-sonnet-4-6`;
  `--provider claude` is an alias
  ([integrations/providers.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/integrations/providers.md)).
- Built-in providers ship as plugins ("OpenRouter, Anthropic, GMI, DeepSeek, Nvidia, …"), and
  third parties can add model-provider plugins for OpenAI-compatible, Anthropic-Messages, or
  Bedrock-native endpoints without repo changes
  ([developer-guide/model-provider-plugin.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/model-provider-plugin.md)).

So a Hermes-agent setup does **not** force Hermes models; it can run entirely on the Anthropic API.

### 2.4 Extensibility

- **Tools**: 40+ built-in tools, toolset system, MCP client support incl. elicitation routed
  through the approval surface
  ([features/mcp.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/mcp.md)).
  Custom capability = custom MCP servers or plugins. Nothing Meta-Ads/Google-Ads/Klaviyo-specific
  ships in the box.
- **Scheduled runs**: first-class cron, executed by the gateway daemon (60s tick). Jobs defined via
  chat, CLI, or a `cronjob` tool; formats: relative delays, intervals, cron expressions, ISO
  timestamps. **Each firing runs a fresh, isolated agent session** (optionally with an injected
  skill, per-job toolset restriction, or "no-agent mode" pure-script execution). Delivery targets
  include `slack`, `email`, `origin`, multiple targets. Execution history persists in
  `~/.hermes/cron/executions.db`; misfire catch-up with a grace window; repeated-failure nudges and
  incident dedup ([features/cron.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/cron.md),
  [developer-guide/cron-internals.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/cron-internals.md)).
  This maps well onto Watchdog-style hourly runs.
- **Multi-agent**: `delegate_task` spawns isolated subagents (parallel batches, orchestrator role,
  steerable mid-flight), **but** "Subagents start with a completely fresh conversation" and the
  docs offer no way to give a subagent its own named persona/prompt/toolset — they inherit the
  parent's toolset; delegation model/provider is a single global config
  ([features/delegation.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/delegation.md)).
  Named specialists ("Buyer", "Accountant"…) would instead map to **profiles**: "Run multiple
  independent Hermes agents on the same machine — each with its own config, API keys, memory,
  sessions, skills, and gateway state", each with its own `SOUL.md` personality and toolsets;
  multi-profile gateways are documented
  ([user-guide/profiles.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/profiles.md),
  [user-guide/multi-profile-gateways.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/multi-profile-gateways.md)).
  That gives 7 isolated agents, but "one voice (Sevi) fronting 7 specialists" is not a built-in
  pattern — you'd compose it (one front profile + subagent delegation, or cron jobs per specialist
  delivering into one Slack channel).
- **Hooks**: four hook systems (Python plugin hooks, shell hooks in `config.yaml`, gateway hooks,
  signed outbound webhooks). `pre_tool_call` hooks can **deterministically block** a tool call
  ("`block` requires a non-empty `message` and short-circuits the tool"; shell hook exit code 2
  blocks), and audit logging of every tool call is an explicitly documented hook use case
  ([features/hooks.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/hooks.md)).

### 2.5 Slack + human-in-the-loop

Native Slack integration over Socket Mode (no public endpoint needed). Dangerous-command /
`execute_code` approvals render as **interactive Block Kit buttons** in Slack, with text-command
fallback (`!approve`/`!deny`); access control via `SLACK_ALLOWED_USERS` member-ID allowlist,
default-deny ([messaging/slack.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/slack.md)).
This is exactly the 7AM Slack-HITL shape — for *shell/code* approvals. Approval prompts for
*arbitrary custom business actions* (e.g. "raise budget on campaign X?") would ride the same
surface via MCP elicitation or a clarify-style prompt, which the docs support but don't showcase
for non-exec actions.

### 2.6 Guardrails and security model

([user-guide/security.md](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/security.md))

- Approval modes: `smart` (default — an **auxiliary LLM assesses risk**), `manual` (always prompt),
  `off`. For 7AM's "guardrails outside the LLM" requirement, note the default is LLM-judged; you'd
  run `manual` plus deterministic layers.
- Deterministic layers exist: a hardline blocklist of irreversible patterns that "cannot be
  overridden by `--yolo` or config"; user-defined `approvals.deny` fnmatch globs enforced before
  any approval logic; `command_allowlist` in `config.yaml`; per-session approval memory; plus the
  `pre_tool_call` block hooks (§2.4). Pre-exec Tirith scanning and context-file
  prompt-injection scanning are built in.
- **But**: these guardrails are about *shell commands and tools generically*. 7AM's actual
  guardrails — budget caps in €, action whitelists per campaign, rate limits per ad account,
  Accountant veto — are domain logic that hermes-agent knows nothing about. They'd live inside
  your custom Meta/Google/Klaviyo tools (or pre_tool_call hooks inspecting their args) regardless
  of harness choice. Hermes gives you good attachment points, not the guardrails themselves.
- **Audit logging**: session state in `~/.hermes/state.db`, logs in `~/.hermes/logs/`, and
  hook-based tool-call audit logging is documented — but there is **no first-class, tamper-evident
  audit-log feature** in the user docs. For 7AM's "every action logged" requirement you'd build it
  on the hook/webhook layer (which is adequate but DIY).

### 2.7 Deployment

Installed via one-liner installers; the gateway is a persistent self-hosted process — a plain EU
VPS/container works, and Socket-Mode Slack means no inbound exposure. Code-execution sandboxes:
local, SSH, **Docker** (hardened flags, dropped capabilities, resource limits), Modal, Daytona,
Vercel Sandbox ([README](https://github.com/NousResearch/hermes-agent/blob/main/README.md),
security.md). EU data residency of the *harness* is therefore fine; the LLM calls go to whatever
provider you configure (Anthropic EU-region options, or a self-hosted model, §4). Note it is a
single-host, filesystem-state architecture (`~/.hermes/*` SQLite/YAML) — fine for one operator or
one internal instance, with no documented multi-tenant or HA story.

---

## 3. Fit assessment against the 7AM requirements

| 7AM requirement | hermes-agent | Notes |
|---|---|---|
| Cron-driven autonomous runs (Watchdog hourly) | **Good** | Built-in gateway cron, fresh sessions, misfire catch-up, failure nudges (§2.4) |
| 7 named specialists, one voice | **Partial / composed** | Profiles = independent agents with own soul/toolsets; subagents are anonymous clones; "one voice fronting specialists" must be assembled |
| Deterministic guardrails outside LLM | **Partial** | Deny globs, hardline blocklist, blocking pre_tool_call hooks are deterministic; default approval mode is LLM-judged; all *domain* guardrails (budget caps, whitelists, veto) are yours to build either way |
| Slack HITL approvals | **Good** | Native Socket-Mode Slack with Block Kit approve/deny buttons, default-deny user allowlist |
| Audit log of every action | **DIY on good hooks** | pre/post_tool_call hooks + signed webhooks; no first-class audit-log product feature |
| Meta/Google/Klaviyo execution | **Absent** | Custom tools/MCP servers in every scenario; harness choice doesn't change this workload |
| EU self-hosting | **Good (harness)** | Self-hosted Python gateway; model calls go wherever you point them |
| Product/multi-tenant backend | **Poor** | Single-operator design, `~/.hermes` filesystem state, no tenant isolation, 0.x churn (~323 PRs per patch release) |

Honest framing: hermes-agent is **not** a coding-assistant that would fight the use case (unlike,
say, repurposing Claude Code) — autonomous scheduled ops with chat-platform HITL is squarely what
it is built for, which is precisely why Polar built their e-commerce agents on it (§1.3). What it
is *not* is a stable product substrate: it is a personal agent moving at hundreds of PRs per week,
MIT-licensed but effectively steered by one lab, with the differentiating 7AM layer (economics
engine, action whitelists, Meta/Google/Klaviyo connectors, multi-client operation) left entirely
to you. And strategically: building on hermes-agent means building on the same public foundation
as a named competitor.

---

## 4. If "Hermes" meant the models: self-hosted Hermes vs. Anthropic API

The Hermes *model family* is a separate thing, and hermes-agent does not require it (§2.3).

- **What exists**: Hermes 4 — 14B (Qwen3 base, Apache-2.0), 70B and 405B (Llama-3.1 base, Llama 3
  community license), open weights, hybrid reasoning, Hermes `<tool_call>` format; technical
  report [arXiv:2508.18255](https://arxiv.org/abs/2508.18255). Later Hermes 4.3 (reported ~36B,
  Seed base, trained on Nous's Psyche network). **Flagged:** huggingface.co and arxiv.org are
  egress-blocked in this session; sizes/licenses/bases come from HF model cards and the report as
  surfaced in search results and were not independently re-read — re-verify before relying on the
  license split.
- **Quality for German user-facing text**: no primary-source German benchmark for Hermes 4 was
  found; the technical report's headline gains are math/code/STEM/reasoning, not multilingual
  quality. Llama-3.1 officially supports German, but "supported" is far below the fluency bar of a
  frontier API model for polished customer-facing German copy (Sevi's entire UX). **This claim is
  an assessment, not a measured result — flagged.** Risk is asymmetric: 7AM's product *is* its
  German-language voice.
- **Tool-use reliability**: Hermes 4 emits structured `<tool_call>` blocks and is agent-oriented,
  but for a system where a malformed or mis-chosen call touches real ad budgets, frontier-model
  tool-use reliability (plus your deterministic whitelist layer) is the conservative choice. No
  head-to-head primary data found — flagged as judgment.
- **Hosting cost/complexity**: EU self-hosting means GPU serving — 14B fits one 24–48 GB GPU;
  70B realistically 2×80 GB-class GPUs (or FP8 on one); 405B is a multi-node project. That is
  thousands of euros/month of always-on EU GPU plus MLOps you don't have today, vs. zero infra for
  the Anthropic API (which also has EU processing options to check separately). **Figures are
  order-of-magnitude estimates, not quotes — flagged.**
- Where self-hosted Hermes *would* make sense: a hard data-sovereignty requirement that excludes
  any US-model API, or cost pressure at very high token volume on low-stakes internal steps
  (e.g. Tracker data-health checks), with Claude retained for user-facing text and Buyer-grade
  decisions. Nothing in the 7AM concept currently forces that.

---

## 5. Verdict

**A Hermes-agent setup is a real contender for 7AM's v0 / internal pilot, and a weak choice as the
long-term product backbone.**

- It genuinely covers, out of the box: scheduled autonomous runs, Slack approval buttons,
  default-deny access control, deterministic tool-blocking hooks, model freedom including native
  Claude, MIT license, EU self-hosting of the harness. For "run Sevi for ourselves/one pilot
  client next month", it is arguably the fastest credible path, and Polar/Hermes proves the
  pattern works for e-commerce ops.
- Conditions for using it: pin versions hard (0.x, ~323 PRs per patch release); set
  `approvals.mode` away from the LLM-judged default and put every Meta/Google/Klaviyo action
  behind your own whitelist-enforcing tools + `pre_tool_call` hooks; build the audit trail on the
  hook/webhook layer; accept single-tenant-per-instance operation (one profile set or one gateway
  per client).
- It does not solve the parts that make 7AM 7AM: the economics engine, veto logic, budget caps,
  the connectors, multi-client operation, and the German-language product voice. Those are the
  same build effort under any harness — which is the strongest argument that the harness should be
  a thin, replaceable layer (hermes-agent today, possibly own orchestration later) rather than a
  foundation to marry.
- Using Hermes *models* instead of the Anthropic API is not recommended for v1: unproven German
  fluency for user-facing text, unproven tool-call reliability at money-moving stakes, and real
  GPU/MLOps cost — with no current requirement forcing it. Revisit only if data-sovereignty
  constraints harden.
