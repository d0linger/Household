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
  let pollAgain = false;
  let dirty = false;
  let failures = 0;
  let lastJob = '';
  let lastResult = '';
  const activeStates = new Set(['requested', 'running', 'preparing', 'awaiting_drain', 'swapping']);
  const sections = [
    '[data-backup-history]', '[data-backup-overview-progress]', '[data-backup-targets]',
    '[data-backup-worker]', '[data-backup-offsite-actions]', '[data-backup-offsite-help]',
    '[data-backup-offsite-guidance]', '[data-backup-offsite-setup]',
    '[data-backup-snapshots="local"]', '[data-backup-snapshots="offsite"]', '[data-backup-retained]',
  ];

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

  function signature(data, job) {
    const jobs = data.jobs || [];
    const states = jobs.map(entry => [entry.id, entry.state, entry.phase || '', entry.result || '']);
    const tracked = job && !jobs.includes(job) ? [job.id, job.state, job.phase, job.result] : null;
    return JSON.stringify([states, tracked, data.service_online, data.offsite_configured, data.retained, data.previous]);
  }
  let known = '';

  async function updateSections() {
    const response = await fetch('/settings/backup', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(String(response.status));
    const shadowPage = new DOMParser().parseFromString(await response.text(), 'text/html');
    if (!shadowPage.querySelector('[data-backup-history]')) throw new Error();
    for (const selector of sections) {
      const section = document.querySelector(selector);
      const next = shadowPage.querySelector(selector);
      if (!section || !next || section.hidden === next.hidden && section.innerHTML === next.innerHTML) continue;
      // Keep editing and reauthentication attached to their original forms.
      const focused = section.contains(document.activeElement) ? document.activeElement : null;
      const history = section.hasAttribute('data-backup-history');
      if ((focused && !history && !next.hidden) || section.querySelector('[aria-busy="true"]') || document.querySelector('#security-reauth[open]')) {
        dirty = true;
        continue;
      }
      const rowID = focused?.closest('[data-backup-job]')?.dataset.backupJob;
      section.replaceChildren(...next.childNodes);
      section.hidden = next.hidden;
      if (focused) {
        const replacement = rowID && section.querySelector('[data-backup-job="' + CSS.escape(rowID) + '"]')?.querySelector('button');
        const target = replacement || (next.hidden ? document.querySelector('[data-backup-history] h2') : section.querySelector('h2'));
        if (target) {
          if (!replacement) target.tabIndex = -1;
          target.focus({ preventScroll: true });
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
      if (pollAgain) return;
      const jobs = data.jobs || [];
      const trackedID = lastJob;
      let job = jobs.find(entry => entry.id === trackedID);
      if (!job && trackedID) {
        const detail = await fetch('/api/v1/core/backup/jobs/' + encodeURIComponent(trackedID), { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
        if (pollAgain) return;
        if (detail.status === 401) { location.assign('/login'); return; }
        if (detail.status === 404) {
          lastJob = '';
          announce(t('Sicherungsauftrag nicht gefunden. Prüfe den Verlauf.'), 'error');
        } else {
          if (!detail.ok) throw new Error(String(detail.status));
          job = await detail.json();
          if (pollAgain) return;
        }
      }
      const next = signature(data, job);
      if (dirty || next !== known) {
        dirty = false;
        try {
          await updateSections();
        } catch (error) {
          dirty = true;
          throw error;
        }
        known = next;
      }
      if (pollAgain) return;
      if (job?.id === lastJob && job.state !== lastResult) {
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
      if (pollAgain) return;
      failures += 1;
      if (failures === 2) announce(t('Der Sicherungsstatus konnte noch nicht aktualisiert werden. Household versucht es erneut.'), 'error');
      schedule(Math.min(30000, 4000 * failures));
    } finally {
      inFlight = false;
      if (pollAgain) {
        pollAgain = false;
        schedule(0);
      }
    }
  }

  function trackJob(id) {
    lastJob = id;
    lastResult = '';
    dirty = true;
    announce(t('Sicherungsauftrag aktualisiert.'));
    if (inFlight) pollAgain = true;
    else schedule(0);
  }

  document.addEventListener('household:security-saved', event => {
    if (!event.target.matches('form[action^="/settings/backup/jobs"]') || !event.detail?.id) return;
    event.preventDefault();
    if (dialog?.open) dialog.close();
    trackJob(event.detail.id);
  });
  document.addEventListener('household:security-failed', event => {
    if (!event.target.matches('form[action^="/settings/backup/jobs"]') || dialog?.open) return;
    announce(event.detail, 'error');
  });
  if (document.querySelector('.backup-running, .backup-waiting, [data-backup-active]')) schedule(4000);

  const upload = document.querySelector('#backup-upload');
  const uploadOpen = document.querySelector('[data-backup-upload-open]');
  if (!upload || !uploadOpen) return;
  const uploadForm = upload.querySelector('[data-backup-upload]');
  const uploadStatus = upload.querySelector('[data-backup-upload-status]');
  const file = uploadForm.elements.archive;
  const submitUpload = uploadForm.querySelector('[data-backup-upload-submit]');
  let request = null;
  function showUploadStatus(message, error = false) {
    uploadStatus.textContent = message;
    uploadStatus.dataset.tone = error ? 'error' : '';
    uploadStatus.hidden = !message;
  }
  uploadOpen.addEventListener('click', () => {
    if (upload.open) return;
    showUploadStatus('');
    upload.showModal();
    file.focus();
  });
  upload.querySelectorAll('[data-backup-upload-close]').forEach(button => button.addEventListener('click', () => upload.close()));
  upload.addEventListener('close', () => {
    request?.abort();
    uploadForm.reset();
    uploadOpen.focus();
  });
  uploadForm.addEventListener('submit', async event => {
    event.preventDefault();
    const archive = file.files[0];
    if (!archive || request) return;
    if (archive.size > 32 * 1024 ** 3) {
      showUploadStatus(t('Die Backup-Datei überschreitet 32 GiB.'), true);
      return;
    }
    request = new AbortController();
    file.disabled = submitUpload.disabled = true;
    uploadForm.setAttribute('aria-busy', 'true');
    showUploadStatus(t('Backup-Datei wird hochgeladen …'));
    try {
      const response = await fetch('/api/v1/core/backup/upload', {
        method: 'POST', credentials: 'same-origin', body: archive, signal: request.signal,
        headers: { Accept: 'application/json', 'Content-Type': 'application/octet-stream',
          'X-CSRF-Token': document.querySelector('meta[name="csrf-token"]').content },
      });
      if (response.status === 401) { location.assign('/login'); return; }
      const result = response.headers.get('content-type')?.includes('application/json') ? await response.json() : {};
      if (!response.ok || !result.id) throw new Error(result.error || t('Die Backup-Datei konnte nicht hochgeladen werden.'));
      trackJob(result.id);
      upload.close();
    } catch (error) {
      if (error.name !== 'AbortError') showUploadStatus(error.message, true);
    } finally {
      request = null;
      file.disabled = submitUpload.disabled = false;
      uploadForm.setAttribute('aria-busy', 'false');
    }
  });
})();
