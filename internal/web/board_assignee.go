package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/store"
	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/sync"
)

// Form fields of the Assignee edit (#224, docs/adr/0012).
//
// account and clear are two fields rather than one because
// jira.UnassignedAccountID is the EMPTY STRING. With a single field, an account
// id that arrived empty — a template typo, a truncated form, a member row with
// no id — would not fail: it would read as "clear" and silently unassign the
// ticket the user meant to hand to someone. So clearing is its own explicit
// input, an empty account is a bad request, and a request carrying both is a
// bad request too (they contradict each other).
const (
	assignKeyField     = "key"
	assignAccountField = "account"
	assignClearField   = "clear"
	assignClearOn      = "1"
	// assignUnassignedOptKey is the Unassigned choice's testid suffix, since it has
	// no account id to name itself with.
	assignUnassignedOptKey = "unassigned"
)

// handleBoardAssignee is the Board's Assignee edit (#224, docs/adr/0012): POST
// /board/assignee with key + either a project member's account id or an explicit
// clear, plus the active filters' hidden inputs. It writes through the Assigner,
// then renders the WHOLE board-panel from the projection — success and failure
// alike, one response shape, exactly as the Prio view's priority edit does.
//
// Rendering the whole panel rather than swapping the control alone is what makes
// a reassignment behave like a transition (docs/adr/0010): the response passes
// through the posted filters, so a card reassigned outside an active assignee
// filter is simply absent from it. A failed write leaves the card exactly as it
// was, with an inline message on that one card and no global banner — the write
// failing is a rendered outcome, not an HTTP error.
//
// A malformed request IS an HTTP error: a missing key, an account that is not a
// member of this project, an empty account, or a clear contradicted by an
// account are all 400 and write nothing.
func (s *Server) handleBoardAssignee(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad assignee request", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.FormValue(assignKeyField))
	if key == "" {
		http.Error(w, "bad assignee request", http.StatusBadRequest)
		return
	}

	// The candidates are read from the projection, never from Jira in a request
	// path (docs/adr/0012), and they are what the submitted id is validated
	// against — the popover offers a fixed short list, so anything else is a
	// forged or stale request.
	members, err := s.rollups.ProjectMembers()
	if err != nil {
		log.Printf("web: board assignee %s: reading the project members failed: %v", key, err)
		s.renderError(w)
		return
	}
	accountID, ok := assignTarget(members, r.PostForm)
	if !ok {
		http.Error(w, "bad assignee request", http.StatusBadRequest)
		return
	}

	edit := boardEdit{Key: key}
	switch {
	case s.assigner == nil:
		log.Printf("web: board assignee %s rejected: no assigner wired", key)
		edit.Error = writeError
	default:
		if _, err := s.assigner.SetAssignee(r.Context(), key, accountID); err != nil {
			edit.Error = assignWriteOutcome(key, accountID, err)
		}
	}
	s.renderBoardWith(w, assignFilters(r.PostForm), "board-panel", edit)
}

// assignWriteOutcome turns a failed SetAssignee into the inline message the card
// should carry — which is NO message when the error is marked with
// sync.ErrWriteLanded.
//
// That marking means the assignment is already a fact in Jira and only the
// projection is behind. Telling the user "couldn't save" would be a lie, and it
// is the precise defect the drag path shipped in #195/#206 and #207 had to fix.
// docs/adr/0010's answer there was to reload rather than lie, and this control
// inherits it for free: the response IS the board re-rendered from the
// projection, so the user sees the board — one sync cycle stale on this one card
// at worst, never a claim that nothing happened. The next cycle reconciles it.
func assignWriteOutcome(key, accountID string, err error) string {
	if errors.Is(err, sync.ErrWriteLanded) {
		log.Printf("web: board assignee %s=%q landed in Jira but was not reconciled; rendering the projection: %v", key, accountID, err)
		return ""
	}
	log.Printf("web: board assignee write %s=%q failed: %v", key, accountID, err)
	return writeError
}

// assignTarget resolves the form's assign choice to the account id to write,
// reporting false for anything the popover could not have produced. It is the
// single place the jira.UnassignedAccountID hazard is contained:
//
//   - an explicit clear (and nothing else) writes jira.UnassignedAccountID;
//   - every other request must name an account that is a member of this project
//     right now, so an empty, unknown, removed or app account is rejected rather
//     than falling through to a silent unassign.
//
// With an empty member table nothing is assignable at all, which is the server
// agreeing with the markup: the avatar is not editable before the first sync, so
// neither a person nor a clear may be written through a stale page.
func assignTarget(members []store.ProjectMember, form url.Values) (string, bool) {
	account := strings.TrimSpace(form.Get(assignAccountField))
	clear := form.Get(assignClearField) == assignClearOn

	if len(members) == 0 {
		return "", false
	}
	if clear {
		// "Clear, to this person" is a contradiction, not a preference order.
		if account != "" {
			return "", false
		}
		return jira.UnassignedAccountID, true
	}
	if account == "" {
		return "", false
	}
	for _, m := range members {
		if m.AccountID == account {
			return account, true
		}
	}
	return "", false
}

// assignFilters is the filter query the panel re-renders with: the posted form
// minus the edit's own fields, leaving exactly the params the hidden
// [data-filterparam] inputs sent — the same shape boardMoveFilters gives the
// Board move and prioPriorityFilters gives the priority edit.
func assignFilters(form url.Values) url.Values {
	q := make(url.Values, len(form))
	for name, vals := range form {
		switch name {
		case assignKeyField, assignAccountField, assignClearField:
			continue
		}
		q[name] = vals
	}
	return q
}
