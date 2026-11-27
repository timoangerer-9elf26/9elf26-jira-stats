// Command jira-stats runs the sprint-stats dashboard: it opens the SQLite
// store, backfills the configured Jira project into it, and serves the
// dashboard over HTTP. All Jira credentials and settings come from the
// environment; no secrets live in the repo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/auth"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

// syncOverlap is the clock-skew window subtracted from last_sync when bounding
// the incremental query, so no issue changed near the boundary is missed.
const syncOverlap = 2 * time.Minute

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Load configuration from a local .env file if present, so settings can be
	// kept in one gitignored file instead of exported by hand. Real environment
	// variables always win over .env, and a missing .env is not an error.
	if err := godotenv.Load(); err == nil {
		log.Print("loaded configuration from .env")
	}

	addr := getenv("LISTEN_ADDR", ":8080")
	dbPath := getenv("DB_PATH", "jira-stats.db")

	tz := getenv("TZ", "Europe/Berlin")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}

	interval, err := time.ParseDuration(getenv("SYNC_INTERVAL", "60s"))
	if err != nil {
		return fmt.Errorf("invalid SYNC_INTERVAL: %w", err)
	}

	velocitySprints, err := parseVelocitySprints(getenv("VELOCITY_SPRINTS", "10"))
	if err != nil {
		return err
	}

	reviewClock, err := parseReviewNow(os.Getenv("REVIEW_NOW"))
	if err != nil {
		return err
	}

	client, fake, err := jiraClient()
	if err != nil {
		return err
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	authOpt, err := authOption(st, fake)
	if err != nil {
		return err
	}

	// The background sync loop keeps SQLite current: it backfills on first run
	// (empty DB) and then does cheap incremental syncs on each interval. It runs
	// for the lifetime of the process and stops cleanly on shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	syncer := sync.NewSyncer(client, st, syncOverlap, sync.RequirePersonalWrites)

	opts := []web.Option{
		web.WithLocation(loc),
		web.WithVelocitySprints(velocitySprints),
		web.WithJiraBaseURL(os.Getenv("JIRA_BASE_URL")),
		// The resync button rebuilds the projection through the same Syncer that
		// runs the periodic loop, so a resync and an incremental never overlap. It
		// runs under the app context (ctx), not a request context, so it survives
		// the POST handler returning promptly.
		web.WithResyncer(resyncer{ctx: ctx, syncer: syncer}),
		// The Board estimate edit's write path (docs/adr/0005): the Syncer's
		// SetEstimate writes the size to Jira, re-reads the issue and persists it.
		// Unlike the fire-and-forget resync it runs under the request context, so
		// no adapter is needed — the Syncer satisfies web.Estimator directly.
		web.WithEstimator(syncer),
		// The Board drag-and-drop transition write path (docs/adr/0010): the same
		// Syncer's SetStatus writes the transition to Jira, re-reads the issue and
		// persists it. Like the estimate edit it runs under the request context,
		// so the Syncer satisfies web.Transitioner directly.
		web.WithTransitioner(syncer),
		// The Prio view's priority edit write path (#212): the same Syncer's
		// SetPriority writes the level to Jira (by name), re-reads the issue and
		// persists it; the Syncer satisfies web.Prioritizer directly.
		web.WithPrioritizer(syncer),
		// The Board's Assignee edit write path (#224, docs/adr/0012): the same
		// Syncer's SetAssignee assigns (or clears) the ticket in Jira by account id,
		// re-reads the issue and persists it; the Syncer satisfies web.Assigner
		// directly.
		web.WithAssigner(syncer),
	}
	// Only override the web clock when REVIEW_NOW is set; leaving it out keeps the
	// server's default time.Now for every production deployment.
	if reviewClock != nil {
		opts = append(opts, web.WithClock(reviewClock))
	}
	if fake {
		opts = append(opts, web.WithFakeJira(client))
	}
	if authOpt != nil {
		opts = append(opts, authOpt)
	}

	srv, err := web.NewServer(st, opts...)
	if err != nil {
		return err
	}

	go syncer.Run(ctx, interval)
	log.Printf("syncing %s from Jira every %s", getenv("JIRA_PROJECT", "DCAI"), interval)

	httpSrv := &http.Server{Addr: addr, Handler: srv}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Printf("listening on %s", addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// resyncer adapts the running Syncer to web.Resyncer, binding the app-lifetime
// context so a resync triggered from the UI survives the HTTP handler returning
// and stops cleanly on shutdown.
type resyncer struct {
	ctx    context.Context
	syncer *sync.Syncer
}

func (r resyncer) Resync() bool    { return r.syncer.TriggerResync(r.ctx) }
func (r resyncer) Resyncing() bool { return r.syncer.Resyncing() }

// authOption enforces OAuth for live Jira. The explicit local bypass only
// works with fake data.
func authOption(st *store.Store, fake bool) (web.Option, error) {
	if envEnabled("AUTH_DISABLED") {
		if !fake {
			return nil, errors.New("AUTH_DISABLED=true is only supported with fake Jira")
		}
		log.Print("serving local fake Jira without authentication")
		return nil, nil
	}
	oauth, err := auth.New(st, auth.Config{
		ClientID:          os.Getenv("JIRA_OAUTH_CLIENT_ID"),
		ClientSecret:      os.Getenv("JIRA_OAUTH_CLIENT_SECRET"),
		RedirectURI:       os.Getenv("JIRA_OAUTH_REDIRECT_URI"),
		CloudID:           os.Getenv("JIRA_OAUTH_CLOUD_ID"),
		AllowedAccountIDs: os.Getenv("JIRA_OAUTH_ALLOWED_ACCOUNT_IDS"),
		TokenKey:          os.Getenv("JIRA_OAUTH_TOKEN_KEY"),
		SiteURL:           os.Getenv("JIRA_BASE_URL"),
		ProjectKey:        getenv("JIRA_PROJECT", "DCAI"),
		BoardID:           getenv("JIRA_BOARD_ID", "8"),
	})
	if err != nil {
		return nil, fmt.Errorf("configure Atlassian sign-in: %w", err)
	}
	return web.WithOAuth(oauth), nil
}

func envEnabled(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

// jiraClient selects the service-account client for background sync or an
// isolated local dataset. A partial service-account config fails at startup.
func jiraClient() (client jira.Client, fake bool, err error) {
	base := os.Getenv("JIRA_BASE_URL")
	email := os.Getenv("JIRA_EMAIL")
	token := os.Getenv("JIRA_API_TOKEN")

	if envEnabled("JIRA_FAKE") || (base == "" && email == "" && token == "") {
		if os.Getenv("REVIEW_DATASET") == "dense" {
			log.Print("using dense review fake Jira")
			return jira.NewDenseFakeClient(), true, nil
		}
		log.Print("using canned fake Jira")
		return jira.NewFakeClient(), true, nil
	}
	if base == "" || email == "" || token == "" {
		return nil, false, errors.New("set JIRA_BASE_URL, JIRA_EMAIL and JIRA_API_TOKEN for live Jira, or set JIRA_FAKE=true for local review")
	}

	return jira.NewLiveClient(jira.Config{
		BaseURL:    strings.TrimRight(base, "/"),
		Email:      email,
		APIToken:   token,
		ProjectKey: getenv("JIRA_PROJECT", "DCAI"),
		BoardID:    getenv("JIRA_BOARD_ID", "8"),
	}), false, nil
}

// parseVelocitySprints reads the VELOCITY_SPRINTS setting: how many trailing
// sprints the Velocity view shows. It must be a positive integer.
func parseVelocitySprints(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid VELOCITY_SPRINTS %q: must be a positive integer", v)
	}
	return n, nil
}

// parseReviewNow reads the optional REVIEW_NOW override: an RFC3339 timestamp
// that pins the web clock used to resolve relative date ranges, so the
// date-bearing views (Sprint window, Velocity sprints, Daily window) render
// reproducibly against the canned fake backend during live/visual review (see
// docs/adr/0001-agent-driven-acceptance-review-harness.md). An empty value means
// unset — the server keeps its default time.Now — so it returns a nil clock and
// no override is applied. A non-empty but unparseable value fails fast at
// startup, consistent with SYNC_INTERVAL.
func parseReviewNow(v string) (func() time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("invalid REVIEW_NOW %q: %w", v, err)
	}
	return func() time.Time { return t }, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
