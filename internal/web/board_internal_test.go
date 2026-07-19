package web

// Unit tests for the Board card's assignee-avatar initials fallback (#68):
// initials computed from the assignee display name, the single-word case, and
// the unassigned case (empty initials → the view renders a neutral circle).

import "testing"

func TestAvatarInitials(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"first and last name", "Ada Lovelace", "AL"},
		{"three names use first and last", "Alan Mathison Turing", "AT"},
		{"single word uses first two letters", "Grace", "GR"},
		{"single letter name", "X", "X"},
		{"lower-cased input is upper-cased", "grace hopper", "GH"},
		{"surrounding whitespace ignored", "  Ada   Lovelace  ", "AL"},
		{"unassigned yields empty", "", ""},
		{"whitespace-only yields empty", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := avatarInitials(c.in); got != c.want {
				t.Errorf("avatarInitials(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
