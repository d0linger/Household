const _householdText_shopping_repurchase_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root) => {
  'use strict';
  function explain(candidate) {
    return _householdText_shopping_repurchase_js("%s unterschiedliche Kaufdaten · Abstände %s Tage · typischer Abstand %s Tage. Zuletzt %s; rechnerisch wieder %s.", candidate.dates.length, candidate.gaps.join(', '), candidate.interval_days, candidate.last_on, candidate.next_on);
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
    status.textContent = _householdText_shopping_repurchase_js("Abgeschlossene Einkäufe werden geprüft …");
    try {
      const response = await fetch(endpoint, {headers:{Accept:'application/json'}, signal:controller.signal});
      const page = await response.json();
      if (current !== revision) return;
      if (!response.ok) throw Error(page.error || _householdText_shopping_repurchase_js("Einkaufsverlauf konnte nicht geladen werden."));
      status.textContent = _householdText_shopping_repurchase_js("%s zuletzt erfasste Abschlüsse (höchstens %s); Kaufdaten in %s, heute %s. %s Produktvarianten haben noch keine drei passenden Kaufdaten. %s", page.checkouts, page.window, page.timezone, page.today, page.insufficient, page.more ? _householdText_shopping_repurchase_js("Die ersten 30 Vorschläge werden nach rechnerischem Termin gezeigt.") : '');
      if (!page.candidates.length) results.append(node('p',_householdText_shopping_repurchase_js("Noch kein verlässlicher Kaufabstand. Mindestens drei verschiedene tatsächliche Kaufdaten derselben Produktpackung sind nötig.")));
      for (const candidate of page.candidates) render(candidate,page);
    } catch (error) {
      if (error.name !== 'AbortError' && current === revision) { status.textContent = error.message + _householdText_shopping_repurchase_js(" Es wurden keine Teilvorschläge übernommen."); }
    } finally { if (current === revision) { busy = false; loadButton.disabled = false; } }
  }
  function render(candidate,page) {
    const article = node('article'); article.className = 'record-card';
    article.append(node('h3',candidate.name),node('p',_householdText_shopping_repurchase_js("%s%s %s pro Packung", candidate.brand ? candidate.brand+' · ' : '', formatQuantity(candidate.pack_quantity), candidate.pack_unit)),node('p',explain(candidate)),node('p',_householdText_shopping_repurchase_js("Kaufdaten: ")+candidate.dates.join(', ')));
    const state = node('p',candidate.on_list ? _householdText_shopping_repurchase_js("Diese Produktvariante ist bereits auf dieser Liste.") : candidate.due ? _householdText_shopping_repurchase_js("Rechnerischer Termin erreicht – Bedarf selbst prüfen.") : _householdText_shopping_repurchase_js("Rechnerischer Termin liegt noch in der Zukunft."));
    article.append(state);
    if (board.dataset.canWrite === 'true' && !candidate.on_list) {
      const form = node('form'), label = node('label',_householdText_shopping_repurchase_js("Anzahl Packungen selbst festlegen")), input = node('input');
      input.name = 'quantity'; input.inputMode = 'decimal'; input.maxLength = 40; input.required = true;
      input.value = drafts.get(candidate.product_id) || '';
      input.addEventListener('input',()=>drafts.set(candidate.product_id,input.value));
      label.append(input);
      const button = node('button',_householdText_shopping_repurchase_js("Geprüft zur Liste hinzufügen")); button.type = 'submit';
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
          const item = await response.json(); if (!response.ok) throw Error(item.error || _householdText_shopping_repurchase_js("Vorschlag erneut prüfen; deine Menge bleibt erhalten."));
          drafts.delete(candidate.product_id);form.remove(); state.textContent = _householdText_shopping_repurchase_js("Zur Liste hinzugefügt. Damit wurde noch kein Einkauf bestätigt.");
          document.querySelector('#shopping-refresh')?.click();
          status.textContent = _householdText_shopping_repurchase_js("Artikel ergänzt. Kaufabstände vor einem weiteren Vorschlag erneut prüfen.");
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
