package main

import (
	"strings"
	"testing"
)

func TestLiveJiraRequiresOAuthEvenWithAuthDisabled(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://site.example")
	t.Setenv("JIRA_EMAIL", "service@example.com")
	t.Setenv("JIRA_API_TOKEN", "service-token")
	t.Setenv("JIRA_FAKE", "")
	t.Setenv("AUTH_DISABLED", "true")
	_, fake, err := jiraClient()
	if err != nil || fake {
		t.Fatalf("expected live Jira configuration, got fake=%v, err=%v", fake, err)
	}
	if _, err := configureAuth(nil, fake); err == nil || !strings.Contains(err.Error(), "AUTH_DISABLED") {
		t.Fatalf("live mode must reject disabled auth, got %v", err)
	}
}

func TestPartialLiveJiraConfigurationFailsClosed(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://site.example")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_FAKE", "")
	if _, _, err := jiraClient(); err == nil {
		t.Fatal("partial service credentials must fail")
	}
}

func TestExplicitFakeModeCanRenderOAuthLogin(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://site.example")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_FAKE", "true")
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("JIRA_OAUTH_CLIENT_ID", "preview")
	t.Setenv("JIRA_OAUTH_CLIENT_SECRET", "preview")
	t.Setenv("JIRA_OAUTH_CLOUD_ID", "preview")
	t.Setenv("JIRA_OAUTH_REDIRECT_URI", "https://app.example/auth/callback")
	t.Setenv("JIRA_OAUTH_ALLOWED_ACCOUNT_IDS", "preview")
	t.Setenv("JIRA_OAUTH_TOKEN_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	_, fake, err := jiraClient()
	if err != nil || !fake {
		t.Fatalf("expected fake Jira configuration, got fake=%v, err=%v", fake, err)
	}
	if opt, err := configureAuth(nil, fake); err != nil || opt == nil {
		t.Fatalf("expected OAuth to be enabled, got err=%v", err)
	}
}
