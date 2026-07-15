// Command jira-stats runs the sprint-stats dashboard: it opens the SQLite
// store, backfills the configured Jira project into it, and serves the
// dashboard over HTTP. All Jira credentials and settings come from the
// environment; no secrets live in the repo.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

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

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client, err := jiraClient()
	if err != nil {
		return err
	}

	log.Printf("backfilling %s from Jira...", getenv("JIRA_PROJECT", "DCAI"))
	n, err := sync.Backfill(context.Background(), client, st)
	if err != nil {
		return err
	}
	log.Printf("backfilled %d issues", n)

	srv, err := web.NewServer(st)
	if err != nil {
		return err
	}

	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, srv)
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
