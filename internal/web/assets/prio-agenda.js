(() => {
  const panel = document.getElementById('prio-panel');
  const agenda = () => panel.querySelector('[data-prio-agenda]');
  if (!agenda()) return;

  function loadState(windowID) {
    try {
      const saved = JSON.parse(sessionStorage.getItem('prio-agenda'));
      if (saved?.windowID === windowID) return saved;
    } catch { /* Session storage can be unavailable. */ }
    return {windowID, discussed: {}, changes: [], cells: {}, scroll: {}};
  }

  let state = loadState(agenda().dataset.window);
  let nextFocus = '';
  let closeTicket = '';
  let renderedStep = agenda().dataset.step;

  function saveState() {
    try {
      sessionStorage.setItem('prio-agenda', JSON.stringify(state));
    } catch { /* Keep the current page usable without session storage. */ }
  }

  function rememberView() {
    const view = agenda();
    if (!view) return;
    const {step, ticket} = view.dataset;
    state.cells[step] = ticket;
    const scroll = {page: window.scrollY};
    for (const area of view.querySelectorAll('[data-prio-scroll]')) {
      scroll[area.dataset.prioScroll] = [area.scrollLeft, area.scrollTop];
    }
    state.scroll[step] = scroll;
    saveState();
  }

  function renderProgress() {
    const view = agenda();
    if (!view) return;
    const discussed = new Set();
    for (const step of view.querySelectorAll('[data-prio-step]')) {
      const stepID = step.dataset.prioStep;
      const reviewed = state.discussed[stepID] || [];
      const count = step.dataset.prioKeys.trim().split(/\s+/).filter(key => key && reviewed.includes(key)).length;
      step.querySelector('[data-prio-progress]').textContent = count;
      step.querySelector('progress').value = count;
      if (stepID === view.dataset.step) {
        const queueCount = view.querySelector('[data-prio-queue-progress]');
        if (queueCount) queueCount.textContent = count;
      }
      if (stepID !== 'overview') {
        for (const key of reviewed) discussed.add(key);
      }
    }
    view.querySelector('[data-prio-summary-count]').textContent =
      `${discussed.size} ${discussed.size === 1 ? 'besprochenes Ticket' : 'besprochene Tickets'} · ${state.changes.length} ${state.changes.length === 1 ? 'Änderung' : 'Änderungen'}`;
    const changes = view.querySelector('[data-prio-summary-changes]');
    changes.replaceChildren(...state.changes.map(change => {
      const row = document.createElement('li');
      row.textContent = change;
      return row;
    }));
  }

  function restoreView() {
    const view = agenda();
    if (!view) return;
    const {step, ticket, window: windowID} = view.dataset;
    if (step !== 'overview' && ticket) {
      const url = new URL(window.location.href);
      if (url.searchParams.get('ticket') !== ticket) {
        url.searchParams.set('ticket', ticket);
        history.replaceState(history.state, '', url);
      }
    }
    if (state.windowID !== windowID) state = loadState(windowID);
    const saved = state.scroll[step];
    if (saved) {
      for (const area of view.querySelectorAll('[data-prio-scroll]')) {
        const position = saved[area.dataset.prioScroll];
        if (position) area.scrollTo({left: position[0], top: position[1], behavior: 'instant'});
      }
      window.scrollTo({top: saved.page, behavior: 'instant'});
    } else if (renderedStep !== step) {
      window.scrollTo({top: 0, behavior: 'instant'});
    }
    renderedStep = step;
    const selected = view.querySelector('[data-prio-queue-key][aria-current="true"]');
    const queue = selected?.closest('[data-prio-scroll]');
    if (queue && (ticket !== state.cells[step] || !saved)) {
      const row = selected.closest('li').getBoundingClientRect();
      const bounds = queue.getBoundingClientRect();
      if (row.top < bounds.top) queue.scrollTop += row.top - bounds.top;
      if (row.bottom > bounds.bottom) queue.scrollTop += row.bottom - bounds.bottom;
    }

    if (nextFocus) {
      const focus = view.querySelector(nextFocus) || view.querySelector('[aria-current="step"]');
      focus?.focus({preventScroll: true});
    }
    nextFocus = '';
    rememberView();
    renderProgress();
  }

  function recordChange(label) {
    if (typeof label !== 'string' || !label) return;
    state.changes.push(label);
    saveState();
    renderProgress();
  }

  function markDiscussed(view) {
    const {step, ticket} = view.dataset;
    const key = step === 'overview' ? 'overview' : ticket;
    if (!key) return;
    const reviewed = state.discussed[step] ||= [];
    if (!reviewed.includes(key)) reviewed.push(key);
    saveState();
    renderProgress();
  }

  panel.addEventListener('click', event => {
    const view = agenda();
    if (!view || event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const target = event.target;
    if (target.closest('[data-prio-next]')) {
      markDiscussed(view);
      nextFocus = '[data-prio-next]';
    } else if (target.closest('[data-prio-prev]')) {
      nextFocus = '[data-prio-prev]';
    } else if (target.closest('[data-prio-step]')) {
      nextFocus = '[aria-current="step"]';
    } else if (target.closest('[data-prio-queue-key]')) {
      nextFocus = '[data-prio-queue-key][aria-current="true"]';
    } else if (target.closest('[data-prio-ticket]')) {
      closeTicket = target.closest('[data-prio-ticket]').dataset.prioTicket;
      nextFocus = '[data-prio-close]';
    } else if (target.closest('[data-prio-close]')) {
      const ticket = closeTicket || view.dataset.ticket;
      nextFocus = `[data-prio-ticket="${CSS.escape(ticket)}"]`;
    }
    if (target.closest('[data-prio-summary]')) {
      renderProgress();
      view.querySelector('#prio-summary').showModal();
    }
  }, true);

  panel.addEventListener('change', event => {
    if (event.target.matches('[data-prio-sort]')) window.location.assign(event.target.value);
  });

  document.addEventListener('keydown', event => {
    const view = agenda();
    if (!view || event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey ||
        event.target.closest?.('input, select, textarea, [contenteditable], [role="textbox"], :popover-open') ||
        view.querySelector('dialog[open]')) return;

    const control = event.key === 'Escape' ? view.querySelector('[data-prio-close]') :
      view.dataset.step === 'overview' ? null :
      event.key === 'j' ? view.querySelector('[data-prio-next]') :
      event.key === 'k' ? view.querySelector('[data-prio-prev]') : null;
    if (control) {
      event.preventDefault();
      control.click();
    }
  });

  document.addEventListener('htmx:configRequest', event => {
    const step = event.detail.elt.dataset.prioStep;
    if (step && state.cells[step]) event.detail.parameters.ticket = state.cells[step];
  });
  document.addEventListener('htmx:beforeSwap', event => {
    if (event.detail.target === panel) rememberView();
  });
  document.addEventListener('htmx:afterSettle', event => {
    if (event.detail.target !== panel) return;
    const view = agenda();
    if (view?.dataset.prioChange) recordChange(view.dataset.prioChange);
    restoreView();
  });
  document.addEventListener('htmx:historyRestore', restoreView);
  document.addEventListener('prio:change', event => recordChange(event.detail));
  window.addEventListener('pagehide', rememberView);
  // Browser history owns the page offset. Reapply queue/table offsets as well.
  requestAnimationFrame(restoreView);
})();
