(() => {
  const history = document.getElementById('velocity-history');
  const controls = document.getElementById('velocity-history-controls');
  const [earlier, later] = controls.querySelectorAll('button');

  function updateControls() {
    const maxScroll = history.scrollWidth - history.clientWidth;
    controls.hidden = maxScroll <= 1;
    earlier.disabled = history.scrollLeft <= 1;
    later.disabled = history.scrollLeft >= maxScroll - 1;
  }

  history.scrollLeft = history.scrollWidth;
  history.addEventListener('scroll', updateControls, {passive: true});
  new ResizeObserver(updateControls).observe(history);
  for (const button of [earlier, later]) {
    button.addEventListener('click', () => {
      history.scrollBy({left: Number(button.dataset.velocityDirection) * history.clientWidth});
    });
  }
  updateControls();
})();
