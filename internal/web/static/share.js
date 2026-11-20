(() => {
  'use strict';
  const t = (text, ...args) => window.HouseholdI18n?.t(text, ...args) ?? text;
  const CACHE = 'household-share-v1';
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const list = document.querySelector('[data-share-list]');
  const empty = document.querySelector('[data-share-empty]');
  const errorBox = document.querySelector('[data-share-error]');
  const template = document.querySelector('[data-share-item]');
  const params = new URLSearchParams(location.search);
  if (params.has('missed')) document.querySelector('[data-share-missed]').hidden = false;
  if (params.has('failed')) document.querySelector('[data-share-failed]').hidden = false;
  const fail = message => { errorBox.textContent = message; errorBox.hidden = false; };
  const json = async response => {
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || t('Speichern fehlgeschlagen.'));
    return body;
  };
  const refreshEmpty = () => { empty.hidden = list.children.length > 0; };

  const user = document.body.dataset.user || '';
  // Saving and discarding one shared file are serialized across tabs; the
  // state always comes from the cache entry, never from a page's old copy.
  // Without Web Locks (every current browser has them) the page offers no
  // actions rather than risking two documents for one file.
  const exclusive = (request, work) => navigator.locks.request('household-share:' + request.url, work);
  // update stores new state headers on the parked file and returns the entry.
  async function update(cache, request, response, values) {
    const headers = new Headers(response.headers);
    for (const [key, value] of Object.entries(values)) headers.set(key, value);
    const next = new Response(await response.clone().blob(), { headers });
    await cache.put(request, next.clone());
    return next;
  }
  const stored = (response, key) => decodeURIComponent(response.headers.get(key) || '');
  const gone = () => new Error(t('Diese Datei wurde bereits gespeichert oder verworfen.'));

  // One shared file becomes exactly one document: its create payload and
  // request keys are fixed on the first attempt and kept with the file, and
  // once the document exists only its upload is retried.
  async function save(request, title, status) {
    const bootstrap = await json(await fetch('/api/v1/core/bootstrap', { headers: { Accept: 'application/json' }, cache: 'no-store' }));
    const documents = bootstrap.states?.documents;
    if (!documents || documents.state !== 'enabled') throw new Error(t('Das Modul Dokumente ist ausgeschaltet.'));
    return exclusive(request, async () => {
      const cache = await caches.open(CACHE);
      let response = await cache.match(request);
      if (!response || stored(response, 'X-Share-Owner') !== user) throw gone();
      if (!response.headers.get('X-Share-Create-Key')) {
        response = await update(cache, request, response, {
          'X-Share-Create-Key': crypto.randomUUID(), 'X-Share-Upload-Key': crypto.randomUUID(),
          'X-Share-Title': encodeURIComponent(title), 'X-Share-Generation': String(documents.generation) });
      }
      let id = stored(response, 'X-Share-Document'), version = stored(response, 'X-Share-Document-Version');
      const generation = stored(response, 'X-Share-Generation');
      if (!id) {
        status.textContent = t('Dokument wird angelegt …');
        const record = await json(await fetch('/api/v1/m/documents/documents', {
          method: 'POST', cache: 'no-store',
          headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': response.headers.get('X-Share-Create-Key') },
          body: JSON.stringify({ data: { title: stored(response, 'X-Share-Title') }, generation: Number(generation) }) }));
        id = String(record.id);
        version = String(record.version);
        response = await update(cache, request, response, { 'X-Share-Document': encodeURIComponent(id), 'X-Share-Document-Version': version });
      }
      status.textContent = t('Datei wird hochgeladen …');
      const blob = await response.clone().blob();
      const name = stored(response, 'X-Share-Name') || 'datei';
      const form = new FormData();
      form.set('file', new File([blob], name, { type: blob.type }));
      form.set('generation', generation);
      form.set('document_version', version);
      form.set('current', 'true');
      await json(await fetch('/api/v1/documents/' + encodeURIComponent(id) + '/original', {
        method: 'POST', cache: 'no-store', body: form,
        headers: { Accept: 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': response.headers.get('X-Share-Upload-Key') } }));
      await cache.delete(request);
      return id;
    });
  }

  async function render() {
    if (!('caches' in window)) { refreshEmpty(); return; }
    if (!navigator.locks?.request) {
      fail(t('Dieser Browser kann geteilte Dateien nicht sicher speichern. Bitte aktualisiere ihn.'));
      refreshEmpty();
      return;
    }
    const cache = await caches.open(CACHE);
    for (const request of await cache.keys()) {
      // A parked file belongs to the first account that opens it here;
      // another account on this device never sees or saves it.
      const response = await exclusive(request, async () => {
        const current = await cache.match(request);
        if (!current) return null;
        const owner = stored(current, 'X-Share-Owner');
        if (owner && owner !== user) return null;
        return owner ? current : update(cache, request, current, { 'X-Share-Owner': encodeURIComponent(user) });
      });
      if (!response) continue;
      const item = template.content.firstElementChild.cloneNode(true);
      const name = decodeURIComponent(response.headers.get('X-Share-Name') || '') || t('Datei');
      const blob = await response.clone().blob();
      item.querySelector('[data-share-meta]').textContent = t('%s · %s KB', name, Math.max(1, Math.round(blob.size / 1024)));
      const title = item.querySelector('input[name="title"]');
      title.value = name.replace(/\.[A-Za-z0-9]{1,5}$/, '').slice(0, 200) || t('Geteiltes Dokument');
      // After a first attempt the title is part of the fixed create payload.
      if (response.headers.get('X-Share-Title')) {
        title.value = stored(response, 'X-Share-Title');
        title.readOnly = true;
      }
      // A preview URL keeps its blob alive; it is released with the preview.
      let previewURL = '';
      const releasePreview = () => { if (previewURL) URL.revokeObjectURL(previewURL); previewURL = ''; };
      if (blob.type.startsWith('image/')) {
        const preview = item.querySelector('[data-share-preview]');
        previewURL = URL.createObjectURL(blob);
        preview.src = previewURL;
        preview.alt = t('Vorschau von %s', name);
        preview.hidden = false;
      }
      const status = item.querySelector('[data-share-status]');
      const discard = item.querySelector('[data-share-discard]');
      item.querySelector('[data-share-save]').addEventListener('click', async event => {
        const button = event.currentTarget;
        if (!title.value.trim()) { title.reportValidity(); return; }
        // Discarding waits while a save can still write this file's state.
        button.disabled = true;
        discard.disabled = true;
        try {
          const id = await save(request, title.value.trim(), status);
          status.textContent = '';
          const link = document.createElement('a');
          link.href = '/m/documents/workspace/review?id=' + encodeURIComponent(id);
          link.textContent = t('Gespeichert. Dokument öffnen');
          status.append(link);
          item.querySelectorAll('button').forEach(node => { node.disabled = true; });
        } catch (error) {
          status.textContent = '';
          fail(error.message);
          button.disabled = false;
          discard.disabled = false;
        }
      });
      discard.addEventListener('click', async () => {
        await exclusive(request, async () => { await (await caches.open(CACHE)).delete(request); });
        releasePreview();
        item.remove();
        refreshEmpty();
      });
      list.append(item);
    }
    refreshEmpty();
  }
  render().catch(() => { fail(t('Die geteilten Dateien konnten auf diesem Gerät nicht gelesen werden.')); refreshEmpty(); });
})();
