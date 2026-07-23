---
status: accepted
---

# Automated release + deploy pipeline (CI tags, releases, and deploys on merge to main)

Every merge to `main` now automatically produces a **release** (a versioned,
tagged, immutable build artifact) and **deploys** it to the single production
instance — no manual `./scripts/deploy.sh` from a laptop. This supersedes the
v1 decision (spec #124) that CI/CD was out of scope: v1 is shipped, and the
manual deploy left no answer to "which build is live?". The pipeline runs as two
jobs (`release`, then `deploy`) appended to the existing `ci.yml`, gated behind
the existing `build-and-test` job.

Two terms this ADR pins down (distinct from the Jira **"Released / Deployed"**
*ticket status* in `CONTEXT.md` — see the disambiguation note there):

- **Release** — an immutable, versioned build artifact of the app: a git tag, a
  GitHub Release, and the stamped arm64 binary. Produced once per merge to `main`.
- **Deploy** — shipping a *released* artifact to the production instance and
  restarting the service. Consumes a Release; never rebuilds.

## The non-obvious decisions

### CI authenticates to AWS via GitHub OIDC, not stored keys

The deploy job assumes a purpose-built IAM role in the internal-tooling account
(`214519213070`) via **GitHub OIDC federation** — no long-lived credentials are
stored in the repo. An IAM OIDC provider for `token.actions.githubusercontent.com`
plus a role whose trust policy is scoped to *this repo on `main`* (and the
rollback `workflow_dispatch`) is created one-time. The role's permissions are
scoped to exactly what a deploy needs and nothing more: `s3:GetObject`/`PutObject`
on the `jira-stats-artifacts-214519213070` bucket, `ssm:SendCommand` to the one
instance (`i-0220fc1a6bee863d6`) and the `AWS-RunShellScript` document, and
`ssm:GetCommandInvocation`.

- *Alternative rejected — IAM user access keys as GitHub secrets:* simpler to set
  up, but puts long-lived credentials in GitHub (broader blast radius on leak,
  manual rotation). OIDC mints a short-lived token per run and stores nothing.
- Note this is a **narrower** path than the human deploy, which assumes the
  broad `OrganizationAccountAccessRole` from the management account. CI never
  goes through the management account.

### CalVer versioning — no semver, no bump logic

Tags are `vYYYY.MM.DD.<github.run_number>` (e.g. `v2026.07.23.142`). The app has
**no external consumers** pinning a dependency on it, so semantic-versioning
semantics (major/minor/patch) carry no information. CalVer is auto-generated with
zero human decision, unique even for multiple merges the same day, and sorts by
recency. The binary is stamped with the tag **and** the git SHA via `-ldflags`.

- *Alternative rejected — semver auto-bumped from conventional commits:* more
  moving parts (commit-message parsing, a bumping tool) to encode information
  nobody consumes.

### Continuous deployment, gated on green tests and a post-deploy version check

There is one environment (the prod instance), so a merge to `main` deploys to
production automatically once `build-and-test` is green. This is proportionate:
an internal tool, a rebuildable SQLite projection (a bad deploy loses no data),
and a fast rollback. Two guardrails keep it honest:

1. Deploy `needs` the test job — a red build can never deploy.
2. Deploy ends with a **health check**: it hits `https://jira-stats.in.9elf26.ai/version`
   and asserts the returned version equals the tag it just built. This proves the
   full path (cert + Caddy + app) *and* that the **new** binary — not a silently
   unchanged old one — is live.

- *Alternative rejected — manual promote gate (GitHub Environment approval):*
  safer, but adds a human click to every deploy, cutting against "both done in
  CI". Adding an Environment approval later is a one-line change if wanted.

### Build once, deploy that exact artifact

The `release` job builds the stamped binary **once**; that identical file is what
is tagged, attached to the Release, uploaded to S3, and shipped to the instance.
"What we tested = what we released = what is running." `scripts/deploy.sh` is
refactored so its build step becomes "consume the released artifact" (and it
accepts a version/tag), rather than building locally.

### Retention: tags forever, binaries pruned to the last 5

Git tags are kept **permanently** — they are the cheap, complete provenance record
("tag → commit"). The heavy artifacts (GitHub Release assets and the ~19 MB S3
binaries) are **pruned to the most recent 5** at the end of each successful deploy.
Anything older can be rebuilt from its tag if ever truly needed. This matches the
intent: keep enough to roll back a few steps, don't hoard binaries.

### Rollback is a one-click redeploy of a prior tag

The deploy job is also triggerable via **`workflow_dispatch` with a `tag` input**,
which redeploys that exact prior artifact from S3 (~30 s, no rebuild) — the reason
the last-5 window exists. Forward-fix (revert the bad PR → merge → normal pipeline)
remains available for non-urgent cases.

### Deploys are serialized

The deploy job uses a `deploy-prod` concurrency group with
`cancel-in-progress: false`, so two merges landing seconds apart **queue** rather
than overlap — no half-finished SSM push getting clobbered, and the later commit
ends up live.

## Consequences

- **A new AWS trust relationship exists**: GitHub Actions on this repo can deploy
  to the tooling account. Scoped tightly (three actions, one bucket, one instance),
  but it is real standing access that must be revoked if the repo is compromised.
- **`main` is now directly wired to production.** A merge is a deploy. The
  `build-and-test` gate and the post-deploy health check are the safety net; branch
  protection on `main` (already in place) is what keeps unreviewed code out.
- **The binary now carries a version** (`/version` endpoint + a small UI footer
  marker), so "what's live?" is answerable for the first time.
- **The manual `./scripts/deploy.sh` still works** as a break-glass path, but the
  normal path is CI.
