(() => {
  'use strict';
  const t = (text, ...args) => window.HouseholdI18n?.t(text, ...args) ?? text;
  const CACHE = 'household-share-v1';
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const list = document.querySelector('[data-share-list]');
  const empty = document.querySelector('[data-share-empty]');
  const errorBox = document.querySelector('[data-share-error]');
  const template = document.querySelector('[data-share-item]');
  if (new URLSearchParams(location.search).has('missed')) document.querySelector('[data-share-missed]').hidden = false;
  const fail = message => { errorBox.textContent = message; errorBox.hidden = false; };
  const json = async response => {
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || t('Speichern fehlgeschlagen.'));
    return body;
  };
  const refreshEmpty = () => { empty.hidden = list.children.length > 0; };

  async function save(request, response, title, status) {
    const bootstrap = await json(await fetch('/api/v1/core/bootstrap', { headers: { Accept: 'application/json' }, cache: 'no-store' }));
    const documents = bootstrap.states?.documents;
    if (!documents || documents.state !== 'enabled') throw new Error(t('Das Modul Dokumente ist ausgeschaltet.'));
    status.textContent = t('Dokument wird angelegt …');
    const record = await json(await fetch('/api/v1/m/documents/documents', {
      method: 'POST', cache: 'no-store',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID() },
      body: JSON.stringify({ data: { title }, generation: documents.generation }) }));
    status.textContent = t('Datei wird hochgeladen …');
    const blob = await response.blob();
    const name = decodeURIComponent(response.headers.get('X-Share-Name') || '') || 'datei';
    const form = new FormData();
    form.set('file', new File([blob], name, { type: blob.type }));
    form.set('generation', String(documents.generation));
    form.set('document_version', String(record.version));
    form.set('current', 'true');
    await json(await fetch('/api/v1/documents/' + record.id + '/original', {
      method: 'POST', cache: 'no-store', body: form,
      headers: { Accept: 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID() } }));
    await (await caches.open(CACHE)).delete(request);
    return record.id;
  }

  async function render() {
    if (!('caches' in window)) { refreshEmpty(); return; }
    const cache = await caches.open(CACHE);
    for (const request of await cache.keys()) {
      const response = await cache.match(request);
      if (!response) continue;
      const item = template.content.firstElementChild.cloneNode(true);
      const name = decodeURIComponent(response.headers.get('X-Share-Name') || '') || t('Datei');
      const blob = await response.clone().blob();
      item.querySelector('[data-share-meta]').textContent = t('%s · %s KB', name, Math.max(1, Math.round(blob.size / 1024)));
      const title = item.querySelector('input[name="title"]');
      title.value = name.replace(/\.[A-Za-z0-9]{1,5}$/, '').slice(0, 200) || t('Geteiltes Dokument');
      if (blob.type.startsWith('image/')) {
        const preview = item.querySelector('[data-share-preview]');
        preview.src = URL.createObjectURL(blob);
        preview.alt = t('Vorschau von %s', name);
        preview.hidden = false;
      }
      const status = item.querySelector('[data-share-status]');
      item.querySelector('[data-share-save]').addEventListener('click', async event => {
        const button = event.currentTarget;
        if (!title.value.trim()) { title.reportValidity(); return; }
        button.disabled = true;
        try {
          const id = await save(request, await cache.match(request), title.value.trim(), status);
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
        }
      });
      item.querySelector('[data-share-discard]').addEventListener('click', async () => {
        await cache.delete(request);
        item.remove();
        refreshEmpty();
      });
      list.append(item);
    }
    refreshEmpty();
  }
  render().catch(() => { fail(t('Die geteilten Dateien konnten auf diesem Gerät nicht gelesen werden.')); refreshEmpty(); });
})();
