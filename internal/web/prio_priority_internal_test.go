package web

import (
	"strings"
	"testing"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// The view's icon table and the Jira client's write whitelist enumerate the same
// five levels in the same order: a level the menu offers is always one the write
// accepts, and the menu's order is the sort rank.
func TestPriorityLevelsMirrorJiraPriorities(t *testing.T) {
	if len(priorityLevels) != len(jira.Priorities) {
		t.Fatalf("priorityLevels has %d levels, jira.Priorities %d", len(priorityLevels), len(jira.Priorities))
	}
	for i, level := range priorityLevels {
		if level.Name != jira.Priorities[i] {
			t.Errorf("level %d: view %q, jira %q", i, level.Name, jira.Priorities[i])
		}
		if level.Slug != strings.ToLower(level.Name) || level.Icon.Path == "" {
			t.Errorf("level %q: slug %q / icon %q", level.Name, level.Slug, level.Icon.Path)
		}
	}
}

// The priority cell is editable only when its row says so (#212): a prioRow
// without Editable renders the read-only icon + name and none of the write
// affordance, so a future non-Prio use of the partial cannot inherit the write
// path by accident — the same gate the board card's Editable puts on the
// estimate pill.
func TestPriorityCellReadOnlyUnlessEditable(t *testing.T) {
	s, err := NewServer(nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	row := prioRow{Key: "DCAI-9", Priority: "High", Icon: priorityIconFor("High")}

	var sb strings.Builder
	if err := s.templates.ExecuteTemplate(&sb, "priority-cell", row); err != nil {
		t.Fatalf("render: %v", err)
	}
	ro := sb.String()
	for _, want := range []string{`data-testid="prio:DCAI-9:priority-name">High<`, `data-testid="prio:DCAI-9:priority-icon"`} {
		if !strings.Contains(ro, want) {
			t.Errorf("read-only cell missing %q\n%s", want, ro)
		}
	}
	for _, absent := range []string{"/prio/priority", "popover", "<button", "priority-menu"} {
		if strings.Contains(ro, absent) {
			t.Errorf("read-only cell leaks the write affordance %q\n%s", absent, ro)
		}
	}

	row.Editable = true
	sb.Reset()
	if err := s.templates.ExecuteTemplate(&sb, "priority-cell", row); err != nil {
		t.Fatalf("render editable: %v", err)
	}
	if ed := sb.String(); !strings.Contains(ed, "/prio/priority") || !strings.Contains(ed, `popovertarget="pri-menu-DCAI-9"`) {
		t.Errorf("editable cell lacks the write affordance\n%s", ed)
	}
}
