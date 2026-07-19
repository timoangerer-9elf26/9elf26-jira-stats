package web

// Unit tests for the Board card's epic-pill colour mapping (#69): the Jira
// "Issue color" (customfield_10017) value mapped to a pill background hex, with
// purple the default when the colour is unset or unrecognised.

import "testing"

func TestEpicPillColor(t *testing.T) {
	const defaultPurple = "#6554C0"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"named purple", "purple", defaultPurple},
		{"grey", "grey", "#6B778C"},
		{"green", "green", "#36B37E"},
		{"blue", "blue", "#2684FF"},
		{"dark variant maps distinctly", "dark_teal", "#008DA6"},
		{"dark_orange", "dark_orange", "#B65C02"},
		{"case-insensitive", "PURPLE", defaultPurple},
		{"unset defaults to purple", "", defaultPurple},
		{"unknown defaults to purple", "chartreuse", defaultPurple},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := epicPillColor(c.in); got != c.want {
				t.Errorf("epicPillColor(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
