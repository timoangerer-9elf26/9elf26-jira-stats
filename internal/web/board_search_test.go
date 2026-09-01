package web_test

// Integration tests for the Board search filter (#193): the text box in the Board
// filter chrome, case-insensitive substring matching over key / title / parent
// epic title, the ?q= URL round-trip, composition with the three existing filters,
// and the explicit "no cards match" state.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// boardSearchFixture is an active-sprint (KW29) mix built so each searchable
// field can be isolated: a key-only match, a title-only match, an epic-title-only
// match, and an assignee whose name appears in NO key, title or epic title (so a
// search for it proves assignee is not matched).
func boardSearchFixture() *jira.FakeClient {
	epic := func(key, summary string) jira.Issue {
		return jira.Issue{Key: key, Type: "Epic", Summary: summary, Status: "In Progress", StatusCategory: "In Progress", EpicColor: "green"}
	}
	card := func(key, summary, parent, assignee, size string) jira.Issue {
		return jira.Issue{
			Key: key, Type: "Task", Summary: summary, Status: "In Progress",
			StatusCategory: "In Progress", ActiveSprint: "KW29",
			ParentKey: parent, Assignee: assignee, Size: size,
		}
	}
	return &jira.FakeClient{Sprints: activeSprintKW29(), Issues: []jira.Issue{
		epic("DCAI-200", "Checkout revamp"),
		epic("DCAI-201", "Billing overhaul"),
		// Title carries "retry"; epic carries "Checkout"; Ada; sized.
		card("DCAI-20", "Payment retry logic", "DCAI-200", "Ada Lovelace", "M"),
		// Title carries "banner"; epic carries "Billing"; Grace; unsized.
		card("DCAI-21", "Fix login banner", "DCAI-201", "Grace Hopper", ""),
		// No epic; Ada; unsized.
		card("DCAI-22", "Rework the sign-up flow", "", "Ada Lovelace", ""),
		// Epic carries "Checkout"; Ada; unsized — the survivor of the intersection.
		card("DCAI-23", "Tidy up fixtures", "DCAI-200", "Ada Lovelace", ""),
	}}
}

const boardSearchAllKeys = "DCAI-20 DCAI-21 DCAI-22 DCAI-23"

// assertSearchFixtureCards asserts exactly the given keys render as board cards,
// over the search fixture's four keys and no others.
func assertSearchFixtureCards(t *testing.T, body string, want ...string) {
	t.Helper()
	wanted := map[string]bool{}
	for _, k := range want {
		wanted[k] = true
	}
	for _, key := range strings.Fields(boardSearchAllKeys) {
		present := strings.Contains(body, `data-key="`+key+`"`)
		if wanted[key] && !present {
			t.Errorf("expected card %q to render", key)
		}
		if !wanted[key] && present {
			t.Errorf("expected card %q to be hidden", key)
		}
	}
}

// AC1: the Board filter chrome renders a search box; no other view does.
// AC8: it is debounced, so a keystroke does not fire a request per character.
func TestBoardRendersSearchBox(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())
	body := get(t, app.URL+"/board")

	for _, want := range []string{
		`data-testid="board-search"`,
		`data-testid="board-search-input"`,
		// The id is what lets htmx restore focus and caret across the swap that
		// replaces the very input being typed in — without it, typing dies after
		// the first debounced request.
		`id="board-search-input"`,
		`name="q"`,
		`data-filterparam`,                // siblings include the search text
		`hx-get="/board/results"`,         // typing swaps the board panel
		`hx-include="[data-filterparam]"`, // a text box includes its OWN param too
		`delay:`,                          // debounced input
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board filter chrome missing search-box marker %q\n%s", want, body)
		}
	}
	// Fresh load: empty box, every card visible.
	assertSearchFixtureCards(t, body, "DCAI-20", "DCAI-21", "DCAI-22", "DCAI-23")

	// The search box is Board-only.
	for _, path := range []string{"/daily", "/sprint", "/velocity"} {
		other := get(t, app.URL+path)
		if strings.Contains(other, `data-testid="board-search"`) {
			t.Errorf("%s must not render the Board search box", path)
		}
	}
}

// AC2/AC3: matching is a case-insensitive substring test over key, title and
// parent epic title — and nothing else.
func TestBoardSearchMatchesKeyTitleAndEpic(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())

	cases := []struct {
		name string
		term string
		want []string
	}{
		{"key", "DCAI-21", []string{"DCAI-21"}},
		{"key case-insensitive", "dcai-21", []string{"DCAI-21"}},
		{"title substring", "retry", []string{"DCAI-20"}},
		{"title case-insensitive", "BANNER", []string{"DCAI-21"}},
		{"epic title", "checkout", []string{"DCAI-20", "DCAI-23"}},
		{"epic title mixed case", "ChEcKoUt", []string{"DCAI-20", "DCAI-23"}},
		{"partial word", "sign-up", []string{"DCAI-22"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := get(t, app.URL+"/board/results?q="+url.QueryEscape(tc.term))
			assertAllColumnsRender(t, body) // AC6: filtering never removes a column
			assertSearchFixtureCards(t, body, tc.want...)
		})
	}
}

// AC3: no fuzzy matching, and assignee is deliberately not a searched field.
func TestBoardSearchIsSubstringOnlyAndIgnoresAssignee(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())

	// "Ada" is an assignee on three cards but appears in no key, title or epic.
	byAssignee := get(t, app.URL+"/board/results?q=Ada")
	assertSearchFixtureCards(t, byAssignee /* none */)

	// Fuzzy/subsequence matches must not hit: "rty" is a subsequence of "retry"
	// but not a substring of it.
	fuzzy := get(t, app.URL+"/board/results?q=rty")
	assertSearchFixtureCards(t, fuzzy /* none */)
}

// AC4: the search text round-trips in the URL as ?q= — loading such a URL
// directly reproduces the filtered board with the term still in the box.
func TestBoardSearchRoundTripsInURL(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())

	page := get(t, app.URL+"/board?q=checkout")
	assertAllColumnsRender(t, page)
	assertSearchFixtureCards(t, page, "DCAI-20", "DCAI-23")
	if !strings.Contains(page, `value="checkout"`) {
		t.Errorf("bookmarked /board?q= should re-render the term in the box\n%s", page)
	}

	// Clearing it restores the full board.
	cleared := get(t, app.URL+"/board/results?q=")
	assertSearchFixtureCards(t, cleared, "DCAI-20", "DCAI-21", "DCAI-22", "DCAI-23")
}

// AC5: search composes with the assignee, no-estimate and active-24h filters as a
// plain intersection, in both directions — the other filters' params round-trip
// alongside the search term, and the search box re-renders with its term when
// another filter changes.
func TestBoardSearchComposesWithOtherFilters(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())

	body := get(t, app.URL+"/board/results?q=checkout&assignee="+url.QueryEscape("Ada Lovelace")+"&no-estimate=1")
	assertAllColumnsRender(t, body)
	// checkout-epic ∩ Ada ∩ unsized = DCAI-23 only (DCAI-20 is Ada+checkout but sized).
	assertSearchFixtureCards(t, body, "DCAI-23")

	// The other filters' state round-trips as hidden params, and the search term
	// round-trips in the box — so changing any one preserves the rest.
	for _, want := range []string{
		`data-filterparam name="assignee" value="Ada Lovelace"`,
		`data-filterparam name="no-estimate" value="1"`,
		`value="checkout"`,
		`aria-pressed="true"`, // the no-estimate toggle stays on
	} {
		if !strings.Contains(body, want) {
			t.Errorf("composed board should round-trip %q\n%s", want, body)
		}
	}
}

// AC7: a search matching nothing renders an explicit "no cards match" state
// rather than a silently empty board — with every column still in place.
func TestBoardSearchNoMatchesState(t *testing.T) {
	app := newBoardApp(t, boardSearchFixture())

	body := get(t, app.URL+"/board/results?q=zzzznope")
	assertAllColumnsRender(t, body)
	assertSearchFixtureCards(t, body /* none */)
	if !strings.Contains(body, `data-testid="board-no-match"`) {
		t.Errorf("a search matching nothing should show the no-match state\n%s", body)
	}

	// A board that does match must NOT show the state.
	matched := get(t, app.URL+"/board/results?q=retry")
	if strings.Contains(matched, `data-testid="board-no-match"`) {
		t.Errorf("a matching search must not show the no-match state\n%s", matched)
	}
	fresh := get(t, app.URL+"/board")
	if strings.Contains(fresh, `data-testid="board-no-match"`) {
		t.Errorf("an unfiltered board must not show the no-match state\n%s", fresh)
	}
}
