# Development workflow

How changes get from a working tree onto `main` in this repo. Applies to agents and humans alike.

## Branch → PR → merge

New work does **not** go straight to `main`. Each **bigger feature or set of bug fixes** gets:

1. **Its own branch** off `main` (e.g. `feature/…`, `fix/…`, `chore/…`, `docs/…`).
2. **A PR against `main`** — `gh pr create --base main`. Link the GitHub issues it addresses (the issue tracker is GitHub — see [issue-tracker.md](./issue-tracker.md)).
3. **A squash merge**, deleting the branch after (`gh pr merge <n> --squash --delete-branch`).

PRs exist mainly for **traceability** — one reviewable unit per feature — not as a human-review gate. The agent driving the work **manages its own merges**: open the PR, confirm it's green, merge it.

Group small, unrelated one-offs into a single PR rather than making a PR per trivial change. Docs-only or tooling-only changes still follow the same flow.

## The gate

A PR must be green before merging. CI (`.github/workflows/ci.yml`, job **`build-and-test`**) runs the full suite — gofmt, `go vet`, build, `go test ./...`, smoke — on every push to `main` and every PR, and cancels superseded runs on a PR (`concurrency` with `cancel-in-progress`). Run the same suite locally before pushing:

```sh
make check      # unit/integration tests + smoke tests
```

**Enforcement status: convention, not yet GitHub-enforced.** Making `build-and-test` a *required* status check (so GitHub refuses to merge a red or not-yet-reported PR) needs a repository ruleset or branch protection — and GitHub gates that behind **GitHub Pro for a private repo, or making the repo public** (the API returns `403 "Upgrade to GitHub Pro or make this repository public"`). This repo is currently private on the free plan, so the gate is enforced by **discipline**: don't merge red; the merging agent confirms CI/`make check` is green first.

To turn on real enforcement once the repo is on a supporting plan (Pro) or made public, apply this ruleset (loose mode, no bypass so it applies to everyone including the maintainer):

```sh
gh api -X POST repos/timoangerer-9elf26/9elf26-jira-stats/rulesets --input - <<'JSON'
{
  "name": "require-ci-green",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    { "type": "required_status_checks",
      "parameters": {
        "required_status_checks": [ { "context": "build-and-test" } ],
        "strict_required_status_checks_policy": false
      } }
  ]
}
JSON
```

Full rationale and sharp edges (e.g. never add a `paths:` filter to a required workflow — it would leave the check `Pending` and wedge every merge) are in [`../research/pr-merge-gate-ci.md`](../research/pr-merge-gate-ci.md).

## Commit messages

End commit messages with:

```
Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
```

End PR bodies with the Claude Code attribution line.

## Worktree caveat

This repo is often worked on from a git **worktree** while `main` is checked out in the primary clone. `gh pr merge` merges on the remote fine, but its post-merge step that switches the *local* checkout to `main` fails with `'main' is already used by worktree …`. That error is harmless — the merge already happened. After it:

1. Confirm the merge landed: `gh pr view <n> --json state,mergeCommit`.
2. Bring the working branch up to date: `git fetch origin && git merge --ff-only origin/main`.
3. Delete the merged branch locally and (if it survived) remotely: `git push origin --delete <branch>`.

## History note

The initial v1 batch (16 commits) was pushed directly to `main` at the maintainer's request. The PR-per-feature rule applies to everything after that.
