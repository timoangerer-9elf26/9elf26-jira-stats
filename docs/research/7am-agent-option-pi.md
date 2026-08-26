# 7AM Agent Option: Pi (badlogic's minimal agent harness) configured as Sevi

Research date: 2026-08-26. Scope: evaluate "just a pi agent configured to this" as an
implementation option for 7AM's agent side — the Sevi crew of seven specialist roles
(Watchdog, Buyer, Accountant, Tracker, Critic, Producer, Mailer) behind one voice, with
scheduled autonomous runs, deterministic guardrails outside the LLM, Slack
human-in-the-loop approvals, a full audit log, and EU self-hosting (Docker on Hetzner)
as required by the 7AM concept (konzept v3.1, Teil X / Kapitel 1.8).

Method: primary sources only — a fresh shallow clone of the pi monorepo
(`badlogic/pi-mono`, HEAD `e868230`, committed 2026-08-26), its in-repo docs, package
manifests, and source; plus the rendered GitHub page and npm for metadata. Where a
number came only through a rendered page or a search snippet, it is flagged as such.
Mario Zechner's design-rationale blog posts (`mariozechner.at`) were **not reachable**
from this environment (egress-blocked); philosophy claims below are cited from the
repo's own README, which links those posts.

---

## 1. What pi actually is

Pi is a **minimal terminal coding harness** and, underneath it, a general **agent
toolkit**, created by Mario Zechner (GitHub `badlogic`, of libGDX fame) and now
developed under his company org `earendil-works` (the repo `badlogic/pi-mono`
resolves to the `earendil-works` org page; npm packages are scoped
`@earendil-works/*`). The coding-agent README opens:

> "Pi is a minimal terminal coding harness. Adapt pi to your workflows, not the other
> way around, without having to fork and modify pi internals. Extend it with TypeScript
> Extensions, Skills, Prompt Templates, and Themes." (`packages/coding-agent/README.md`)

Key facts, all from the repo at HEAD `e868230` unless noted:

- **License:** MIT, "Copyright (c) 2025 Mario Zechner" (`LICENSE`).
- **Language/runtime:** TypeScript monorepo, npm workspaces; the coding agent requires
  Node `>=22.19.0` (`packages/coding-agent/package.json`). Standalone Bun-compiled
  binaries are shipped per release (`README.md`, "Building standalone binaries").
- **Version/maintenance:** `@earendil-works/pi-coding-agent` 0.84.3, released
  2026-08-24 (`packages/coding-agent/CHANGELOG.md`); latest commit on `main` dated
  2026-08-26 (the day of this research). Actively maintained at high velocity — the
  changelog shows a steady stream of features, fixes, and **breaking changes** within
  0.x.
- **Popularity:** the GitHub page showed **~97.7k stars, ~12.1k forks, 80 open issues,
  5,815 commits** when fetched 2026-08-26 (read via a rendered-page summary —
  *numbers unverified against the API*, which was not directly reachable here; a
  secondary source put it at ~62k stars in June 2026, so the order of magnitude is
  consistent). Either way: one of the most popular open-source coding agents.
- **Repo description:** "AI agent toolkit: unified LLM API, agent loop, TUI, coding
  agent CLI" (GitHub page, 2026-08-26).
- **Governance quirk:** "New issues and PRs from new contributors are auto-closed by
  default. Maintainers review auto-closed issues daily." (`README.md`) — i.e. a
  deliberately narrow contribution funnel; you consume pi, you don't easily steer it.

### Monorepo layout (packages/, at HEAD)

| Package | Role | Source |
|---|---|---|
| `@earendil-works/pi-ai` | "Unified LLM API with provider collections, automatic auth resolution, token and cost tracking, and simple context persistence and hand-off to other models mid-session." Tool-calling models only. | `packages/ai/README.md` |
| `@earendil-works/pi-agent-core` | "Stateful agent with tool execution and event streaming. Built on pi-ai." The raw loop: system prompt + model + tools + messages. | `packages/agent/README.md` |
| `@earendil-works/pi-coding-agent` | The interactive CLI *and* the SDK (`createAgentSession`, extensions, skills, sessions, compaction). | `packages/coding-agent/README.md` |
| `@earendil-works/pi-tui` | Terminal UI library. | `README.md` |
| `@earendil-works/pi-telemetry` | Vendor-neutral telemetry contracts. | `README.md` |
| `pi-protocol`, `pi-client`, `pi-server`, `session-backends/` | **Experimental** session-server stack (CBOR wire protocol, Unix-socket server, SQLite session backend). `pi-server` README: "Experimental. This package is under active development and may change or be removed without notice." | `packages/server/README.md`, `packages/protocol/README.md` |

### Core loop and defaults

- By default the model gets exactly **four tools: `read`, `write`, `edit`, `bash`**
  (`packages/coding-agent/README.md`, Quick Start). Additional built-ins available by
  name: `grep`, `find`, `ls`, `powershell` (`docs/sdk.md`, "Tools").
- The default system prompt is generated in
  `packages/coding-agent/src/core/system-prompt.ts` and begins: "You are an expert
  coding assistant operating inside pi, a coding agent harness…" — i.e. the *default
  persona* is a coding agent, but the prompt is assembled from the enabled tools,
  context files (`AGENTS.md`/`CLAUDE.md` walked up from cwd), and skills, and is
  **fully replaceable** (see §3).
- **Sessions** are JSONL files with a tree structure (`id`/`parentId` per entry),
  supporting in-place branching, `/fork`, `/clone`, resume by id, and export/import
  (`README.md`, "Sessions"; `docs/session-format.md`). An SQLite session backend
  exists as a separate package (`packages/agent/README.md`).
- **Compaction** (manual and automatic on context pressure) summarizes older messages;
  full history stays in the JSONL (`README.md`, "Compaction"; `docs/compaction.md`).
- **Cost/token tracking** is built in at the pi-ai layer and surfaced per session
  (`packages/ai/README.md`; README footer description) — directly useful for 7AM's
  required LLM cost self-monitoring (`llm_usage`, Tages-Cap; konzept 10.5).

### Philosophy: deliberate omissions

The README's Philosophy section is explicit that pi ships **without** several things
other harnesses bake in, each with the same answer — build it as an extension or
install a package (`packages/coding-agent/README.md`, "Philosophy"):

- **"No MCP."** Build CLI tools with READMEs (skills), or add MCP via an extension.
- **"No sub-agents."** Spawn pi instances yourself or build orchestration in an
  extension.
- **"No permission popups."** "Run in a container, or build your own confirmation flow
  with extensions inline with your environment and security requirements."
- **"No plan mode." / "No built-in to-dos." / "No background bash."**

And on security, the root README is unambiguous:

> "Pi does not include a built-in permission system for restricting filesystem,
> process, network, or credential access. By default, it runs with the permissions of
> the user and process that launched it. If you need stronger boundaries, containerize
> or sandbox Pi." (`README.md`, "Permissions & Containerization"; three documented
> patterns: Gondolin micro-VM, plain Docker, OpenShell — `docs/containerization.md`)

This matters for 7AM: pi takes the same stance the 7AM concept does — **guardrails are
the application's job, not the LLM harness's** — but it means pi contributes *nothing*
toward the guardrail engine; it only gives you clean interception points.

## 2. Model and provider support

`pi-ai` is a unified multi-provider API. Provider list from
`packages/coding-agent/README.md` ("Providers & Models"):

- **Subscriptions (OAuth):** Anthropic Claude Pro/Max, OpenAI ChatGPT Plus/Pro
  (Codex), GitHub Copilot.
- **API keys:** **Anthropic**, OpenAI, Azure OpenAI, Google Gemini, Google Vertex,
  Amazon Bedrock, DeepSeek, Mistral, Groq, Cerebras, xAI, OpenRouter, Fireworks,
  Together, Hugging Face, Cloudflare, and more (~30 entries), plus llama.cpp local
  models and any custom provider speaking the OpenAI/Anthropic/Google wire APIs via
  `~/.pi/agent/models.json` or a provider extension (`docs/models.md`,
  `docs/custom-provider.md`).

So the 7AM concept's "KI-Schicht: Anthropic API" (konzept 10.2) is a first-class
citizen — including streaming, tool calling, thinking levels, and cost tracking — with
free optionality to route cheap roles (e.g. Watchdog triage) to cheaper providers.
Note for a commercial product: Anthropic *subscription* auth is for interactive
personal use; a server product would use API keys — which pi supports plainly via
`ANTHROPIC_API_KEY` (`README.md`, Quick Start).

## 3. The extension/configuration surface

This is the load-bearing question for "configure pi into Sevi", and it is pi's
strongest area. Four mechanisms (`packages/coding-agent/README.md`, "Customization"):

1. **Extensions** — TypeScript modules with an `ExtensionAPI`:
   - `pi.registerTool({name, description, parameters (TypeBox), execute})` — custom
     tools the model can call; built-ins can be replaced entirely
     (`docs/extensions.md`).
   - **Event interception across the whole lifecycle**, including `tool_call`
     handlers that can `return { block: true, reason }` — a deterministic gate that
     runs *before* any tool executes (`docs/extensions.md`, Quick Start example blocks
     `rm -rf`; lifecycle diagram shows `tool_call (can block)`), plus
     `before_agent_start` (modify system prompt / inject messages), `before_provider_request`,
     custom compaction, session-switch hooks, and custom UI.
   - Extension state can persist into the session via `pi.appendEntry()`.
2. **Skills** — on-demand capability packages following the Agent Skills standard
   (agentskills.io): `SKILL.md` files auto-loaded or invoked as `/skill:name`
   (`docs/skills.md`). This is exactly the shape of 7AM's **playbooks**.
3. **Prompt templates** — parameterized Markdown prompts (`docs/prompt-templates.md`).
4. **System prompt replacement** — `.pi/SYSTEM.md` (project) or
   `~/.pi/agent/SYSTEM.md` replaces the default entirely; `APPEND_SYSTEM.md` appends
   (`README.md`, "System Prompt"). In the SDK,
   `new DefaultResourceLoader({ systemPromptOverride: () => "..." })` does the same
   programmatically (`docs/sdk.md`).

**Pi packages** bundle extensions/skills/prompts and install via
`pi install npm:...` or `git:...` — a distribution channel for a "Sevi package"
(`README.md`, "Pi Packages"). Security note from the same section: "Pi packages run
with full system access… Review source code before installing."

### Headless / programmatic operation

Pi "runs in four modes: interactive, print or JSON, RPC for process integration, and
an SDK for embedding in your own apps" (`packages/coding-agent/README.md`):

- `pi -p "prompt"` — print mode, reads stdin, exits (cron-able).
- `pi --mode json` — all agent events as JSON lines (`docs/json.md`).
- `pi --mode rpc` — JSONL-over-stdio protocol for embedding from any language
  (`docs/rpc.md`) — relevant if 7AM stays Python: a Python orchestrator can drive a pi
  subprocess over RPC.
- **SDK** (`docs/sdk.md`) — the serious path: `createAgentSession({...})` with
  `model`, `systemPromptOverride`, `tools: [...]` (allowlist), `noTools: "all" |
  "builtin"`, `excludeTools`, `customTools: [defineTool(...)]`,
  `SessionManager.inMemory()` or file-backed sessions, in-memory settings, event
  subscription, `steer()`/`followUp()`, compaction control. The SDK examples include a
  "minimal assistant" with a non-coding persona, no default tools, and only custom
  tools — demonstrating that **the coding-agent identity is defaults, not
  architecture**. One level down, `pi-agent-core`'s `Agent` class is just
  `{systemPrompt, model, tools, messages}` + a stream function
  (`packages/agent/README.md`).

Non-interactive modes never show trust prompts; project-resource trust is governed by
`defaultProjectTrust` and `--approve/--no-approve` flags (`README.md`, "Project
Trust") — a real consideration for unattended cron runs.

### First-party precedent for non-coding, chat-fronted use

`earendil-works/pi-chat` is "a pi extension that bridges **Discord and Telegram**
channels to a sandboxed pi session. Each connected channel gets its own Gondolin
micro-VM with persistent workspace, shared storage, memory, and skills" (pi-chat
README, fetched 2026-08-26; MIT per its README — *the GitHub page was reported as
showing Apache-2.0 by one fetch; discrepancy unresolved, flagged*). No Slack support
today. So the "pi behind a chat surface, with memory and skills, in a VM per tenant"
pattern exists first-party — but for Slack, 7AM would build its own bridge (Bolt →
pi SDK/RPC), using pi-chat only as a design reference.

## 4. Configuring pi into the Sevi crew: what it covers, what remains

Mapping 7AM's agent-side requirements (konzept Teil X 10.1–10.2, Kapitel 1.8) onto pi:

**What pi genuinely provides out of the box**

- The agent loop with tool calling, streaming, retries, validation (pi-agent-core).
- Anthropic (and 30+ other) provider plumbing incl. token/cost accounting — feeds the
  `llm_usage` requirement.
- A typed custom-tool mechanism → Meta Marketing API, Google Ads, Klaviyo, Shopify
  actions become `defineTool()` implementations; per-role tool allowlists via
  `tools: [...]` per session — **the Buyer's action whitelist maps directly onto the
  SDK tool allowlist**, enforced by the harness (the model can't call what isn't
  registered).
- A `tool_call → {block}` interception point where a deterministic guardrail check
  (budget caps, Regel S / Accountant veto, rate limits) can be *invoked* before any
  side effect.
- Playbooks as skills/prompt templates; per-role personas via `systemPromptOverride`.
- Session persistence (JSONL trees or SQLite backend) — a usable *transcript* record
  per agent run (`agent_runs` raw material), though not an audit log in the
  compliance sense.
- MIT license, plain Node process — trivially Dockerized on Hetzner, fully
  self-hosted; telemetry/update pings are documented and disable-able
  (`PI_TELEMETRY=0`, `PI_OFFLINE=1`, `README.md` "Telemetry and update checks").

**What 7AM must still build around it (all of it deterministic application code)**

1. **Scheduler** — pi has no cron/scheduling of any kind. Hourly Watchdog / daily
   syncs need cron, systemd timers, or the concept's Celery Beat, invoking `pi -p` /
   the SDK per run.
2. **Guardrail engine** — the hard caps, whitelist semantics, rate limits, and the
   Accountant's economics/veto are your code. Pi only offers the hook; and critically,
   the *safer* pattern is to enforce guardrails **inside the tool implementations /
   a separate execution service**, not only in a `tool_call` handler — then the LLM
   layer can be swapped without touching the safety layer.
3. **Proposal/approval state machine + Slack surface** — pi has no approval queue and
   no Slack anything. The Freigabe-Karten flow (proposals, approvals, execution,
   undo) and the Slack Bolt bot are a full subsystem.
4. **State & audit DB** — Postgres tables (`proposals`, `approvals`, `actions_log`,
   `metrics_*`, `kpi_profiles`, …) and the ingestion/sync layer are untouched by pi.
5. **Orchestration of the seven roles** — pi has *deliberately* no sub-agents. "Sevi
   as Schichtleiter" = your orchestrator process spawning N pi sessions (or one
   session per role per run) and merging findings by Kodex priority. That code is
   yours either way, but pi gives you nothing for it beyond the session primitive.
6. **Multi-tenancy** — per-shop isolation, encrypted connection tokens, per-tenant
   config: all yours. (pi's experimental server/protocol packages point toward
   multi-session serving but are explicitly unstable.)

**Where pi's coding-agent assumptions help vs. hurt**

- *Help:* the filesystem/bash default tools are actually useful in ops automation for
  scratch analysis (e.g. Watchdog pulling a CSV and crunching it with a script), the
  `AGENTS.md` context-file convention doubles as per-shop context, and skills-as-files
  keep playbooks versionable in git — matching the concept's versioned `playbooks`
  table.
- *Hurt:* the TUI, editor, keybindings, themes, project-trust flow, and
  session-sharing features are dead weight in a server product; `bash`/`write` on by
  default is a liability for an agent holding ad-account credentials (must be
  explicitly stripped via `tools:`/`noTools` and the process containerized, per pi's
  own security guidance); sessions-as-JSONL-files and `~/.pi` home-directory
  conventions assume a developer machine, so a server deployment must pin
  `agentDir`/session backends deliberately. None of these are blockers — the SDK
  exposes switches for all of them — but "configured" understates it: this is a
  **built product that embeds pi**, not a configured pi.
- *Stack friction:* pi is TypeScript/Node ≥22; the 7AM concept (10.2) chose
  Python + FastAPI + Celery. Adopting pi means either a Node agent-service beside the
  Python core (SDK), or driving `pi --mode rpc` as a subprocess from Python — both
  workable, neither "just configuration".
- *Churn risk:* 0.x with documented breaking changes release-to-release
  (`CHANGELOG.md`), a one-maintainer-org contribution model, and an in-flight org/scope
  migration (`@mariozechner/*` → `@earendil-works/*`; old npm scope still visible on
  npm — flagged, not fully traced). Pin versions hard.

## 5. Brief comparison: pi vs. Claude Code / Claude Agent SDK headless

(Kept short — the Agent SDK option gets its own research doc.)

Both are the same *pattern*: a general agent harness given domain tools + playbook
prompts and run headless on a schedule. Differences that matter for 7AM:

- **What the harness includes:** the Claude Agent SDK ships permission modes, hooks,
  subagents, MCP, and session management as supported product features; pi
  deliberately omits permissioning, sub-agents, and MCP and tells you to build or
  install them (`README.md`, "Philosophy"). For 7AM this is nearly a wash — the
  guardrails 7AM needs are domain-specific and land outside either harness — but the
  Agent SDK's hook/permission layer gives a second enforcement surface for free.
- **Provider coupling:** pi is provider-neutral (30+ providers) with mid-session model
  hand-off (`packages/ai/README.md`); the Agent SDK is Anthropic-native. If 7AM wants
  cheap non-Anthropic models for high-frequency Watchdog sweeps, pi (or pi-ai alone)
  is the more flexible substrate.
- **Backing/stability:** Anthropic-maintained SDK vs. a superb but fast-moving 0.x
  community project with a narrow contribution funnel.
- **Weight:** pi-agent-core is genuinely small and readable — if 7AM wants to *own*
  its agent loop someday, pi is the better study object and the easier fork.

## 6. Verdict

**"Just a pi agent configured to this" is not a real description of the work — but pi
as an embedded harness is a legitimate contender, and as a prototyping vehicle it is
excellent.**

- The phrase understates what 7AM is: the agent loop is maybe 10–20% of the system.
  The scheduler, guardrail engine, approval queue, Slack surface, connectors,
  Postgres state/audit layer, and seven-role orchestration are deterministic
  application code that no harness — pi, Claude Agent SDK, or hand-rolled — provides.
  Pi itself agrees: no permissions, no scheduler, no sub-agents, by design.
- Within its lane, pi holds up under primary-source scrutiny: MIT, very actively
  maintained, Anthropic-first-class among 30+ providers, a real SDK with full
  system-prompt/tool control (`noTools`, `customTools`, `systemPromptOverride`,
  in-memory sessions), typed custom tools, a blockable `tool_call` gate, skills that
  map 1:1 onto 7AM playbooks, and headless print/JSON/RPC modes that cron can drive.
  The coding-agent identity is a default persona, not a structural constraint.
- **As a prototype:** strongest recommendation of this doc. Interactive pi + one
  extension registering read-only Meta/Shopify tools + playbook skills + a Sevi
  `SYSTEM.md` would validate the Watchdog/Accountant behavior against real Tattup data
  in days, before any 7AM infrastructure exists — and the transcripts (JSONL sessions)
  become early Lern-DB material.
- **The decision hinges on:** (1) stack — committing to Node/TypeScript for the agent
  layer (or a Python↔RPC bridge) vs. the concept's Python plan; (2) risk appetite for
  a 0.x community harness with breaking changes vs. Anthropic's supported Agent SDK;
  (3) whether multi-provider freedom (pi) outweighs an Anthropic-native feature set
  (Agent SDK), given the concept already names the Anthropic API as the KI-Schicht.
  If 7AM stays Python-first, use pi as the prototyping lab and study object, and pick
  the harness for production in the Agent-SDK comparison doc's frame; if 7AM is happy
  going TypeScript, embedding `pi-coding-agent`'s SDK (or the smaller
  `pi-agent-core`) is a sound production choice.

---

## Sources

- Pi monorepo, shallow clone of `https://github.com/badlogic/pi-mono` at commit
  `e868230` (2026-08-26): `README.md`, `LICENSE`, root `package.json`,
  `packages/coding-agent/README.md`, `packages/coding-agent/CHANGELOG.md`,
  `packages/coding-agent/package.json`,
  `packages/coding-agent/docs/{sdk,extensions,skills,rpc,json,containerization}.md`,
  `packages/coding-agent/src/core/system-prompt.ts`,
  `packages/{agent,ai,server,protocol}/README.md`.
- GitHub rendered page `github.com/badlogic/pi-mono` (redirects to `earendil-works`),
  fetched 2026-08-26 — star/fork/issue counts (*flagged: read via rendered page, not
  API*).
- `earendil-works/pi-chat` README (raw, fetched 2026-08-26).
- Not reachable from this environment (egress-blocked): `mariozechner.at` blog posts
  linked from the README ("pi coding agent" 2025-11-30, "What if you don't need MCP"
  2025-11-02) — rationale claims are therefore cited from the README only.
- 7AM requirements: konzept v3.1 — Seite 5 summary, Kapitel 1.8 (crew), Teil X
  10.1–10.6 (architecture, stack, data model, phases).
