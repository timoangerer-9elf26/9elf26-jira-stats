package jira

import (
	"fmt"
	"time"
)

// This file builds the DENSE review dataset (issue #104): a single, deterministic
// fixture that simultaneously stresses the LAYOUT of all four views (Velocity,
// Sprint, Board, Daily) under the pinned review clock REVIEW_NOW=2026-07-15T12:00:00Z
// (14:00 Europe/Berlin, Wed KW29). It is loaded by the review binary via
// NewDenseFakeClient (see cmd/jira-stats, gated on REVIEW_DATASET=dense) — NOT a
// test-only helper — and it does NOT touch the canonical canned_*.json fixtures,
// whose exact values the numeric ACs and existing tests depend on.
//
// The guiding principle is "stress layout, not just logic": for each view it
// covers zero / one / many / max / longest-string. Rather than hand-writing
// sprawling JSON it constructs jira.Issue / jira.Sprint values through small
// builders, so the shape is auditable and the timestamps are computed against the
// pinned clock in one place.
//
// DenseMe is the "me" identity the Daily view should be pinned to for this
// dataset (its "Tickets I created" panel and default assignee). review-up.sh sets
// DAILY_ME to this exact string when REVIEW_DATASET=dense — keep the two in sync.
const DenseMe = "Alexandra Featherstone-Wallington"

// Dense dataset assignees. One deliberately long name (DenseMe) stresses card
// and control layout; the spread gives the Daily assignee control several
// distinct options.
const (
	denseBo    = "Bo"
	denseCarla = "Carla Mendez-Ortiz"
	denseDev   = "Devraj Subramaniam"
	denseEka   = "Ekaterina Vasilyeva"
)

// Dense dataset statuses (the authoritative DCAI workflow — see store.doneStatuses
// / openStatuses). Kept as local consts so the fixture reads clearly.
const (
	stRefinement = "Refinement"
	stReadyToDo  = "Ready To Do"
	stInProgress = "In Progress"
	stReview     = "Review / Testing"
	stDone       = "DONE (This Sprint)"
	stReadyForR  = "Ready for Release"
	stReleased   = "Released / Deployed"
	stCanceled   = "Canceled"
)

// denseSprintID is the active sprint's id/name for the dense dataset; historical
// sprints use the ids/names below.
const (
	denseActiveSprintID   = 29
	denseActiveSprintName = "KW29"
)

// NewDenseFakeClient returns a FakeClient loaded with the dense/adversarial review
// dataset (issue #104) instead of the canonical canned dataset.
func NewDenseFakeClient() *FakeClient {
	return &FakeClient{Issues: denseIssues(), Sprints: denseSprints()}
}

// mustInstant parses an RFC3339 instant for the fixture, panicking on a malformed
// literal (a programmer error in this file, never a runtime condition).
func mustInstant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("densereview: bad timestamp %q: %v", s, err))
	}
	return t
}

// clog builds a `status` changelog entry.
func clog(id, from, to, at string) ChangelogEntry {
	return ChangelogEntry{ID: id, Field: "status", From: from, To: to, Timestamp: mustInstant(at)}
}

// smc builds a sprint-membership change (entering or leaving a sprint).
func smc(id string, sprintID int, name string, entered bool, at string) SprintMembershipChange {
	return SprintMembershipChange{EntryID: id, SprintID: sprintID, SprintName: name, Entered: entered, Timestamp: mustInstant(at)}
}

// denseCategory maps a status to a plausible Jira status category. The app never
// buckets on this (it uses the explicit status strings), but the field is stored,
// so the fixture sets a realistic value including the two intentional Jira
// mismatches (Canceled = Done, Triage = To Do).
func denseCategory(status string) string {
	switch status {
	case stInProgress, stReview:
		return "In Progress"
	case stDone, stReadyForR, stReleased, stCanceled:
		return "Done"
	default:
		return "To Do"
	}
}

// denseSprints returns the dense dataset's board sprints: nine completed sprints
// plus the active KW29, oldest to newest. The Velocity view (trailing 10) shows
// them all as ~10 narrow columns; KW23 carries a deliberately long name to stress
// the bar label, and KW29 is the active/ongoing sprint (no completion instant, so
// it renders the "… – now (ongoing)" line).
func denseSprints() []Sprint {
	closed := func(id int, name, activated, completed string) Sprint {
		return Sprint{ID: id, Name: name, State: "closed", ActivatedAt: mustInstant(activated), CompletedAt: mustInstant(completed)}
	}
	return []Sprint{
		closed(20, "KW20", "2026-05-11T07:00:00Z", "2026-05-18T06:30:00Z"),
		closed(21, "KW21", "2026-05-18T07:00:00Z", "2026-05-25T06:30:00Z"),
		closed(22, "KW22", "2026-05-25T07:00:00Z", "2026-06-01T06:30:00Z"),
		closed(23, "KW23 · Payments platform hardening & data-migration epic", "2026-06-01T07:00:00Z", "2026-06-08T06:30:00Z"),
		closed(24, "KW24", "2026-06-08T07:00:00Z", "2026-06-15T06:30:00Z"),
		closed(25, "KW25", "2026-06-15T07:00:00Z", "2026-06-22T06:30:00Z"),
		closed(26, "KW26", "2026-06-22T07:00:00Z", "2026-06-29T06:30:00Z"),
		closed(27, "KW27", "2026-06-29T07:00:00Z", "2026-07-06T06:30:00Z"),
		closed(28, "KW28", "2026-07-06T07:00:00Z", "2026-07-13T06:30:00Z"),
		{ID: denseActiveSprintID, Name: denseActiveSprintName, State: "active", ActivatedAt: mustInstant("2026-07-13T07:00:00Z")},
	}
}

// denseIssues assembles the full dense issue set: the parent epics (for Board
// pills), the active-sprint KW29 issues that stress the Sprint / Board / Daily
// views, and the historical finished/open issues that give the Velocity bars
// their spread and tall outlier.
func denseIssues() []Issue {
	var issues []Issue
	issues = append(issues, denseEpics()...)
	issues = append(issues, denseKW29Issues()...)
	issues = append(issues, denseHistoricIssues()...)
	return issues
}

// denseEpics are the parent epics the Board cards resolve their coloured pill
// from (via ParentKey → Epic issue → EpicColor). Epics are stored but excluded
// from every rollup, so they never affect counts.
func denseEpics() []Issue {
	epic := func(key, summary, color string) Issue {
		return Issue{Key: key, Type: "Epic", Summary: summary, Status: stInProgress,
			StatusCategory: "In Progress", EpicColor: color, CreatedAt: mustInstant("2026-05-01T09:00:00Z")}
	}
	return []Issue{
		epic("DCAI-E1", "Platform Foundations", "green"),
		epic("DCAI-E2", "Payments, Billing & Invoicing Overhaul", "dark_teal"),
		epic("DCAI-E3", "Growth Experiments", "orange"),
	}
}

// startedEntry / addedEntry are the KW29 membership-entry instants that place a
// ticket in the Started-with cohort (at/before the one-hour grace-window end,
// 08:00Z) or the Added cohort (after it).
const (
	kw29StartedEntry = "2026-07-13T07:30:00Z"
	kw29AddedEntry   = "2026-07-14T09:30:00Z"
)

// kw29 builds a KW29 active-sprint issue in the Started-with cohort (entered
// before the grace-window end), the common case. Special members (Added,
// left-sprint, carry-over) are built inline below.
func kw29(key, summary, typ, status, size, assignee, parent string, cl []ChangelogEntry) Issue {
	return Issue{
		Key: key, Type: typ, Summary: summary, Status: status, StatusCategory: denseCategory(status),
		Size: size, Sprint: denseActiveSprintName, ActiveSprint: denseActiveSprintName, ActiveSprintID: denseActiveSprintID,
		Assignee: assignee, ParentKey: parent, Creator: assignee, CreatedAt: mustInstant("2026-07-13T06:00:00Z"),
		Changelog:     cl,
		SprintChanges: []SprintMembershipChange{smc("sm-"+key, denseActiveSprintID, denseActiveSprintName, true, kw29StartedEntry)},
	}
}

// denseKW29Issues are the active-sprint issues. Between them they populate every
// Board column, every Sprint cohort×outcome cell (including an excluded
// pre-finished carry-over and a long Finished drill-down), and a dense Daily
// digest for DenseMe on the pinned "Today" (Wed 15 Jul, Europe/Berlin) —
// including an intra-done sequence that must be dropped (#98) and created tickets.
func denseKW29Issues() []Issue {
	const longSummary = "Investigate intermittent 504s on the checkout aggregation endpoint under peak concurrent sprint-review load and add backpressure with jittered retries"
	issues := []Issue{
		// --- Started-with × Finished (a long drill-down list) ---------------------
		// D1: finishes TODAY → also a Daily "finished" card for DenseMe.
		kw29("DCAI-D1", "Wire the dense-review dashboard shell and its responsive grid", "Story", stDone, "L", DenseMe, "DCAI-E1", []ChangelogEntry{
			clog("cl-D1-1", stReadyToDo, stInProgress, "2026-07-13T09:00:00Z"),
			clog("cl-D1-2", stInProgress, stDone, "2026-07-15T08:00:00Z"),
		}),
		// D2: finished yesterday, currently Ready for Release (Board column).
		kw29("DCAI-D2", longSummary, "Bug", stReadyForR, "M", DenseMe, "DCAI-E2", []ChangelogEntry{
			clog("cl-D2-1", stReadyToDo, stInProgress, "2026-07-13T10:00:00Z"),
			clog("cl-D2-2", stInProgress, stReadyForR, "2026-07-14T10:00:00Z"),
		}),
		// D3: finished, then an intra-done hop to Released/Deployed (Board column).
		kw29("DCAI-D3", "Rollup ignores no-estimate tickets in the cohort split", "Bug", stReleased, "S", denseBo, "DCAI-E1", []ChangelogEntry{
			clog("cl-D3-1", stReadyToDo, stInProgress, "2026-07-13T09:30:00Z"),
			clog("cl-D3-2", stInProgress, stDone, "2026-07-14T11:00:00Z"),
			clog("cl-D3-3", stDone, stReleased, "2026-07-14T11:30:00Z"),
		}),
		kw29("DCAI-D4", "Reconcile sprint-membership replay with synthetic created entries", "Task", stDone, "L", denseCarla, "DCAI-E2", []ChangelogEntry{
			clog("cl-D4-1", stInProgress, stDone, "2026-07-14T09:00:00Z"),
		}),
		kw29("DCAI-D5", "Add the epic-colour pill and legible white-on-tint contrast tokens", "Story", stReadyForR, "M", denseDev, "DCAI-E3", []ChangelogEntry{
			clog("cl-D5-1", stInProgress, stReadyForR, "2026-07-14T08:00:00Z"),
		}),
		kw29("DCAI-D6", "Backfill avatar initials fallback for unassigned board cards", "Task", stDone, "S", "", "DCAI-E1", []ChangelogEntry{
			clog("cl-D6-1", stInProgress, stDone, "2026-07-13T15:00:00Z"),
		}),
		kw29("DCAI-D7", "Stress the Velocity axis headroom against a tall outlier bar", "Story", stReleased, "L", denseEka, "DCAI-E2", []ChangelogEntry{
			clog("cl-D7-1", stInProgress, stDone, "2026-07-14T13:00:00Z"),
			clog("cl-D7-2", stDone, stReleased, "2026-07-14T14:00:00Z"),
		}),
		// D8: no-estimate finished (NoEstimate cell, 0 points), finishes TODAY.
		kw29("DCAI-D8", "Triage the un-estimated spillover ticket before the review", "Task", stDone, "", DenseMe, "DCAI-E3", []ChangelogEntry{
			clog("cl-D8-1", stReadyToDo, stInProgress, "2026-07-13T12:00:00Z"),
			clog("cl-D8-2", stInProgress, stDone, "2026-07-15T09:15:00Z"),
		}),
		// D19: finished yesterday, then intra-done hops TODAY (dropped from Daily #98),
		// currently Released/Deployed (Board column).
		kw29("DCAI-D19", "Promote the completed migration through the release gate", "Task", stReleased, "M", DenseMe, "DCAI-E2", []ChangelogEntry{
			clog("cl-D19-1", stReadyToDo, stInProgress, "2026-07-13T09:00:00Z"),
			clog("cl-D19-2", stInProgress, stDone, "2026-07-14T09:00:00Z"),
			clog("cl-D19-3", stDone, stReadyForR, "2026-07-15T09:30:00Z"),
			clog("cl-D19-4", stReadyForR, stReleased, "2026-07-15T10:30:00Z"),
		}),

		// --- Started-with × Open (fills the open Board columns) -------------------
		// D9: advances TODAY → Daily "advanced" card for DenseMe.
		kw29("DCAI-D9", "Debounce the resync button and surface freshness inline", "Story", stInProgress, "L", DenseMe, "DCAI-E1", []ChangelogEntry{
			clog("cl-D9-1", stReadyToDo, stInProgress, "2026-07-15T09:00:00Z"),
		}),
		kw29("DCAI-D10", "Review the daily digest bucket ordering copy", "Task", stReview, "M", denseBo, "DCAI-E3", nil),
		kw29("DCAI-D11", "Refine the acceptance-review layout-stress checklist", "Task", stRefinement, "S", denseCarla, "DCAI-E1", nil),
		kw29("DCAI-D12", "Ready the narrow-viewport screenshot protocol", "Story", stReadyToDo, "", "", "DCAI-E2", nil),
		// D22: advances TODAY (Refinement → Ready To Do) → Daily "advanced".
		kw29("DCAI-D22", "Second densely-populated advanced card to fatten the digest", "Task", stReadyToDo, "S", DenseMe, "DCAI-E3", []ChangelogEntry{
			clog("cl-D22-1", stRefinement, stReadyToDo, "2026-07-15T07:45:00Z"),
		}),
		kw29("DCAI-D23", longSummary, "Story", stInProgress, "L", denseDev, "DCAI-E2", nil),
		kw29("DCAI-D24", "Extra review card so the Review / Testing column overflows", "Task", stReview, "M", denseEka, "DCAI-E1", nil),

		// --- Started-with × Removed (cancelled or reprioritised out) --------------
		// D13: cancelled TODAY → Daily "pulled back"; Removed via cancellation.
		kw29("DCAI-D13", "Abandon the duplicate spike after triage", "Bug", stCanceled, "M", DenseMe, "DCAI-E1", []ChangelogEntry{
			clog("cl-D13-1", stReadyToDo, stInProgress, "2026-07-13T11:00:00Z"),
			clog("cl-D13-2", stInProgress, stCanceled, "2026-07-15T10:00:00Z"),
		}),
	}

	// D14: Started-with member reprioritised OUT of the sprint (left it), still
	// open — Removed under the Started-with cohort's "no longer a member" arm. Its
	// active_sprint is cleared (it left), so it is absent from the Board and Daily
	// but its membership history keeps it in the Sprint Removed cell.
	d14 := Issue{
		Key: "DCAI-D14", Type: "Story", Summary: "Reprioritised out of the sprint during mid-week replanning",
		Status: stInProgress, StatusCategory: denseCategory(stInProgress), Size: "L",
		Sprint: "", Assignee: denseCarla, ParentKey: "DCAI-E3", Creator: denseCarla, CreatedAt: mustInstant("2026-07-13T06:00:00Z"),
		Changelog: []ChangelogEntry{clog("cl-D14-1", stReadyToDo, stInProgress, "2026-07-13T09:00:00Z")},
		SprintChanges: []SprintMembershipChange{
			smc("sm-D14-in", denseActiveSprintID, denseActiveSprintName, true, kw29StartedEntry),
			smc("sm-D14-out", denseActiveSprintID, denseActiveSprintName, false, "2026-07-14T12:00:00Z"),
		},
	}

	// D15: pre-finished CARRY-OVER (#87). A Started-with member currently in a done
	// status whose only Done-crossing happened in a PRIOR sprint (before this
	// window) — it lingers un-Released and is EXCLUDED from every Sprint cell, yet
	// still appears on the Board (it is an active-sprint member) in Ready for Release.
	d15 := kw29("DCAI-D15", "Carry-over awaiting release from a prior sprint", "Task", stReadyForR, "L", denseDev, "DCAI-E2", []ChangelogEntry{
		clog("cl-D15-1", stInProgress, stDone, "2026-07-06T09:00:00Z"),
		clog("cl-D15-2", stDone, stReadyForR, "2026-07-06T10:00:00Z"),
	})

	// --- Added cohort (first entry after the grace window) --------------------
	added := func(key, summary, typ, status, size, assignee, parent string, cl []ChangelogEntry) Issue {
		iss := kw29(key, summary, typ, status, size, assignee, parent, cl)
		iss.SprintChanges = []SprintMembershipChange{smc("sm-"+key, denseActiveSprintID, denseActiveSprintName, true, kw29AddedEntry)}
		return iss
	}
	d16 := added("DCAI-D16", "Late scope creep pulled in after planning", "Task", stInProgress, "M", denseDev, "DCAI-E1", []ChangelogEntry{
		clog("cl-D16-1", stReadyToDo, stInProgress, "2026-07-14T10:00:00Z"),
	})
	d17 := added("DCAI-D17", "Added mid-sprint and finished the same week", "Story", stDone, "L", denseEka, "DCAI-E3", []ChangelogEntry{
		clog("cl-D17-1", stInProgress, stDone, "2026-07-15T07:00:00Z"),
	})
	d18 := added("DCAI-D18", "Added then cancelled — the only Added Removed case", "Bug", stCanceled, "S", denseBo, "DCAI-E2", []ChangelogEntry{
		clog("cl-D18-1", stReadyToDo, stInProgress, "2026-07-14T10:00:00Z"),
		clog("cl-D18-2", stInProgress, stCanceled, "2026-07-14T15:00:00Z"),
	})

	// --- Daily "Tickets I created" for DenseMe (authored TODAY, not sprint-scoped) ---
	created := func(key, summary, typ, size string) Issue {
		return Issue{Key: key, Type: typ, Summary: summary, Status: stReadyToDo, StatusCategory: "To Do",
			Size: size, Assignee: DenseMe, Creator: DenseMe, CreatedAt: mustInstant("2026-07-15T07:30:00Z")}
	}
	c20 := created("DCAI-D20", "Draft the layout-stress review protocol amendments and checklist", "Task", "M")
	c21 := created("DCAI-D21", "File the narrow-viewport regression evidence for the velocity bugs", "Bug", "S")

	issues = append(issues, d14, d15, d16, d17, d18, c20, c21)
	return issues
}

// denseHistoricIssues builds the closed-sprint issues whose Finished points give
// the Velocity bars their spread. Each sprint's target points come from a list of
// finished sizes (S=1/M=2/L=3); KW23 is the tall outlier and KW21 is left with a
// single unfinished member so its bar is flat (an all-zero bar).
func denseHistoricIssues() []Issue {
	type hist struct {
		sprintID       int
		name           string
		activated      string
		finishedSizes  []string
		openMemberOnly bool // KW21: a member that never finished → 0-point bar
	}
	specs := []hist{
		{20, "KW20", "2026-05-11T07:00:00Z", []string{"L"}, false},
		{21, "KW21", "2026-05-18T07:00:00Z", nil, true},
		{22, "KW22", "2026-05-25T07:00:00Z", []string{"M"}, false},
		{23, "KW23", "2026-06-01T07:00:00Z", []string{"L", "L", "L", "L", "L", "L", "L", "L"}, false},
		{24, "KW24", "2026-06-08T07:00:00Z", []string{"L"}, false},
		{25, "KW25", "2026-06-15T07:00:00Z", []string{"S"}, false},
		{26, "KW26", "2026-06-22T07:00:00Z", []string{"L", "S"}, false},
		{27, "KW27", "2026-06-29T07:00:00Z", []string{"M"}, false},
		{28, "KW28", "2026-07-06T07:00:00Z", []string{"L", "M"}, false},
	}

	var issues []Issue
	for _, sp := range specs {
		activated := mustInstant(sp.activated)
		// Members enter 30 min after activation (inside the grace window → Started-with).
		enter := activated.Add(30 * time.Minute).UTC().Format(time.RFC3339)
		// A finished ticket crosses into Done ~1 day into the sprint (inside its window).
		cross := activated.Add(24 * time.Hour).UTC().Format(time.RFC3339)

		mkHist := func(n int, status, size string, cl []ChangelogEntry) Issue {
			key := fmt.Sprintf("DCAI-H%d-%d", sp.sprintID, n)
			return Issue{
				Key: key, Type: "Story", Summary: fmt.Sprintf("%s completed work item %d", sp.name, n),
				Status: status, StatusCategory: denseCategory(status), Size: size,
				Sprint: sp.name, Assignee: denseDev, Creator: denseDev, CreatedAt: activated,
				Changelog:     cl,
				SprintChanges: []SprintMembershipChange{smc("sm-"+key, sp.sprintID, sp.name, true, enter)},
			}
		}

		if sp.openMemberOnly {
			issues = append(issues, mkHist(1, stInProgress, "M", []ChangelogEntry{
				clog(fmt.Sprintf("cl-H%d-1", sp.sprintID), stReadyToDo, stInProgress, cross),
			}))
			continue
		}
		for i, size := range sp.finishedSizes {
			n := i + 1
			issues = append(issues, mkHist(n, stReleased, size, []ChangelogEntry{
				clog(fmt.Sprintf("cl-H%d-%d", sp.sprintID, n), stInProgress, stDone, cross),
			}))
		}
	}
	return issues
}
