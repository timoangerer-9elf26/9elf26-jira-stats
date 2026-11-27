package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

const (
	privacyBatchSize     = 90
	privacyCheckInterval = 24 * time.Hour
)

var errNoReportingGrant = errors.New("no usable stored Atlassian grant")

// PrivacyReporter submits the due inventory with the app's stored 3LO grants.
type PrivacyReporter struct {
	service *Service
	owner   string
	now     func() time.Time
	mu      sync.Mutex
}

func NewPrivacyReporter(service *Service, owner string, now func() time.Time) *PrivacyReporter {
	return &PrivacyReporter{service: service, owner: strings.TrimSpace(owner), now: now}
}

// Run checks at startup, then daily or at a server-directed retry time.
func (r *PrivacyReporter) Run(ctx context.Context) {
	for ctx.Err() == nil {
		count, retryAt, err := r.Check(ctx)
		if count > 0 {
			log.Printf("jira privacy: successfully reported %d accounts", count)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("jira privacy: %v", err)
		}
		delay := privacyCheckInterval
		if !retryAt.IsZero() {
			delay = max(time.Second, time.Until(retryAt))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Check reports every account currently due and commits each acknowledged batch
// before sending the next. It returns the number committed and any retry deadline.
func (r *PrivacyReporter) Check(ctx context.Context) (count int, retryAt time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.service
	cycle, retryAt, err := s.store.PrivacyPoll(ctx)
	if err != nil {
		return 0, time.Time{}, errors.New("could not read reporting schedule")
	}
	if retryAt.After(r.now()) {
		return 0, retryAt, nil
	}
	for {
		batch, err := s.store.PrivacyDue(ctx, r.now(), privacyBatchSize)
		if err != nil {
			return count, time.Time{}, errors.New("could not read reporting inventory")
		}
		if len(batch) == 0 {
			break
		}

		nextCycle, deadline, err := r.submitBatch(ctx, batch, cycle)
		if err != nil || !deadline.IsZero() {
			return count, deadline, err
		}
		cycle = nextCycle
		count += len(batch)
	}
	return count, time.Time{}, r.refreshProfiles(ctx)
}

func (r *PrivacyReporter) refreshProfiles(ctx context.Context) error {
	s := r.service
	accounts, err := s.store.PendingPrivacyProfiles(ctx)
	if err != nil {
		return errors.New("could not read pending privacy profiles")
	}
	if len(accounts) == 0 {
		return nil
	}

	// Each candidate's token can serve several profiles. If an account is
	// invisible to that grant, another authorized grant can still supply it.
	for _, candidate := range r.candidateAccounts() {
		bearer, err := s.reportingBearer(ctx, candidate)
		if err != nil {
			continue
		}
		client := s.provider.client(bearer)
		pending := accounts[:0]
		for _, account := range accounts {
			if err := ctx.Err(); err != nil {
				return err
			}
			profile, err := client.FetchUserProfile(ctx, account)
			if err != nil {
				pending = append(pending, account)
				continue
			}
			if err := s.store.RefreshPrivacyProfile(ctx, profile, r.now()); err != nil {
				return errors.New("could not save refreshed privacy profile")
			}
		}
		accounts = pending
		if len(accounts) == 0 {
			break
		}
	}
	return nil
}

func (r *PrivacyReporter) candidateAccounts() []string {
	ids := make([]string, 0, len(r.service.accounts))
	for id := range r.service.accounts {
		if id != r.owner {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if r.service.accounts[r.owner] != nil {
		ids = append([]string{r.owner}, ids...)
	}
	return ids
}

func (s *Service) reportingBearer(ctx context.Context, account string) (string, error) {
	s.exchanges.RLock()
	defer s.exchanges.RUnlock()
	lock := s.accounts[account]
	lock.Lock()
	defer lock.Unlock()
	g, err := s.loadUsableGrant(ctx, account)
	return g.AccessToken, err
}

func (s *Service) applyPrivacyReport(ctx context.Context, now time.Time, cycle time.Duration, batch []store.PrivacyAccount, actions map[string]string) error {
	// Prevent a rotating grant refresh from restoring profile data as it
	// replaces the token pair. SQLite guards the projection and audit writes.
	s.exchanges.Lock()
	defer s.exchanges.Unlock()
	return s.store.ApplyPrivacyReport(ctx, now, cycle, batch, actions, func(account string, encrypted []byte) []byte {
		g, err := s.open(account, encrypted)
		if err != nil {
			return nil
		}
		g.TimeZone = ""
		envelope, err := s.seal(account, g)
		if err != nil {
			return nil
		}
		return envelope
	})
}

type privacyResponse struct {
	cycle     time.Duration
	retry     string
	limited   bool
	rejected  bool
	uncertain bool
	actions   map[string]string
}

// submitBatch owns one request's durable scheduling state, including the
// interval between sending the report and committing Atlassian's answer.
func (r *PrivacyReporter) submitBatch(ctx context.Context, batch []store.PrivacyAccount, cycle time.Duration) (time.Duration, time.Time, error) {
	var attempted bool
	var result privacyResponse
	reportErr := errNoReportingGrant
	for _, account := range r.candidateAccounts() {
		bearer, err := r.service.reportingBearer(ctx, account)
		if err != nil {
			continue
		}
		now := r.now()
		if err := r.service.store.ReservePrivacyBatch(ctx, batch, now, now.Add(cycle).Add(atlassianRequestTimeout)); err != nil {
			reportErr = errors.New("could not reserve reporting batch")
			break
		}
		attempted = true
		result, reportErr = r.service.provider.reportAccounts(ctx, bearer, batch)
		if !result.rejected {
			break
		}
		reportErr = errNoReportingGrant
	}
	if !attempted {
		return cycle, time.Time{}, reportErr
	}

	// Save a known result even during graceful shutdown.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	now := r.now()
	if result.uncertain {
		if result.cycle > cycle {
			if err := r.service.store.ReservePrivacyBatch(saveCtx, batch, now, now.Add(result.cycle)); err != nil {
				return cycle, time.Time{}, errors.New("could not extend uncertain reporting deadline")
			}
		}
		return cycle, time.Time{}, reportErr
	}
	if reportErr != nil || result.retry != "" || result.limited {
		var deadline time.Time
		if result.retry != "" || result.limited {
			deadline = privacyRetryAt(now, result.retry)
		}
		if err := r.service.store.ReleasePrivacyBatch(saveCtx, batch, deadline); err != nil {
			return cycle, deadline, errors.New("could not save reporting retry state")
		}
		return cycle, deadline, reportErr
	}

	if result.cycle != 0 {
		cycle = result.cycle
	}
	if err := r.service.applyPrivacyReport(saveCtx, now, cycle, batch, result.actions); err != nil {
		return cycle, time.Time{}, errors.New("could not apply Atlassian's reporting response")
	}
	return cycle, time.Time{}, nil
}

func (p provider) reportAccounts(ctx context.Context, bearer string, batch []store.PrivacyAccount) (privacyResponse, error) {
	payload, err := json.Marshal(struct {
		Accounts []store.PrivacyAccount `json:"accounts"`
	}{batch})
	if err != nil {
		return privacyResponse{}, errors.New("could not encode the reporting request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.APIBaseURL+"/app/report-accounts/", bytes.NewReader(payload))
	if err != nil {
		return privacyResponse{}, errors.New("could not create the reporting request")
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return privacyResponse{uncertain: true}, errors.New("reporting request failed")
	}
	defer resp.Body.Close()

	var result privacyResponse
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		result.rejected = true
	case http.StatusTooManyRequests:
		result.limited, result.retry = true, resp.Header.Get("Retry-After")
	case http.StatusServiceUnavailable:
		if result.retry = resp.Header.Get("Retry-After"); result.retry == "" {
			return result, errors.New("reporting endpoint unavailable")
		}
	case http.StatusOK, http.StatusNoContent:
		// Atlassian specifies Cycle-Period in days.
		result.cycle = privacyDuration(resp.Header.Get("Cycle-Period"), 24*time.Hour)
		if resp.StatusCode == http.StatusOK {
			var body struct {
				Accounts []struct {
					AccountID string `json:"accountId"`
					Status    string `json:"status"`
				} `json:"accounts"`
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.Accounts == nil {
				result.uncertain = true
				return result, errors.New("invalid Atlassian reporting response")
			}
			result.actions = make(map[string]string, len(body.Accounts))
			for _, action := range body.Accounts {
				if action.Status == "closed" || (action.Status == "updated" && result.actions[action.AccountID] != "closed") {
					result.actions[action.AccountID] = action.Status
				}
			}
		}
	default:
		return result, errors.New("reporting endpoint returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return result, nil
}

func privacyDuration(header string, unit time.Duration) time.Duration {
	value, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64)
	if err != nil || value <= 0 || value > int64(time.Duration(1<<63-1)/unit) {
		return 0
	}
	return time.Duration(value) * unit
}

func privacyRetryAt(now time.Time, header string) time.Time {
	if seconds := privacyDuration(header, time.Second); seconds > 0 {
		return now.Add(seconds)
	}
	if deadline, err := http.ParseTime(header); err == nil {
		if deadline.After(now) {
			return deadline
		}
		return now
	}
	if strings.TrimSpace(header) == "0" {
		return now
	}
	return now.Add(privacyCheckInterval)
}
