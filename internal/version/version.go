// Package version exposes the build identity stamped into the binary at
// release time. It answers "which build is live?" — surfaced by the /version
// endpoint and the UI footer, and asserted by the deploy health check
// (docs/adr/0006).
//
// This is the Release version (a versioned build artifact), NOT the Jira
// "Released / Deployed" ticket status — see the disambiguation note in
// CONTEXT.md.
package version

// Version is the build identity: the CalVer release tag plus the git short SHA
// (e.g. "v2026.07.23.142 (a1b2c3d)"). The release build overrides it via
//
//	-ldflags "-X github.com/timoangerer-9elf26/9elf26-jira-stats/internal/version.Version=..."
//
// (wired through the Makefile). It defaults to "dev" so an unstamped local
// build (a plain `go build`) reports a clear placeholder rather than an empty
// string.
var Version = "dev"
