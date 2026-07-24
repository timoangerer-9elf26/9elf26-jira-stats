package web

import "testing"

// TestTrimVersionSHA covers the display-only trim that drops the git short SHA
// from the build identity for the nav marker (#164): the full "tag (sha)" string
// stays reachable at GET /version, but the UI shows the CalVer tag alone. A
// local "dev" build (no SHA) is passed through unchanged.
func TestTrimVersionSHA(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"release tag with sha", "v2026.07.24.162 (abc1234)", "v2026.07.24.162"},
		{"dev build", "dev", "dev"},
		{"tag without sha", "v2026.07.24.162", "v2026.07.24.162"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trimVersionSHA(tc.in); got != tc.want {
				t.Fatalf("trimVersionSHA(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
