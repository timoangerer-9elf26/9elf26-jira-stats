package store

// A canned Jira Cloud v3 HTTP server for the backfill integration test. It
// mimics the shapes the real LiveClient parses:
//   - GET /rest/api/3/search/jql  (token-paginated JQL search, expand=changelog)
//   - GET /rest/api/3/issue/{key}/changelog  (startAt-paginated full history)
//
// DCAI-3's search-embedded changelog is deliberately truncated (total 2, one
// history) to force the per-issue fallback, whose history is served in two
// pages to exercise changelog pagination.

import (
	"net/http"
	"strings"
)

func fakeJira(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/rest/api/3/search/jql":
		fakeSearch(w, r)
	case strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") && strings.HasSuffix(r.URL.Path, "/changelog"):
		fakeIssueChangelog(w, r)
	case r.URL.Path == "/rest/agile/1.0/board/8/sprint":
		writeJSON(w, sprintsPage)
	default:
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
	}
}

// sprintsPage is the board's sprints from the Agile API: a closed sprint (both
// lifecycle instants set) and the active one (activated, not yet completed).
// activatedDate/completeDate are the ACTUAL lifecycle instants; the planned
// startDate/endDate are present but deliberately ignored by the parser.
var sprintsPage = `{
      "maxResults": 50, "startAt": 0, "isLast": true,
      "values": [
        {"id": 41, "name": "Sprint 41", "state": "closed",
         "startDate": "2026-07-06T07:00:00.000Z", "endDate": "2026-07-13T07:00:00.000Z",
         "activatedDate": "2026-07-06T07:05:00.000Z", "completeDate": "2026-07-13T06:30:00.000Z"},
        {"id": 42, "name": "Sprint 42", "state": "active",
         "startDate": "2026-07-13T07:00:00.000Z", "endDate": "2026-07-20T07:00:00.000Z",
         "activatedDate": "2026-07-13T07:05:00.000Z"}
      ]
    }`

func fakeSearch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, searchPages[r.URL.Query().Get("nextPageToken")])
}

// Keyed by the incoming nextPageToken ("" = first page).
var searchPages = map[string]string{
	"": `{
      "nextPageToken": "PAGE2",
      "issues": [
        {
          "key": "DCAI-1",
          "fields": {
            "summary": "Wire up the dashboard shell",
            "issuetype": {"name": "Story", "subtask": false},
            "status": {"name": "In Progress", "statusCategory": {"key": "indeterminate", "name": "In Progress"}},
            "assignee": {"displayName": "Ada"},
            "customfield_10040": {"value": "Large"},
            "customfield_10020": [
              {"name": "Sprint 41", "state": "closed", "startDate": "2026-07-06T07:00:00.000Z", "endDate": "2026-07-13T07:00:00.000Z"},
              {"name": "Sprint 42", "state": "active", "startDate": "2026-07-13T07:00:00.000Z", "endDate": "2026-07-20T07:00:00.000Z"}
            ]
          },
          "changelog": {
            "startAt": 0, "maxResults": 100, "total": 2,
            "histories": [
              {"id": "9000", "created": "2026-07-10T09:00:00.000+0200",
               "items": [{"field": "Estimated Time", "fieldId": "customfield_10040", "fromString": "Medium", "toString": "Large"}]},
              {"id": "9001", "created": "2026-07-13T09:00:00.000+0200",
               "items": [{"field": "status", "fromString": "Ready to Do", "toString": "In Progress"}]}
            ]
          }
        },
        {
          "key": "DCAI-2",
          "fields": {
            "summary": "Define the issue snapshot schema",
            "issuetype": {"name": "Task", "subtask": false},
            "status": {"name": "Ready to Do", "statusCategory": {"key": "new", "name": "To Do"}},
            "assignee": null,
            "customfield_10040": {"value": "Medium"},
            "customfield_10020": [{"name": "Sprint 42", "state": "active", "startDate": "2026-07-13T07:00:00.000Z", "endDate": "2026-07-20T07:00:00.000Z"}]
          },
          "changelog": {
            "startAt": 0, "maxResults": 100, "total": 1,
            "histories": [
              {"id": "9010", "created": "2026-07-11T12:00:00.000+0200",
               "items": [
                 {"field": "status", "fromString": "Refinement", "toString": "Ready to Do"},
                 {"field": "Estimated Time", "fieldId": "customfield_10040", "fromString": "", "toString": "Medium"},
                 {"field": "assignee", "fromString": "", "toString": "Grace"}
               ]}
            ]
          }
        }
      ]
    }`,
	"PAGE2": `{
      "issues": [
        {
          "key": "DCAI-3",
          "fields": {
            "summary": "Rollup ignores no-estimate tickets",
            "issuetype": {"name": "Bug", "subtask": false},
            "status": {"name": "DONE (This Sprint)", "statusCategory": {"key": "done", "name": "Done"}},
            "assignee": {"displayName": "Alan"},
            "customfield_10040": null,
            "customfield_10020": []
          },
          "changelog": {
            "startAt": 0, "maxResults": 1, "total": 2,
            "histories": [
              {"id": "9003", "created": "2026-07-14T08:00:00.000+0200",
               "items": [{"field": "status", "fromString": "Review / Testing", "toString": "DONE (This Sprint)"}]}
            ]
          }
        }
      ]
    }`,
}

func fakeIssueChangelog(w http.ResponseWriter, r *http.Request) {
	// Only DCAI-3 should ever be fetched here (it was the truncated one).
	if !strings.Contains(r.URL.Path, "DCAI-3") {
		http.Error(w, "unexpected changelog fetch: "+r.URL.Path, http.StatusBadRequest)
		return
	}
	writeJSON(w, changelogPages[r.URL.Query().Get("startAt")])
}

// Two-page full history for DCAI-3, keyed by the incoming startAt.
var changelogPages = map[string]string{
	"0": `{
      "startAt": 0, "maxResults": 1, "total": 2, "isLast": false,
      "values": [
        {"id": "9002", "created": "2026-07-13T10:00:00.000+0200",
         "items": [{"field": "status", "fromString": "In Progress", "toString": "Review / Testing"}]}
      ]
    }`,
	"1": `{
      "startAt": 1, "maxResults": 1, "total": 2, "isLast": true,
      "values": [
        {"id": "9003", "created": "2026-07-14T08:00:00.000+0200",
         "items": [{"field": "status", "fromString": "Review / Testing", "toString": "DONE (This Sprint)"}]}
      ]
    }`,
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
