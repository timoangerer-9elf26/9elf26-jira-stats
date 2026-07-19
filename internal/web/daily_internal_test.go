package web

// White-box unit tests for the Daily range selection: the working-day preset
// resolution (Today / Yesterday / day-before-yesterday, walking back over
// weekends), the weekend disabling of Today, the default selection, custom-range
// parsing, and the invalid-range error path. dailyRangeSelection only reads
// s.loc, so a bare Server with the display timezone is enough to exercise it with
// a fixed clock.

import (
	"net/url"
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

// berlinDay is a convenience constructor for a Berlin midnight.
func berlinDay(loc *time.Location, y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// selectedPreset returns the key of the selected preset, or "" when none is
// selected (custom mode).
func selectedPreset(res dailyRangeResult) string {
	for _, p := range res.presets {
		if p.Selected {
			return p.Key
		}
	}
	return ""
}

// presetByKey looks up a preset view in the result.
func presetByKey(res dailyRangeResult, key string) (dailyPresetView, bool) {
	for _, p := range res.presets {
		if p.Key == key {
			return p, true
		}
	}
	return dailyPresetView{}, false
}

// TestDailyPresetResolutionEveryWeekday walks the whole week (2026-07-13 is a
// Monday) and checks each preset's resolved day plus the weekend disabling of
// Today. Yesterday / day-before walk back over weekends to the most recent
// working days.
func TestDailyPresetResolutionEveryWeekday(t *testing.T) {
	s := rangeServer(t)
	loc := s.loc

	cases := []struct {
		name          string
		now           time.Time
		wantToday     time.Time
		wantYesterday time.Time
		wantDayBefore time.Time
		todayDisabled bool
	}{
		{
			name:          "Monday walks back over the weekend",
			now:           berlinDay(loc, 2026, time.July, 13).Add(8 * time.Hour), // Mon
			wantToday:     berlinDay(loc, 2026, time.July, 13),                    // Mon
			wantYesterday: berlinDay(loc, 2026, time.July, 10),                    // Fri
			wantDayBefore: berlinDay(loc, 2026, time.July, 9),                     // Thu
		},
		{
			name:          "Tuesday",
			now:           berlinDay(loc, 2026, time.July, 14).Add(9 * time.Hour), // Tue
			wantToday:     berlinDay(loc, 2026, time.July, 14),                    // Tue
			wantYesterday: berlinDay(loc, 2026, time.July, 13),                    // Mon
			wantDayBefore: berlinDay(loc, 2026, time.July, 10),                    // Fri
		},
		{
			name:          "Wednesday",
			now:           berlinDay(loc, 2026, time.July, 15).Add(9 * time.Hour), // Wed
			wantToday:     berlinDay(loc, 2026, time.July, 15),                    // Wed
			wantYesterday: berlinDay(loc, 2026, time.July, 14),                    // Tue
			wantDayBefore: berlinDay(loc, 2026, time.July, 13),                    // Mon
		},
		{
			name:          "Thursday",
			now:           berlinDay(loc, 2026, time.July, 16).Add(9 * time.Hour), // Thu
			wantToday:     berlinDay(loc, 2026, time.July, 16),                    // Thu
			wantYesterday: berlinDay(loc, 2026, time.July, 15),                    // Wed
			wantDayBefore: berlinDay(loc, 2026, time.July, 14),                    // Tue
		},
		{
			name:          "Friday",
			now:           berlinDay(loc, 2026, time.July, 17).Add(9 * time.Hour), // Fri
			wantToday:     berlinDay(loc, 2026, time.July, 17),                    // Fri
			wantYesterday: berlinDay(loc, 2026, time.July, 16),                    // Thu
			wantDayBefore: berlinDay(loc, 2026, time.July, 15),                    // Wed
		},
		{
			name:          "Saturday disables Today, walks back to Friday",
			now:           berlinDay(loc, 2026, time.July, 18).Add(9 * time.Hour), // Sat
			wantToday:     berlinDay(loc, 2026, time.July, 18),                    // Sat
			wantYesterday: berlinDay(loc, 2026, time.July, 17),                    // Fri
			wantDayBefore: berlinDay(loc, 2026, time.July, 16),                    // Thu
			todayDisabled: true,
		},
		{
			name:          "Sunday disables Today, walks back to Friday",
			now:           berlinDay(loc, 2026, time.July, 19).Add(9 * time.Hour), // Sun
			wantToday:     berlinDay(loc, 2026, time.July, 19),                    // Sun
			wantYesterday: berlinDay(loc, 2026, time.July, 17),                    // Fri
			wantDayBefore: berlinDay(loc, 2026, time.July, 16),                    // Thu
			todayDisabled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Resolve each preset explicitly and check its [day, day+1) range.
			checks := []struct {
				key string
				day time.Time
			}{
				{dailyPresetToday, tc.wantToday},
				{dailyPresetYesterday, tc.wantYesterday},
				{dailyPresetDayBefore, tc.wantDayBefore},
			}
			for _, c := range checks {
				res := s.dailyRangeSelection(url.Values{"preset": {c.key}}, tc.now)
				// A disabled Today is never actually selected: it falls back to
				// Yesterday, so skip the range assertion for that case.
				if c.key == dailyPresetToday && tc.todayDisabled {
					if selectedPreset(res) != dailyPresetYesterday {
						t.Errorf("weekend Today should fall back to Yesterday, got %q", selectedPreset(res))
					}
				} else {
					if !res.from.Equal(c.day) {
						t.Errorf("%s from = %s, want %s", c.key, res.from, c.day)
					}
					if want := c.day.AddDate(0, 0, 1); !res.to.Equal(want) {
						t.Errorf("%s to = %s, want %s", c.key, res.to, want)
					}
					if selectedPreset(res) != c.key {
						t.Errorf("%s should be the selected preset, got %q", c.key, selectedPreset(res))
					}
				}
				// The Today preset button carries the disabled flag on weekends.
				today, ok := presetByKey(res, dailyPresetToday)
				if !ok {
					t.Fatalf("Today preset button missing")
				}
				if today.Disabled != tc.todayDisabled {
					t.Errorf("Today.Disabled = %v, want %v", today.Disabled, tc.todayDisabled)
				}
				yest, _ := presetByKey(res, dailyPresetYesterday)
				db, _ := presetByKey(res, dailyPresetDayBefore)
				if yest.Disabled || db.Disabled {
					t.Errorf("Yesterday/day-before must never be disabled")
				}
			}

			// The day-before button is labelled with the full weekday name of its day.
			res := s.dailyRangeSelection(url.Values{"preset": {dailyPresetDayBefore}}, tc.now)
			db, _ := presetByKey(res, dailyPresetDayBefore)
			if want := tc.wantDayBefore.Format("Monday"); db.Label != want {
				t.Errorf("day-before label = %q, want %q", db.Label, want)
			}
			// Each preset carries a concrete-date hover title.
			today, _ := presetByKey(res, dailyPresetToday)
			if want := tc.wantToday.Format(dailyTitleFormat); today.Title != want {
				t.Errorf("Today title = %q, want %q", today.Title, want)
			}
			yest, _ := presetByKey(res, dailyPresetYesterday)
			if want := tc.wantYesterday.Format(dailyTitleFormat); yest.Title != want {
				t.Errorf("Yesterday title = %q, want %q", yest.Title, want)
			}
		})
	}
}

// TestDailyDefaultSelection: with no preset/from/to params the default is Today
// on a working day, falling back to Yesterday when Today is disabled (weekend).
func TestDailyDefaultSelection(t *testing.T) {
	s := rangeServer(t)
	loc := s.loc

	weekday := s.dailyRangeSelection(url.Values{}, berlinDay(loc, 2026, time.July, 16).Add(9*time.Hour)) // Thu
	if got := selectedPreset(weekday); got != dailyPresetToday {
		t.Errorf("weekday default = %q, want today", got)
	}

	weekend := s.dailyRangeSelection(url.Values{}, berlinDay(loc, 2026, time.July, 18).Add(9*time.Hour)) // Sat
	if got := selectedPreset(weekend); got != dailyPresetYesterday {
		t.Errorf("weekend default = %q, want yesterday", got)
	}
}

// TestDailyCustomRangeParsing: a valid custom From/Until is honoured verbatim
// (weekend transitions included), no preset is highlighted, and the inputs
// round-trip in datetime-local form.
func TestDailyCustomRangeParsing(t *testing.T) {
	s := rangeServer(t)
	loc := s.loc
	now := berlinDay(loc, 2026, time.July, 16).Add(9 * time.Hour)

	// A range that straddles a weekend — must be honoured verbatim.
	res := s.dailyRangeSelection(url.Values{
		"from": {"2026-07-10T14:30"},
		"to":   {"2026-07-13T09:00"},
	}, now)

	if res.errMsg != "" {
		t.Fatalf("valid custom range should not error, got %q", res.errMsg)
	}
	wantFrom := time.Date(2026, time.July, 10, 14, 30, 0, 0, loc)
	wantTo := time.Date(2026, time.July, 13, 9, 0, 0, 0, loc)
	if !res.from.Equal(wantFrom) {
		t.Errorf("from = %s, want %s", res.from, wantFrom)
	}
	if !res.to.Equal(wantTo) {
		t.Errorf("to = %s, want %s", res.to, wantTo)
	}
	if got := selectedPreset(res); got != "" {
		t.Errorf("custom range should highlight no preset, got %q", got)
	}
	if res.customFrom != "2026-07-10T14:30" || res.customTo != "2026-07-13T09:00" {
		t.Errorf("custom inputs should round-trip, got from=%q to=%q", res.customFrom, res.customTo)
	}
}

// TestDailyInvalidRange: From >= Until and a malformed field both yield an inline
// error and no resolved range (no fallback).
func TestDailyInvalidRange(t *testing.T) {
	s := rangeServer(t)
	loc := s.loc
	now := berlinDay(loc, 2026, time.July, 16).Add(9 * time.Hour)

	cases := []struct {
		name string
		q    url.Values
	}{
		{"from after until", url.Values{"from": {"2026-07-16T10:00"}, "to": {"2026-07-16T08:00"}}},
		{"from equals until", url.Values{"from": {"2026-07-16T10:00"}, "to": {"2026-07-16T10:00"}}},
		{"malformed from", url.Values{"from": {"nonsense"}, "to": {"2026-07-16T10:00"}}},
		{"missing to", url.Values{"from": {"2026-07-16T10:00"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := s.dailyRangeSelection(tc.q, now)
			if res.errMsg == "" {
				t.Errorf("expected an inline error for %s", tc.name)
			}
			if !res.from.IsZero() || !res.to.IsZero() {
				t.Errorf("invalid range must resolve no window (no fallback), got [%s, %s)", res.from, res.to)
			}
			if got := selectedPreset(res); got != "" {
				t.Errorf("invalid range must highlight no preset, got %q", got)
			}
		})
	}
}
