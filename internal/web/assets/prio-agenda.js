(() => {
  const panel = document.getElementById('prio-panel');
  const agenda = () => panel.querySelector('[data-prio-agenda]');
  if (!agenda()) return;

  function loadState(windowID) {
    try {
      const saved = JSON.parse(sessionStorage.getItem('prio-agenda'));
      if (saved?.windowID === windowID) {
        const {discussed, cells, scroll} = saved;
        return {windowID, discussed, cells, scroll};
      }
    } catch { /* Session storage can be unavailable. */ }
    return {windowID, discussed: {}, cells: {}, scroll: {}};
  }

  let state = loadState(agenda().dataset.window);
  let nextFocus = '';
  let closeTicket = '';
  let editFieldsOpen = false;
  let filtersOpen = false;
  let lastJiraPreset = '';
  let presetPrefetchPending = false;
  let lastPresetPrefetch = -Infinity;
  let filterRequest;
  let renderedStep = agenda().dataset.step;

  function saveState() {
    try {
      sessionStorage.setItem('prio-agenda', JSON.stringify(state));
    } catch { /* Keep the current page usable without session storage. */ }
  }

  function scrollKey(area, ticket) {
    const name = area.dataset.prioScroll;
    return name === 'drawer' || name === 'ticket' ? name + ':' + ticket : name;
  }

  function rememberView() {
    const view = agenda();
    if (!view) return;
    const {step, ticket} = view.dataset;
    state.cells[step] = ticket;
    const scroll = {...state.scroll[step], page: window.scrollY};
    for (const area of view.querySelectorAll('[data-prio-scroll]')) {
      scroll[scrollKey(area, ticket)] = [area.scrollLeft, area.scrollTop];
    }
    state.scroll[step] = scroll;
    saveState();
  }

  function renderProgress() {
    const view = agenda();
    if (!view) return;
    const discussed = new Set(Object.entries(state.discussed)
      .filter(([stepID]) => stepID !== 'overview')
      .flatMap(([, keys]) => keys));
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
    }
    view.querySelector('[data-prio-summary-count]').textContent =
      `${discussed.size} ${discussed.size === 1 ? 'besprochenes Ticket' : 'besprochene Tickets'}`;
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
    const filterForm = view.querySelector('[data-prio-filters]');
    if (filterForm && (filtersOpen || location.hash === '#prio-filter-controls')) {
      filterForm.closest('details').open = true;
    }
    filtersOpen = false;
    restoreFocus(view);
    const saved = state.scroll[step];
    if (saved) {
      for (const area of view.querySelectorAll('[data-prio-scroll]')) {
        const position = saved[scrollKey(area, ticket)];
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

    rememberView();
    renderProgress();
  }

  function restoreFocus(view) {
    const controlsPending = view.querySelector('[data-prio-controls][hx-get]');
    if (editFieldsOpen && !controlsPending) {
      const fields = view.querySelector('[data-prio-edit-fields]');
      if (fields) fields.open = true;
      editFieldsOpen = false;
    }
    if (nextFocus) {
      const focus = view.querySelector(nextFocus);
      if (!focus && controlsPending) return;
      (focus || view.querySelector('[aria-current="step"]'))?.focus({preventScroll: true});
      nextFocus = '';
    }
  }

  function syncDrawerSelection(view) {
    const ticket = view.querySelector('[data-prio-drawer-ticket]')?.dataset.prioDrawerTicket || '';
    view.dataset.ticket = ticket;
    const cutoffForm = view.querySelector('[data-prio-cutoff-form]');
    const filterForm = view.querySelector('[data-prio-filters]');
    const epicForm = view.querySelector('[data-prio-ticket-action="epic"]');
    for (const container of [view, cutoffForm, filterForm, epicForm]) {
      if (!container) continue;
      container.querySelector(':scope > input[name="ticket"]')?.remove();
      if (!ticket) continue;
      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'ticket';
      input.value = ticket;
      if (container === view) input.dataset.filterparam = '';
      container.prepend(input);
    }
    for (const link of view.querySelectorAll('[data-prio-page], [data-prio-share-cutoff], [data-prio-filter-reset], [data-prio-clear-preset]')) {
      link.href = withTicket(link.href, ticket);
    }
    for (const option of view.querySelectorAll('[data-prio-sort] option')) {
      option.value = withTicket(option.value, ticket);
    }
    for (const rowLink of view.querySelectorAll('[data-prio-ticket]')) {
      if (rowLink.dataset.prioTicket === ticket) rowLink.setAttribute('aria-current', 'true');
      else rowLink.removeAttribute('aria-current');
    }
  }

  function withTicket(href, ticket) {
    const url = new URL(href, location.href);
    if (ticket) url.searchParams.set('ticket', ticket);
    else url.searchParams.delete('ticket');
    return url.pathname + url.search + url.hash;
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
    } else if (target.closest('[data-prio-edit-filters], [data-prio-filter-reset]')) {
      filtersOpen = true;
      nextFocus = '#prio-period';
    } else if (target.closest('[data-prio-step]')) {
      nextFocus = '[aria-current="step"]';
    } else if (target.closest('[data-prio-page]')) {
      nextFocus = '[data-prio-page-number]';
    } else if (target.closest('[data-prio-queue-key]')) {
      nextFocus = '[data-prio-queue-key][aria-current="true"]';
    } else if (target.closest('[data-prio-ticket]')) {
      closeTicket = target.closest('[data-prio-ticket]').dataset.prioTicket;
      nextFocus = '[data-prio-close]';
    } else if (target.closest('[data-prio-close]')) {
      const ticket = closeTicket || view.dataset.ticket;
      nextFocus = `[data-prio-ticket="${CSS.escape(ticket)}"]`;
    }
    if (target.closest('[data-prio-epic-open]')) view.querySelector('#prio-epic-dialog').showModal();
    if (target.closest('[data-prio-epic-close]')) view.querySelector('#prio-epic-dialog').close();
    if (target.closest('[data-prio-summary]')) {
      renderProgress();
      view.querySelector('#prio-summary').showModal();
      window.htmx?.ajax('GET', '/prio/meeting/summary?window=' + encodeURIComponent(view.dataset.window), {
        target: view.querySelector('#prio-summary-changes'), swap: 'innerHTML'
      });
    }
  }, true);

  function updateFilterSummary(form) {
    const {period, tech, level, preset} = form.elements;
    const levelLabel = form.querySelector('[name="level"]:checked').nextElementSibling.textContent;
    const selectedPreset = preset.selectedOptions[0];
    const parts = ['Zeitraum: ' + period.selectedOptions[0].text, 'Ebene: ' + levelLabel];
    if (tech.value !== 'all') parts.push('Technical: ' + tech.selectedOptions[0].text);
    if (preset.value !== 'all') parts.push('Jira: ' + selectedPreset.text);
    form.closest('details').querySelector('[data-prio-filter-summary]').textContent = parts.join(' · ');
    form.querySelector('[data-prio-preset-description]').textContent = selectedPreset.dataset.description || '';
  }

  function prefetchJiraPreset(picker) {
    if (!picker || presetPrefetchPending || performance.now() - lastPresetPrefetch < 15000) return;
    const known = [picker.value, lastJiraPreset, '10519'];
    const preset = known.find(id => /^\d+$/.test(id) && [...picker.options].some(option => option.value === id));
    if (!preset) return;
    const params = new URLSearchParams({preset});
    const cutoff = agenda()?.querySelector(':scope > input[name="cutoff"]')?.value;
    if (cutoff) params.set('cutoff', cutoff);
    lastPresetPrefetch = performance.now();
    presetPrefetchPending = true;
    fetch('/prio/preset-prefetch?' + params, {priority: 'low', redirect: 'manual'})
      .catch(() => { /* Selecting the filter still performs its normal request. */ })
      .finally(() => { presetPrefetchPending = false; });
  }

  panel.addEventListener('focusin', event => {
    if (event.target.matches('#prio-preset')) prefetchJiraPreset(event.target);
  });
  panel.addEventListener('toggle', event => {
    if (event.target.matches('[data-testid="prio-filters"][open]')) {
      prefetchJiraPreset(event.target.querySelector('#prio-preset'));
    }
  }, true);

  panel.addEventListener('change', event => {
    const control = event.target;
    if (control.matches('[data-prio-filter]')) {
      if (control.id === 'prio-preset' && control.value !== 'all') lastJiraPreset = control.value;
      const form = control.form;
      updateFilterSummary(form);
      form.requestSubmit();
    }
    if (control.matches('[data-prio-sort]')) window.location.assign(control.value);
  });

  panel.addEventListener('submit', event => {
    if (!event.target.matches('[data-prio-filters]')) return;
    // Keep the newest choice when Arrow keys or successive clicks change filters
    // in flight. Leave the agenda's shared request lock in place for ticket edits.
    if (filterRequest) filterRequest.abort();
    const focused = document.activeElement;
    filtersOpen = true;
    if (event.target.contains(focused) && focused.id) nextFocus = '#' + CSS.escape(focused.id);
  }, true);

  document.addEventListener('htmx:beforeRequest', event => {
    const {elt, xhr} = event.detail;
    if (elt.matches('[data-prio-step], [data-prio-ticket], [data-prio-page]')) {
      elt.setAttribute('aria-busy', 'true');
      xhr.addEventListener('loadend', () => elt.removeAttribute('aria-busy'), {once: true});
    }
    if (!elt.matches('[data-prio-filters]')) return;
    filterRequest = xhr;
    const status = elt.querySelector('[data-prio-filter-status]');
    status.textContent = 'Filter werden angewendet …';
    xhr.addEventListener('loadend', () => {
      if (filterRequest !== xhr) return;
      filterRequest = undefined;
      status.textContent = xhr.status >= 200 && xhr.status < 400 ? '' : 'Filter konnten nicht angewendet werden.';
    });
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
    const {target, shouldSwap} = event.detail;
    if (!shouldSwap) return;
    if (target.matches('[data-prio-agenda]')) {
      filtersOpen = !!target.querySelector('[data-testid="prio-filters"][open]');
    } else if (target !== panel && target.id !== 'prio-drawer-slot') {
      return;
    }
    rememberView();
    const form = event.detail.requestConfig.elt;
    const action = form?.dataset.prioTicketAction;
    if (action) {
      editFieldsOpen = !!agenda()?.querySelector('[data-prio-edit-fields][open]');
      const focused = form.contains(document.activeElement) && document.activeElement.id
        ? document.activeElement : form.querySelector('button[type="submit"]');
      nextFocus = action === 'epic' ? '[data-prio-epic-open]' :
        focused?.id ? `#${CSS.escape(focused.id)}` : '';
    }
  });
  document.addEventListener('htmx:afterSettle', event => {
    const view = agenda();
    const {target} = event.detail;
    if (!view || !target) return;
    if (target.id === 'prio-drawer-slot' && view.contains(target)) {
      syncDrawerSelection(view);
      restoreView();
    } else if (target === panel || target.matches('[data-prio-agenda]')) {
      restoreView();
    } else if (target.matches('[data-prio-controls]') && view.dataset.ticket === target.dataset.prioControls) {
      restoreFocus(view);
    } else if (target.matches('[data-prio-preset-picker]')) {
      prefetchJiraPreset(view.querySelector('[data-testid="prio-filters"][open] #prio-preset'));
    }
  });
  document.addEventListener('htmx:historyRestore', restoreView);
  window.addEventListener('pagehide', rememberView);
  // Browser history owns the page offset. Reapply queue/table offsets as well.
  requestAnimationFrame(restoreView);
})();
