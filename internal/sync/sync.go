// Package sync orchestrates pulling Jira issues (via a jira.Client) into the
// store. It offers a full-project backfill, an incremental sync of only changed
// issues, and a resilient background loop that keeps SQLite current cheaply.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// Store is the subset of the persistence layer the sync engine writes to and
// reads bookkeeping from.
type Store interface {
	SaveIssue(iss jira.Issue, syncedAt string) error
	SaveSprint(sp jira.Sprint) error
	ReplaceProjectMembers(members []jira.ProjectMember) error
	IssueCount() (int, error)
	IssueKeys(context.Context) ([]string, error)
	RemoveIssues(context.Context, []string) error
	LastSync() (t time.Time, ok bool, err error)
	SetLastSync(t time.Time) error
	// SetLastFullResync records the completion instant of a successful full
	// resync. Written only by resync (never by the cold-start backfill), so it
	// stays "never" until the first user-triggered full resync.
	SetLastFullResync(t time.Time) error
	// Reset empties the rebuildable projection (all snapshots, transition logs,
	// sprints and last_sync). It is the first half of a full resync: clear, then
	// re-backfill.
	Reset() error
}

// Backfill persists every issue snapshot, its details and tracked changes.
// The store dedups changes by Jira changelog entry ID. It returns the number
// of issues persisted, all stamped with the same synced_at timestamp.
func Backfill(ctx context.Context, client jira.Client, store Store) (int, error) {
	if err := syncSprints(ctx, client, store); err != nil {
		return 0, err
	}
	refreshProjectMembers(ctx, client, store)
	issues, err := client.FetchIssues(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch issues: %w", err)
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, iss := range issues {
		if err := store.SaveIssue(iss, syncedAt); err != nil {
			return 0, fmt.Errorf("save issue %s: %w", iss.Key, err)
		}
	}
	return len(issues), nil
}

// syncSprints fetches the board's sprint entities and upserts each into the
// store, so the active-sprint window and completed-sprint lifecycle instants
// stay current. Run on every backfill and incremental cycle: sprints are few
// and the whole set is cheap to re-fetch, and the store upserts idempotently.
func syncSprints(ctx context.Context, client jira.Client, store Store) error {
	sprints, err := client.FetchSprints(ctx)
	if err != nil {
		return fmt.Errorf("fetch sprints: %w", err)
	}
	for _, sp := range sprints {
		if err := store.SaveSprint(sp); err != nil {
			return fmt.Errorf("save sprint %d: %w", sp.ID, err)
		}
	}
	return nil
}

// refreshProjectMembers refreshes the people who can be assigned work on the
// project. Jira's answer REPLACES the stored list rather than merging into it,
// so someone removed from the project stops being a member on this very cycle
// (docs/adr/0012). It runs on every cycle, backfill and incremental alike: the
// list is short and the projection is what every read path uses.
//
// It is BEST-EFFORT and returns no error, deliberately: it must never abort a
// sync cycle. Jira gates its user search on a permission separate from issue
// read, so a token allowed to read issues but not to browse users fails here on
// every single cycle — and propagating that would stop issues and sprints
// syncing too, silently staling the whole dashboard over a list of seven names.
// Unlike sprints, nothing load-bearing reads this table. On failure the stored
// list is left exactly as it was (no empty write), which is the staleness
// docs/adr/0012 already accepts.
//
// Backfill is deliberately treated the SAME way rather than hard-failing: the
// cold-start backfill is precisely where an unusable token would otherwise leave
// the dashboard permanently empty, and an empty member table is a state the ADR
// already provides for — "between a deploy and the first sync the member table
// is empty, so the avatar is simply not editable. It self-heals within one
// cycle."
func refreshProjectMembers(ctx context.Context, client jira.Client, store Store) {
	members, err := client.FetchProjectMembers(ctx)
	if err != nil {
		log.Printf("sync: project members not refreshed, keeping the stored list: %v", err)
		return
	}
	if err := store.ReplaceProjectMembers(members); err != nil {
		log.Printf("sync: project members not saved, keeping the stored list: %v", err)
	}
}

// Once runs a single full-project backfill, discarding the issue count. It is
// the entry point used by the web integration harness.
func Once(ctx context.Context, client jira.Client, store Store) error {
	_, err := Backfill(ctx, client, store)
	return err
}

// incremental refreshes only issues changed since the given bound: it re-fetches
// their snapshots and appends any new changelog transitions (deduped in the
// store by changelog entry id).
func incremental(ctx context.Context, client jira.Client, store Store, since time.Time) error {
	if err := syncSprints(ctx, client, store); err != nil {
		return err
	}
	refreshProjectMembers(ctx, client, store)
	issues, err := client.FetchIssuesUpdatedSince(ctx, since)
	if err != nil {
		return fmt.Errorf("fetch updated issues: %w", err)
	}
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	for _, iss := range issues {
		if err := store.SaveIssue(iss, syncedAt); err != nil {
			return fmt.Errorf("save issue %s: %w", iss.Key, err)
		}
	}
	return nil
}

// Syncer keeps the store current through full backfill and incremental sync
// cycles. It reads incremental changes since the last successful sync with an
// overlap to account for clock skew.
type Syncer struct {
	client              jira.Client
	store               Store
	overlap             time.Duration
	personalWrites      bool
	nextMembershipCheck time.Time // Protected by projectionLock.

	// A buffered channel serializes projection updates with cancellable acquisition.
	projectionLock chan struct{}
	// resyncing reports (and guards) an in-flight full resync: TriggerResync flips
	// it true and only one resync may hold it, so an overlapping trigger is a
	// safe no-op. It is read by the web layer to show the in-progress state.
	resyncing atomic.Bool
}

// NewSyncer builds a Syncer. overlap is the window subtracted from the last
// sync time when bounding the incremental query (e.g. 2 minutes).
func NewSyncer(client jira.Client, store Store, overlap time.Duration, opts ...func(*Syncer)) *Syncer {
	s := &Syncer{client: client, store: store, overlap: overlap, projectionLock: make(chan struct{}, 1)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RequirePersonalWrites prevents an interactive edit from using the background
// service account when no personal client was attached to its request.
func RequirePersonalWrites(s *Syncer) {
	s.personalWrites = true
}

func (s *Syncer) writeClient(ctx context.Context, key string) (jira.Client, error) {
	if client, ok := jira.PersonalClientFromContext(ctx); ok {
		scoped, ok := client.(interface {
			CheckIssueProject(context.Context, string) error
		})
		if !ok {
			return nil, errors.New("personal Jira client cannot check issue project")
		}
		if err := scoped.CheckIssueProject(ctx, key); err != nil {
			return nil, err
		}
		return client, nil
	}
	if s.personalWrites {
		return nil, errors.New("interactive Jira writes require a personal client")
	}
	return s.client, nil
}

// TriggerResync starts a full resync in the background and returns true, unless
// one is already running — in which case it does nothing and returns false (the
// "already running" no-op the button relies on). The resync runs under ctx (the
// app's lifetime context, NOT a request context, so it survives the HTTP handler
// returning promptly). Failures are logged; the projection is only ever left
// cleared-and-rebuilt or, on error mid-flight, recoverable by the next cycle.
func (s *Syncer) TriggerResync(ctx context.Context) bool {
	if !s.resyncing.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer s.resyncing.Store(false)
		if err := s.resync(ctx); err != nil {
			log.Printf("sync: resync failed: %v", err)
		}
	}()
	return true
}

// Resyncing reports whether a full resync is currently running.
func (s *Syncer) Resyncing() bool { return s.resyncing.Load() }

func (s *Syncer) lockProjection(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.projectionLock <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.unlockProjection()
			return err
		}
		return nil
	}
}

func (s *Syncer) unlockProjection() { <-s.projectionLock }

// RefreshIssue reads the current issue through the caller's Jira client and
// saves its projection. Serialize the read as well as the save so a slower
// refresh cannot overwrite a newer snapshot from another refresh or sync.
func (s *Syncer) RefreshIssue(ctx context.Context, client interface {
	FetchIssue(context.Context, string) (jira.Issue, error)
}, key string) (jira.Issue, error) {
	if err := s.lockProjection(ctx); err != nil {
		return jira.Issue{}, err
	}
	defer s.unlockProjection()

	iss, err := client.FetchIssue(ctx, key)
	if err != nil {
		return jira.Issue{}, fmt.Errorf("re-fetch issue: %w", err)
	}
	if iss.Key != key {
		return jira.Issue{}, fmt.Errorf("re-fetch issue %s returned incompatible key %q", key, iss.Key)
	}
	if err := s.store.SaveIssue(iss, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return jira.Issue{}, fmt.Errorf("save issue: %w", err)
	}
	return iss, nil
}

// SetEstimate is the Board estimate edit's write path (docs/adr/0005): it writes
// the ticket's size back to Jira, then immediately re-reads that one issue from
// Jira and persists it, so the projection is set only ever from a Jira read
// (never from local UI state) and stays a pure projection. It returns the
// authoritative size the re-fetched issue carries ("S"/"M"/"L" or "" for
// no-estimate). The write can succeed even if reconciliation returns an error.
func (s *Syncer) SetEstimate(ctx context.Context, key, size string) (string, error) {
	client, err := s.writeClient(ctx, key)
	if err != nil {
		return "", err
	}
	if err := client.UpdateIssueSize(ctx, key, size); err != nil {
		return "", fmt.Errorf("update size: %w", err)
	}
	iss, err := s.RefreshIssue(ctx, client, key)
	return iss.Size, err
}

// SetPriority is the Prio view's priority edit write path (#212): it writes the
// level (one of jira.Priorities, BY NAME) to Jira, then immediately re-reads that
// one issue and persists it — the identical write → re-read → persist shape as
// SetEstimate (docs/adr/0005) and SetStatus (docs/adr/0010), so the projection
// is only ever set from a Jira read. Unlike SetEstimate it hands nothing back:
// the caller re-renders the whole Prio panel from the store afterwards, which is
// what re-sorts the table around the change. The write can succeed even if
// reconciliation returns an error.
func (s *Syncer) SetPriority(ctx context.Context, key, priority string) error {
	client, err := s.writeClient(ctx, key)
	if err != nil {
		return err
	}
	if err := client.UpdateIssuePriority(ctx, key, priority); err != nil {
		return fmt.Errorf("update priority: %w", err)
	}
	_, err = s.RefreshIssue(ctx, client, key)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteLanded, err)
	}
	return nil
}

// SetStatus is the Board transition write path (docs/adr/0010): it moves a
// ticket into the requested status in Jira, then immediately re-reads that one
// issue and persists it, so the projection is set only ever from a Jira read —
// the identical write → re-read → persist shape as SetEstimate (docs/adr/0005).
// Note the units differ by direction, deliberately: it takes a status ID (the
// only unambiguous way to name a target) and returns the status NAME the
// re-fetched issue carries — the value the projection stores and a card renders.
//
// statusID is a Jira STATUS id (see jira.StatusID*), not a transition id and not
// a status name: Jira offers a transition labelled "Done" that lands in a
// different column than its name suggests, so the transition is resolved from
// the issue's currently offered set by target status id (jira.TransitionTo).
// When Jira offers no transition into that status the call fails with
// jira.ErrNoTransition and nothing is written — never a fallback to some other
// transition. A failure up to and including the transition itself is returned
// and leaves both Jira and the projection unchanged; a failure in the
// reconciliation read/save is returned too, but by then Jira HAS moved and only
// the projection is behind (see ErrWriteLanded).
func (s *Syncer) SetStatus(ctx context.Context, key, statusID string) (string, error) {
	client, err := s.writeClient(ctx, key)
	if err != nil {
		return "", err
	}
	offered, err := client.FetchTransitions(ctx, key)
	if err != nil {
		return "", fmt.Errorf("fetch transitions: %w", err)
	}
	tr, err := jira.TransitionTo(offered, statusID)
	if err != nil {
		return "", fmt.Errorf("transition for %s: %w", key, err)
	}
	if err := client.TransitionIssue(ctx, key, tr.ID); err != nil {
		return "", fmt.Errorf("transition issue: %w", err)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	iss, err := s.RefreshIssue(refreshCtx, client, key)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrWriteLanded, err)
	}
	return iss.Status, nil
}

// ErrWriteLanded marks an error raised AFTER a Jira write has already
// succeeded: the follow-up single-issue re-read, or the persist of what it
// returned. It separates "Jira changed but the projection has not caught up yet"
// from "nothing changed anywhere", which a caller needs before it tells a user a
// write failed — reporting a landed write as a failure is exactly the defect
// #207 had to fix on the drag path. The next sync cycle reconciles the
// projection either way, so a marked error is stale, never wrong.
var ErrWriteLanded = errors.New("the Jira write landed; the follow-up did not")

// SetAssignee is the Board's assignee write (#223, docs/adr/0012): assign the
// issue to the person behind accountID — or clear it, when accountID is
// jira.UnassignedAccountID — then re-read that one issue and persist what the
// read returned, so the projection is only ever set from Jira. It returns the
// authoritative assignee display name from the re-read ("" when unassigned),
// which need not be the person asked for if something else moved the ticket
// first: last-write-wins, no locking guard, as for the other three writes.
//
// A failed write persists nothing and leaves Jira untouched. A failure past that
// point is wrapped with ErrWriteLanded, because the assignment is a fact in Jira
// by then.
func (s *Syncer) SetAssignee(ctx context.Context, key, accountID string) (string, error) {
	client, err := s.writeClient(ctx, key)
	if err != nil {
		return "", err
	}
	if err := client.UpdateIssueAssignee(ctx, key, accountID); err != nil {
		return "", fmt.Errorf("update assignee: %w", err)
	}
	iss, err := s.RefreshIssue(ctx, client, key)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrWriteLanded, err)
	}
	return iss.Assignee, nil
}

// resync clears the projection and re-backfills the whole project — a full
// resync (CONTEXT.md → Sync). It holds the sync mutex for the duration so no
// incremental cycle interleaves with the wipe or the rebuild, and on success
// records the start instant both as the new last_sync (incremental heartbeat)
// and as the last_full_resync stamp. Recording last_full_resync ONLY here (never
// in Cycle's cold-start backfill) is what keeps a full resync distinct from a
// cold-start backfill.
func (s *Syncer) resync(ctx context.Context) error {
	if err := s.lockProjection(ctx); err != nil {
		return err
	}
	defer s.unlockProjection()

	startedAt := time.Now().UTC()
	if err := s.store.Reset(); err != nil {
		return fmt.Errorf("reset projection: %w", err)
	}
	n, err := Backfill(ctx, s.client, s.store)
	if err != nil {
		return err
	}
	log.Printf("sync: resync backfilled %d issues", n)
	if err := s.store.SetLastSync(startedAt); err != nil {
		return fmt.Errorf("record last_sync: %w", err)
	}
	if err := s.store.SetLastFullResync(startedAt); err != nil {
		return fmt.Errorf("record last_full_resync: %w", err)
	}
	return nil
}

// Cycle runs one sync cycle: a full backfill if the store is empty or the
// incremental watermark is missing, otherwise it fetches changes since the
// last sync minus the overlap window. On success it records the cycle start
// as the new last_sync. Retries dedup transitions by changelog entry ID.
func (s *Syncer) Cycle(ctx context.Context) error {
	// Serialize with a full resync (which also holds the projection lock): the periodic incremental
	// must never interleave with a resync's clear-and-rebuild.
	if err := s.lockProjection(ctx); err != nil {
		return err
	}
	defer s.unlockProjection()

	startedAt := time.Now().UTC()

	count, err := s.store.IssueCount()
	if err != nil {
		return fmt.Errorf("issue count: %w", err)
	}

	last, hasWatermark, err := s.store.LastSync()
	if err != nil {
		return err
	}
	if count == 0 || !hasWatermark {
		n, err := Backfill(ctx, s.client, s.store)
		if err != nil {
			return err
		}
		log.Printf("sync: backfilled %d issues", n)
	} else {
		if err := incremental(ctx, s.client, s.store, last.Add(-s.overlap)); err != nil {
			return err
		}
	}

	if err := s.store.SetLastSync(startedAt); err != nil {
		return fmt.Errorf("record last_sync: %w", err)
	}
	if err := s.reconcileProjectIssues(ctx, startedAt); err != nil {
		log.Printf("sync: project membership check failed, keeping the projection: %v", err)
	}
	return nil
}

// Run drives Cycle immediately and then on every interval tick until ctx is
// cancelled. A failed cycle is logged and retried on the next tick; the loop
// never crashes the server and always returns cleanly on cancellation.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := s.Cycle(ctx); err != nil {
			log.Printf("sync: cycle failed, retrying in %s: %v", interval, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
