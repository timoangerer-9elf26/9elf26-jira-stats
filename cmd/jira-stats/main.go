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
	"syscall"
	"time"

	"github.com/joho/godotenv"

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

	velocityWeeks, err := parseVelocityWeeks(getenv("VELOCITY_WEEKS", "10"))
	if err != nil {
		return err
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client, err := jiraClient()
	if err != nil {
		return err
	}

	// The background sync loop keeps SQLite current: it backfills on first run
	// (empty DB) and then does cheap incremental syncs on each interval. It runs
	// for the lifetime of the process and stops cleanly on shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	syncer := sync.NewSyncer(client, st, syncOverlap)
	go syncer.Run(ctx, interval)
	log.Printf("syncing %s from Jira every %s", getenv("JIRA_PROJECT", "DCAI"), interval)

	srv, err := web.NewServer(st, web.WithLocation(loc), web.WithVelocityWeeks(velocityWeeks))
	if err != nil {
		return err
	}

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

// jiraClient builds the live Jira client from environment configuration when
// credentials are present, and otherwise falls back to the canned fake so the
// dashboard is runnable locally without live access.
func jiraClient() (jira.Client, error) {
	base := os.Getenv("JIRA_BASE_URL")
	email := os.Getenv("JIRA_EMAIL")
	token := os.Getenv("JIRA_API_TOKEN")

	if base == "" || email == "" || token == "" {
		log.Print("JIRA_BASE_URL/JIRA_EMAIL/JIRA_API_TOKEN not all set; using canned fake Jira")
		return jira.NewFakeClient(), nil
	}

	return jira.NewLiveClient(jira.Config{
		BaseURL:    base,
		Email:      email,
		APIToken:   token,
		ProjectKey: getenv("JIRA_PROJECT", "DCAI"),
		BoardID:    getenv("JIRA_BOARD_ID", "8"),
	}), nil
}

// parseVelocityWeeks reads the VELOCITY_WEEKS setting: how many trailing ISO
// weeks the Velocity view shows. It must be a positive integer.
func parseVelocityWeeks(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid VELOCITY_WEEKS %q: must be a positive integer", v)
	}
	return n, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
