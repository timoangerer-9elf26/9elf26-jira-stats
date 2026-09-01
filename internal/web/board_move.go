package web

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/timoangerer-9elf26/9elf26-jira-stats/internal/jira"
)

// boardMoveError is the generic inline message a failed drop shows. The
// technical cause (permissions, a 4xx, a dead network) is logged server-side and
// never rendered — the user's only useful action is to try again. The client
// carries the same string (GENERIC_ERROR in assets/board-drag.js) for failures
// that never reach the server, so a failed drop always reads the same.
const boardMoveError = "Couldn't move — try again."

// boardDragStatuses is the Board's drag surface (#195): the five columns that
// take part in dragging, mapped to the Jira STATUS id a drop into them writes.
// Any of the five can move to any other — all twenty ordered pairs are legal
// transitions in the DCAI workflow — and nothing else is reachable by dragging.
//
// The exclusions are deliberate and are enforced HERE, not only in the browser:
//   - Ready for Release and Released / Deployed are outside the drag system
//     entirely (not drop targets, and their cards are not draggable either), so
//     the rule reads as one rule rather than two.
//   - Canceled and Triage stay off-board: Jira permits both transitions, but
//     cancelling a ticket is exactly the consequential move that should not
//     happen by a mis-drop on a standup board.
//
// Because the map is keyed by the column's status NAME as the Board renders it,
// a drop posts what the user sees; the write path then resolves the transition
// by target status id (never by transition name — see docs/adr/0010).
var boardDragStatuses = map[string]string{
	"Refinement":         jira.StatusIDRefinement,
	"Ready To Do":        jira.StatusIDReadyToDo,
	"In Progress":        jira.StatusIDInProgress,
	"Review / Testing":   jira.StatusIDReviewTesting,
	"DONE (This Sprint)": jira.StatusIDDoneThisSprint,
}

// boardDragTarget resolves a Board column name to the status id a drop into it
// writes, reporting false for every column and status outside the drag surface.
func boardDragTarget(status string) (string, bool) {
	id, ok := boardDragStatuses[strings.TrimSpace(status)]
	return id, ok
}

// handleBoardMove is the Board drag-and-drop write (#195, docs/adr/0010). It
// takes the dropped card's key and the target column's status name, writes the
// transition to Jira through the Transitioner (which re-reads that one issue and
// persists it), and answers with the whole board panel re-rendered from the
// authoritative projection — so the client never has to trust its own optimistic
// DOM. The response is rendered through the request's own filter params, so a
// card that no longer matches an active filter is simply absent from it.
//
// A target outside the drag surface is a 400 and writes nothing. A failed write
// answers 502 with the generic boardMoveError text, which the client shows
// inline after snapping the card back to its origin column; the cause is logged.
func (s *Server) handleBoardMove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad move request", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	status := strings.TrimSpace(r.FormValue("status"))
	statusID, ok := boardDragTarget(status)
	if key == "" || !ok {
		http.Error(w, "bad move request", http.StatusBadRequest)
		return
	}

	if s.transitioner == nil {
		log.Printf("web: board move %s→%q rejected: no transitioner wired", key, status)
		boardMoveFailed(w)
		return
	}
	if _, err := s.transitioner.SetStatus(r.Context(), key, statusID); err != nil {
		log.Printf("web: board move %s→%q failed: %v", key, status, err)
		boardMoveFailed(w)
		return
	}
	s.renderBoardValues(w, boardMoveFilters(r.PostForm), "board-panel")
}

// boardMoveFilters strips the move's own params from the posted form, leaving
// the Board filter params the client sent along (the [data-filterparam] inputs),
// so the re-rendered fragment is filtered exactly like the board on screen.
func boardMoveFilters(form url.Values) url.Values {
	q := make(url.Values, len(form))
	for name, vals := range form {
		if name == "key" || name == "status" {
			continue
		}
		q[name] = vals
	}
	return q
}

// boardMoveFailed answers a failed move with the generic inline message and a
// non-OK status, which is what tells the client to snap the card back.
func boardMoveFailed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	if _, err := w.Write([]byte(boardMoveError)); err != nil {
		log.Printf("web: writing board move error: %v", err)
	}
}
