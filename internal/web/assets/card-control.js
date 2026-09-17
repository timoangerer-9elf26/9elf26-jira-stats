// card-control.js — the one marker a control on a board card needs.
//
// A board card is two things at once: the whole card is an <a> to the Jira
// issue, and on the Board's drag surface it is also a Sortable drag source. So
// any control placed on a card has to opt out of BOTH. Without the link
// opt-out, using the control navigates away from the dashboard; without the
// drag opt-out, pressing it picks the card up instead of working the control.
//
// Opting out of only one of the two fails SILENTLY — nothing throws, the
// control simply does not respond — which is why both opt-outs are keyed off a
// single attribute rather than arranged separately per control.
// `data-card-control` is that attribute: this file gives it the link opt-out,
// and the Sortable `filter` in board-drag.js reads the same attribute for the
// drag opt-out.
//
// ONE RULE ON ANYTHING THAT USES THE MARKER: do not call stopPropagation() on a
// click between a marked control and the document. The listener below is
// delegated, so a marked control whose own handler (or a wrapper's) stops the
// click short of the document is correctly marked and still navigates away —
// the same silent failure, re-created. A control needing to suppress a sibling
// handler should aim narrower than stopPropagation.
(function () {
  "use strict";

  // Delegated on the document, so a control that htmx swaps into the page is
  // covered with no re-binding — the marker in the markup is the whole
  // contract.
  //
  // Bubble phase is deliberate. A control's own listeners (htmx's, bound to the
  // element carrying hx-post) run first and still fire; only the click's
  // default action is cancelled, and that action — following the enclosing
  // card <a> — is resolved after propagation finishes, so cancelling it here is
  // in time.
  document.addEventListener("click", function (evt) {
    var t = evt.target;
    if (!t || !t.closest) return;
    if (t.closest("[data-card-control]")) evt.preventDefault();
  });
})();
