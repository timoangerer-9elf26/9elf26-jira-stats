/*
 * Board drag-and-drop transitions (#195, docs/adr/0010).
 *
 * Binds the vendored Sortable to the five Board columns that take part in
 * dragging (marked data-drag-target by the server, which is also the only place
 * the legal set is decided — this file never hard-codes column names). A drop in
 * a different column POSTs /board/move; the server writes the transition to
 * Jira, re-reads the issue and answers with the whole board panel, which is
 * swapped in. The move is therefore SERVER-AUTHORITATIVE: the optimistic DOM
 * position is only ever a pending placeholder.
 *
 *   - No reordering within a column (sort: false) and a same-column drop writes
 *     nothing: a drop means exactly one thing, "change the status".
 *   - On drag start EVERY legal column outlines at once, so the whole legal move
 *     set is visible immediately; the hovered one fills more strongly. The two
 *     excluded columns stay flat.
 *   - While the write is in flight the card sits muted in the target column.
 *   - On failure the card snaps back to its origin column and shows a generic
 *     inline message (the cause is logged server-side).
 *
 * The board re-renders as an HTMX fragment on every filter change and after
 * every move, which destroys these bindings — so they are re-established after
 * each swap. That is the one place this script and HTMX know about each other.
 */
(function () {
  "use strict";

  var PANEL_ID = "board-panel";
  var GENERIC_ERROR = "Couldn't move — try again.";
  var instances = [];

  function panel() {
    return document.getElementById(PANEL_ID) || document;
  }

  function dragColumns() {
    return Array.prototype.slice.call(
      document.querySelectorAll('[data-drag-target="true"]')
    );
  }

  function markLegal(on) {
    dragColumns().forEach(function (col) {
      col.classList.toggle("bd-legal", on);
      if (!on) col.classList.remove("bd-over");
    });
  }

  function markHover(list) {
    var target = list && list.closest('[data-drag-target="true"]');
    dragColumns().forEach(function (col) {
      col.classList.toggle("bd-over", col === target);
    });
  }

  function clearError(card) {
    var old = card.querySelector("[data-move-error]");
    if (old) old.remove();
  }

  function showError(card, message) {
    clearError(card);
    var key = card.getAttribute("data-key") || "";
    var span = document.createElement("span");
    span.className = "bd-error";
    span.setAttribute("data-move-error", "");
    span.setAttribute("data-testid", "card:" + key + ":move-error");
    span.setAttribute("role", "alert");
    span.textContent = message;
    card.appendChild(span);
  }

  // filterParams collects the Board filter form's current params, so the
  // re-rendered fragment the server sends back is filtered exactly like the
  // board on screen (a moved card that no longer matches simply won't be in it).
  function filterParams(body) {
    document
      .querySelectorAll("[data-filterparam][name]")
      .forEach(function (input) {
        if (input.name) body.append(input.name, input.value);
      });
  }

  function move(card, fromList, oldIndex, targetStatus) {
    var body = new URLSearchParams();
    body.set("key", card.getAttribute("data-key") || "");
    body.set("status", targetStatus);
    filterParams(body);

    clearError(card);
    card.classList.add("bd-pending");
    card.setAttribute("data-move-pending", "true");

    fetch("/board/move", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    })
      .then(function (res) {
        if (!res.ok) {
          return res.text().then(function (text) {
            throw new Error((text || "").trim() || GENERIC_ERROR);
          });
        }
        return res.text();
      })
      .then(function (html) {
        var target = document.getElementById(PANEL_ID);
        if (!target) return;
        target.innerHTML = html;
        if (window.htmx) window.htmx.process(target);
        bind();
      })
      .catch(function (err) {
        // Snap back: put the card where it came from, exactly where it was.
        card.classList.remove("bd-pending");
        card.removeAttribute("data-move-pending");
        var sibling = fromList.children[oldIndex] || null;
        fromList.insertBefore(card, sibling);
        showError(card, (err && err.message) || GENERIC_ERROR);
      });
  }

  function destroy() {
    instances.forEach(function (s) {
      try {
        s.destroy();
      } catch (e) {
        /* the list is already gone with the swapped fragment */
      }
    });
    instances = [];
  }

  function bind() {
    destroy();
    if (typeof window.Sortable === "undefined") return;
    dragColumns().forEach(function (col) {
      var list = col.querySelector("[data-drag-list]");
      if (!list) return;
      instances.push(
        new window.Sortable(list, {
          group: "board-columns",
          // The board writes no rank field, so within-column order is not the
          // user's to set and must not look like it is.
          sort: false,
          draggable: '[data-testid="board-card"]',
          // The estimate popover lives inside the card and stays clickable.
          filter: "[data-estimate-control]",
          preventOnFilter: false,
          animation: 120,
          ghostClass: "bd-ghost",
          chosenClass: "bd-chosen",
          onStart: function () {
            markLegal(true);
          },
          onMove: function (evt) {
            markHover(evt.to);
            return true;
          },
          onEnd: function (evt) {
            markLegal(false);
            if (evt.from === evt.to) return; // same column: no write
            var status = evt.to.getAttribute("data-status");
            if (!status) return;
            move(evt.item, evt.from, evt.oldIndex, status);
          },
        })
      );
    });
  }

  function ready(fn) {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", fn);
    } else {
      fn();
    }
  }

  // Re-bind after any HTMX swap that touched the board panel (a filter change
  // swaps the whole panel, destroying every binding inside it).
  document.addEventListener("htmx:afterSwap", function (evt) {
    var t = evt.target;
    if (!t || !t.closest) return;
    if (t.id === PANEL_ID || t.closest("#" + PANEL_ID) || panel().contains(t)) {
      bind();
    }
  });

  ready(bind);
})();
