package jira

// Jira has no "set status" operation. To move an issue you read the transitions
// its workflow currently offers, then post the id of the one you want. This file
// is the seam that turns "put this ticket in status X" into "perform transition
// N" — the groundwork for the Board's drag-and-drop (docs/adr/0010, #194/#195).

import (
	"errors"
	"fmt"
)

// Transition is one workflow transition Jira offers for an issue right now.
//
// Name is the transition's LABEL and is deliberately not something to select on:
// the DCAI workflow contains a transition labelled "Done" whose target status is
// "Ready for release", so a name match would silently land a ticket in the wrong
// column. ToStatusID is the stable identity a caller should ask for.
type Transition struct {
	ID               string // transition id to POST
	Name             string // transition label — ambiguous, never select on this
	ToStatusID       string // id of the status the transition lands in
	ToStatusName     string // name of that status, as Jira spells it
	ToStatusCategory string // its Jira status category ("To Do"/"In Progress"/"Done")
}

// DCAI workflow status ids, verified against live Jira 2026-09-01 (project DCAI,
// board 8). Status ids are stable per site; the names are Jira's own spelling —
// note "Ready for release" is lower-case in Jira while the Board column and
// CONTEXT write it "Ready for Release" (the store normalises case).
const (
	StatusIDTriage           = "10097" // Triage
	StatusIDRefinement       = "10017" // Refinement
	StatusIDReadyToDo        = "10014" // Ready To Do
	StatusIDInProgress       = "10015" // In Progress
	StatusIDReviewTesting    = "10018" // Review / Testing
	StatusIDDoneThisSprint   = "10064" // DONE (This Sprint)
	StatusIDReadyForRelease  = "10016" // Ready for release
	StatusIDReleasedDeployed = "10019" // Released / Deployed
	StatusIDCanceled         = "10099" // Canceled
)

// ErrNoTransition reports that the workflow offers no transition into the
// requested target status for that issue. Callers must fail on it rather than
// falling back to some other transition — a wrong transition writes real
// workflow history (docs/adr/0010).
var ErrNoTransition = errors.New("jira: no transition into target status")

// TransitionTo picks, from the transitions Jira currently offers for an issue,
// the one whose TARGET STATUS ID is statusID. Selection is by target status id
// only; transition names are never consulted. When nothing lands in that status
// it returns the zero Transition and an error wrapping ErrNoTransition.
func TransitionTo(offered []Transition, statusID string) (Transition, error) {
	for _, tr := range offered {
		if tr.ToStatusID == statusID {
			return tr, nil
		}
	}
	return Transition{}, fmt.Errorf("%w: status %s (offered: %s)", ErrNoTransition, statusID, offeredSummary(offered))
}

// offeredSummary renders the offered set for an error message, so a failure says
// what Jira actually allowed rather than just "not allowed".
func offeredSummary(offered []Transition) string {
	if len(offered) == 0 {
		return "none"
	}
	out := ""
	for i, tr := range offered {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s→%s(%s)", tr.ID, tr.ToStatusID, tr.ToStatusName)
	}
	return out
}

// DCAITransitions is the live DCAI workflow's transition set, captured
// 2026-09-01 from two different source statuses (In Progress and Refinement),
// which offered the identical set. The workflow is effectively all-to-all: every
// one of the nine statuses is reachable from any status.
//
// It backs the fake Jira client so the transition path is exercisable without
// live Jira, and it is the fixture the selection tests use — in particular the
// transition labelled "Done" (id 31), which lands in Ready for release and
// matches no Board column by name.
func DCAITransitions() []Transition {
	return []Transition{
		{ID: "17", Name: "Triage", ToStatusID: StatusIDTriage, ToStatusName: "Triage", ToStatusCategory: "To Do"},
		{ID: "2", Name: "Refinement", ToStatusID: StatusIDRefinement, ToStatusName: "Refinement", ToStatusCategory: "To Do"},
		{ID: "10", Name: "Ready To Do", ToStatusID: StatusIDReadyToDo, ToStatusName: "Ready To Do", ToStatusCategory: "To Do"},
		{ID: "21", Name: "In Progress", ToStatusID: StatusIDInProgress, ToStatusName: "In Progress", ToStatusCategory: "In Progress"},
		{ID: "3", Name: "Review / Testing", ToStatusID: StatusIDReviewTesting, ToStatusName: "Review / Testing", ToStatusCategory: "In Progress"},
		{ID: "5", Name: "DONE (This Sprint)", ToStatusID: StatusIDDoneThisSprint, ToStatusName: "DONE (This Sprint)", ToStatusCategory: "Done"},
		{ID: "31", Name: "Done", ToStatusID: StatusIDReadyForRelease, ToStatusName: "Ready for release", ToStatusCategory: "Done"},
		{ID: "4", Name: "Released / Deployed", ToStatusID: StatusIDReleasedDeployed, ToStatusName: "Released / Deployed", ToStatusCategory: "Done"},
		{ID: "6", Name: "Canceled", ToStatusID: StatusIDCanceled, ToStatusName: "Canceled", ToStatusCategory: "Done"},
	}
}
