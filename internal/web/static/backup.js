(() => {
  'use strict';
  const t = (text, ...args) => window.HouseholdI18n?.t(text, ...args) ?? text;
  const dialog = document.querySelector('#backup-restore');
  const form = dialog?.querySelector('[data-restore-form]');
  let opener = null;

  document.querySelectorAll('[data-restore-open]').forEach(button => button.addEventListener('click', () => {
    if (!dialog || !form) return;
    opener = button;
    form.reset();
    form.elements.snapshot.value = button.dataset.snapshot;
    form.elements.target.value = button.dataset.target;
    dialog.querySelector('[data-restore-when]').textContent = button.dataset.when;
    dialog.querySelector('[data-restore-phrase]').textContent = button.dataset.confirmation;
    form.querySelectorAll('[data-security-status]').forEach(node => { node.textContent = ''; node.hidden = true; });
    dialog.showModal();
    form.elements.confirmation.focus();
  }));
  dialog?.querySelectorAll('[data-restore-close]').forEach(button => button.addEventListener('click', () => dialog.close()));
  dialog?.addEventListener('close', () => opener?.focus());

  document.querySelectorAll('[data-copy]').forEach(button => button.addEventListener('click', async () => {
    const label = button.textContent;
    try {
      await navigator.clipboard.writeText(button.dataset.copy);
      button.textContent = t('Kopiert');
    } catch {
      button.textContent = button.dataset.copy.slice(0, 16) + '…';
    }
    setTimeout(() => { button.textContent = label; }, 2500);
  }));

  // Follow running work: reload when it changes state; a live restore ends
  // every session, so the poller then leads to the sign-in page.
  const active = document.querySelector('[data-backup-active]');
  // Queued jobs are followed too, so the page shows their result without a reload.
  const running = document.querySelectorAll('.backup-running, .backup-waiting').length > 0 || active;
  if (!running) return;
  let failures = 0;
  const poll = async () => {
    try {
      const response = await fetch('/api/v1/core/backup', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      if (response.status === 401) { location.assign('/login'); return; }
      if (response.status === 503) { failures = 0; setTimeout(poll, 5000); return; }
      if (!response.ok) throw new Error(String(response.status));
      const data = await response.json();
      const stillRunning = (data.jobs || []).some(job => ['requested', 'running', 'preparing', 'awaiting_drain', 'swapping'].includes(job.state));
      if (!stillRunning) { location.reload(); return; }
      setTimeout(poll, 4000);
    } catch {
      failures += 1;
      setTimeout(poll, Math.min(30000, 4000 * failures));
    }
  };
  setTimeout(poll, 4000);
})();
