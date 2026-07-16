# AGENTS.md

## Agent skills

### Issue tracker

Issues live in GitHub Issues (`gh` CLI), repo `timoangerer-9elf26/9elf26-jira-stats`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Development workflow

Each bigger feature or bug-fix set goes through its own branch → PR against `main` → squash-merge; the agent driving the work manages its own merges, and `make check` (tests + smoke) is the gate. See `docs/agents/workflow.md`.
