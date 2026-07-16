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

A PR must be green before merging:

```sh
make check      # unit/integration tests + smoke tests
```

CI (`.github/workflows/ci.yml`) runs the same steps (gofmt, vet, build, `go test ./...`, smoke) on every push to `main` and every PR. Don't merge red.

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
