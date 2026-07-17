package jira

// Unit test for mapping a Jira Agile board-sprint DTO into the domain Sprint
// entity. Jira Cloud's Agile REST API exposes no actual-activation field (there
// is no activatedDate); the sprint's start/window anchor is startDate (the value
// set in the "Start sprint" dialog), with createdDate as a fallback. completeDate
// remains the completion instant.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestToSprintUsesStartDateForActivation(t *testing.T) {
	// The exact board-sprint page shape Jira Cloud returns: startDate + createdDate
	// on the active sprint, startDate + completeDate on the closed one, and NO
	// activatedDate anywhere (it does not exist in the API).
	const body = `{
      "isLast": true,
      "values": [
        {
          "id": 28,
          "state": "closed",
          "name": "KW28",
          "startDate": "2026-07-06T09:00:00.000+0200",
          "endDate": "2026-07-13T09:00:00.000+0200",
          "completeDate": "2026-07-13T08:30:00.000+0200",
          "createdDate": "2026-07-01T10:00:00.000+0200"
        },
        {
          "id": 29,
          "state": "active",
          "name": "KW29",
          "startDate": "2026-07-13T09:00:00.000+0200",
          "endDate": "2026-07-20T09:00:00.000+0200",
          "createdDate": "2026-07-06T10:00:00.000+0200"
        }
      ]
    }`

	var page sprintsResponse
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode sprints page: %v", err)
	}

	byID := map[int]Sprint{}
	for _, dto := range page.Values {
		sp, err := toSprint(dto)
		if err != nil {
			t.Fatalf("toSprint(%d): %v", dto.ID, err)
		}
		byID[sp.ID] = sp
	}

	// Active sprint: activation instant comes from startDate (13 Jul 07:00 UTC).
	kw29 := byID[29]
	wantKW29 := time.Date(2026, time.July, 13, 7, 0, 0, 0, time.UTC)
	if !kw29.ActivatedAt.UTC().Equal(wantKW29) {
		t.Errorf("KW29 activation = %v, want %v (from startDate)", kw29.ActivatedAt.UTC(), wantKW29)
	}
	if !kw29.CompletedAt.IsZero() {
		t.Errorf("active KW29 must have no completion instant, got %v", kw29.CompletedAt)
	}

	// Closed sprint: activation from startDate (06 Jul 07:00 UTC), completion from
	// completeDate.
	kw28 := byID[28]
	wantKW28Start := time.Date(2026, time.July, 6, 7, 0, 0, 0, time.UTC)
	if !kw28.ActivatedAt.UTC().Equal(wantKW28Start) {
		t.Errorf("KW28 activation = %v, want %v (from startDate)", kw28.ActivatedAt.UTC(), wantKW28Start)
	}
	wantKW28Complete := time.Date(2026, time.July, 13, 6, 30, 0, 0, time.UTC)
	if !kw28.CompletedAt.UTC().Equal(wantKW28Complete) {
		t.Errorf("KW28 completion = %v, want %v (from completeDate)", kw28.CompletedAt.UTC(), wantKW28Complete)
	}
}

func TestToSprintFallsBackToCreatedDate(t *testing.T) {
	// A future sprint that has never been started has no startDate; the createdDate
	// is the only available anchor.
	dto := agileSprintDTO{
		ID: 30, Name: "KW30", State: "future",
		CreatedDate: "2026-07-13T10:00:00.000+0200",
	}
	sp, err := toSprint(dto)
	if err != nil {
		t.Fatalf("toSprint: %v", err)
	}
	want := time.Date(2026, time.July, 13, 8, 0, 0, 0, time.UTC)
	if !sp.ActivatedAt.UTC().Equal(want) {
		t.Errorf("KW30 activation = %v, want %v (fallback to createdDate)", sp.ActivatedAt.UTC(), want)
	}
}
