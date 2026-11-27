package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

const CallbackPath = "/auth/callback"

var jiraScopes = []string{"read:jira-work", "write:jira-work", "read:jira-user"}

// Config describes a resource-restricted Atlassian 3LO app. Endpoint overrides
// are for an HTTP test provider. Runtime configuration uses Atlassian's endpoints.
type Config struct {
	ClientID, ClientSecret, RedirectURI, CloudID, SiteURL string
	AllowedAccountIDs, TokenKey                           string
	ProjectKey, BoardID                                   string
	AuthBaseURL, APIBaseURL                               string
}

type provider struct {
	cfg  Config
	http *http.Client
}

func (p provider) authorizeURL(state string) string {
	scopes := strings.Join(jiraScopes, " ") + " offline_access"
	return p.cfg.AuthBaseURL + "/authorize?" + url.Values{
		"audience": {"api.atlassian.com"}, "client_id": {p.cfg.ClientID},
		"scope": {scopes}, "redirect_uri": {p.cfg.RedirectURI}, "state": {state},
		"response_type": {"code"}, "prompt": {"consent"},
	}.Encode()
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func (p provider) exchange(ctx context.Context, fields map[string]string) (tokenPair, error) {
	fields["client_id"], fields["client_secret"] = p.cfg.ClientID, p.cfg.ClientSecret
	payload, err := json.Marshal(fields)
	if err != nil {
		return tokenPair{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.AuthBaseURL+"/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return tokenPair{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return tokenPair{}, fmt.Errorf("Atlassian token request: %w", err)
	}
	defer resp.Body.Close()
	var pair tokenPair
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pair)
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && pair.Error == "invalid_grant" {
			return tokenPair{}, ErrSignIn
		}
		return tokenPair{}, fmt.Errorf("Atlassian token request: HTTP %d", resp.StatusCode)
	}
	if decodeErr != nil || pair.AccessToken == "" || pair.ExpiresIn <= 0 {
		return tokenPair{}, fmt.Errorf("Atlassian returned an incomplete token response")
	}
	if pair.RefreshToken == "" {
		return tokenPair{}, ErrSignIn
	}
	return pair, nil
}

func (p provider) checkSite(ctx context.Context, bearer string) error {
	var sites []struct {
		ID, URL string
		Scopes  []string
	}
	if err := p.get(ctx, p.cfg.APIBaseURL+"/oauth/token/accessible-resources", bearer, &sites); err != nil {
		return err
	}
	for _, site := range sites {
		if site.ID == p.cfg.CloudID && strings.TrimRight(site.URL, "/") == p.cfg.SiteURL &&
			slices.Contains(site.Scopes, jiraScopes[0]) && slices.Contains(site.Scopes, jiraScopes[1]) &&
			slices.Contains(site.Scopes, jiraScopes[2]) {
			return nil
		}
	}
	return ErrSignIn
}

func (p provider) checkProject(ctx context.Context, bearer string) error {
	const permission = "BROWSE_PROJECTS"
	var result struct {
		Permissions map[string]struct {
			HavePermission bool
		}
	}
	query := url.Values{"projectKey": {p.cfg.ProjectKey}, "permissions": {permission}}
	if err := p.get(ctx, p.jiraURL()+"/rest/api/3/mypermissions?"+query.Encode(), bearer, &result); err != nil {
		return err
	}
	if !result.Permissions[permission].HavePermission {
		return ErrProjectAccess
	}
	return nil
}

func (p provider) myself(ctx context.Context, bearer string) (string, string, error) {
	var user struct {
		AccountID string `json:"accountId"`
		TimeZone  string `json:"timeZone"`
		Active    bool   `json:"active"`
	}
	if err := p.get(ctx, p.jiraURL()+"/rest/api/3/myself", bearer, &user); err != nil {
		return "", "", err
	}
	if user.AccountID == "" || !user.Active {
		return "", "", ErrSignIn
	}
	if _, err := jiraTimeZone(user.TimeZone); err != nil {
		return "", "", err
	}
	return user.AccountID, user.TimeZone, nil
}

func jiraTimeZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("Jira Myself did not provide an IANA time zone")
	}
	return time.LoadLocation(name)
}

func (p provider) jiraURL() string {
	return p.cfg.APIBaseURL + "/ex/jira/" + url.PathEscape(p.cfg.CloudID)
}

func (p provider) client(bearer string) *jira.LiveClient {
	return jira.NewBearerClient(jira.Config{
		BaseURL: p.jiraURL(), ProjectKey: p.cfg.ProjectKey, BoardID: p.cfg.BoardID,
	}, bearer, p.http)
}

func (p provider) get(ctx context.Context, endpoint, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("Atlassian resource request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrSignIn
	default:
		return fmt.Errorf("Atlassian resource request: HTTP %d", resp.StatusCode)
	}
}
