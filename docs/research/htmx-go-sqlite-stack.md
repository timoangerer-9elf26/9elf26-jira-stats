# HTMX + Go + SQLite: Current Best Practices for a Simple, Self-Hosted Stack

Research date: 2026-07-15. Scope: a server-rendered web app built with HTMX, Go, and
SQLite, optimized for local-first development and self-hosting on a Mac mini.

This document follows every non-obvious claim back to a primary source (official docs,
source repos, specs). Where evidence was thin or inferred, it is called out explicitly.

---

## 1. Idiomatic Go project structure & libraries

### Project layout

The official Go guidance ("Organizing a Go module", go.dev) is to **start simple and grow
incrementally** — not to reach for an elaborate layout on day one. It lays out a
progression:

- **A basic command** is just `go.mod` + `main.go` (+ more `.go` files) in the root
  directory. No subdirectories required.
- Introduce an **`internal/`** directory for supporting packages when the root file gets
  large. The docs recommend this explicitly: "it's recommended placing such packages into
  a directory named `internal`; [this prevents] other modules from depending on packages
  we don't necessarily want to expose." The Go tool enforces this — packages under
  `internal/` cannot be imported from outside the module.
  ([go.dev/doc/modules/layout](https://go.dev/doc/modules/layout),
  [cmd/go internal directories](https://pkg.go.dev/cmd/go#hdr-Internal_Directories))
- Add **`cmd/<progname>/main.go`** only when you have more than one binary.
- For **server projects** specifically, the official recommendation is: "Since server
  projects typically won't have packages for export... it's recommended to keep the Go
  packages implementing the server's logic in the `internal` directory."
  ([go.dev/doc/modules/layout](https://go.dev/doc/modules/layout))

Note on `golang-standards/project-layout`: this widely-cited repo is **not** an official
standard (it says so itself in its README). For a small local-first app, prefer the
official go.dev guidance — a flat root package, then `internal/` — over prematurely
importing the heavier `cmd/ pkg/ internal/ api/ ...` tree.
([github.com/golang-standards/project-layout](https://github.com/golang-standards/project-layout))

### Routing: stdlib `net/http` (Go 1.22+) vs. chi

Go 1.22 (Feb 2024) added **method matching and wildcards** to the standard-library
`http.ServeMux`, which removes most of the historical reason to reach for a third-party
router. Primary source: the Go team's "Routing Enhancements for Go 1.22" blog post.
([go.dev/blog/routing-enhancements](https://go.dev/blog/routing-enhancements))

Key facts, all from that post:

- **Method in the pattern:** `http.HandleFunc("GET /posts/{id}", handler)`. Method patterns
  match exactly, "except GET also matches HEAD; all the other methods match exactly."
- **Automatic 405:** a request whose method doesn't match "a `net/http` server will reply
  to such a request with a `405 Method Not Allowed` error that lists the available methods
  in an `Allow` header."
- **Single-segment wildcard:** `{id}` matches one path segment; retrieve it with
  `req.PathValue("id")`.
- **Multi-segment wildcard:** a pattern ending in `...`, e.g. `/files/{pathname...}`,
  "can match all the remaining segments of the path."
- **Exact trailing-slash match:** `/posts/{$}` "will match `/posts/` but not `/posts` or
  `/posts/234`" (whereas `/posts/` alone matches any path with that prefix).
- **Precedence is by specificity, not registration order:** "the most specific pattern
  wins" — `/posts/latest` beats `/posts/{id}`, and `GET /posts/{id}` beats `/posts/{id}`.
- **Genuine conflicts panic:** if two patterns overlap and neither is more specific (e.g.
  `/posts/{id}` and `/{resource}/latest`), "registering both of them (in either order!)
  will panic."
- `Request` also gained `SetPathValue`, so external routers can expose their own parsed
  path values via `PathValue`.

**When chi is still worth it.** `chi` is a lightweight router that is "100% compatible with
net/http" and has "No external dependencies - plain ol' Go stdlib + net/http"; its core is
"less than 1000 LOC." It implements `http.Handler`, so any net/http middleware works.
([github.com/go-chi/chi](https://github.com/go-chi/chi)) Over the enhanced stdlib mux it
adds first-class **middleware composition** (`.Use()`, `.With()`), **sub-routers / route
groups** (`.Route()`, `.Mount()`), and a bundled common-middleware package. For a small
app, the stdlib mux plus a couple of hand-written middleware wrappers is idiomatic and
sufficient; reach for chi when you want structured route groups and a ready-made middleware
stack. (Assessment/inference based on the two primary sources above; the "when to choose"
framing is my synthesis, not a quoted claim.)

---

## 2. HTMX + Go integration

### The core pattern: server returns HTML fragments, not JSON

HTMX extends HTML with attributes (`hx-get`, `hx-post`, `hx-put`, `hx-patch`, `hx-delete`)
that fire AJAX requests. The defining property for a Go backend: **the server responds with
HTML, not JSON.** The htmx docs state directly: "when you are using htmx, on the server side
you typically respond with _HTML_, not _JSON_."
([htmx.org/docs](https://htmx.org/docs/))

Swap mechanics (from the same docs):

- `hx-target` chooses which element receives the response; by default the response replaces
  the target's `innerHTML`.
- `hx-swap` selects the strategy: `outerHTML`, `beforebegin`, `afterend`, etc.
- After a swap, htmx does a "settle" step (old attribute values are briefly retained, then
  new ones swapped in after a settle delay) to enable CSS transitions.

This maps cleanly onto Go's `html/template`: a full-page handler renders the whole document,
and a partial handler renders just the fragment (a named template) that will be swapped in.

### Rendering fragments with `html/template`

`html/template` is the package to use for any HTML output. Its docs: it "implements
data-driven templates for generating HTML output safe against code injection. It provides
the same interface as text/template and should be used instead of text/template whenever the
output is HTML." ([pkg.go.dev/html/template](https://pkg.go.dev/html/template))

Two features make it a good fit for the HTMX partials pattern:

1. **Contextual autoescaping.** "The escaping is contextual, so actions can appear within
   JavaScript, CSS, and URI contexts" and data is auto-escaped for the context it lands in.
   The package "assumes that template authors are trusted, that Execute's data parameter is
   not" — i.e. all data passed to `Execute`/`ExecuteTemplate` is treated as untrusted and
   escaped. This is exactly what you want when spraying user data into HTML fragments.
   ([pkg.go.dev/html/template](https://pkg.go.dev/html/template))
2. **Named templates + `ExecuteTemplate`.** Define fragments with `{{define "row"}}...{{end}}`
   (and page skeletons with `{{block}}`), then render a specific fragment by name:
   `t.ExecuteTemplate(w, "row", data)`. "ExecuteTemplate applies the template associated with
   t that has the given name to the specified data object and writes the output to wr." A
   full-page handler executes the outer template; an HTMX partial handler executes just the
   fragment name. ([pkg.go.dev/html/template](https://pkg.go.dev/html/template))

### HTMX request/response headers

HTMX communicates out-of-band via HTTP headers, documented in the htmx reference.
([htmx.org/reference](https://htmx.org/reference/))

**Request headers htmx sends** (useful for branching between full-page and fragment
responses server-side):

| Header | Meaning (quoted from htmx reference) |
|---|---|
| `HX-Request` | always `"true"` — lets the server detect an htmx request |
| `HX-Boosted` | request is via an element using `hx-boost` |
| `HX-Current-URL` | the current URL of the browser |
| `HX-History-Restore-Request` | `"true"` if the request is for history restoration after a cache miss |
| `HX-Prompt` | the user's response to an `hx-prompt` |
| `HX-Target` | the `id` of the target element, if it exists |
| `HX-Trigger` | the `id` of the triggered element, if it exists |
| `HX-Trigger-Name` | the `name` of the triggered element, if it exists |

**Response headers the server can set** to drive client behavior:

| Header | Meaning (quoted from htmx reference) |
|---|---|
| `HX-Location` | "allows you to do a client-side redirect that does not do a full page reload" |
| `HX-Push-Url` | "pushes a new url into the history stack" |
| `HX-Redirect` | "can be used to do a client-side redirect to a new location" |
| `HX-Refresh` | if `"true"`, the client does a full page refresh |
| `HX-Replace-Url` | replaces the current URL in the location bar |
| `HX-Reswap` | specify how the response will be swapped (overrides `hx-swap`) |
| `HX-Retarget` | a CSS selector that changes the swap target |
| `HX-Reselect` | a CSS selector choosing which part of the response is swapped in |
| `HX-Trigger` | "allows you to trigger client-side events" |
| `HX-Trigger-After-Settle` | trigger client-side events after the settle step |
| `HX-Trigger-After-Swap` | trigger client-side events after the swap step |

Detail worth flagging: the dedicated `HX-Redirect` header page notes that
htmx **does not process response headers on 3xx responses** — so return a 200 with
`HX-Redirect`/`HX-Location` set rather than an HTTP redirect status when you want htmx to
handle it. ([htmx.org/headers/hx-redirect](https://htmx.org/headers/hx-redirect/),
[htmx.org/headers/hx-location](https://htmx.org/headers/hx-location/))

Typical Go handler shape (inference/synthesis from the primary sources above, not a quote):
inspect `r.Header.Get("HX-Request")`; if `"true"`, `ExecuteTemplate` the fragment; otherwise
render the full page. Set `w.Header().Set("HX-Trigger", ...)` etc. before writing the body.

---

## 3. SQLite in Go

### Driver choice: `mattn/go-sqlite3` (cgo) vs. `modernc.org/sqlite` (pure Go)

| | `mattn/go-sqlite3` | `modernc.org/sqlite` |
|---|---|---|
| Implementation | cgo binding to the real C SQLite | "a CGo-free port of the C SQLite3 library" (transpiled C→Go) |
| Build requirement | "required to set the environment variable `CGO_ENABLED=1` and have a `gcc` compiler present within your path" | no C compiler; builds with `CGO_ENABLED=0` |
| `database/sql` driver name | `"sqlite3"` | `"sqlite"` |
| Cross-compilation | harder (needs a cross C toolchain, e.g. xgo/musl-cross) | trivial (`GOOS`/`GOARCH` only) |

Sources: [github.com/mattn/go-sqlite3](https://github.com/mattn/go-sqlite3),
[pkg.go.dev/modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite).

`modernc.org/sqlite` is a pure-Go transpilation of the SQLite C source (its docs report
tracking SQLite version 3.53.2 and supporting Linux/macOS/FreeBSD/Windows across many
architectures). It opens via `sql.Open("sqlite", dsn)` and supports DSN pragmas, e.g.
`file:app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)`.
([pkg.go.dev/modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite))

**Tradeoff / recommendation.** The pure-Go driver is measurably slower on writes — an
independent benchmark (multiprocessio/DataStation, a secondary source, flagged as such)
found the translation "twice as slow in INSERTs" and up to ~2x on some SELECTs. But it
eliminates cgo, which is the single biggest simplification for the goal here: a
`CGO_ENABLED=0` build produces a portable static binary with no C toolchain in dev or CI.
For a local-first, self-hosted app with modest write volume, **`modernc.org/sqlite` is the
better default**; switch to `mattn/go-sqlite3` only if profiling shows write throughput is a
bottleneck or you need a C-only SQLite extension. (The "twice as slow" figure is from a
secondary benchmark, not an official source; the recommendation is my synthesis.)
([datastation.multiprocess.io benchmark](https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html),
[github.com/multiprocessio/sqlite-cgo-no-cgo](https://github.com/multiprocessio/sqlite-cgo-no-cgo))

### Concurrency: WAL, single-writer, busy_timeout, and the connection pool

The relevant primary source is SQLite's own WAL documentation.
([sqlite.org/wal.html](https://www.sqlite.org/wal.html))

- **Enable WAL:** `PRAGMA journal_mode=WAL;`. WAL mode is **persistent** — once set, the
  database stays in WAL across close/reopen (unlike most journal modes).
- **Concurrency guarantee:** in WAL, "writers and readers can run at the same time." Readers
  don't block writers and writers don't block readers.
- **Single writer:** "since there is only one WAL file, there can only be one writer at a
  time." This is the fundamental SQLite constraint.
- **`SQLITE_BUSY` can still happen** even in WAL (e.g. during recovery after a crash, or
  when the last connection checkpoints while cleaning up), so the app must still be prepared
  for it. Set a **busy timeout** (`PRAGMA busy_timeout=5000` or DSN `_pragma=busy_timeout(5000)`)
  so SQLite waits for a lock instead of failing immediately.
  ([sqlite.org/wal.html](https://www.sqlite.org/wal.html))
- **Checkpointing:** an automatic checkpoint fires when the WAL reaches ~1000 pages
  (`PRAGMA wal_autocheckpoint`), and when the last connection closes. Litestream (below)
  takes over checkpointing.

**Go connection-pool settings.** Because SQLite allows only one writer, `database/sql`'s
pool (which opens multiple connections) can produce `database is locked` errors under
concurrent writes. The common, well-established mitigation is `db.SetMaxOpenConns(1)`, which
serializes all access through one connection. I could not find this specific recommendation
in an *official* SQLite or Go doc — it is documented across the driver ecosystem and
practitioner writeups (flagged as secondary). A pragmatic middle ground documented by River
(a Go queue) is one writer connection pool + a separate reader pool, relying on WAL to let
reads proceed concurrently. ([riverqueue.com/docs/sqlite](https://riverqueue.com/docs/sqlite),
[SQLITE_BUSY despite timeout — berthub.eu](https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/))
The safe baseline for a small app: WAL + `busy_timeout` + `SetMaxOpenConns(1)`; loosen to a
multi-reader/single-writer split only if read concurrency becomes a bottleneck. (Synthesis.)

### Migrations

Three viable approaches, in increasing ceremony:

- **Plain SQL, embedded.** Ship `.sql` files via Go's `embed` and apply them at startup with
  a small version-tracking table. Zero extra dependencies; fits the single-binary goal.
  (Inference — no single primary doc, but `embed` is standard library.)
- **goose** (`pressly/goose`): "a database migration tool ... which manages your database
  schema by creating incremental SQL changes or Go functions", supports SQLite, and supports
  embedding migrations into the binary via `embed.FS`. Both a CLI and a library.
  ([github.com/pressly/goose](https://github.com/pressly/goose),
  [goose embed docs](https://pressly.github.io/goose/blog/2021/embed-sql-migrations/))
- **golang-migrate**: supports many databases including SQLite; uses paired
  `{version}_{title}.up.sql` / `.down.sql` files; usable as CLI or library, with embedded
  filesystem support. ([github.com/golang-migrate/migrate](https://github.com/golang-migrate/migrate))

For a single-binary self-hosted app, **embedding migrations** (via goose-as-library or plain
`embed` + a runner) is the cleanest fit — the binary carries its own schema and applies it on
boot, so deployment stays "copy one file." (Synthesis; the "embed" capability of both tools
is documented above.)

---

## 4. Simplest deployment story

### The single static binary

Go compiles to a single self-contained executable. Setting `CGO_ENABLED=0` "tells the
toolchain to skip the C compiler entirely" and forces pure-Go implementations, producing a
static binary that "runs anywhere on the target OS without worrying about shared libraries."
Cross-compilation is just environment variables: `GOOS`/`GOARCH` select target OS/arch.
(These specifics are from gofaq.org / Medium writeups — secondary sources — but the
`CGO_ENABLED`, `GOOS`, `GOARCH` variables themselves are standard Go toolchain behavior.)
([gofaq.org static binaries](https://www.gofaq.org/en/how-to-build-static-binaries-in-go/),
[gofaq.org cross-compile](https://www.gofaq.org/en/how-to-cross-compile-go-programs-goos-and-goarch/))

A production build often looks like:
`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o app ./cmd/app`
(`-s -w` strip the symbol table and DWARF debug info to shrink the binary).

This is why the **pure-Go SQLite driver matters for deployment**: with
`modernc.org/sqlite` the whole app — HTTP server, templates (embed them with `//go:embed`),
migrations, and SQLite engine — is one file with no runtime dependencies. On a Mac mini you
build with `CGO_ENABLED=0 go build`, `scp` the binary, and run it.

### Local-first dev workflow

Nothing exotic is needed: `go run ./cmd/app` runs the server against a local `app.db` file
(SQLite is just a file). Because the same binary and the same SQLite file work in dev and
prod, "local-first then self-host" is essentially the same setup twice. (Inference from the
static-binary and file-based-SQLite facts above; no single doc asserts this end to end.)

### Running as a service on macOS via launchd

macOS's service manager is **launchd**, configured with `.plist` files. Apple's "Creating
Launch Daemons and Agents" is the primary source.
([Apple: Creating Launchd Jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html))

- **Agent vs daemon:** a **LaunchAgent** runs only while a specific user is logged in; a
  **LaunchDaemon** runs system-wide regardless of login. For a Mac mini that reboots
  unattended and should serve without anyone logged in, use a **LaunchDaemon**. For a machine
  where you're always logged in, a per-user LaunchAgent is simpler.
- **plist locations:** LaunchDaemons go in `/Library/LaunchDaemons/`; LaunchAgents in
  `/Library/LaunchAgents/` or `~/Library/LaunchAgents/`.
- **Required keys:** `Label` ("a unique string that identifies your daemon") and
  `ProgramArguments` (array; first element is the program path — use an absolute path,
  followed by args). Useful optional keys: `KeepAlive` (restart/keep running — "specifies
  whether your daemon launches on-demand or must always be running"), `StandardOutPath` /
  `StandardErrorPath` (log files), `WorkingDirectory`.
- **Do not daemonize:** Apple is explicit — "You must not daemonize your process. This
  includes calling the `daemon` function, calling `fork` followed by `exec`..." launchd
  supervises the process directly, so a Go binary that just runs in the foreground is exactly
  right (no backgrounding needed).
- **Security/ownership:** LaunchDaemons must be owned by root and not group/world-writable.
  ([Apple: Creating Launchd Jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html),
  [launchd.plist(5) man page](https://www.manpagez.com/man/5/launchd.plist/))
- **Loading:** modern launchctl uses `launchctl bootstrap system/ /Library/LaunchDaemons/com.example.app.plist`
  (older syntax: `launchctl load`). (The bootstrap/bootout vs load/unload distinction is from
  the launchctl man page / ss64 — secondary sources.)
  ([ss64 launchctl](https://ss64.com/mac/launchctl.html))

Minimal LaunchDaemon plist (adapted from Apple's example; `KeepAlive`/log paths added):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>            <string>com.example.myapp</string>
    <key>ProgramArguments</key> <array><string>/usr/local/bin/myapp</string></array>
    <key>KeepAlive</key>        <true/>
    <key>WorkingDirectory</key> <string>/usr/local/var/myapp</string>
    <key>StandardOutPath</key>  <string>/usr/local/var/log/myapp.log</string>
    <key>StandardErrorPath</key><string>/usr/local/var/log/myapp.err.log</string>
</dict>
</plist>
```

### SQLite backup strategies

Three tiers, from simplest to most robust:

1. **`VACUUM INTO` (point-in-time snapshot).** `VACUUM main INTO 'backup.db';` — "the original
   database file is unchanged and a new database is created in a file named by the argument."
   The result is minimal in size and purges deleted content. SQLite's docs position it as
   "an alternative to the backup API for generating backup copies of a live database," noting
   the backup API "uses fewer CPU cycles and can be executed incrementally" while VACUUM INTO
   produces smaller, cleaner output. This is a safe way to snapshot a live WAL-mode DB.
   ([sqlite.org/lang_vacuum.html](https://www.sqlite.org/lang_vacuum.html),
   [sqlite.org/backup.html](https://www.sqlite.org/backup.html))
2. **Online Backup API / `.backup`.** The C backup API (and the `sqlite3` shell's `.backup`
   command that wraps it) copies a live database incrementally, page by page, safe against
   concurrent writes. Use it for larger DBs where you want low-impact incremental copies.
   ([sqlite.org/backup.html](https://www.sqlite.org/backup.html))
   (Note: I confirmed the backup *API* on sqlite.org; the `.backup` *shell command* is the
   documented CLI wrapper around it — treat the shell-command detail as lightly sourced.)
3. **Litestream (continuous replication).** For near-continuous, point-of-failure recovery,
   Litestream "Continuously stream[s] SQLite changes to your preferred cloud storage or local
   files" and crucially "Runs as a separate process so you can integrate into existing
   applications with **no code changes**." Internally it works *with* WAL: it holds a
   long-running read transaction to prevent normal checkpoints, "continually copies over new
   WAL pages to a staging area called the shadow WAL and manually calls out to SQLite to
   perform checkpoints," organizing snapshots + contiguous WAL frames into "generations" for
   restore. It targets object storage (e.g. S3) or local files and restores to the point of
   failure. ([litestream.io](https://litestream.io/),
   [litestream.io/how-it-works](https://litestream.io/how-it-works/))

**Recommendation for a Mac mini:** run in WAL mode; take a periodic `VACUUM INTO` snapshot
(cron/launchd timer) for simple offsite copies, and add **Litestream** as a second launchd
service replicating to S3-compatible storage if you want continuous, low-RPO backups without
touching app code. (Synthesis; each mechanism is individually sourced above.)

---

## Where evidence was thin or inferred

- **`SetMaxOpenConns(1)`** for SQLite is ecosystem best practice, not an official Go/SQLite
  doc statement. Sourced to River's docs and practitioner writeups (secondary).
- **Driver performance ("~2x slower inserts")** comes from an independent benchmark
  (multiprocessio/DataStation), not an official source.
- **The full-page-vs-fragment handler pattern** (`HX-Request` branching + `ExecuteTemplate`)
  is synthesized from the htmx and html/template docs; neither doc spells out the exact Go
  handler idiom.
- **`launchctl bootstrap` vs `load`** and the `.backup` shell command are from man pages /
  community references, not Apple/SQLite tutorial prose (though the underlying backup *API*
  is officially documented).
- **`-ldflags="-s -w"` / `-trimpath`** build recipe is from secondary Go writeups; the
  `CGO_ENABLED`, `GOOS`, `GOARCH` variables are standard toolchain behavior.
- **"chi vs stdlib, when to choose"** framing and the overall stack recommendations are my
  synthesis of the cited primary facts.

---

## Sources (primary)

Go language & stdlib
- Organizing a Go module — https://go.dev/doc/modules/layout
- Routing Enhancements for Go 1.22 — https://go.dev/blog/routing-enhancements
- `cmd/go` internal directories — https://pkg.go.dev/cmd/go#hdr-Internal_Directories
- `html/template` package docs — https://pkg.go.dev/html/template

HTMX
- htmx Documentation — https://htmx.org/docs/
- htmx Reference (request/response headers) — https://htmx.org/reference/
- HX-Redirect response header — https://htmx.org/headers/hx-redirect/
- HX-Location response header — https://htmx.org/headers/hx-location/
- HX-Trigger response headers — https://htmx.org/headers/hx-trigger/

SQLite
- Write-Ahead Logging (WAL) — https://www.sqlite.org/wal.html
- VACUUM (incl. `INTO`) — https://www.sqlite.org/lang_vacuum.html
- Online Backup API — https://www.sqlite.org/backup.html

Go SQLite drivers & migrations
- modernc.org/sqlite (pure Go) — https://pkg.go.dev/modernc.org/sqlite
- mattn/go-sqlite3 (cgo) — https://github.com/mattn/go-sqlite3
- pressly/goose — https://github.com/pressly/goose
- golang-migrate/migrate — https://github.com/golang-migrate/migrate
- go-chi/chi — https://github.com/go-chi/chi

Deployment (Apple / Litestream)
- Apple: Creating Launch Daemons and Agents — https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html
- launchd.plist(5) man page — https://www.manpagez.com/man/5/launchd.plist/
- Litestream — https://litestream.io/
- Litestream: How it works — https://litestream.io/how-it-works/

Secondary (benchmarks / practitioner references, flagged inline)
- SQLite in Go with/without cgo (benchmark) — https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html
- multiprocessio/sqlite-cgo-no-cgo — https://github.com/multiprocessio/sqlite-cgo-no-cgo
- River: Using with SQLite — https://riverqueue.com/docs/sqlite
- SQLITE_BUSY despite timeout — https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/
- golang-standards/project-layout (not an official standard) — https://github.com/golang-standards/project-layout
