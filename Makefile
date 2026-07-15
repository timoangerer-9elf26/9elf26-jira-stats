TAILWIND_INPUT  := internal/web/assets/input.css
TAILWIND_OUTPUT := internal/web/assets/output.css

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-8s %s\n", $$1, $$2}'

# Regenerate the committed Tailwind stylesheet from the templates. Uses the
# Tailwind v4 CLI via npx (Node only needed to build CSS, never to `go build`).
# The generated output.css is committed and embedded, so `make build` does not
# depend on this; run `make css` after changing template classes.
.PHONY: css
css: ## Rebuild the committed Tailwind stylesheet (requires npx).
	npx @tailwindcss/cli -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --minify

# Equivalent to `make css` via the //go:generate directive in internal/web.
.PHONY: generate
generate: ## Run go generate (rebuilds Tailwind CSS via go:generate).
	go generate ./...

# Build the single static binary. output.css and htmx are embedded, so this
# needs no Node and produces a CGO-free binary at bin/jira-stats.
.PHONY: build
build: ## Build the single static binary at bin/jira-stats.
	CGO_ENABLED=0 go build -o bin/jira-stats ./cmd/jira-stats

.PHONY: test
test: ## Run the full test suite.
	go test ./...

.PHONY: run
run: ## Run the dashboard locally (falls back to fake Jira without creds).
	go run ./cmd/jira-stats
