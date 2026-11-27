package web

import (
	"testing"
	"time"
)

func TestRangeLabel(t *testing.T) {
	berlin := time.FixedZone("CET", 60*60)
	start := time.Date(2026, time.November, 27, 9, 0, 0, 0, berlin)

	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want string
	}{
		{"first sprint day", start, start.Add(time.Minute).UTC(), "27 Nov 2026"},
		{"exclusive midnight", start, time.Date(2026, time.November, 28, 0, 0, 0, 0, berlin), "27 Nov 2026"},
		{"after midnight", start, time.Date(2026, time.November, 28, 0, 1, 0, 0, berlin), "27 Nov – 28 Nov 2026"},
		{"different years", time.Date(2025, time.December, 30, 0, 0, 0, 0, berlin), time.Date(2026, time.January, 2, 0, 0, 0, 0, berlin), "30 Dec 2025 – 1 Jan 2026"},
		{"empty", start, start, "27 Nov 2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rangeLabel(tt.from, tt.to); got != tt.want {
				t.Errorf("rangeLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
