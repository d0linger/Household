(() => {
  'use strict';
  const dialog = document.querySelector('#new-household-user');
  const opener = document.querySelector('[data-open-user]');
  if (!dialog || !opener) return;
  const form = dialog.querySelector('form');
  const pending = () => form.getAttribute('aria-busy') === 'true';
  const outside = event => {
    const bounds = dialog.getBoundingClientRect();
    return event.target === dialog && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom);
  };
  let backdropStart = false;
  opener.addEventListener('click', () => {
    if (dialog.open) return;
    dialog.showModal();
    dialog.scrollTop = 0;
    form.elements.username.focus({preventScroll: true});
  });
  opener.disabled = false;
  dialog.querySelectorAll('[data-close-user]').forEach(button => button.addEventListener('click', () => {
    if (!pending()) dialog.close();
  }));
  dialog.addEventListener('cancel', event => { if (pending()) event.preventDefault(); });
  dialog.addEventListener('pointerdown', event => { backdropStart = outside(event); });
  dialog.addEventListener('click', event => {
    if (backdropStart && outside(event) && !pending()) dialog.close();
    backdropStart = false;
  });
  dialog.addEventListener('close', () => {
    form.reset();
    const status = form.querySelector('[data-security-status]');
    status.textContent = '';
    status.classList.remove('security-error');
    opener.focus({preventScroll: true});
  });
  addEventListener('pagehide', () => {
    form.reset();
    if (dialog.open) dialog.close();
  });
})();
