TAILWIND_INPUT  := internal/web/assets/input.css
TAILWIND_OUTPUT := internal/web/assets/output.css

# Release version stamped into the binary via -ldflags (docs/adr/0006). CI's
# release job overrides VERSION with the CalVer tag + short SHA; a bare local
# build derives a git-based identity, falling back to "dev" outside a checkout.
# A plain `go build` (no make) is intentionally left unstamped and reports the
# "dev" default from internal/version.
MODULE  := github.com/timoangerer-9elf26/9elf26-jira-stats
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X '$(MODULE)/internal/version.Version=$(VERSION)'

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-8s %s\n", $$1, $$2}'

# Regenerate the committed Tailwind stylesheet from the templates. npm ci makes
# this work from a clean checkout and pins the build through package-lock.json.
# Node is only needed to build CSS, never to `go build`. The generated output.css
# is committed and embedded; run `make css` after changing template classes.
.PHONY: css
css: ## Install pinned Tailwind tooling and rebuild the committed stylesheet.
	npm ci
	./node_modules/.bin/tailwindcss -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --minify

# Equivalent to `make css` via the //go:generate directive in internal/web.
.PHONY: generate
generate: ## Run go generate (rebuilds Tailwind CSS via go:generate).
	go generate ./...

# Build the single static binary. output.css and htmx are embedded, so this
# needs no Node and produces a CGO-free binary at bin/jira-stats.
.PHONY: build
build: ## Build the single static binary at bin/jira-stats.
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/jira-stats ./cmd/jira-stats

# Cross-compile the release binary for linux/arm64 (AWS Graviton / t4g). Pure-Go
# SQLite means CGO_ENABLED=0 cross-compiles cleanly with no C toolchain, and the
# templates/CSS/HTMX are embedded, so this produces a self-contained aarch64
# binary at bin/jira-stats-linux-arm64. `make build` (host arch) is unaffected.
.PHONY: build-arm64
build-arm64: ## Cross-compile the linux/arm64 release binary (bin/jira-stats-linux-arm64).
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/jira-stats-linux-arm64 ./cmd/jira-stats

.PHONY: test
test: ## Run the unit/integration test suite.
	go test ./...

# Smoke tests build the real binary, boot it against the built-in fake Jira,
# and assert every route serves. Guarded by the `smoke` build tag so they stay
# out of `make test`. Requires Go only (no running services, no credentials).
.PHONY: smoke
smoke: ## Build the binary and run end-to-end smoke tests.
	go test -tags smoke -count=1 ./smoke/

# The pre-ship gate: unit/integration tests plus smoke tests. Run this in CI /
# before deploying.
.PHONY: check
check: test smoke ## Run all tests + smoke tests (CI / pre-deploy gate).

.PHONY: run
run: ## Run the dashboard locally (falls back to fake Jira without creds).
	go run ./cmd/jira-stats

# Ephemeral, per-review launcher: builds the binary, picks a free port, boots it
# against the fake backend with a pinned clock, and records tmp/review/{url,pid,log}.
# All logic lives in the scripts (see docs/adr/0001-...); these are thin wrappers.
.PHONY: review-up
review-up: ## Boot an ephemeral review instance; writes tmp/review/{url,pid,log}.
	@scripts/review-up.sh

.PHONY: review-down
review-down: ## Stop the review instance and remove its temp DB + tmp/review state.
	@scripts/review-down.sh

# Hot-reload dev loop: rebuilds and restarts on every .go/.html/.css save via
# air (config in .air.toml). Templates/CSS are embedded, so air's rebuild is
# what makes edits show up; pair with the Now board's 30s browser poll.
# Install once: go install github.com/air-verse/air@latest
.PHONY: dev
dev: ## Run with hot reload (rebuilds on save; requires air).
	@command -v air >/dev/null 2>&1 || { \
		echo "air not found. Install it with:"; \
		echo "  go install github.com/air-verse/air@latest"; \
		exit 1; \
	}
	air
