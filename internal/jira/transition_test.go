package jira

// Selection semantics for the Board transition seam (docs/adr/0010, #194):
// a transition is resolved by the id of the status it lands in, never by its
// name. The DCAI workflow contains a transition literally labelled "Done" whose
// target is "Ready for release" — matching on names would pick it for a request
// that means "DONE (This Sprint)".

import (
	"errors"
	"testing"
)

func TestTransitionToResolvesByTargetStatusID(t *testing.T) {
	offered := DCAITransitions()

	tr, err := TransitionTo(offered, StatusIDDoneThisSprint)
	if err != nil {
		t.Fatalf("TransitionTo(DONE (This Sprint)): %v", err)
	}
	if tr.ToStatusID != StatusIDDoneThisSprint {
		t.Errorf("target status id = %q, want %q", tr.ToStatusID, StatusIDDoneThisSprint)
	}
	// The decoy: a transition named "Done" exists, and it lands somewhere else.
	if tr.Name == "Done" {
		t.Errorf("selected the ambiguous %q transition (id %s) for target status %s",
			tr.Name, tr.ID, StatusIDDoneThisSprint)
	}
}

func TestTransitionToIgnoresTransitionNames(t *testing.T) {
	// Two transitions whose NAMES both look like the request, distinguished only
	// by where they land.
	offered := []Transition{
		{ID: "31", Name: "Done", ToStatusID: "10016", ToStatusName: "Ready for release"},
		{ID: "5", Name: "Done", ToStatusID: "10064", ToStatusName: "DONE (This Sprint)"},
	}
	tr, err := TransitionTo(offered, "10064")
	if err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if tr.ID != "5" {
		t.Errorf("transition id = %q, want %q (the one landing in 10064)", tr.ID, "5")
	}
}

func TestTransitionToRejectsAStatusJiraDoesNotOffer(t *testing.T) {
	// Model the workflow WITHOUT a route into Ready for release, the case the
	// Board must fail cleanly on rather than performing some other transition.
	var offered []Transition
	for _, tr := range DCAITransitions() {
		if tr.ToStatusID != StatusIDReadyForRelease {
			offered = append(offered, tr)
		}
	}

	tr, err := TransitionTo(offered, StatusIDReadyForRelease)
	if err == nil {
		t.Fatalf("TransitionTo returned transition %q (%s), want an error", tr.Name, tr.ID)
	}
	if !errors.Is(err, ErrNoTransition) {
		t.Errorf("error = %v, want it to wrap ErrNoTransition", err)
	}
	if tr.ID != "" {
		t.Errorf("returned transition = %+v, want the zero value on error", tr)
	}
}

func TestTransitionToRejectsAnEmptyOfferedSet(t *testing.T) {
	if _, err := TransitionTo(nil, StatusIDInProgress); !errors.Is(err, ErrNoTransition) {
		t.Errorf("error = %v, want ErrNoTransition", err)
	}
}

// The canned DCAI set is the live workflow (verified 2026-09-01): every status
// is reachable, and the "Done" label lands in Ready for release.
func TestDCAITransitionsMirrorsTheLiveWorkflow(t *testing.T) {
	byStatus := map[string]Transition{}
	for _, tr := range DCAITransitions() {
		byStatus[tr.ToStatusID] = tr
	}
	for _, id := range []string{
		StatusIDTriage, StatusIDRefinement, StatusIDReadyToDo, StatusIDInProgress,
		StatusIDReviewTesting, StatusIDDoneThisSprint, StatusIDReadyForRelease,
		StatusIDReleasedDeployed, StatusIDCanceled,
	} {
		if _, ok := byStatus[id]; !ok {
			t.Errorf("no transition into status %s", id)
		}
	}
	if got := byStatus[StatusIDReadyForRelease].Name; got != "Done" {
		t.Errorf("transition into Ready for release is named %q, want %q", got, "Done")
	}
}
