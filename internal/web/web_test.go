package web_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// TestIndexRendersTotalOpenPoints drives the whole vertical slice over HTTP:
// canned fake Jira -> one-shot sync -> temp SQLite -> real "/" handler, and
// asserts the rendered rollup number.
//
// Expected value from the canned dataset (open = status not Done, types
// Task/Bug/Story; points S=1,M=2,L=3):
//   DCAI-1 Story In Progress  L -> 3
//   DCAI-2 Task  Ready to Do  M -> 2
//   DCAI-3 Bug   In Progress  S -> 1
//   DCAI-4 Task  In Progress  (none) -> 0
//   DCAI-5 Story DONE         M -> excluded (Done)
//   DCAI-6 Epic  In Progress  L -> excluded (not a rollup type)
// Total open points = 6.
func TestIndexRendersTotalOpenPoints(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	body := get(t, app.URL+"/")

	if !strings.Contains(body, `data-testid="total-open-points">6<`) {
		t.Fatalf("expected total open points 6 in rendered page, got:\n%s", body)
	}
}

func TestIndexServesEmbeddedAssetsWithoutCDN(t *testing.T) {
	app := newTestApp(t, jira.NewFakeClient())

	page := get(t, app.URL+"/")
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
