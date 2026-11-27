package jira

import (
	"regexp"
	"strings"
)

var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9:-]{1,128}$`)

// IsReportableAccountID identifies account IDs that can link retained Jira
// identities to an Atlassian personal data report.
func IsReportableAccountID(id string) bool {
	switch strings.ToLower(id) {
	case "unknown", "stub", "anonymous", "unassigned", "-1":
		return false
	}
	return accountIDPattern.MatchString(id)
}
