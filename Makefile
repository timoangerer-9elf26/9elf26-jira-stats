TAILWIND_INPUT  := internal/web/assets/input.css
TAILWIND_OUTPUT := internal/web/assets/output.css

# Regenerate the committed Tailwind stylesheet from the templates. Uses the
# Tailwind v4 CLI via npx (Node only needed to build CSS, never to `go build`).
.PHONY: css
css:
	npx @tailwindcss/cli -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --minify

.PHONY: build
build:
	CGO_ENABLED=0 go build -o bin/jira-stats ./cmd/jira-stats

.PHONY: test
test:
	go test ./...

.PHONY: run
run:
	go run ./cmd/jira-stats
