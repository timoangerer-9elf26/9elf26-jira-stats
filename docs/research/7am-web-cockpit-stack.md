# 7AM Web Cockpit: Stack Options Compared

Research date: 2026-08-26. Scope: the strongest realistic stack options for the 7AM
user-facing web cockpit ("Zentrale") — (a) Go + HTMX server-rendered, (b) Python FastAPI
with an HTMX or React frontend, (c) full-stack TypeScript (Next.js / React Router), and
(d) notable 2026 alternatives for internal-tool/ops dashboards — evaluated against the
concept's six core screens (esp. the live Schichtbrett), Slack-identical approval cards,
German-first i18n, auth for a small multi-shop SaaS, API-first design, operational weight
on a single EU VPS, and code/type sharing with the Sevi agent backend.

This document follows every non-obvious claim back to a primary source (official docs,
source repos, package registries). Where evidence was thin or inferred, it is called out
explicitly. **Access note:** the research environment's egress proxy blocked several
official doc sites (htmx.org, api.slack.com, fastapi.tiangolo.com, nextjs.org, MDN). For
those, the *source files of the same docs* in the projects' GitHub repos were used
instead, plus the npm / PyPI / Go-module registries for version facts. Affected claims
are marked.

---

## 1. What the concept actually demands of the cockpit

From the 7AM Konzept v3.1 (internal document, `konzept.txt`, page numbers refer to the
PDF):

- **Six core screens** (p. 7): 1. Übersicht (traffic-light KPIs per shop, max. 3 open
  decisions), 2. Freigaben (feed of proposal cards with Diagnose/Effekt/Risiko/
  Konfidenz), 3. Creatives (scoreboard, library, pre-flight, briefings), 4. Einstellungen
  (every row with recommendation + Warum text), 5. Schichtbrett (live view of the crew:
  who is checking what, last runs, open tasks), 6. Berichte (weekly/monthly report,
  Lern-Retro, audit log).
- **Approval cards identical in Slack and web** (p. 6): "Freigaben sind
  Ein-Klick-Entscheidungen mit den Buttons Ausführen / Anpassen / Später / Ablehnen — in
  Slack und später im Web-Cockpit identisch."
- **German-first** (p. 7): "Deutsch als Erstsprache der Oberfläche und aller Erklärungen;
  Englisch als zweite Sprache vorbereitet." Every field carries a Warum explanation
  (p. 6, 2.7).
- **API-first** (p. 32, 10.1): building block (7) is "Web-Cockpit (später; API-first,
  damit UI austauschbar bleibt)" — the cockpit comes *after* the 16-week plan (p. 33:
  "Danach: … Web-Cockpit, Mandantenfähigkeit …").
- **Schichtbrett data source** (p. 32, 10.3): the `agent_runs` table ("welcher Agent hat
  wann was geprüft und befunden — Grundlage für Schichtbrett und Bericht-Zuschreibung").
- **Concept's own stack recommendation** (p. 32, 10.2): Python + FastAPI, Celery + Redis,
  PostgreSQL, Slack Bolt (Python), Anthropic SDK, Docker Compose on an EU server
  (Hetzner); "Kubernetes ist in dieser Grösse unnötig."
- **Roles** (p. 31): Inhaber (everything), Operator (approvals up to an amount limit),
  Beobachter (read-only) — i.e. app-level RBAC, not just login.

Two structural observations that shape every option below (synthesis, not quotes):

1. **The Schichtbrett is one-directional live data** (server pushes "Watchdog is
   checking shop X"). Server-Sent Events are sufficient; WebSockets are only needed if
   the cockpit ever pushes *into* a live channel, which no screen requires. The htmx SSE
   extension docs make the same point for the general case: SSE is "uni-directional …
   you cannot send messages back to the server"; for bidirectional needs "consider
   WebSockets" ([htmx sse.md source](https://github.com/bigskysoftware/htmx/blob/master/www/content/extensions/sse.md)).
2. **"Identical cards" means one card model, two renderers.** Slack cards are Block Kit
   JSON; web cards are HTML. Block Kit renders only inside Slack surfaces (messages,
   modals, home tab) — it cannot be embedded in your own web page, so "identical" can
   only mean both renderers read the same `proposals`/`approvals` records (concept
   p. 32, 10.3) and the same approval endpoint. **(Inference — api.slack.com was
   blocked; the Slack-surface-only nature of Block Kit could not be re-verified against
   the official Block Kit page, though it is corroborated by the Bolt frameworks, which
   only ever deliver blocks via Slack APIs.)** Consequence: whichever process owns the
   proposal objects (the agent backend) should ideally also serve the web cards, or the
   cockpit must consume them over an internal API.

---

## 2. Cross-cutting version check (all registries queried 2026-08-26)

| Library | Latest | Source | Notes |
|---|---|---|---|
| htmx (`htmx.org`) | **2.0.10** stable; `next` = 4.0.0-beta6 | [npm dist-tags](https://registry.npmjs.org/htmx.org) | v2 is the maintained stable line; a v4 rewrite is in beta (there is no v3) |
| htmx-ext-sse | 2.2.4 | [npm](https://registry.npmjs.org/htmx-ext-sse) | separate extension package in htmx 2.x |
| templ (Go) | v0.3.1020, 2026-05-10 | [Go module proxy](https://proxy.golang.org/github.com/a-h/templ/@latest) | still 0.x — API may evolve pre-1.0 |
| go-i18n | v2.6.1, 2026-01-01 | [Go proxy](https://proxy.golang.org/github.com/nicksnyder/go-i18n/v2/@latest) | CLDR plurals for 200+ languages ([README](https://github.com/nicksnyder/go-i18n)) |
| slack-go/slack | v0.29.0, 2026-08-15 | [Go proxy](https://proxy.golang.org/github.com/slack-go/slack/@latest) | community lib, explicitly pre-1.0 (see 3.4) |
| anthropic-sdk-go | **v1.66.0**, 2026-08-19 | [Go proxy](https://proxy.golang.org/github.com/anthropics/anthropic-sdk-go/@latest) | official Anthropic Go SDK, stable 1.x |
| FastAPI | 0.141.1 | [PyPI](https://pypi.org/pypi/fastapi/json) | still 0.x versioning; very actively released |
| Starlette | 1.6.0 | [PyPI](https://pypi.org/pypi/starlette/json) | has crossed 1.0 |
| sse-starlette | 3.4.8 | [PyPI](https://pypi.org/pypi/sse-starlette/json) | SSE for Starlette/FastAPI |
| slack-bolt (Python) | 1.30.0 | [PyPI](https://pypi.org/pypi/slack-bolt/json) | official Slack framework |
| Jinja2 / Babel / Celery | 3.1.6 / 2.18.0 / 5.6.3 | PyPI | |
| anthropic (Python) | **1.0.0** | [PyPI](https://pypi.org/pypi/anthropic/json) | official SDK reached 1.0 |
| Next.js | 16.3.3 (15.5.24 backport line maintained) | [npm dist-tags](https://registry.npmjs.org/next) | |
| React Router | 8.3.0 (`version-7` = 7.18.2 maintained) | [npm dist-tags](https://registry.npmjs.org/react-router) | framework + library modes (see 5) |
| Better Auth | 1.7.1 | [npm](https://registry.npmjs.org/better-auth) | |
| next-auth (Auth.js) | 4.24.15 stable; **v5 still 5.0.0-beta.32** | [npm dist-tags](https://registry.npmjs.org/next-auth) | v5 has been in beta long-term |
| next-intl | 4.13.7 | [npm](https://registry.npmjs.org/next-intl) | |
| @slack/bolt (JS) | 5.0.0 | [npm](https://registry.npmjs.org/@slack/bolt) | official |
| @anthropic-ai/sdk (TS) | 0.120.0 | [npm](https://registry.npmjs.org/@anthropic-ai/sdk) | official but still 0.x |
| @refinedev/core | 5.0.12 | [npm](https://registry.npmjs.org/@refinedev/core) | |
| Datastar | repo tag v1.0.2 (2026-06-02); Go SDK `datastar-go` v1.2.2 | [Go proxy](https://proxy.golang.org/github.com/starfederation/datastar/@latest) | version signals inconsistent — see 6.2 |
| oapi-codegen | v2.8.0, 2026-07-17 | Go proxy | OpenAPI → Go codegen |
| openapi-typescript | 7.13.0 | npm | OpenAPI → TS types |

---

## 3. Option A — Go + HTMX server-rendered (owner's existing stack)

This is the stack the repo owner has already researched in depth
(`docs/research/htmx-go-sqlite-stack.md`, 2026-07-15) and
(`docs/research/htmx-ui-libraries.md`): Go stdlib router (1.22+ pattern matching),
`html/template` or templ, HTMX partials, single static binary.

### 3.1 Fit for the six screens

Five of the six screens (Übersicht, Freigaben, Creatives, Einstellungen, Berichte) are
classic server-rendered pages with light interactivity — exactly HTMX's home turf: the
server responds with HTML fragments, `hx-post` on the four card buttons swaps the card
into its decided state. No screen needs client-side state management. (Assessment.)

**Schichtbrett (live):** the htmx **SSE extension** covers it declaratively:
`hx-ext="sse"` + `sse-connect="<url>"` opens an EventSource; `sse-swap="<event-name>"`
swaps each named event's payload into the DOM, and `hx-trigger="sse:<event>"` can trigger
follow-up requests. The extension layers an "exponential-backoff algorithm" on top of
browser reconnection ([sse.md source in the htmx repo](https://github.com/bigskysoftware/htmx/blob/master/www/content/extensions/sse.md);
htmx.org itself was proxy-blocked). Server-side, SSE in Go is a plain `text/event-stream`
HTTP handler with periodic flushes — no library required (well-established practice;
not re-verified against the WHATWG spec, which was unreachable). If bidirectional ever
becomes necessary, `coder/websocket` v1.8.15 (2026-06-15, Go proxy) is maintained.

The Schichtbrett feed itself is just rows from `agent_runs` (concept p. 32) pushed as
rendered `<tr>`/card fragments — a natural fit for "server renders HTML" stacks.

### 3.2 i18n / German-first

`go-i18n` v2.6.1 (released 2026-01-01) "supports pluralized strings for all 200+
languages in the Unicode Common Locale Data Repository (CLDR)", message files "of any
format (e.g. JSON, TOML, YAML)", named variables via `text/template` syntax, and ships
the `goi18n` CLI for extract/merge workflows
([README](https://github.com/nicksnyder/go-i18n)). German-first with English "prepared"
(concept p. 7) maps to: author messages in German as the default bundle, add `en`
catalogs later. Nothing framework-level is missing; Go has no *built-in* i18n, so this
one dependency is load-bearing. (Assessment.)

### 3.3 Auth for a small multi-shop SaaS

Go has no batteries-included auth; the idiomatic small-SaaS setup is session cookies +
password login built on `alexedwards/scs` v2.9.0 (server-side session manager, Go proxy
2025-04-17) and, if SSO/Google-login is wanted, OIDC via `coreos/go-oidc` v3.20.0
(2026-07-08, Go proxy). The concept's three roles (Inhaber/Operator/Beobachter with
amount limits, p. 31) are application logic in any stack — no off-the-shelf auth product
covers "Operator may approve up to X EUR". (Assessment; scs/go-oidc versions are
registry facts, the "idiomatic" framing is synthesis.)

### 3.4 Slack side (if the whole backend were Go)

The only Go Slack library of note is community-maintained `slack-go/slack` v0.29.0
(2026-08-15). It covers "most if not all of the api.slack.com REST calls", Socket Mode
and the Events API, and defines full Block Kit types (`Block` interface, section/actions/
context blocks, `BlockAction`) in
[`block.go`](https://github.com/slack-go/slack/blob/master/block.go). But its README is
explicit: "There is currently no major version released. Therefore, minor version
releases may include backward incompatible changes"
([README](https://github.com/slack-go/slack)). There is **no official Bolt for Go** —
for a product whose primary interface is Slack, this is Option A's biggest structural
weakness. Conversely, the **official Anthropic Go SDK is past 1.0** (v1.66.0,
2026-08-19), so the LLM side of a Go agent backend is no longer the gap it once was.

### 3.5 API-first, ops weight, code sharing

- **API-first:** hand-written JSON handlers plus an OpenAPI spec; `oapi-codegen` v2.8.0
  can generate server stubs and clients from a spec-first workflow. Go does not generate
  OpenAPI from code as automatically as FastAPI does. (Assessment.)
- **Ops weight: best in class.** One static binary (CGO_ENABLED=0) behind Caddy/nginx in
  Docker Compose; the owner's prior research documents the whole single-binary story.
  RAM footprint is typically tens of MB. (Synthesis; no benchmark run.)
- **Code sharing with the agent backend:** *if the backend is also Go*, perfect — the
  cockpit is just more handlers in the same binary, and card rendering, guardrails,
  i18n strings and Warum texts live once. *If the backend stays Python (per concept),
  the cockpit becomes a second language*: types shared only via OpenAPI codegen, i18n
  catalogs and Warum texts duplicated or extracted into shared data files. That
  duplication cost is the crux of the whole decision (see 8).

---

## 4. Option B — Python FastAPI (the concept's recommendation)

FastAPI 0.141.1 is "based on (and fully compatible with) the open standards for APIs:
OpenAPI … and JSON Schema", generates interactive docs at `/docs` and `/redoc`
automatically, and builds on Starlette (web) + Pydantic (validation)
([README](https://github.com/fastapi/fastapi)). Note it is still versioned 0.x.

There are two sub-variants for the frontend.

### 4.1 Variant B1 — FastAPI + Jinja2 + HTMX (server-rendered)

**Key insight: HTMX is backend-agnostic, so the owner's HTMX experience transfers
wholesale to Python.** FastAPI supports server-rendered templates via `Jinja2Templates`
(provided by Starlette); "A common choice is Jinja2, the same one used by Flask", and
"You can use any template engine you want with FastAPI"
([templates.md source](https://github.com/fastapi/fastapi/blob/master/docs/en/docs/advanced/templates.md)).
Jinja2 is at 3.1.6 (PyPI). The HTMX patterns from the owner's existing research
(fragment endpoints, `HX-Request` branching, `HX-Trigger` headers) apply unchanged.

**Schichtbrett:** `sse-starlette` 3.4.8 provides `EventSourceResponse`, a "production
ready Server-Sent Events implementation for Starlette and FastAPI following the W3C SSE
specification", with automatic client-disconnect detection, graceful shutdown, and
configurable ping intervals ([README](https://github.com/sysid/sse-starlette)). Pair it
with the same htmx SSE extension on the client as in Option A. WebSockets, if ever
needed, are native: FastAPI exposes Starlette's `WebSocket` "directly just as a
convenience", with `Depends`/`Cookie`/`Query` dependencies working in websocket
endpoints ([websockets.md source](https://github.com/fastapi/fastapi/blob/master/docs/en/docs/advanced/websockets.md)).

**Slack cards:** the decisive advantage. `slack-bolt` 1.30.0 is Slack's **official**
Python framework — listeners for events, Block Kit element actions (buttons, selects),
modals, Socket Mode via `SocketModeHandler`, and an async mode with documented
**FastAPI/Starlette integrations** ([README](https://github.com/slackapi/bolt-python)).
The Bolt app and the cockpit can literally run in one process (or one codebase, two
containers), both rendering from the same `Proposal` Pydantic models: one function emits
Block Kit JSON, a sibling template renders the HTML card. The "identisch" requirement
(concept p. 6) falls out naturally.

**i18n:** FastAPI/Starlette have **no built-in i18n** (absence claim — inferred from the
official docs, which contain no i18n chapter; flagged as unverifiable-by-positive-source).
The standard Python route is gettext catalogs managed with Babel 2.18.0
("Internationalization utilities", PyPI) wired into Jinja2's i18n extension. Works, and
German-first is just the default locale — but it is the least ergonomic i18n story of
the three main options (gettext `.po` tooling vs. go-i18n's TOML/JSON or next-intl's
ICU). (Assessment.)

**Auth:** Starlette ships `SessionMiddleware` — "signed cookie-based HTTP sessions.
Session information is readable but not modifiable", HttpOnly by default
([middleware.md](https://github.com/encode/starlette/blob/master/docs/middleware.md)) —
plus FastAPI's security utilities for OAuth2/password flows. As with Go, roles and
amount limits are app logic. (Registry/doc facts + assessment.)

**API-first:** the strongest of all options — the JSON API the concept demands ("damit
UI austauschbar bleibt", p. 32) is FastAPI's core competence, with OpenAPI generated from
the same Pydantic models the agents already use; `openapi-typescript` 7.13.0 can emit TS
types from it if a JS UI is ever swapped in.

**Ops weight:** uvicorn (0.52.4) workers + Celery (5.6.3) + Redis + Postgres in Docker
Compose. Heavier than one Go binary, but the concept's architecture (p. 32) *already*
budgets Celery, Redis and Postgres for the agent system — the cockpit adds no new
container class. (Synthesis.)

**Code sharing:** total, by construction — cockpit, Slack bot, guardrails, Warum texts,
and the Anthropic SDK (Python `anthropic` 1.0.0) live in one codebase and one type
system (Pydantic 2.13.4).

### 4.2 Variant B2 — FastAPI + React/Next.js SPA

Same backend virtues, but the frontend becomes a second codebase in a second language
with its own build, routing, i18n and auth-session handling; types flow only through
OpenAPI codegen. For six mostly-read-only screens with one-click actions, a React SPA
buys nothing the concept needs and doubles the surface area. Only worth it if the
Schichtbrett is envisioned as a rich animated "watch your team work" showpiece beyond
what server-rendered fragments can do (concept p. 11 calls it "das stärkste
Verkaufsbild"). (Assessment.)

---

## 5. Option C — Full-stack TypeScript

Two credible shapes in 2026:

- **Next.js 16.3.3** (npm `latest`; the 15.x line still receives backports —
  `backport: 15.5.24`). Route Handlers support streaming responses via the standard Web
  APIs (`ReadableStream`); the docs' own streaming examples use the AI SDK or raw
  streams — there is *no explicit SSE example* in the route-handler reference
  ([route.mdx source](https://github.com/vercel/next.js/blob/canary/docs/01-app/03-api-reference/03-file-conventions/route.mdx)).
  SSE from a route handler works on a self-hosted Node server by writing an
  `text/event-stream` ReadableStream (synthesis — not shown in official docs). The
  backend-for-frontend guide warns that on serverless "long-running handlers may be
  terminated due to timeouts. WebSockets won't work" — a caveat that does **not** apply
  to a self-hosted `next start` on a VPS
  ([backend-for-frontend.mdx](https://github.com/vercel/next.js/blob/canary/docs/01-app/02-guides/backend-for-frontend.mdx)).
- **React Router 8.3.0** — "a multi-strategy router for React", usable "maximally as a
  React framework or minimally as a library" ([README](https://github.com/remix-run/react-router));
  this is the continuation of Remix (v7 line still maintained at 7.18.2, npm dist-tags).
  Leaner than Next.js for a self-hosted server-rendered app, but the same
  second-ecosystem cost applies.

**i18n:** best-in-class libraries — `next-intl` 4.13.7 offers ICU messages
("interpolation, cardinal & ordinal plurals, enum-based label selection"), App
Router/Server Components support, and type-safe message keys
([README](https://github.com/amannn/next-intl)).

**Auth:** **Better Auth 1.7.1**, "a framework-agnostic authentication (and
authorization) framework for TypeScript" with email/password, social login, 2FA and
organization/multi-tenant plugins, MIT
([README](https://github.com/better-auth/better-auth)) — notably more finished than
Auth.js/next-auth, whose v5 has sat in beta for years (npm: stable 4.24.15, `beta`
5.0.0-beta.32 as of 2026-08-26).

**Slack:** official `@slack/bolt` 5.0.0 (npm) — parity with Python's Bolt.

**Agent backend sharing:** only pays off if the *agent backend itself* is TypeScript.
That is possible (official `@anthropic-ai/sdk` exists) but the TS Anthropic SDK is still
0.x (0.120.0), vs. Python 1.0.0 and Go 1.66.0, and the concept's data/LLM-ecosystem
argument for Python (p. 32) applies against it. Job scheduling also has no Celery-grade
default in Node (assessment — no primary source consulted for the Node job-queue
landscape; flagged).

**Ops weight:** a Node server (standalone Next build or React Router server) — fine on a
VPS, but the heaviest toolchain of the three (bundler, RSC build, framework major
upgrades: npm dist-tags show three major Next.js lines — 14, 15, 16 — receiving releases
simultaneously, an indicator of upgrade churn; inference from registry data).

---

## 6. Option D — Notable 2026 alternatives for ops dashboards

### 6.1 Refine (React meta-framework for internal tools)

Refine v5 (`@refinedev/core` 5.0.12, MIT) is "a React meta-framework for CRUD-heavy web
applications" targeting "internal tools, admin panels, dashboards, and B2B apps", with a
headless architecture, 15+ data providers (incl. plain REST), auth/access-control
provider hooks, realtime/live support, audit-log hooks and i18n, and UI bindings for Ant
Design/Material UI/Mantine/Chakra ([github.com/refinedev/refine](https://github.com/refinedev/refine),
repo page: 35.6k stars). It is the most credible "don't hand-build the admin" option —
its auditLog and access-control providers even echo 7AM concepts. But it is still a
React SPA framework over an API you must build anyway, and the cockpit is *not*
CRUD-shaped: it is an opinionated product UI in Sevi's voice (one-decision cards, Warum
texts, traffic lights). Refine would style the wrong skeleton. (Assessment.)

### 6.2 Datastar (SSE-first hypermedia framework)

Datastar is "a lightweight framework for building everything from simple sites to
real-time collaborative web applications" via HTML `data-*` attributes, ~10.76 KiB
bundle ([README](https://github.com/starfederation/datastar)). Its model — the server
pushes fragment/signal updates over a long-lived SSE connection — is arguably a *better*
conceptual fit for the Schichtbrett than htmx-plus-extension, and it has an official Go
SDK (`starfederation/datastar-go` v1.2.2, 2026-06-02, Go proxy). **Version status is
inconsistent across channels and flagged as unverified:** the repo has a v1.0.2 tag
(2026-06-02, per Go module proxy) but the README on `main` still displays 1.0.0-RC.7 and
npm's `latest` is 1.0.0-beta.11. Treat it as just-1.0/young: a defensible experiment for
one screen, not the foundation bet for the product's control surface. (Assessment.)

### 6.3 Low-code platforms (ToolJet, Appsmith, …) — ruled out

ToolJet describes itself as an "enterprise app generation platform for building internal
tools, dashboard[s], business applications", licensed **AGPL-3.0** with a paid enterprise
tier ([github.com/ToolJet/ToolJet](https://github.com/ToolJet/ToolJet)). For 7AM the
category fails on requirements, not maturity: the cockpit is a customer-facing product
surface with a strict brand voice ("Die Firma spricht nur auf Verträgen … Überall sonst
spricht Sevi", concept p. 9) and German-first Warum texts on every field — generic
drag-and-drop widget UIs cannot deliver that, and AGPL complicates a future SaaS.
(Assessment; Appsmith not individually verified — flagged.)

---

## 7. Side-by-side

| Criterion | A: Go + HTMX | B1: FastAPI + Jinja2 + HTMX | C: Full TS (Next.js / RR) |
|---|---|---|---|
| 6 screens incl. approval cards | very good (server-rendered fragments) | very good (same HTMX patterns) | good, but SPA machinery unneeded |
| Schichtbrett live | htmx SSE ext + plain Go handler | htmx SSE ext + sse-starlette | ReadableStream SSE (no official SSE example) |
| Cards shared with Slack | only if backend is Go too; slack-go is community, pre-1.0 | one process/codebase with **official Bolt** | official Bolt JS, if backend is TS |
| German-first i18n | go-i18n (CLDR, active) | gettext/Babel (workable, clunkiest) | next-intl (ICU, best DX) |
| Auth (small multi-shop SaaS) | scs sessions + go-oidc, hand-rolled RBAC | Starlette sessions + FastAPI security, hand-rolled RBAC | Better Auth 1.7 (most batteries) |
| API-first | manual/spec-first (oapi-codegen) | **automatic OpenAPI from Pydantic** | secondary concern in UI frameworks |
| Ops weight on one EU VPS | **one static binary** | uvicorn+celery+redis (already budgeted by concept) | Node server + heaviest toolchain |
| Shares code/types with agent backend | total *iff* backend is Go | **total, backend per concept** | total *iff* backend is TS (TS Anthropic SDK still 0.x) |
| Matches owner experience | fully | HTMX yes, language no | no |
| Matches concept doc | no | fully | no |

---

## 8. Recommendation

The cockpit is thin; the agent backend is the product. So the cockpit-stack question is
really the backend-language question, and the frontend technique — HTMX server-rendering
— is *common to the top two options*, which defuses most of the "concept (Python) vs.
owner taste (Go/HTMX)" tension: **the owner's HTMX/server-rendered taste survives either
backend choice; only the Go-vs-Python part is actually in conflict.**

1. **FastAPI + Jinja2 + HTMX + sse-starlette (Option B1)** — recommended. It keeps the
   concept's architecture intact (official Slack Bolt with FastAPI integration, Pydantic
   models as the single type system, automatic OpenAPI satisfying the API-first
   requirement, Anthropic Python SDK at 1.0), and the approval-card "identisch in Slack
   und Web" requirement collapses into one model with two renderers in one codebase. The
   cockpit costs no second language and no second deployment. Weakest point: gettext-
   based i18n ergonomics, and FastAPI's perpetual 0.x versioning.
2. **All-Go: Go backend + HTMX/templ cockpit (Option A)** — the right choice *only as a
   package deal* in which the whole Sevi backend is also Go (now more defensible than a
   year ago: official Anthropic Go SDK v1.66). It wins decisively on ops weight (one
   binary) and on the owner's fluency, and go-i18n's German story is solid. The
   deal-breaker risk for a Slack-first product: no official Bolt for Go — Slack
   interactivity rides on a community library that warns of breaking minor releases.
   Building the cockpit in Go while the backend stays Python is the worst variant on the
   table (duplicated card rendering, Warum texts, and i18n catalogs across two
   languages) and should be avoided.
3. **Full-stack TypeScript (Option C)** — take only if a rich React Schichtbrett becomes
   a hard product requirement or the team turns TS-native. Best i18n (next-intl) and
   auth (Better Auth) libraries of the field and an official Bolt, but it matches
   neither the concept nor the owner's stack, carries the heaviest toolchain for six
   mostly server-shaped screens, and its Anthropic SDK is the least mature of the three.

Refine and Datastar (Option D) are worth knowing but not adopting now: Refine solves a
CRUD-admin problem the cockpit doesn't have; Datastar is a promising just-1.0 candidate
to prototype the Schichtbrett against, nothing more. Low-code platforms are ruled out by
brand-voice and AGPL constraints.

---

## Where evidence was thin or inferred

- **Block Kit renders only inside Slack surfaces** — inference corroborated by the Bolt
  frameworks' design; api.slack.com was blocked by the egress proxy and could not be
  quoted directly.
- **htmx.org, fastapi.tiangolo.com, nextjs.org, MDN, WHATWG blocked** — all htmx/
  FastAPI/Next.js doc claims were verified against the docs' *source files* in the
  official GitHub repos instead (links inline).
- **SSE server-side in Go/Next.js** ("plain text/event-stream handler works") is
  established practice, not quoted from an official doc; Next.js docs show generic
  ReadableStream streaming but no explicit SSE example.
- **FastAPI/Starlette have no built-in i18n** — an absence claim; inferred from the
  official docs' lack of any i18n chapter.
- **Datastar's release status** is inconsistent across its own channels (repo tag v1.0.2
  vs. README RC.7 vs. npm beta.11) — flagged, do not rely on a specific version.
- **Next.js upgrade-churn** point is inferred from npm dist-tags (three concurrent major
  lines), not from an official statement.
- **Node job-queue landscape** ("no Celery-grade default") was not researched against
  primary sources.
- **Ops-weight comparisons** (binary vs. uvicorn/celery vs. Node) are qualitative
  synthesis; no measurements were taken.
- **Appsmith** was not individually verified; the low-code verdict rests on ToolJet plus
  requirements analysis.
- All version numbers are registry facts as of 2026-08-26 (npm dist-tags, PyPI JSON API,
  Go module proxy); GitHub's API was not accessible from this session.

---

## Sources (primary)

Concept
- 7AM Konzept v3.1 (internal, 2026-08-26) — pages 5–12 (product/UX, six screens, Sevi
  voice), 31 (roles), 32–33 (Teil X architecture, tech recommendation, data model, plan)

Registries (versions, queried 2026-08-26)
- npm registry dist-tags — https://registry.npmjs.org/ (htmx.org, htmx-ext-sse, next,
  react-router, better-auth, next-auth, next-intl, @slack/bolt, @anthropic-ai/sdk,
  @refinedev/core, openapi-typescript, @starfederation/datastar)
- PyPI JSON API — https://pypi.org/pypi/<pkg>/json (fastapi, starlette, sse-starlette,
  slack-bolt, jinja2, babel, celery, uvicorn, pydantic, anthropic)
- Go module proxy — https://proxy.golang.org/<module>/@latest (templ, go-i18n,
  slack-go/slack, scs, go-oidc, oapi-codegen, anthropic-sdk-go, coder/websocket,
  chi, pgx, datastar, datastar-go)

Docs / source repos
- htmx SSE extension (doc source) — https://github.com/bigskysoftware/htmx/blob/master/www/content/extensions/sse.md
- FastAPI README — https://github.com/fastapi/fastapi/blob/master/README.md
- FastAPI WebSockets doc source — https://github.com/fastapi/fastapi/blob/master/docs/en/docs/advanced/websockets.md
- FastAPI Templates doc source — https://github.com/fastapi/fastapi/blob/master/docs/en/docs/advanced/templates.md
- Starlette middleware docs (SessionMiddleware) — https://github.com/encode/starlette/blob/master/docs/middleware.md
- sse-starlette README — https://github.com/sysid/sse-starlette
- Bolt for Python README — https://github.com/slackapi/bolt-python
- slack-go/slack README + block.go — https://github.com/slack-go/slack
- go-i18n README — https://github.com/nicksnyder/go-i18n
- templ README — https://github.com/a-h/templ
- Next.js route.mdx / backend-for-frontend.mdx (doc sources) — https://github.com/vercel/next.js/tree/canary/docs
- React Router README — https://github.com/remix-run/react-router
- Better Auth README — https://github.com/better-auth/better-auth
- next-intl README — https://github.com/amannn/next-intl
- Refine repo — https://github.com/refinedev/refine
- Datastar repo — https://github.com/starfederation/datastar
- ToolJet repo (license) — https://github.com/ToolJet/ToolJet

Related in-repo research
- docs/research/htmx-go-sqlite-stack.md (2026-07-15) — owner's existing Go/HTMX research
- docs/research/htmx-ui-libraries.md
