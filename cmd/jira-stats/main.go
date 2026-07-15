// Command jira-stats runs the sprint-stats dashboard: it opens the SQLite
// store, runs a one-shot sync (from the fake Jira in the walking skeleton), and
// serves the dashboard over HTTP.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/web"
)

func main() {
	addr := getenv("LISTEN_ADDR", ":8080")
	dbPath := getenv("DB_PATH", "jira-stats.db")

	if err := run(addr, dbPath); err != nil {
		log.Fatal(err)
	}
}

func run(addr, dbPath string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Walking skeleton: sync from the fake Jira. A later ticket swaps in the
	// live client and a background sync loop.
	if err := sync.Once(context.Background(), jira.NewFakeClient(), st); err != nil {
		return err
	}

	srv, err := web.NewServer(st)
	if err != nil {
		return err
	}

	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, srv)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
