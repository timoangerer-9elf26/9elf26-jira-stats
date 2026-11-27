package web_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

func TestPrioEpicCheckSuggestsFromCurrentDirectChildrenAcrossTheProject(t *testing.T) {
	fixture := &jira.FakeClient{Issues: []jira.Issue{
		{Key: "DCAI-1", Type: "Epic", Status: "In Progress", Summary: "Publish PDF links", Labels: []string{"Technical"}},
		{Key: "DCAI-2", Type: "Task", ParentKey: "DCAI-1", Status: "Ready for Release", Summary: "Implement links"},
		{Key: "DCAI-3", Type: "Task", ParentKey: "DCAI-1", Status: "Canceled", StatusCategory: "Done", Summary: "Discarded approach"},
		{Key: "DCAI-4", Type: "Task", ParentKey: "DCAI-2", Status: "Ready To Do", Summary: "Indirect open descendant"},
		{Key: "DCAI-5", Type: "Epic", Status: "Ready To Do", Summary: "Improve sharing"},
		{Key: "DCAI-6", Type: "Task", ParentKey: "DCAI-5", Status: "Review / Testing", Summary: "Review sharing"},
		{Key: "DCAI-7", Type: "Epic", Status: "In Progress"},
		{Key: "DCAI-8", Type: "Task", ParentKey: "DCAI-7", Status: "Canceled"},
		{Key: "DCAI-9", Type: "Epic", Status: "In Progress"},
		{Key: "DCAI-10", Type: "Task", ParentKey: "DCAI-9", Status: "DONE (This Sprint)"},
		{Key: "DCAI-11", Type: "Task", ParentKey: "DCAI-9", Status: "Triage"},
		{Key: "DCAI-12", Type: "Epic", Status: "In Progress"},
	}}
	app := newWritablePrioApp(t, fixture)
	base := app.URL + "/prio?step=epic-check&preset=10519&tech=non-technical&parent=none&ticket="

	page := get(t, base+"DCAI-1")
	assertOrder(t, page, `data-prio-queue-key="DCAI-1"`, `data-prio-queue-key="DCAI-5"`)
	if n := strings.Count(page, "data-prio-queue-key="); n != 2 {
		t.Fatalf("Epic-Prüfung has %d suggestions, want 2", n)
	}
	assertOrder(t, page, "Publish PDF links", "In Progress", "Vorschlag · Fertig")
	assertOrder(t, page, "Direkte Kinder (2)", "Fertig (1)", "Implement links", "Ready for Release", "Canceled (1)", "Discarded approach", "Für den Vorschlag ausgenommen")
	if strings.Contains(page, "Indirect open descendant") {
		t.Fatal("Epic-Prüfung included a grandchild")
	}
	page = get(t, base+"DCAI-5")
	assertOrder(t, page, "Improve sharing", "Ready To Do", "Vorschlag · In Arbeit")
	_, children, found := strings.Cut(page, "Direkte Kinder (1)")
	if !found {
		t.Fatal("the Epic panel is missing its direct child")
	}
	assertOrder(t, children, "In Arbeit (1)", "Review sharing", "Review / Testing")
}

func TestPrioEpicCheckConfirmsAnOfferedDoneTransitionAndUpdatesFertig(t *testing.T) {
	fixture := &jira.FakeClient{
		Issues: []jira.Issue{
			{Key: "DCAI-41", Type: "Epic", Status: "In Progress", Summary: "Ship PDF links"},
			{Key: "DCAI-42", Type: "Task", ParentKey: "DCAI-41", Status: "Ready for Release"},
			{Key: "DCAI-43", Type: "Task", ParentKey: "DCAI-41", Status: "Canceled"},
		},
		Transitions: []jira.Transition{
			{ID: "31", Name: "Release", ToStatusID: jira.StatusIDReadyForRelease, ToStatusName: "Ready for Release", ToStatusCategory: "Done"},
			{ID: "4", Name: "Deploy", ToStatusID: jira.StatusIDReleasedDeployed, ToStatusName: "Released / Deployed", ToStatusCategory: "Done"},
			{ID: "6", Name: "Done", ToStatusID: jira.StatusIDCanceled, ToStatusName: "Canceled", ToStatusCategory: "Done"},
		},
		TransitionFields: map[string]map[string]jira.EditField{
			"4": {"resolution": {Name: "Resolution", Required: true}},
		},
	}
	app := newWritablePrioApp(t, fixture)
	cutoff := "2026-01-01T00:00:00Z"
	checkURL := app.URL + "/prio?step=epic-check&preset=all&cutoff=" + cutoff
	doneURL := app.URL + "/prio?step=done&preset=all&cutoff=" + cutoff

	page := get(t, checkURL)
	_, proposal, found := strings.Cut(page, `aria-label="Epic-Vorschlag"`)
	if !found {
		t.Fatal("the Epic panel is missing its proposal")
	}
	proposal, _, _ = strings.Cut(proposal, "</section>")
	for _, want := range []string{`name="action" value="transition"`, `name="transition" value="31"`, "Released / Deployed in Jira", "Pflichtfelder · Resolution"} {
		if !strings.Contains(proposal, want) {
			t.Errorf("proposal missing %q", want)
		}
	}
	if strings.Contains(proposal, `name="transition" value="6"`) {
		t.Error("Canceled cannot confirm the Done proposal")
	}
	if strings.Count(page, `id="prio-save-transition-31"`) != 1 {
		t.Error("the suggested transition must be rendered once")
	}
	if strings.Contains(get(t, doneURL), `data-prio-queue-key="DCAI-41"`) {
		t.Fatal("viewing a proposal must not finish the Epic")
	}

	page = postPrioTicket(t, app, url.Values{
		"step": {"epic-check"}, "preset": {"all"}, "cutoff": {cutoff},
		"key": {"DCAI-41"}, "action": {"transition"}, "transition": {"31"},
	})
	for _, want := range []string{`data-prio-change="DCAI-41 · Status → Ready for Release"`, "In Progress → Ready for Release", "Keine Epics zur Prüfung"} {
		if !strings.Contains(page, want) {
			t.Errorf("transition response missing %q", want)
		}
	}
	if strings.Contains(page, `data-prio-queue-key="DCAI-41"`) {
		t.Error("the corrected Epic still has a proposal")
	}
	assertOrder(t, get(t, doneURL), "2 · Fertig", `data-prio-queue-key="DCAI-41"`)
}
