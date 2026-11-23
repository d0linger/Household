(() => {
  'use strict';
  const t = (text, ...args) => window.HouseholdI18n?.t(text, ...args) ?? text;
  const dialog = document.querySelector('#backup-restore');
  const form = dialog?.querySelector('[data-restore-form]');
  const feedback = document.querySelector('[data-backup-feedback]');
  if (!feedback) return;

  let opener = null;
  let timer = 0;
  let inFlight = false;
  let dirty = false;
  let failures = 0;
  let lastJob = '';
  let lastResult = '';
  const activeStates = new Set(['requested', 'running', 'preparing', 'awaiting_drain', 'swapping']);
  const sections = ['[data-backup-overview-progress]', '[data-backup-targets]', '[data-backup-history]', '[data-backup-snapshots]'];

  document.addEventListener('click', async event => {
    const restore = event.target.closest('[data-restore-open]');
    if (restore && dialog && form && !dialog.open) {
      opener = restore;
      form.reset();
      form.elements.snapshot.value = restore.dataset.snapshot;
      form.elements.target.value = restore.dataset.target;
      dialog.querySelector('[data-restore-when]').textContent = restore.dataset.when;
      dialog.querySelector('[data-restore-phrase]').textContent = restore.dataset.confirmation;
      form.querySelectorAll('[data-security-status]').forEach(node => { node.textContent = ''; node.hidden = true; });
      dialog.showModal();
      form.elements.confirmation.focus();
    }
    const copy = event.target.closest('[data-copy]');
    if (!copy) return;
    const label = copy.textContent;
    try {
      await navigator.clipboard.writeText(copy.dataset.copy);
      copy.textContent = t('Kopiert');
    } catch {
      copy.textContent = copy.dataset.copy.slice(0, 16) + '…';
    }
    setTimeout(() => { copy.textContent = label; }, 2500);
  });
  dialog?.querySelectorAll('[data-restore-close]').forEach(button => button.addEventListener('click', () => dialog.close()));
  dialog?.addEventListener('close', () => opener?.focus());

  function announce(message, tone = '') {
    feedback.dataset.tone = tone;
    feedback.hidden = false;
    feedback.textContent = message;
  }

  function schedule(delay) {
    clearTimeout(timer);
    timer = setTimeout(poll, delay);
  }

  function signature(jobs) {
    return JSON.stringify(jobs.map(job => [job.id, job.state, job.phase || '', job.result || '']));
  }
  let known = signature([...document.querySelectorAll('[data-backup-job]')].map(row => ({
    id: row.dataset.backupJob, state: row.dataset.jobState, phase: row.dataset.jobPhase, result: row.dataset.jobResult,
  })));

  async function updateSections() {
    const response = await fetch('/settings/backup', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(String(response.status));
    const shadowPage = new DOMParser().parseFromString(await response.text(), 'text/html');
    if (!shadowPage.querySelector('[data-backup-history]')) throw new Error();
    for (const selector of sections) {
      const current = document.querySelectorAll(selector);
      const next = shadowPage.querySelectorAll(selector);
      if (current.length !== next.length) continue;
      for (const [index, section] of current.entries()) {
        // Keep keyboard focus and pending reauthentication attached to their form.
        const focused = section.contains(document.activeElement) ? document.activeElement : null;
        const history = section.hasAttribute('data-backup-history');
        if ((focused && !history) || section.querySelector('[aria-busy="true"]') || document.querySelector('#security-reauth[open]')) {
          dirty = true;
          continue;
        }
        const rowID = focused?.closest('[data-backup-job]')?.dataset.backupJob;
        section.replaceChildren(...next[index].childNodes);
        if (focused) {
          const replacement = rowID && section.querySelector('[data-backup-job="' + CSS.escape(rowID) + '"]')?.querySelector('button');
          const target = replacement || section.querySelector('h2');
          if (target) {
            if (!replacement) target.tabIndex = -1;
            target.focus({ preventScroll: true });
          }
        }
      }
    }
  }

  async function poll() {
    if (inFlight) return;
    inFlight = true;
    try {
      const response = await fetch('/api/v1/core/backup', { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: AbortSignal.timeout(10000) });
      if (response.status === 401) { location.assign('/login'); return; }
      if (response.status === 503) { schedule(5000); return; }
      if (!response.ok) throw new Error(String(response.status));
      const data = await response.json();
      const jobs = data.jobs || [];
      const next = signature(jobs);
      if (dirty || next !== known) {
        dirty = false;
        await updateSections();
        known = next;
      }
      let job = jobs.find(entry => entry.id === lastJob);
      if (!job && lastJob) {
        const detail = await fetch('/api/v1/core/backup/jobs/' + encodeURIComponent(lastJob), { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
        if (detail.ok) job = await detail.json();
      }
      if (job && job.state !== lastResult) {
        lastResult = job.state;
        if (job.state === 'requested') announce(t('Sicherungsauftrag eingereiht.'));
        else if (job.state === 'complete') announce(job.kind === 'offsite_test' ? t('Verbindung zum Sicherungsziel geprüft.') : t('Sicherungsauftrag abgeschlossen.'), 'success');
        else if (job.state === 'failed') announce(t('Sicherungsauftrag fehlgeschlagen. Details stehen im Verlauf.'), 'error');
        else if (job.state === 'cancelled') announce(t('Auftrag abgebrochen.'));
        else announce(t('Der Sicherungsdienst bearbeitet den Auftrag.'));
      }
      failures = 0;
      if (dirty || jobs.some(entry => activeStates.has(entry.state)) || (job && activeStates.has(job.state))) schedule(4000);
    } catch {
      failures += 1;
      if (failures === 2) announce(t('Der Sicherungsstatus konnte noch nicht aktualisiert werden. Household versucht es erneut.'), 'error');
      schedule(Math.min(30000, 4000 * failures));
    } finally {
      inFlight = false;
    }
  }

  document.addEventListener('household:security-saved', event => {
    if (!event.target.matches('form[action^="/settings/backup/jobs"]') || !event.detail?.id) return;
    event.preventDefault();
    lastJob = event.detail.id;
    lastResult = '';
    dirty = true;
    if (dialog?.open) dialog.close();
    announce(t('Sicherungsauftrag aktualisiert.'));
    schedule(0);
  });
  document.addEventListener('household:security-failed', event => {
    if (!event.target.matches('form[action^="/settings/backup/jobs"]') || dialog?.open) return;
    announce(event.detail, 'error');
  });
  if (document.querySelector('.backup-running, .backup-waiting, [data-backup-active]')) schedule(4000);
})();
