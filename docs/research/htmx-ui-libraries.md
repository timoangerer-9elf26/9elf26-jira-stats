# UI helper/component libraries for HTMX + Go (html/template) + SQLite

**Context:** Small internal Jira sprint-stats dashboard. Server-rendered (Go `html/template`), HTMX for interactivity, SQLite. Self-hosted, low-traffic, single-binary goal, minimal build tooling.

**Bottom line up front:** Yes, Tailwind is usable with this stack and does *not* clash with HTMX's server-rendered-fragments model — but it introduces a build step. For a single-binary, minimal-tooling internal dashboard, a classless CSS file (Pico CSS or Simple.css) embedded via `go:embed` is the lower-friction default; reach for Tailwind only if you want fine-grained utility control and are willing to run its standalone CLI. See [Recommendation](#5-recommendation-for-this-stack).

---

## 1. Is Tailwind suitable with HTMX + Go server-rendered templates?

### How Tailwind works without a JS framework

Tailwind is a build-time tool, not a runtime one. It scans your source files for class names as **plain text** and generates a static CSS file containing only the utilities you actually used:

> "Tailwind works by scanning all of your HTML files, JavaScript components, and any other templates for class names, generating the corresponding styles and then writing them to a static CSS file." ([Tailwind CLI docs](https://tailwindcss.com/docs/installation/tailwind-cli))

Because detection is pure text-token scanning, it is framework-agnostic — it does not need React/Vue/etc. Tailwind "looks for tokens that match valid class name characters, then attempts to generate CSS for them" and "discards tokens that don't map to known utilities" ([Detecting classes in source files](https://tailwindcss.com/docs/detecting-classes-in-source-files)).

**Important constraint for Go templates:** class names must appear as *complete strings* in the source. Constructing them dynamically (e.g. `bg-{{.Color}}-600`) will not be detected. The docs state this explicitly for JSX and it applies equally to Go templates: don't split class names; map values to full static class names, or use `@source inline(...)` safelisting ([Detecting classes in source files](https://tailwindcss.com/docs/detecting-classes-in-source-files)).

### Content detection and Go template files

Tailwind v4 has **automatic content detection** — it scans all project files by default, excluding only: files in `.gitignore`, `node_modules`, binary files (images/video/zip), CSS files, and lockfiles ([Detecting classes in source files](https://tailwindcss.com/docs/detecting-classes-in-source-files)). Go template files (`.html`, `.tmpl`, `.gohtml`) are therefore scanned by default as long as they are not gitignored. You can also register extra locations with `@source "..."` or set a base path with `@import "tailwindcss" source("../src")`.

### The v4 build model

Tailwind CSS v4 is a ground-up rewrite ("Oxide" engine, compiled in Rust). Key changes relevant here:

- Configuration moved from `tailwind.config.js` into CSS via the `@theme` directive; the entry CSS is just `@import "tailwindcss";` ([Tailwind CSS v4.0 announcement](https://tailwindcss.com/blog/tailwindcss-v4)).
- It dropped the PostCSS dependency in favor of Lightning CSS and ships a first-party Vite plugin ([Tailwind CSS v4.0 announcement](https://tailwindcss.com/blog/tailwindcss-v4)).

The build workflow with the CLI:

```bash
# watch during development
tailwindcss -i ./input.css -o ./static/output.css --watch
# minified production build
tailwindcss -i ./input.css -o ./static/output.css --minify
```
([Tailwind CLI docs](https://tailwindcss.com/docs/installation/tailwind-cli))

### The standalone CLI (single binary, no Node)

This is the key fact for the minimal-tooling goal. Tailwind ships **a self-contained executable that needs no Node.js or npm**:

> "a new standalone CLI build that gives you the full power of Tailwind CLI in a self-contained executable — no Node.js or npm required." ([Standalone CLI blog post](https://tailwindcss.com/blog/standalone-cli))

- Distributed as a single binary per platform via GitHub releases (Linux x64/arm64 glibc+musl, macOS x64/arm64, Windows x64) ([Standalone CLI blog post](https://tailwindcss.com/blog/standalone-cli)).
- Built with Vercel's `pkg`, which "bundles all of the parts your project needs right into the executable itself" ([Standalone CLI blog post](https://tailwindcss.com/blog/standalone-cli)).
- Caveat from Tailwind itself: for npm-based projects the npm version is preferred because it's "simpler to update" and has "smaller file size" — the standalone binary is large (community reports put it around ~80 MB; treat that figure as approximate/secondary).

Install example (macOS arm64):
```bash
curl -sLO https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-macos-arm64
chmod +x tailwindcss-macos-arm64 && mv tailwindcss-macos-arm64 tailwindcss
```
([Standalone CLI blog post](https://tailwindcss.com/blog/standalone-cli))

### Does it clash with HTMX?

No. HTMX swaps server-rendered HTML fragments into the DOM. Tailwind classes are baked into the class *attributes* of that HTML and resolved by the pre-built static stylesheet that is already loaded on the page. When HTMX injects a new fragment containing `class="px-2 py-1 bg-blue-500"`, the browser applies the already-present CSS — no re-build or JS runtime is involved at swap time. The only requirement is that every utility used in any fragment was present in the source at build time so it made it into `output.css` (this is the "complete class names" constraint above, plus safelisting for anything generated dynamically).

### Build-step tradeoff vs. the single-binary goal

- **Against:** Tailwind requires a separate build/watch process to regenerate `output.css`. That is one more moving part than "just link a CSS file," even with the standalone binary (which is itself a large extra artifact to keep in the toolchain).
- **For / mitigation:** The *output* is a plain static `.css` file. That file can be committed and then **embedded into the Go binary with `go:embed`**, so the shipped artifact is still a single binary. `embed.FS` implements `io.FS` and works with `http.FileServer` / `template.ParseFS`, giving genuine single-binary deployment ("you ship one file… no window where you have a new binary running against old templates") ([Go `embed` package](https://pkg.go.dev/embed)). The build step stays at *development* time; runtime remains a single binary.

So Tailwind is compatible with the single-binary *deployment* goal, but not with the "no build tooling at all" goal — you accept a dev-time CSS build in exchange for utility-class control.

---

## 2. Classless / minimal CSS options (no build step)

These are the zero-tooling alternatives: a single `.css` file you link (or embed with `go:embed`) that styles semantic HTML directly. Ideal when you write plain `<table>`, `<nav>`, `<article>` and want it to look decent with no classes.

| Framework | Classless? | Size (approx) | Build step | Notes |
|---|---|---|---|---|
| **Pico CSS** | Class-light + optional fully classless build | small; 130+ CSS vars | None (drop-in CSS) | "Minimal CSS Framework for semantic HTML" |
| **Simple.css** | Mostly classless | ~10 KB minified | None | Typography/readability focus |
| **water.css** | Fully classless | tiny (advertised) | None | Auto/dark/light themes |
| **MVP.css** | Fully classless | ~10 KB | None | Built for quick MVPs |
| **Bulma** | Class-based (not classless) | single CSS file | None (Sass optional) | Component/utility classes, no JS |

### Pico CSS
"Minimal CSS Framework for semantic HTML." Styles HTML tags directly using very few classes, "works seamlessly without dependencies, package managers, external files, or JavaScript," and offers a dedicated **class-less version** for pure semantic HTML. Customizable via 130+ CSS variables and ships color themes and modular components ([picocss.com](https://picocss.com/), [Pico class-less docs](https://picocss.com/docs/classless)). Good middle ground: nicer defaults than a raw reset, plus themeable via CSS vars, still no build step.

### Simple.css
"Mostly classless" — styles semantic HTML out of the box, with optional helper classes. Minified CSS "around 10KB," includes a good local font stack, typographic defaults, and automatic dark mode via `prefers-color-scheme` ([simplecss.org](https://simplecss.org/)). Best for content/typography-heavy pages.

### water.css
Fully classless: lists "No classes" as a core feature — "You just include it in your `<head>` and forget about it." Drop-in via CDN, with automatic/dark/light theme variants (`prefers-color-scheme`) ([water.css GitHub](https://github.com/kognise/water.css)). Smallest/simplest; described as making "simple websites just a little nicer."

### MVP.css
Fully classless, ~10 KB, drop-in via CDN (`<link rel="stylesheet" href="https://unpkg.com/mvp.css">`), "No class names, no frameworks, just semantic HTML and you're done." Optimized for shipping MVPs fast ([andybrewer.github.io/mvp](https://andybrewer.github.io/mvp/)).

### Bulma (for contrast — class-based)
Not classless. Bulma is "a free, open source framework that provides ready-to-use frontend components" using modifier classes like `button is-primary is-large`. It is **CSS-only (no JavaScript)** and offers a downloadable single CSS file, though deep customization uses Sass variables (which would reintroduce a build step) ([bulma.io](https://bulma.io/)).

### When to pick a classless framework over Tailwind (for this tool)
- You want **zero build tooling** — link/embed one `.css` and never think about it again.
- The UI is mostly semantic content: tables of ticket sizes/points, counts, progress views — exactly what classless frameworks style well by default.
- You value being able to hand-write templates without memorizing utility names.
- Tradeoff: less pixel-level control and harder to build a bespoke design; you're accepting the framework's opinions. For an internal dashboard that's usually fine.

---

## 3. HTMX-friendly component / utility ecosystems

### Official htmx extensions
In htmx 2, the team slimmed the core and maintains a small set of **core extensions** (many older extensions were moved to community maintenance). The currently team-maintained core extensions are ([htmx.org/extensions](https://htmx.org/extensions/)):

- **head-support** — merges `<head>` tag info (styles, etc.) across htmx requests.
- **htmx-1-compat** — rolls htmx 2 behavior back to htmx 1 defaults.
- **idiomorph** — a morphing swap strategy (created by the htmx team) that preserves DOM/focus/state across swaps.
- **preload** — loads HTML fragments into browser cache before they're requested.
- **response-targets** — swap different targets based on HTTP response codes.
- **sse** — Server-Sent Events directly from HTML.
- **ws** — bi-directional WebSockets from HTML.

Extensions are opt-in via an attribute (e.g. `hx-ext="preload"`) and the mechanism exists to "take pressure off the core library" ([htmx.org/extensions](https://htmx.org/extensions/)). For a low-traffic dashboard you likely need none of these; `idiomorph` is the one worth knowing about if fragment swaps ever clobber client-side state (see §4).

### _hyperscript (htmx's companion)
`_hyperscript` is "a scripting language for the web" ("Scripting HTML without tears"), from the same org as htmx (bigskysoftware). It's event-oriented ("events are first class citizens"), reads like English, embeds in HTML attributes, is ~38 KB with zero dependencies, and is included via a single script tag ([hyperscript.org](https://hyperscript.org/)). It's the natural companion for small inline behaviors (toggles, class changes) when you want htmx's philosophy extended to scripting.

### templ (a-h/templ) — typed Go components
`templ` is a **component-based HTML templating system for Go** where "Components are compiled into performant Go code," you write standard Go `if`/`switch`/`for` inside templates, and it "ships with IDE autocompletion" ([templ.guide](https://templ.guide/)).

**Relationship to `html/template`:** templ is an *alternative* templating layer, not a wrapper around `html/template`. Whereas `html/template` parses template text (at runtime, or from an embedded FS) and is checked dynamically, templ has its own `.templ` syntax that a code generator (`templ generate`, installed via `go install github.com/a-h/templ/cmd/templ@latest`) compiles into `.go` source. The payoff is **compile-time type checking** of the data passed to each component and editor autocomplete — errors surface at build time instead of at render time. (Note: templ's own homepage does not directly frame itself as a comparison to `html/template`; the "compile-time vs runtime checking" contrast here is inferred from templ compiling to Go code vs `html/template` parsing template text. Flagged as partly inferred.)

**Single-binary fit:** Because templ compiles templates into Go code, the templates become part of the compiled binary automatically — there are no `.tmpl` files to ship or embed. That is arguably *more* single-binary-friendly than `html/template` (though `html/template` + `go:embed` also yields a single binary). The tradeoff is an extra codegen step in the dev loop (`templ generate`), which cuts against the "minimal tooling" goal.

**htmx integration:** templ's docs show the standard htmx fragment pattern — a component renders HTML that carries `hx-post` / `hx-select` / `hx-swap` attributes so htmx can "selectively replace content within a web page" rather than doing full-page postbacks ([templ + htmx docs](https://templ.guide/server-side-rendering/htmx/)). Rendering is just calling the component's `Render(ctx, w)` to write HTML to the `http.ResponseWriter`. This is the same model you'd use with `html/template` (execute a named template into the writer for a fragment).

---

## 4. The interactivity gap (what HTMX doesn't cover)

HTMX handles server round-trips: "making AJAX requests to the server, receiving HTML, and dynamically updating parts of the page." It does **not** manage local, ephemeral client state (dropdown open/closed, tab selection, client-side filtering without a server hit). Three idiomatic options:

- **Plain JS** — for one or two trivial toggles, a few lines of vanilla JS (or `hx-on:` inline handlers) is the lightest possible option, zero dependencies.
- **_hyperscript** — htmx's companion; keeps behaviors declarative and inline, same design philosophy, ~38 KB ([hyperscript.org](https://hyperscript.org/)).
- **Alpine.js** — a small declarative framework for "logic tied to local client state." The common pairing is: HTMX drives data from the backend, Alpine handles client-side enhancements (dropdowns, accordions, client-side filtering). (This division-of-labor framing comes from a secondary source, [InfoWorld](https://www.infoworld.com/article/3856520/htmx-and-alpine-js-how-to-combine-two-great-lean-front-ends.html) — flagged as secondary. Alpine's ~15 KB size figure there is also secondary/approximate.)

**Coexistence caveat (well-supported):** When HTMX swaps out DOM that contained an Alpine component, Alpine's local state can be lost because the nodes are replaced. The htmx project provides a compatibility path: the `hx-alpine-compat` / alpine-morph extension "provides a compatibility layer between htmx and Alpine.js, ensuring Alpine components are correctly initialized and preserved across htmx-driven DOM updates" ([hx-alpine-compat](https://four.htmx.org/extensions/hx-alpine-compat), [alpine-morph (htmx v1)](https://v1.htmx.org/extensions/alpine-morph/)). Using a morph-based swap (idiomorph) is the general mechanism to retain state across swaps.

For this dashboard, the interactivity is minimal (maybe a filter dropdown or collapse toggle), so the ordering is: **plain JS / `hx-on:` → _hyperscript → Alpine** only if client-state complexity actually grows. Adding Alpine also means adding the morph/compat consideration above.

---

## 5. Recommendation for THIS stack

For a single-binary, minimal-tooling, low-traffic internal Jira dashboard:

1. **CSS: start classless, embedded.** Use **Pico CSS** (best defaults + CSS-variable theming while staying build-free) or **Simple.css** (if it's table/typography-heavy). Vendor the single `.css` file and serve it via `go:embed` — zero build step, single binary ([picocss.com](https://picocss.com/), [Go `embed`](https://pkg.go.dev/embed)). This is the best fit for the stated goals.

2. **Adopt Tailwind only if** you find yourself fighting the classless framework for layout/spacing control. It is fully compatible with HTMX (classes live in the pre-built stylesheet; fragment swaps just use it) and its standalone CLI needs no Node ([Standalone CLI](https://tailwindcss.com/blog/standalone-cli)). You can still ship one binary by committing the built `output.css` and embedding it. But it *does* add a dev-time build/watch step and a large CLI binary to your toolchain — a real cost against the "minimal tooling" goal. Watch the "complete class names" rule in Go templates ([Detecting classes](https://tailwindcss.com/docs/detecting-classes-in-source-files)).

3. **Templating: `html/template` + `go:embed` is sufficient and lowest-tooling.** Consider **templ** only if you want compile-time-checked, autocompleted components and are comfortable adding the `templ generate` codegen step. Both reach the single-binary goal; templ gives type safety at the cost of tooling.

4. **Interactivity: don't add a framework yet.** Cover the small gaps with plain JS / htmx `hx-on:` handlers, graduating to `_hyperscript` (htmx's companion) and then Alpine.js only if client-side state genuinely grows — and if you add Alpine, plan for the alpine-morph/idiomorph state-preservation issue across swaps.

**Net:** classless CSS (Pico/Simple) + `html/template` + `go:embed` + htmx keeps you at true single-binary, near-zero build tooling. Tailwind and templ are each viable upgrades that trade a build step for more control/safety.

---

## Where evidence was thin or inferred
- **Tailwind standalone binary size (~80 MB):** community-reported/secondary; Tailwind's own docs only say the standalone build has "smaller file size" for the npm version and is a self-contained binary, without a precise size.
- **templ vs `html/template` "compile-time vs runtime" contrast:** inferred from templ compiling to Go code; templ's homepage does not directly benchmark itself against `html/template`.
- **Alpine.js sizes and the HTMX/Alpine "division of labor" framing:** from InfoWorld (secondary). The *state-preservation-across-swaps* problem and its fix are, by contrast, documented by htmx itself (primary).

---

## Sources
- Tailwind Standalone CLI (no Node.js): https://tailwindcss.com/blog/standalone-cli
- Tailwind CLI installation / build workflow: https://tailwindcss.com/docs/installation/tailwind-cli
- Tailwind CSS v4.0 (build-model rewrite): https://tailwindcss.com/blog/tailwindcss-v4
- Tailwind detecting classes in source files (`@source`, complete class names): https://tailwindcss.com/docs/detecting-classes-in-source-files
- Go `embed` package (single-binary embedding): https://pkg.go.dev/embed
- Pico CSS: https://picocss.com/ and class-less docs: https://picocss.com/docs/classless
- Simple.css: https://simplecss.org/
- water.css (GitHub): https://github.com/kognise/water.css
- MVP.css: https://andybrewer.github.io/mvp/
- Bulma: https://bulma.io/
- htmx extensions (core extensions list): https://htmx.org/extensions/
- _hyperscript: https://hyperscript.org/
- templ: https://templ.guide/ and htmx integration: https://templ.guide/server-side-rendering/htmx/
- htmx hx-alpine-compat extension: https://four.htmx.org/extensions/hx-alpine-compat
- htmx alpine-morph (v1): https://v1.htmx.org/extensions/alpine-morph/
- HTMX + Alpine.js division of labor (secondary, InfoWorld): https://www.infoworld.com/article/3856520/htmx-and-alpine-js-how-to-combine-two-great-lean-front-ends.html
