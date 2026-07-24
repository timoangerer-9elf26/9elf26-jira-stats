package web_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// TestRootRedirectsToSprint asserts the root path is a redirect to the Sprint
// view (the Now view was removed), not a page of its own.
func TestRootRedirectsToSprint(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't follow, so we can inspect the redirect
	}}
	resp, err := client.Get(app.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Fatalf("GET /: status %d, want a 3xx redirect", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/sprint" {
		t.Fatalf("GET /: Location = %q, want /sprint", loc)
	}
}

func TestServesEmbeddedAssetsWithoutCDN(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	page := get(t, app.URL+"/sprint")
	if strings.Contains(page, "unpkg.com") || strings.Contains(page, "cdn.") {
		t.Fatalf("page references a CDN; assets must be embedded and self-served:\n%s", page)
	}

	css := get(t, app.URL+"/static/output.css")
	if !strings.Contains(css, "tabular-nums") {
		t.Fatalf("output.css did not contain expected utility class")
	}

	js := get(t, app.URL+"/static/htmx.min.js")
	if !strings.Contains(js, "htmx") {
		t.Fatalf("htmx.min.js was not served from embedded assets")
	}
}

// assertOrder fails unless each needle appears in body in the given order.
// TestVersionEndpointReportsInjectedValue asserts GET /version serves the full
// injected build identity unauthenticated (docs/adr/0006 health check), while
// the nav marker surfaces the CalVer tag alone — the git short SHA is trimmed
// for display only (#164).
func TestVersionEndpointReportsInjectedValue(t *testing.T) {
	const (
		want    = "v2026.07.23.142 (a1b2c3d)"
		wantTag = "v2026.07.23.142"
	)
	app := newTestApp(t, jira.NewFakeClient(), web.WithVersion(want))

	resp, err := http.Get(app.URL + "/version")
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /version: status %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /version body: %v", err)
	}
	if got := string(body); got != want {
		t.Fatalf("GET /version body = %q, want %q", got, want)
	}

	page := get(t, app.URL+"/sprint")
	if !strings.Contains(page, `data-testid="version-marker"`) {
		t.Fatalf("version-marker not found in /sprint page:\n%s", page)
	}
	if !strings.Contains(page, wantTag) {
		t.Fatalf("version marker %q not found in /sprint page:\n%s", wantTag, page)
	}
	if strings.Contains(page, "(a1b2c3d)") {
		t.Fatalf("version marker should not show the git SHA, but /sprint page contains it:\n%s", page)
	}
}

func assertOrder(t *testing.T, body string, needles ...string) {
	t.Helper()
	prev := -1
	for _, n := range needles {
		i := strings.Index(body, n)
		if i < 0 {
			t.Fatalf("expected %q in body", n)
		}
		if i < prev {
			t.Fatalf("expected %q to appear after the previous column (order wrong)", n)
		}
		prev = i
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
