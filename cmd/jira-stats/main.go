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
	"syscall"
	"time"

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
	addr := getenv("LISTEN_ADDR", ":8080")
	dbPath := getenv("DB_PATH", "jira-stats.db")

	tz := getenv("TZ", "Europe/Berlin")
	if _, err := time.LoadLocation(tz); err != nil {
		return err
	}

	interval, err := time.ParseDuration(getenv("SYNC_INTERVAL", "60s"))
	if err != nil {
		return fmt.Errorf("invalid SYNC_INTERVAL: %w", err)
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

	srv, err := web.NewServer(st)
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
