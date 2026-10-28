((root) => {
  'use strict';
  function explain(candidate) {
    return `${candidate.dates.length} unterschiedliche Kaufdaten · Abstände ${candidate.gaps.join(', ')} Tage · typischer Abstand ${candidate.interval_days} Tage. Zuletzt ${candidate.last_on}; rechnerisch wieder ${candidate.next_on}.`;
  }
  function formatQuantity(value) {
    const [whole, fraction = ''] = String(value).split('.');
    const significant = fraction.replace(/0+$/, '');
    return significant ? `${whole},${significant}` : whole;
  }
  if (typeof module === 'object' && module.exports) { module.exports = {explain, formatQuantity}; return; }
  const board = document.querySelector('[data-shopping-list]'), panel = document.querySelector('#shopping-repurchase');
  if (!board || !panel) return;
  const loadButton = panel.querySelector('#repurchase-load'), status = panel.querySelector('#repurchase-status'), results = panel.querySelector('#repurchase-results');
  const endpoint = `/api/v1/shopping/lists/${board.dataset.shoppingList}/repurchase`;
  let busy = false, revision = 0, controller;
  const drafts = new Map();
  const node = (tag, text) => { const element = document.createElement(tag); if (text) element.textContent = text; return element; };
  async function load() {
    if (busy) return;
    const current = ++revision;
    controller?.abort(); controller = new AbortController();
    busy = true; loadButton.disabled = true; results.replaceChildren();
    status.textContent = 'Abgeschlossene Einkäufe werden geprüft …';
    try {
      const response = await fetch(endpoint, {headers:{Accept:'application/json'}, signal:controller.signal});
      const page = await response.json();
      if (current !== revision) return;
      if (!response.ok) throw Error(page.error || 'Einkaufsverlauf konnte nicht geladen werden.');
      status.textContent = `${page.checkouts} zuletzt erfasste Abschlüsse (höchstens ${page.window}); Kaufdaten in ${page.timezone}, heute ${page.today}. ${page.insufficient} Produktvarianten haben noch keine drei passenden Kaufdaten. ${page.more ? 'Die ersten 30 Vorschläge werden nach rechnerischem Termin gezeigt.' : ''}`;
      if (!page.candidates.length) results.append(node('p','Noch kein verlässlicher Kaufabstand. Mindestens drei verschiedene tatsächliche Kaufdaten derselben Produktpackung sind nötig.'));
      for (const candidate of page.candidates) render(candidate,page);
    } catch (error) {
      if (error.name !== 'AbortError' && current === revision) { status.textContent = error.message + ' Es wurden keine Teilvorschläge übernommen.'; }
    } finally { if (current === revision) { busy = false; loadButton.disabled = false; } }
  }
  function render(candidate,page) {
    const article = node('article'); article.className = 'record-card';
    article.append(node('h3',candidate.name),node('p',`${candidate.brand ? candidate.brand+' · ' : ''}${formatQuantity(candidate.pack_quantity)} ${candidate.pack_unit} pro Packung`),node('p',explain(candidate)),node('p','Kaufdaten: '+candidate.dates.join(', ')));
    const state = node('p',candidate.on_list ? 'Diese Produktvariante ist bereits auf dieser Liste.' : candidate.due ? 'Rechnerischer Termin erreicht – Bedarf selbst prüfen.' : 'Rechnerischer Termin liegt noch in der Zukunft.');
    article.append(state);
    if (board.dataset.canWrite === 'true' && !candidate.on_list) {
      const form = node('form'), label = node('label','Anzahl Packungen selbst festlegen'), input = node('input');
      input.name = 'quantity'; input.inputMode = 'decimal'; input.maxLength = 40; input.required = true;
      input.value = drafts.get(candidate.product_id) || '';
      input.addEventListener('input',()=>drafts.set(candidate.product_id,input.value));
      label.append(input);
      const button = node('button','Geprüft zur Liste hinzufügen'); button.type = 'submit';
      const errorBox = node('p'); errorBox.className = 'form-error'; errorBox.setAttribute('role','alert'); errorBox.tabIndex = -1;
      form.append(label,button,errorBox); article.append(form);
      let pending;
      form.addEventListener('submit',async event => {
        event.preventDefault(); if (busy || !form.reportValidity()) return;
        const quantity = input.value.trim().replace(',','.');
        if (!pending || pending.quantity !== quantity) pending = {quantity,key:crypto.randomUUID()};
        busy = true; button.disabled = true; loadButton.disabled = true; errorBox.textContent = '';
        try {
          const response = await fetch(endpoint,{method:'POST',headers:{Accept:'application/json','Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':pending.key},body:JSON.stringify({generation:page.generation,list_version:page.list_version,product_id:candidate.product_id,product_version:candidate.product_version,fingerprint:candidate.fingerprint,quantity})});
          const item = await response.json(); if (!response.ok) throw Error(item.error || 'Vorschlag erneut prüfen; deine Menge bleibt erhalten.');
          drafts.delete(candidate.product_id);form.remove(); state.textContent = 'Zur Liste hinzugefügt. Damit wurde noch kein Einkauf bestätigt.';
          document.querySelector('#shopping-refresh')?.click();
          status.textContent = 'Artikel ergänzt. Kaufabstände vor einem weiteren Vorschlag erneut prüfen.';
          loadButton.focus();
        } catch(error) { errorBox.textContent = error.message; errorBox.focus(); }
        finally { busy = false; button.disabled = false; loadButton.disabled = false; }
      });
    }
    results.append(article);
  }
  loadButton.addEventListener('click',()=>void load());
  root.addEventListener('pagehide',()=>{++revision;controller?.abort();});
})(globalThis);
