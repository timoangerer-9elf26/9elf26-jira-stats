//go:build smoke

// Package smoke holds end-to-end smoke tests: they build the real jira-stats
// binary, boot it against the built-in canned fake Jira (no credentials), and
// assert that every route actually serves. This is the "does it come up and
// respond" check — distinct from the in-process integration tests under
// internal/web, which exercise handlers directly.
//
// Guarded by the `smoke` build tag so it never runs in the default `go test`
// pass. Run it with `make smoke` (or `go test -tags smoke ./smoke/`).
package smoke

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildBinary compiles the dashboard into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jira-stats")
	cmd := exec.Command("go", "build", "-o", bin, "../cmd/jira-stats")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// freePort asks the OS for an unused TCP port and returns "127.0.0.1:PORT".
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// startDashboard builds and launches the binary with the fake Jira (no creds),
// waits until it serves, and returns its base URL. Cleanup stops the process.
func startDashboard(t *testing.T) string {
	t.Helper()
	return startDashboardEnv(t)
}

// startDashboardEnv is startDashboard with extra "KEY=VALUE" env entries
// appended (so callers can pin settings like REVIEW_NOW). Later entries win, so
// the extras override the defaults set here.
func startDashboardEnv(t *testing.T, extraEnv ...string) string {
	t.Helper()
	bin := buildBinary(t)
	addr := freePort(t)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"LISTEN_ADDR="+addr,
		"DB_PATH="+filepath.Join(t.TempDir(), "smoke.db"),
		"SYNC_INTERVAL=1s",
		// Deliberately unset JIRA_* so the binary falls back to the fake.
		"JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	if out, err := os.Create(filepath.Join(t.TempDir(), "server.log")); err == nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	base := "http://" + addr
	waitUntilServing(t, base)
	return base
}

// waitUntilServing polls the root route until it answers 200 or times out.
func waitUntilServing(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("dashboard did not start serving at %s within timeout", base)
}

// get fetches a path and returns its status code and body.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp.StatusCode, string(body)
}

// TestDashboardServesAllRoutes boots the real binary and checks every route
// answers 200, with a content marker on each of the three views and the static
// assets served from the embedded filesystem.
func TestDashboardServesAllRoutes(t *testing.T) {
	base := startDashboard(t)

	// Give the first (1s-interval) sync a moment to backfill the fake dataset.
	time.Sleep(1500 * time.Millisecond)

	cases := []struct {
		path   string
		marker string // substring that must appear in the body ("" = 200 only)
	}{
		{"/", "Now"},
		{"/board", "Board"},
		{"/daily", "Daily"},
		{"/weekly", "Weekly"},
		{"/velocity", "Velocity"},
		{"/now/board", ""},
		{"/weekly/results", ""},
		{"/daily/results", ""},
		{"/static/output.css", ""},
		{"/static/htmx.min.js", ""},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			code, body := get(t, base+tc.path)
			if code != http.StatusOK {
				t.Fatalf("GET %s: got status %d, want 200", tc.path, code)
			}
			if tc.marker != "" && !strings.Contains(body, tc.marker) {
				t.Fatalf("GET %s: body missing marker %q", tc.path, tc.marker)
			}
		})
	}
}

// TestReviewNowPinsDateViews boots the binary with REVIEW_NOW pinned to a fixed
// instant and asserts the date-bearing views resolve against it deterministically
// (independent of the wall clock this test runs on). The pinned instant
// 2026-07-15T12:00:00Z is 14:00 Europe/Berlin on Wednesday 15 Jul 2026, which
// sits in ISO week KW29 (Mon 13 Jul – Sun 19 Jul), so:
//   - /weekly (default Work week: Mon 00:00 → Sat 00:00 Berlin) echoes the
//     window "13 Jul – 17 Jul 2026" (Monday → Friday, the weekend excluded), and
//   - /velocity's latest bar is labelled KW29.
//
// Both labels are computed from `now` alone (not the synced tally), so the
// assertion holds without waiting on the background sync. /weekly renders the
// window because the canned fake has an active sprint (KW29).
func TestReviewNowPinsDateViews(t *testing.T) {
	base := startDashboardEnv(t, "REVIEW_NOW=2026-07-15T12:00:00Z")

	if code, body := get(t, base+"/weekly"); code != http.StatusOK {
		t.Fatalf("GET /weekly: got status %d, want 200", code)
	} else if want := "13 Jul – 17 Jul 2026"; !strings.Contains(body, want) {
		t.Fatalf("/weekly: body missing pinned work-week window %q", want)
	}

	if code, body := get(t, base+"/velocity"); code != http.StatusOK {
		t.Fatalf("GET /velocity: got status %d, want 200", code)
	} else if want := `data-week="KW29"`; !strings.Contains(body, want) {
		t.Fatalf("/velocity: body missing pinned week label %q", want)
	}
}

// TestStaticAssetsAreEmbedded confirms the CSS and JS are served from the
// binary itself (non-empty, correct-ish content types), i.e. no CDN needed.
func TestStaticAssetsAreEmbedded(t *testing.T) {
	base := startDashboard(t)

	for _, path := range []string{"/static/output.css", "/static/htmx.min.js"} {
		code, body := get(t, base+path)
		if code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, code)
		}
		if len(body) == 0 {
			t.Fatalf("GET %s: empty body (asset not embedded?)", path)
		}
	}
	// The rendered page must reference the embedded asset, never a CDN.
	_, home := get(t, base+"/")
	if strings.Contains(home, "unpkg.com") || strings.Contains(home, "cdn.") {
		t.Fatalf("home page references a CDN; assets should be embedded and served from /static")
	}
	if !strings.Contains(home, "/static/") {
		t.Fatalf("home page does not reference /static assets")
	}
	fmt.Fprintln(os.Stderr, "smoke: embedded assets served OK")
}
