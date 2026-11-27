// Package auth manages Atlassian 3LO grants and dashboard sessions.
package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
)

const (
	FlowLifetime    = 10 * time.Minute
	SessionLifetime = 30 * 24 * time.Hour
)

// ErrSignIn means the browser has no usable session and must authorize again.
var ErrSignIn = errors.New("Atlassian sign-in required")

// ErrProjectAccess means the personal grant cannot browse the configured Jira project.
var ErrProjectAccess = errors.New("Jira project browse permission required")

type Identity struct {
	AccountID string
	TimeZone  *time.Location
}

type grant struct {
	AccessToken, RefreshToken, TimeZone string
	ExpiresAt                           time.Time
	PendingSiteCheck                    bool
	CheckedProject                      string
}

type Service struct {
	store    *store.Store
	provider provider
	cipher   cipher.AEAD
	accounts map[string]*sync.Mutex

	// The account is unknown until a callback exchanges its code. Keep those
	// exchanges out of refreshes so an older pair cannot overwrite a new login.
	exchanges sync.RWMutex
}

func New(st *store.Store, cfg Config) (*Service, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("set JIRA_OAUTH_CLIENT_ID and JIRA_OAUTH_CLIENT_SECRET")
	}
	if cfg.CloudID == "" || strings.ContainsAny(cfg.CloudID, "/\\?# \r\n\t") || cfg.CloudID == "." || cfg.CloudID == ".." {
		return nil, errors.New("set JIRA_OAUTH_CLOUD_ID to the Jira site Cloud ID")
	}
	redirect, err := url.Parse(cfg.RedirectURI)
	if err != nil || !httpsURL(redirect) || redirect.EscapedPath() != CallbackPath {
		return nil, fmt.Errorf("set JIRA_OAUTH_REDIRECT_URI to the HTTPS callback at %s", CallbackPath)
	}
	cfg.SiteURL = strings.TrimRight(cfg.SiteURL, "/")
	site, err := url.Parse(cfg.SiteURL)
	if err != nil || !httpsURL(site) || site.Path != "" {
		return nil, errors.New("set JIRA_BASE_URL to the HTTPS Jira site origin")
	}

	accounts := make(map[string]*sync.Mutex)
	for _, id := range strings.Split(cfg.AllowedAccountIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			accounts[id] = &sync.Mutex{}
		}
	}
	if len(accounts) == 0 {
		return nil, errors.New("set JIRA_OAUTH_ALLOWED_ACCOUNT_IDS to a comma-separated list of Atlassian account IDs")
	}

	key, err := base64.StdEncoding.DecodeString(cfg.TokenKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("set JIRA_OAUTH_TOKEN_KEY to 32 random bytes encoded as standard base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}

	if cfg.ProjectKey == "" {
		cfg.ProjectKey = jira.PrioProject
	}
	if cfg.AuthBaseURL == "" {
		cfg.AuthBaseURL = "https://auth.atlassian.com"
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = "https://api.atlassian.com"
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &Service{store: st, provider: provider{cfg, client}, cipher: aead, accounts: accounts}, nil
}

func httpsURL(u *url.URL) bool {
	return u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// Start creates a short-lived authorization attempt and returns the Atlassian
// redirect URL and the opaque browser binding for its cookie.
func (s *Service) Start(ctx context.Context, next string) (authorization, browser string, err error) {
	state, browser := randomToken(), randomToken()
	now := time.Now()
	err = s.store.SaveOAuthFlow(ctx, digest(state), digest(browser), next, now.Unix(), now.Add(FlowLifetime).Unix())
	if err != nil {
		return "", "", err
	}
	return s.provider.authorizeURL(state), browser, nil
}

// Complete consumes the state even when Atlassian declined authorization.
func (s *Service) Complete(ctx context.Context, state, browser, code, previous string) (session, next string, err error) {
	if state == "" || browser == "" {
		return "", "", ErrSignIn
	}
	next, err = s.store.ConsumeOAuthFlow(ctx, digest(state), digest(browser), time.Now().Unix())
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrSignIn
	}
	if err != nil {
		return "", "", err
	}
	if code == "" {
		return "", next, ErrSignIn
	}

	s.exchanges.Lock()
	defer s.exchanges.Unlock()
	pair, err := s.provider.exchange(ctx, map[string]string{
		"grant_type": "authorization_code", "code": code, "redirect_uri": s.provider.cfg.RedirectURI,
	})
	if err != nil {
		return "", next, err
	}
	now := time.Now()
	if err := s.provider.checkSite(ctx, pair.AccessToken); err != nil {
		return "", next, err
	}
	account, zone, err := s.provider.myself(ctx, pair.AccessToken)
	if err != nil {
		return "", next, err
	}
	if s.accounts[account] == nil {
		return "", next, ErrSignIn
	}
	if err := s.provider.checkProject(ctx, pair.AccessToken); err != nil {
		return "", next, err
	}

	envelope, err := s.seal(account, grant{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, TimeZone: zone,
		ExpiresAt: now.Add(time.Duration(pair.ExpiresIn) * time.Second), CheckedProject: s.provider.cfg.ProjectKey,
	})
	if err != nil {
		return "", next, err
	}
	session = randomToken()
	if err := s.store.SaveOAuthLogin(ctx, account, envelope, digest(session), digest(previous),
		now.Unix(), now.Add(SessionLifetime).Unix()); err != nil {
		return "", next, err
	}
	return session, next, nil
}

// Resolve authenticates the browser, refreshes its grant when needed, and builds
// a request-local client for the configured Jira site.
func (s *Service) Resolve(ctx context.Context, session string) (Identity, jira.Client, error) {
	if session == "" {
		return Identity{}, nil, ErrSignIn
	}
	account, err := s.store.OAuthSessionAccount(ctx, digest(session), time.Now().Unix())
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, nil, ErrSignIn
	}
	if err != nil {
		return Identity{}, nil, err
	}
	lock := s.accounts[account]
	if lock == nil {
		return Identity{}, nil, ErrSignIn
	}

	s.exchanges.RLock()
	defer s.exchanges.RUnlock()
	lock.Lock()
	defer lock.Unlock()

	envelope, err := s.store.OAuthGrant(ctx, account)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, nil, ErrSignIn
	}
	if err != nil {
		return Identity{}, nil, err
	}
	g, err := s.open(account, envelope)
	if err != nil {
		return Identity{}, nil, err
	}
	g, err = s.usableGrant(ctx, account, g)
	if errors.Is(err, ErrSignIn) || errors.Is(err, ErrProjectAccess) {
		if revokeErr := s.store.DeleteOAuthGrant(ctx, account); revokeErr != nil {
			return Identity{}, nil, revokeErr
		}
	}
	if err != nil {
		return Identity{}, nil, err
	}
	zone, err := jiraTimeZone(g.TimeZone)
	if err != nil {
		return Identity{}, nil, err
	}
	return Identity{AccountID: account, TimeZone: zone}, s.provider.client(g.AccessToken), nil
}

func (s *Service) usableGrant(ctx context.Context, account string, g grant) (grant, error) {
	if time.Until(g.ExpiresAt) <= 30*time.Second && (!g.PendingSiteCheck || time.Now().After(g.ExpiresAt)) {
		pair, err := s.provider.exchange(ctx, map[string]string{
			"grant_type": "refresh_token", "refresh_token": g.RefreshToken,
		})
		if err != nil {
			return grant{}, err
		}
		g.AccessToken, g.RefreshToken = pair.AccessToken, pair.RefreshToken
		g.ExpiresAt = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
		g.PendingSiteCheck = true
		// The provider already rotated the refresh token. Persist both tokens
		// even if the browser disconnected or the subsequent site check fails.
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.saveGrant(persistCtx, account, g); err != nil {
			return grant{}, err
		}
	}
	if g.PendingSiteCheck || g.CheckedProject != s.provider.cfg.ProjectKey {
		if g.PendingSiteCheck {
			if err := s.provider.checkSite(ctx, g.AccessToken); err != nil {
				return grant{}, err
			}
		}
		if err := s.provider.checkProject(ctx, g.AccessToken); err != nil {
			return grant{}, err
		}
		g.PendingSiteCheck = false
		g.CheckedProject = s.provider.cfg.ProjectKey
		if err := s.saveGrant(ctx, account, g); err != nil {
			return grant{}, err
		}
	}
	return g, nil
}

func (s *Service) Logout(ctx context.Context, session string) error {
	return s.store.DeleteOAuthSession(ctx, digest(session))
}

func (s *Service) saveGrant(ctx context.Context, account string, g grant) error {
	envelope, err := s.seal(account, g)
	if err != nil {
		return err
	}
	return s.store.ReplaceOAuthGrant(ctx, account, envelope)
}

func (s *Service) seal(account string, g grant) ([]byte, error) {
	data, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	return s.cipher.Seal(nil, nil, data, s.grantBinding(account)), nil
}

func (s *Service) open(account string, data []byte) (grant, error) {
	var g grant
	plaintext, err := s.cipher.Open(nil, nil, data, s.grantBinding(account))
	if err != nil {
		return g, fmt.Errorf("decrypt Jira grant: %w", err)
	}
	err = json.Unmarshal(plaintext, &g)
	return g, err
}

func (s *Service) grantBinding(account string) []byte {
	cfg := s.provider.cfg
	return []byte(strings.Join([]string{cfg.ClientID, cfg.CloudID, cfg.SiteURL, account}, "\x00"))
}

func randomToken() string {
	var token [32]byte
	rand.Read(token[:])
	return base64.RawURLEncoding.EncodeToString(token[:])
}

func digest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
