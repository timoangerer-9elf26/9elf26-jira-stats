# Turning the CI Workflow into a Real PR Merge Gate (and Keeping It Fast)

Research date: 2026-07-16. Scope: how to make GitHub *actually block* a PR merge on a
red CI run for this repo (`timoangerer-9elf26/9elf26-jira-stats`), plus how to keep that
"runs all tests" pipeline quick. Solo / agent-driven workflow, squash-merges,
PR-per-feature, small pure-Go suite.

This document follows every non-obvious claim back to a primary source (GitHub Docs,
the `actions/setup-go` README, GitHub REST/CLI docs). Where evidence is thin or inferred,
it is called out explicitly.

Two things the maintainer's ask ("a simple pipeline that runs before a PR can be merged —
quick, but runs all tests") bundles together, kept separate throughout:

1. **Enforcement** — making CI an actual *merge gate*, not a convention.
2. **Speed** — keeping the full suite fast.

---

## 0. Where this repo stands today (verified against the files)

- `.github/workflows/ci.yml`: one job named **`build-and-test`** on `ubuntu-latest`,
  triggered on `push: [main]` and `pull_request` (no `paths:` filter). Steps: gofmt check,
  `go vet`, `CGO_ENABLED=0 go build`, `go test ./...`, then smoke tests
  (`go test -tags smoke -count=1 ./smoke/`). Uses `actions/checkout@v4` and
  `actions/setup-go@v5` with `go-version: "1.26"`, `check-latest: true`.
- `docs/agents/workflow.md` §"The gate": *"A PR must be green before merging… Don't merge
  red."* This is the whole gate today — a **convention**. Nothing on GitHub blocks a red
  merge; `gh pr merge` will happily merge a PR whose checks failed or never ran.
- Module `github.com/timoangerer-9elf26/9elf26-jira-stats`, Go 1.26, pure-Go deps
  (`modernc.org/sqlite`, goose), single CGO-free binary. There **is** a committed
  `go.sum` (relevant to caching below).

The gap is purely **enforcement**: the pipeline exists and is green-on-PR; GitHub just
isn't told that green is *mandatory*.

---

## 1. Making CI actually block a merge

### 1.1 The mechanism: required status checks

GitHub blocks merges via **required status checks** attached to a branch, configured
either through a classic **branch protection rule** or a newer **repository ruleset**.
Both express the same idea: named checks that *must* pass before the base branch (`main`)
can be updated.

> "Required status checks ensure that all required CI tests are passing before
> collaborators can make changes to a branch or tag… all required status checks must pass
> before collaborators can merge changes into the branch or tag."
> — [Creating rulesets for a repository](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository)

**How a check gets selected as required:** you require a check *by its context name*. For
a GitHub Actions job, that context name is the **job name** — here, `build-and-test`. When
adding the requirement you can pin the expected source app, or accept "any source":

> "When you add a required status check, you can select an app that has recently set this
> check as the expected source of status updates… you can still manually verify the author
> of each status, listed in the merge box." — [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

**"Require branches to be up to date before merging" (strict mode):**

> "The branch **must** be up to date with the base branch before merging" (strict) vs.
> "The branch **does not** have to be up to date" (loose). Strict mode notes: "More builds
> may be required." — [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

Strict mode guarantees the code was tested against the *tip* of `main`, at the cost of
re-running CI whenever `main` moves under an open PR. For a solo repo where PRs rarely sit
open while `main` advances, strict mode is mostly harmless but adds re-run churn; see the
recommendation in §6.

### 1.2 Classic branch protection vs. rulesets

Rulesets are GitHub's newer, layerable system; classic branch protection is the older
"only one rule wins" model. GitHub itself steers you toward rulesets:

> "Only a single branch protection rule can apply at a time, which means it can be
> difficult to know which rule will apply when multiple versions of a rule target the same
> branch." (— followed by a pointer to "About rulesets")
> — [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

Both enforce required checks identically; the practical difference (rulesets layer and
compose, branch protection doesn't) is irrelevant at one-rule scale. Either works for this
repo. Rulesets are the forward-looking choice and are fully API-scriptable (§4).

### 1.3 The sharp edge: a required check that never reports blocks merge *forever*

This is the failure mode to understand before turning the gate on. A required check only
"passes" if it reports a terminal success-like state:

> "Required status checks must have a `successful`, `skipped`, or `neutral` status before
> collaborators can make changes to a protected branch."
> — [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

The trap is a required check that is *expected* but **never runs**, so it never reports —
it sits `Pending` and the PR is blocked indefinitely. The most common cause is workflow-level
**path/branch filtering**:

> "If a workflow is skipped due to path filtering, branch filtering or a commit message,
> then checks associated with that workflow will remain in a 'Pending' state. A pull
> request that requires those checks to be successful will be blocked from merging."
> — [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks)

Crucially, a *job* skipped by an `if:` conditional is **not** the same as a *workflow*
skipped by a `paths:` filter — the former reports success, the latter reports nothing:

> "If, however, a job within a workflow is skipped due to a conditional, it will report its
> status as 'Success'." — [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks)

For dependent jobs, GitHub's documented fix is `always()`:

> "use the `always()` conditional expression in addition to `needs`."
> — [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks)

**Relevance to this repo:** `ci.yml` has **no `paths:` filter** and a single
unconditional job, so this trap does not exist today. The actionable takeaway is
preventive: *do not add a `paths:`/`paths-ignore:` filter to `ci.yml` once `build-and-test`
is a required check* — a docs-only PR (or any PR that doesn't touch the filtered paths)
would then be un-mergeable. (The common community workaround — a second always-passing
workflow that reports the same check name for the filtered-out case — exists, but is *not*
in GitHub's primary troubleshooting doc, which instead advises simply not filtering
required workflows. Flagging that as community practice, not first-party guidance.)

One more documented subtlety — same-name check + status both count:

> "If you have a check and a status with the same name, and you select that name as a
> required status check, both the check and the status are required."
> — [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks)

Not an issue here (only one Actions check produces `build-and-test`), but worth knowing if
a second tool ever posts a status of the same name.

---

## 2. Merge queues (`merge_group`) — and why they're overkill here

A merge queue serializes and pre-tests merges so a busy branch never breaks:

> "A merge queue helps increase velocity by automating pull request merges into a busy
> branch and ensuring the branch is never broken by incompatible changes."
> — [Managing a merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)

It works by grouping a PR with the current tip (and anything ahead of it in the queue) and
running checks on that combined result before merging. If you adopt one, your workflow
**must** also trigger on the `merge_group` event or required checks never report for
queued PRs (the same "never reports → blocked" trap as §1.3):

> "You **must** use the `merge_group` event to trigger your GitHub Actions workflow when a
> pull request is added to a merge queue."
> — [Managing a merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)

GitHub scopes the feature explicitly to high-traffic branches:

> "Using a merge queue is particularly useful on branches that have a relatively high
> number of pull requests merging each day from many different users."
> — [Managing a merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)

**Recommendation for this repo:** skip it. A solo, PR-per-feature workflow has ~one PR in
flight at a time and no concurrent-merge race for the queue to solve. It adds a
`merge_group` trigger to maintain and buys nothing at this scale. (Strict "up to date
before merging" from §1.1 already covers the only real risk — merging code untested
against the latest `main` — without a queue.)

---

## 3. Keeping the pipeline fast for Go

### 3.1 `setup-go` caching — this repo already gets it

`actions/setup-go@v5` caches by default and keys off `go.sum`:

> "The `cache` input is optional, and caching is turned on by default." … "The action
> defaults to search for the dependency file — `go.sum` in the repository root, and uses
> its hash as a part of the cache key." — [actions/setup-go v5 README](https://raw.githubusercontent.com/actions/setup-go/v5/README.md)

It caches both the module download cache and the build cache ("Go modules and build
outputs"). Because this repo pins `@v5` **and has a committed `go.sum`**, `ci.yml` is
*already* getting module + build caching with zero extra config — no separate
`actions/cache` step is needed. (Confirmed against the current `ci.yml`, which does not set
`cache:` and therefore inherits the `true` default.)

> ⚠️ Version caveat if you ever bump to `@v6`: the default cache key changed. "By default,
> the cache key for Go modules is based on `go.mod`. To use `go.sum`, configure the
> `cache-dependency-path` input." — [actions/setup-go README (main)](https://raw.githubusercontent.com/actions/setup-go/main/README.md).
> On `@v5` you don't need to do anything; on `@v6` you'd add
> `cache-dependency-path: go.sum` to preserve the current keying. Staying on `@v5` is fine.

Minor note: `check-latest: true` makes `setup-go` resolve the newest patch of Go 1.26 on
every run instead of the toolcache-pinned one. That's a correctness/freshness choice, not a
caching one, and it doesn't defeat the module/build cache (which is keyed on `go.sum`, not
the Go patch version). Harmless to keep.

### 3.2 Cancel superseded runs with `concurrency`

When you push twice to the same PR, the first CI run is wasted work. `concurrency` with
`cancel-in-progress` kills the in-flight run when a newer commit arrives:

> The documented pull-request pattern:
> ```yaml
> concurrency:
>   group: ${{ github.workflow }}-${{ github.ref }}
>   cancel-in-progress: true
> ```
> "any previously in-progress or pending job will be canceled" within the current workflow.
> — [Using concurrency](https://docs.github.com/en/actions/using-jobs/using-concurrency)

This is the single cheapest speed/throughput win and it's currently **absent** from
`ci.yml`. Caveat: `github.ref` differs between the PR and the `push: main` run, so this
won't cancel a legitimate `main` build — good. (If you later want `main` builds to never
cancel each other while PR builds do, you can make `cancel-in-progress` an expression, but
that refinement isn't needed here.)

### 3.3 One job vs. splitting into parallel jobs

The suite here is small (unit/integration + a smoke boot). Splitting gofmt/vet/build/test/
smoke into separate parallel jobs would let them run concurrently, but each job pays its own
runner-spin-up + `checkout` + `setup-go` (cache restore) cost. For a suite that finishes in
a couple of minutes, that fixed per-job overhead typically **exceeds** the parallelism
saved, and it multiplies the number of check contexts you'd have to keep in sync with the
required-checks list. GitHub's docs don't prescribe a split either way; this is a
trade-off judgment, not a documented rule — flagging it as **my recommendation, not a
sourced claim**: keep it a single `build-and-test` job. Fewer moving parts, one required
context, and the steps are already fast because of the shared cache.

---

## 4. Configuring the gate via `gh` / API (no web UI required)

You can set the whole gate from the CLI — useful for an agent-driven repo.

### 4.1 Rulesets (recommended path)

`gh` has a **read-only** `ruleset` command group — good for verifying, not for creating:

> `gh ruleset` subcommands are `check`, `list`, `view` — "These commands allow you to view
> information about them." (no create/edit) — [gh ruleset manual](https://cli.github.com/manual/gh_ruleset)

To *create* a ruleset you POST via `gh api`:

> "**POST /repos/{owner}/{repo}/rulesets** creates a ruleset for a repository."
> — [REST API endpoints for rules](https://docs.github.com/en/rest/repos/rules)

The `required_status_checks` rule shape (field names quoted from the REST docs):

- `type`: `"required_status_checks"`
- `parameters.required_status_checks[]`: each has `context` — *"The status check context
  name that must be present on the commit"* — and optional `integration_id` — *"The optional
  integration ID that this status check must originate from"*.
- `parameters.strict_required_status_checks_policy`: *"Whether pull requests targeting a
  matching branch must be tested with the latest code"* (this is the "up to date before
  merging" toggle from §1.1).
- `conditions.ref_name.include`: target branches, e.g. `["refs/heads/main"]` (or the
  built-in `"~DEFAULT_BRANCH"`). — [Creating rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository)

Concrete command (the context `build-and-test` is this repo's job name; `enforcement:
active` makes it binding; `strict_...: false` = loose mode — flip to `true` for strict):

```sh
gh api -X POST repos/timoangerer-9elf26/9elf26-jira-stats/rulesets \
  --input - <<'JSON'
{
  "name": "require-ci-green",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    {
      "type": "required_status_checks",
      "parameters": {
        "required_status_checks": [ { "context": "build-and-test" } ],
        "strict_required_status_checks_policy": false
      }
    }
  ]
}
JSON
```

Verify afterwards with `gh ruleset list` / `gh ruleset view`.
*(Note: the exact JSON above is assembled from the documented field names; I have not
executed it against this repo. Field names are sourced; the composed body is my
construction — sanity-check the response.)*

### 4.2 Classic branch protection (alternative)

If you prefer classic protection, the endpoint is
`PUT /repos/{owner}/{repo}/branches/{branch}/protection`
([REST: protected branches](https://docs.github.com/en/rest/branches/branch-protection)),
with `required_status_checks.checks[].context = "build-and-test"` and
`required_status_checks.strict = true|false`. Rulesets (§4.1) are the newer, preferred
route; pick one, not both, to avoid two overlapping gates.

### 4.3 Solo-repo gotcha: who the rule applies to

By default a ruleset/branch protection applies to everyone including the owner. On a solo
repo that's what you want (the point is to stop *yourself/the agent* merging red). Just
don't add a bypass entry for your own account, or the gate becomes advisory again. (Inferred
from how bypass lists work; not quoting a specific line.)

---

## 5. Sanity-check of the existing `ci.yml`

Against current best practice, `ci.yml` is in good shape. Concretely:

- **Action versions** — `checkout@v4`, `setup-go@v5` are current-major and fine. No change
  needed; do **not** feel obliged to jump to `setup-go@v6` (see the cache-key caveat in
  §3.1).
- **Caching** — already active and correctly keyed on the committed `go.sum` via the
  `setup-go@v5` default. Nothing to add. ✅
- **No `paths:` filter** — correct for a would-be required check; keep it that way (§1.3).
- **Triggers** — `push: [main]` + `pull_request` is exactly right for a PR gate. (Add
  `merge_group` only if you ever adopt a queue, which §2 says you shouldn't.)
- **Gap: `concurrency`** — missing. Add the block from §3.2 to stop stale runs piling up on
  a PR that gets multiple pushes. This is the one concrete workflow change worth making.

I did not find any real problems to invent beyond that. The pipeline is already "quick, and
runs all tests"; it just isn't *enforced*.

---

## 6. Recommendation for THIS repo

The maintainer's two needs map cleanly:

**Enforcement (the actual gap) — do this:**

1. Create a **repository ruleset** targeting the default branch that requires the
   **`build-and-test`** status check, `enforcement: active` — via the `gh api` command in
   §4.1. This is the single most important change: it converts
   `workflow.md`'s "Don't merge red" convention into a mechanism GitHub enforces, so
   `gh pr merge` refuses a PR whose CI failed or hasn't reported.
2. Use **loose mode** (`strict_required_status_checks_policy: false`) to start. For a solo
   repo with ~one PR open at a time, requiring "up to date before merging" mostly adds
   re-run churn for little safety gain. Turn it on later only if you start seeing PRs merged
   green-but-stale.
3. **Do not** add a merge queue (§2) and **do not** add a `paths:` filter to `ci.yml`
   (§1.3) — both would, at this scale, either do nothing useful or actively wedge the gate.

**Speed — one change:**

4. Add a `concurrency` block to `ci.yml` (§3.2) so superseded PR runs are cancelled.
   Caching is already handled by `setup-go@v5`; keep the single-job layout.

**Docs follow-up (not code):** once the ruleset exists, `docs/agents/workflow.md` §"The
gate" should note that green is now *enforced by a branch ruleset*, not just convention —
so future agents know the merge will be *blocked*, not merely frowned upon.

Minimal path to a real gate: run the §4.1 `gh api` command, confirm with `gh ruleset list`,
open a throwaway PR with a deliberately failing test, and confirm `gh pr merge` is refused.
That's the whole enforcement story; the `concurrency` edit is the only worthwhile speed
tweak on top.

---

## Sources

- [About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- [Troubleshooting required status checks](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/troubleshooting-required-status-checks)
- [Managing a merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)
- [Creating rulesets for a repository](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository)
- [REST API endpoints for rules (rulesets)](https://docs.github.com/en/rest/repos/rules)
- [REST API endpoints for protected branches](https://docs.github.com/en/rest/branches/branch-protection)
- [gh ruleset manual](https://cli.github.com/manual/gh_ruleset)
- [actions/setup-go v5 README](https://raw.githubusercontent.com/actions/setup-go/v5/README.md)
- [actions/setup-go README (main / v6 note)](https://raw.githubusercontent.com/actions/setup-go/main/README.md)
- [Using concurrency](https://docs.github.com/en/actions/using-jobs/using-concurrency)
