package web

// White-box unit tests for dailyRange's pure window maths. dailyRange only reads
// s.loc, so a bare Server with the display timezone is enough to exercise it with
// a fixed clock. The "Since yesterday" window spans the last *working* day, so a
// Monday (and the weekend) reaches back to Friday rather than the prior calendar
// day.

import (
	"testing"
	"time"
)

func rangeServer(t *testing.T) *Server {
	t.Helper()
	loc, err := time.LoadLocation(displayTimeZone)
	if err != nil {
		t.Fatalf("load %s: %v", displayTimeZone, err)
	}
	return &Server{loc: loc}
}

func TestDailyRangeSinceYesterday(t *testing.T) {
	s := rangeServer(t)
	loc := s.loc

	// 2026-07-13 is a Monday; 2026-07-16 is a Thursday.
	cases := []struct {
		name     string
		now      time.Time
		wantFrom time.Time
	}{
		{
			name:     "Monday reaches back to Friday",
			now:      time.Date(2026, time.July, 13, 8, 30, 0, 0, loc), // Monday
			wantFrom: time.Date(2026, time.July, 10, 0, 0, 0, 0, loc),  // Friday 00:00
		},
		{
			name:     "mid-week is the previous calendar day",
			now:      time.Date(2026, time.July, 16, 10, 0, 0, 0, loc), // Thursday
			wantFrom: time.Date(2026, time.July, 15, 0, 0, 0, 0, loc),  // Wednesday 00:00
		},
		{
			name:     "Tuesday is the previous calendar day (Monday)",
			now:      time.Date(2026, time.July, 14, 9, 0, 0, 0, loc), // Tuesday
			wantFrom: time.Date(2026, time.July, 13, 0, 0, 0, 0, loc), // Monday 00:00
		},
		{
			name:     "Saturday reaches back to Friday",
			now:      time.Date(2026, time.July, 11, 9, 0, 0, 0, loc), // Saturday
			wantFrom: time.Date(2026, time.July, 10, 0, 0, 0, 0, loc), // Friday 00:00
		},
		{
			name:     "Sunday reaches back to Friday",
			now:      time.Date(2026, time.July, 12, 9, 0, 0, 0, loc), // Sunday
			wantFrom: time.Date(2026, time.July, 10, 0, 0, 0, 0, loc), // Friday 00:00
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, to := s.dailyRange(dailyWindowSinceYesterday, tc.now)
			if !from.Equal(tc.wantFrom) {
				t.Errorf("from = %s, want %s", from, tc.wantFrom)
			}
			if !to.Equal(tc.now) {
				t.Errorf("to = %s, want now %s", to, tc.now)
			}
		})
	}
}

func TestDailyRangeLast24hUnchanged(t *testing.T) {
	s := rangeServer(t)
	now := time.Date(2026, time.July, 13, 8, 30, 0, 0, s.loc) // Monday
	from, to := s.dailyRange(dailyWindowLast24h, now)
	if want := now.Add(-24 * time.Hour); !from.Equal(want) {
		t.Errorf("last-24h from = %s, want %s", from, want)
	}
	if !to.Equal(now) {
		t.Errorf("last-24h to = %s, want now %s", to, now)
	}
}
