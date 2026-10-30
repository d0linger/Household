/* Household uses only bundled assets. Private pages are never service-worker cached. */
(() => {
  'use strict';
  const $ = (s, root = document) => root.querySelector(s);
  const all = (s, root = document) => [...root.querySelectorAll(s)];
  const user = document.body.dataset.user;
  const household = document.body.dataset.household;
  const csrf = () => $('meta[name="csrf-token"]')?.content || '';
  function rememberShoppingFocus(url) {
    try {sessionStorage.setItem('household-focus',JSON.stringify({user,household,url,path:location.pathname+location.search,until:Date.now()+15000}));} catch {}
  }
  try {
    const focus=JSON.parse(sessionStorage.getItem('household-focus')||'null');sessionStorage.removeItem('household-focus');
    if(focus&&focus.user===user&&focus.household===household&&focus.path===location.pathname+location.search&&focus.until>Date.now())all('[data-check]').find(button=>button.dataset.actionUrl===focus.url)?.focus();
  } catch {}
  let deferredInstall, worker, pendingDelete;
  let toastTimer;
  function toast(message) {const box = $('#toast'); box.textContent = message; box.hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => box.hidden = true, 6000);}
  async function clearPrivateDeviceData(includePush=false){
    const reset=device=>{try{return device&&typeof device.reset==='function'?Promise.resolve(device.reset()):Promise.reject(Error('Gerätebereinigung ist nicht verfügbar.'));}catch(error){return Promise.reject(error);}};
    const pending=[clearQueue(),reset(window.HouseholdPantryDevice),reset(window.HouseholdShoppingDevice),Promise.resolve().then(()=>window.HouseholdRecipeDevice?.clear?.())];
    if(includePush)pending.push(reset(window.HouseholdPushDevice));
    const bounded=promise=>{let timer;return Promise.race([Promise.resolve(promise),new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Gerätebereinigung wurde nicht rechtzeitig bestätigt.')),6500);})]).finally(()=>clearTimeout(timer));};
    const settled=await Promise.allSettled(pending.map(bounded));
    if(settled.some(result=>result.status==='rejected'))throw Error('Lokale Gerätedaten konnten nicht vollständig gelöscht werden. Gerätespeicher prüfen.');
  }
  try{if(sessionStorage.getItem('household-device-cleanup-warning')){sessionStorage.removeItem('household-device-cleanup-warning');toast('Lokale Gerätedaten konnten nicht vollständig gelöscht werden. Gerätespeicher prüfen.');}}catch{}
  function connection() {const box = $('#connection'); box.hidden = navigator.onLine; box.textContent = 'Offline · Änderungen erst nach erneuter Verbindung und Prüfung übertragen.';}
  addEventListener('online', connection); addEventListener('offline', connection); connection();
  try {const theme = localStorage.getItem('household-theme'); if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme;} catch {}
  const nav = $('.mobile-nav');
  if (nav) {const links = all('a', nav); if (links.length > 5) {links.slice(4).forEach(a => a.hidden = true); $('.more-button', nav).hidden = false;} if (!links.length) nav.hidden = true;}
  all('[data-open]').forEach(button => button.addEventListener('click', () => {const dialog = document.getElementById(button.dataset.open); dialog.showModal(); $('input:not([type=hidden]),select,textarea', dialog)?.focus();}));
  all('[data-close]').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
  all('[data-back]').forEach(button => button.addEventListener('click', () => history.length > 1 ? history.back() : location.assign('/')));
  all('[data-time]').forEach(input => {if (input.value && input.value.includes('Z')) {const d = new Date(input.value); if (!isNaN(d)) {d.setMinutes(d.getMinutes() - d.getTimezoneOffset()); input.value = d.toISOString().slice(0,16);}}});
  const referenceButtons = new WeakMap();
  function referenceSubmissionBlocked(form) {
    // Disabled fields are omitted by FormData, including already chosen optional references.
    return all('[data-reference]', form).some(select => select.dataset.referenceState !== 'ready' && (select.required || select.multiple || Boolean(select.value)));
  }
  function syncReferenceButtons(form) {
    const blocked = referenceSubmissionBlocked(form) || form.dataset.submitting === 'true';
    all('button[type=submit],button:not([type]),input[type=submit]', form).forEach(button => {
      if (!referenceButtons.has(button)) referenceButtons.set(button, button.disabled);
      button.disabled = blocked || referenceButtons.get(button);
    });
  }
  all('[data-reference]').forEach((select, index) => {
    const form = select.closest('form');
    if (!form) return;
    // A parent fieldset owns its temporary or read-only state independently.
    const initiallyDisabled = select.disabled;
    const label = select.labels?.[0]?.childNodes[0]?.textContent.trim() || 'Auswahl';
    const wrapper = document.createElement('div'); wrapper.className = 'reference-field';
    const original = select.closest('label') || select;
    original.replaceWith(wrapper); wrapper.append(original);
    const controls = document.createElement('div'); controls.className = 'reference-tools';
    const search = document.createElement('input'); search.type = 'search'; search.maxLength = 100;
    search.setAttribute('aria-label', label + ' durchsuchen'); search.placeholder = 'Auswahl durchsuchen …';
    const find = document.createElement('button'); find.type = 'button'; find.textContent = 'Suchen';
    const next = document.createElement('button'); next.type = 'button'; next.textContent = 'Weitere Treffer'; next.hidden = true;
    const status = document.createElement('small'); status.setAttribute('role', 'status');
    controls.append(search, find, next); wrapper.append(controls, status);
    if (!select.multiple && !all('option', select).some(option => option.value === '')) {
      const empty = document.createElement('option'); empty.value = ''; empty.textContent = 'Bitte auswählen';
      select.prepend(empty); select.value = '';
    }
    const notice = document.createElement('p');
    notice.className = 'form-error reference-error'; notice.id = 'reference-error-' + index;
    notice.setAttribute('role', 'alert'); notice.tabIndex = -1; notice.hidden = true;
    const message = document.createElement('span');
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = 'Erneut laden';
    notice.append(message, document.createTextNode(' '), retry);
    wrapper.append(notice);
    select.setAttribute('aria-describedby', [select.getAttribute('aria-describedby'), notice.id].filter(Boolean).join(' '));
    let loading = false, nextCursor = '', currentQuery = '', retryCursor = '';
    async function loadReferences(cursor = '') {
      if (loading) return;
      retryCursor = cursor;
      if (!cursor) currentQuery = search.value.trim();
      loading = true; select.dataset.referenceState = 'loading'; select.disabled = true;
      select.setAttribute('aria-busy', 'true'); retry.disabled = true; syncReferenceButtons(form);
      search.disabled = true; find.disabled = true; next.disabled = true; status.textContent = 'Auswahl wird geladen …';
      try {
        const params = new URLSearchParams({q:currentQuery,cursor});
        if(select.dataset.favorites==='1')params.set('favorites','1');
        const response = await fetch('/api/v1/core/reference-options/' + select.dataset.reference + '?' + params, {headers:{Accept:'application/json'}});
        if (!response.ok) throw new Error('reference request failed');
        const page = await response.json(), rows = page.records;
        if (!Array.isArray(rows) || typeof page.next_cursor !== 'string') throw new Error('invalid references');
        const selected = new Set([...select.selectedOptions].map(option => option.value));
        // Keep chosen values even when they are outside this result page.
        all('option', select).filter(option => option.value && !selected.has(option.value)).forEach(option => option.remove());
        const existing = new Map(all('option', select).map(option => [option.value, option]));
        const fragment = document.createDocumentFragment();
        for (const row of rows) {
          if (existing.has(row.id)) { existing.get(row.id).textContent = row.label || row.id; continue; }
          const option = document.createElement('option'); option.value = row.id; option.textContent = row.label || row.id;
          option.dataset.loadedReference = 'true'; if (selected.has(option.value)) option.selected = true;
          fragment.append(option);
        }
        const restoreFocus = document.activeElement === retry;
        select.append(fragment); select.disabled = initiallyDisabled; select.dataset.referenceState = 'ready'; notice.hidden = true;
        nextCursor = page.next_cursor; next.hidden = !nextCursor;
        status.textContent = rows.length ? rows.length + ' Treffer auf dieser Seite. Gewählte Einträge bleiben erhalten.' : 'Keine passenden Treffer. Gewählte Einträge bleiben erhalten.';
        if (restoreFocus) select.focus();
      } catch {
        select.dataset.referenceState = 'error';
        status.textContent = '';
        message.textContent = label + ' konnte nicht geladen werden. Prüfe die Verbindung und versuche es erneut. Deine Eingaben bleiben erhalten.';
        notice.hidden = false;
      } finally {
        loading = false; retry.disabled = false; select.removeAttribute('aria-busy'); syncReferenceButtons(form);
        search.disabled = initiallyDisabled; find.disabled = initiallyDisabled; next.disabled = initiallyDisabled;
      }
    }
    retry.addEventListener('click', () => void loadReferences(retryCursor));
    find.addEventListener('click', () => void loadReferences());
    next.addEventListener('click', () => void loadReferences(nextCursor));
    search.addEventListener('keydown', event => {if(event.key === 'Enter'){event.preventDefault();void loadReferences();}});
    void loadReferences();
  });
  const dbPromise = new Promise((resolve, reject) => {
    if (!('indexedDB' in window)) {reject(new Error('Lokaler Speicher nicht verfügbar'));return;}
    const request = indexedDB.open('household-offline',1);
    request.onupgradeneeded = () => request.result.createObjectStore('queue',{keyPath:'key'});
    request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
  });
  dbPromise.catch(() => {});
  async function queueOp(mode, operation) {const db = await dbPromise; return new Promise((resolve,reject) => {const tx = db.transaction('queue',mode); const result = operation(tx.objectStore('queue')); tx.oncomplete = () => resolve(result?.result); tx.onerror = () => reject(tx.error); tx.onabort = () => reject(tx.error);});}
  async function clearQueue() {await queueOp('readwrite', store => store.clear());}
  function financialMutation(url) {
    try {const target=new URL(url,location.origin);return target.origin===location.origin&&(/^\/api\/v1\/(?:m\/expenses(?:\/|$)|expenses\/editor(?:\/|$))/.test(target.pathname)||actionTargets(target.pathname).includes('expenses'));}catch{return false;}
  }
  function financialRecovery(url) {
    const target=new URL(url,location.origin),match=target.pathname.match(/^\/api\/v1\/m\/(expenses|fuel)\/(entries|entry_lines|categories|fillups)(?:\/([0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}))?(?:\/actions\/[a-z_]+)?$/i);
    if(target.origin!==location.origin||!match)return {url:'/m/expenses/entries',label:'Vorhandene Buchungen in neuem Tab prüfen'};
    const [,module,entity,record]=match;
    if(module==='expenses'&&entity==='entries')return record?{url:'/m/expenses/editor?entry='+encodeURIComponent(record),label:'Betroffene Buchung in neuem Tab prüfen'}:{url:'/m/expenses/entries',label:'Vorhandene Buchungen in neuem Tab prüfen'};
    return {url:'/m/'+module+'/'+entity+(record?'/'+encodeURIComponent(record):''),label:module==='fuel'?'Tankvorgang in neuem Tab prüfen':'Betroffenen Stammdatensatz in neuem Tab prüfen'};
  }
  async function queueMutation(url, method, body, key) {
    if (financialMutation(url)) throw new Error('Ausgaben werden nicht auf diesem Gerät vorgemerkt. Verbindung wiederherstellen und den unveränderten Vorgang erneut prüfen. Deine Formulareingaben bleiben erhalten.');
    if (!user || localStorage.getItem('household-offline-owner') !== user) throw new Error('Offline-Speicherung ist auf diesem Gerät nicht aktiviert.');
    const entries = await queueOp('readonly', store => store.getAll()); if (entries.length >= 100) throw new Error('Maximal 100 wartende Änderungen. Zuerst synchronisieren.');
    await queueOp('readwrite', store => store.put({key,user,household,url,method,body,format:1,created:new Date().toISOString()}));
    toast('Änderung vorgemerkt. Nach Verbindung in den Einstellungen prüfen.');
  }
  async function request(url, method, body, key) {
    const response = await fetch(url,{method,headers:{'Content-Type':'application/json','Accept':'application/json','X-CSRF-Token':csrf(),'Idempotency-Key':key},body:JSON.stringify(body)});
    const result = await response.json().catch(() => ({error:'Unerwartete Serverantwort'}));
    if (!response.ok) {const error = new Error(result.error || 'Vorgang fehlgeschlagen'); error.status = response.status; throw error;}
    return result;
  }
  function formBody(form) {const fields = new FormData(form); const data = {};
    const targets=actionTargets(form.getAttribute('action')),targetGenerations={};
    const overrides={};for (const [name,val] of fields) {if (['csrf','version','generation','idempotency_key'].includes(name)) continue;if(name.startsWith('target_generation_')){const mid=name.slice(18);if(targets.includes(mid))targetGenerations[mid]=Number(val);continue;}if(/^unit_[0-9a-f-]{36}$/.test(name)){overrides[name.slice(5)]=val;continue;}const input = form.elements.namedItem(name);data[name] = input instanceof HTMLInputElement && input.hasAttribute('data-time') && val ? new Date(val).toISOString() : val;}
    for(const name of ['ingredient_ids','tag_ids'])if(form.querySelector(`[name="${name}"]`))data[name]=fields.getAll(name);if(Object.keys(overrides).length)data.unit_overrides=overrides;
    return {data,version:Number(fields.get('version') || 0),generation:Number(fields.get('generation') || 0),...(targets.length?{target_generations:targetGenerations}:{})};
  }
  function actionTargets(url){const match=url.match(/\/m\/([^/]+)\/([^/]+)\/[^/]+\/actions\/([^/]+)$/);if(!match)return [];return ({'recipes/recipes/shopping':['shopping'],'pantry/products/shopping':['shopping'],'fuel/fillups/expense':['expenses'],'mealplan/plans/shopping':['shopping']})[match.slice(1).join('/')]||[];}
  all('[data-record-form]').forEach(form => form.addEventListener('submit', async event => {
    event.preventDefault();
    if (referenceSubmissionBlocked(form)) { const notice = $('.reference-error:not([hidden])', form); if (notice) notice.focus(); return; }
    if (!form.reportValidity()) return;
    const button = $('button:not([type=button])',form); const errorBox = $('.form-error:not(.reference-error)',form); if (errorBox) errorBox.hidden = true;
    const body = formBody(form);const key = form.elements.idempotency_key.value || crypto.randomUUID();const method = form.dataset.method || 'POST';
    form.dataset.submitting = 'true';
    if (button) button.disabled = true;
    try {const result=await request(form.getAttribute('action'),method,body,key);if(result.url)location.assign(result.url);else location.reload();}
    catch(error) {
      if (!error.status && !navigator.onLine) {try {await queueMutation(form.getAttribute('action'),method,body,key);form.closest('dialog')?.close();}catch(queueError){toast(queueError.message);}}
      else {if(errorBox){errorBox.textContent=error.message;errorBox.hidden=false;errorBox.focus();}else toast(error.message);if(error.status===409&&method==='PATCH')await reviewFormConflict(form,body);}
    } finally {delete form.dataset.submitting; if(button) button.disabled=false; if ($('[data-reference]', form)) syncReferenceButtons(form);}
  }));
  all('[data-check]').forEach(button => button.addEventListener('click',async () => {
    button.disabled = true;const body = {data:{checked:button.dataset.check === 'true'},version:Number(button.dataset.version),generation:Number(button.dataset.generation)};const key = crypto.randomUUID();
    try {const result=await request(button.dataset.actionUrl,'POST',body,key);try{sessionStorage.setItem('household-undo',JSON.stringify({user,url:button.dataset.actionUrl,until:Date.now()+10000,body:{data:{checked:!body.data.checked},version:result.version,generation:body.generation}}));}catch{}rememberShoppingFocus(button.dataset.actionUrl);location.reload();}
    catch(error){if(!error.status && !navigator.onLine){try{await queueMutation(button.dataset.actionUrl,'POST',body,key);}catch(e){toast(e.message);}}else toast(error.message);button.disabled=false;}
  }));
  all('[data-delete]').forEach(button => button.addEventListener('click',() => {pendingDelete=button;$('#confirm-dialog').showModal();}));
  $('#confirm-delete')?.addEventListener('click',async event => {
    if(!pendingDelete)return;const button=pendingDelete;event.currentTarget.disabled=true;
    try{await request(button.dataset.delete,'DELETE',{data:{},version:Number(button.dataset.version),generation:Number(button.dataset.generation)},crypto.randomUUID());location.assign(button.dataset.return);}
    catch(error){toast(error.message);$('#confirm-dialog').close();}finally{event.currentTarget.disabled=false;}
  });
  try{const saved=JSON.parse(sessionStorage.getItem('household-undo')||'null');sessionStorage.removeItem('household-undo');if(saved&&saved.user===user&&saved.until>Date.now()&&/^\/api\/v1\/m\/shopping\/items\/[0-9a-f-]{36}\/actions\/check$/.test(saved.url)){toast('Artikel aktualisiert.');const undo=document.createElement('button');undo.textContent='Rückgängig';undo.addEventListener('click',async()=>{undo.disabled=true;try{await request(saved.url,'POST',saved.body,crypto.randomUUID());rememberShoppingFocus(saved.url);location.reload();}catch(error){toast(error.message);}});$('#toast').append(undo);setTimeout(()=>undo.remove(),saved.until-Date.now());}}catch{}
  const preferences = $('#preferences');
  if(preferences){try{preferences.elements.theme.value=localStorage.getItem('household-theme')||'system';preferences.elements.offline.checked=localStorage.getItem('household-offline-owner')===user;}catch{}
    preferences.addEventListener('submit',async event=>{event.preventDefault();const theme=preferences.elements.theme.value,offline=preferences.elements.offline.checked;
      try{const response=await fetch('/api/v1/core/preferences',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({theme,offline})});if(!response.ok)throw new Error('Einstellungen konnten nicht gespeichert werden.');localStorage.setItem('household-theme',theme);if(theme==='system')delete document.documentElement.dataset.theme;else document.documentElement.dataset.theme=theme;if(offline)localStorage.setItem('household-offline-owner',user);else{localStorage.removeItem('household-offline-owner');await clearPrivateDeviceData();}toast('Geräteeinstellungen gespeichert.');}catch(error){toast(error.message);}
    });
  }
  const choiceText=value=>({piece:'Stück',portion:'Portionen',tsp:'TL',tbsp:'EL',available:'Verfügbar',unavailable:'Nicht verfügbar',substituted:'Ersatz gewählt',diesel:'Diesel',petrol:'Benzin',lpg:'LPG',cng:'CNG',receive:'Zugang',consume:'Verbrauch',waste:'Entsorgung',adjust:'Korrektur',expense:'Ausgabe',refund:'Erstattung',dinner:'Abendessen',lunch:'Mittagessen',breakfast:'Frühstück',snack:'Zwischenmahlzeit',actual:'Abgelesen',estimated:'Geschätzt'})[value]||String(value);
  function localDate(value,time=false){const date=new Date(value);return isNaN(date)?String(value):new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',...(time?{timeStyle:'short'}:{}),timeZone:'Europe/Vienna'}).format(date);}
  async function reviewFormConflict(form, body) {
    $('.conflict-review',form)?.remove();
    const article=document.createElement('section');article.className='conflict-review queue-entry';article.tabIndex=-1;
    const caption=document.createElement('p');caption.textContent='Aktueller Serverwert → deine Eingabe. Prüfe die Unterschiede vor einer erneuten Speicherung.';article.append(caption);form.append(article);
    try{
      const response=await fetch('/api/v1/core/bootstrap',{headers:{Accept:'application/json'}});if(!response.ok)throw new Error('Anmeldung erneut prüfen.');
      const fresh=await response.json(),url=form.getAttribute('action'),mid=url.match(/\/m\/([^/]+)\//)?.[1];
      if(fresh.user.id!==user||fresh.user.household_id!==household||fresh.states[mid]?.state!=='enabled'||fresh.states[mid]?.generation!==body.generation)throw new Error('Konto oder Modulstand hat sich geändert. Seite neu öffnen und Eingaben prüfen.');
      const cache=new Map();await describeDraft({url,method:'PATCH',body},fresh,cache,article);
      const current=await cache.get(url);if(!current?.version)throw new Error('Aktueller Datensatz konnte nicht geprüft werden.');
      const apply=document.createElement('button');apply.type='button';apply.textContent='Geprüfte Eingaben erneut speichern';
      apply.addEventListener('click',async()=>{
        if(referenceSubmissionBlocked(form)){caption.textContent='Die gewählte Zuordnung wird noch geladen oder konnte nicht geladen werden. Auswahl erneut laden, bevor du die Änderung bestätigst.';article.focus();return;}
        if(JSON.stringify(formBody(form))!==JSON.stringify(body)){await reviewFormConflict(form,formBody(form));return;}
        apply.disabled=true;
        try{await request(url,'PATCH',{...body,version:current.version},crypto.randomUUID());location.reload();}
        catch(error){if(error.status===409)await reviewFormConflict(form,body);else{caption.textContent=error.message+' Eingaben bleiben erhalten.';apply.disabled=false;}}
      });article.append(apply);article.focus();
    }catch(error){caption.textContent=error.message;article.focus();}
  }
  async function describeDraft(entry,fresh,cache,article){
    const match=entry.url.match(/^\/api\/v1\/m\/([^/]+)\/([^/]+)(?:\/([0-9a-f-]{36}))?(?:\/actions\/([^/]+))?$/);
    if(!match)throw new Error('Diesen Vorgang bitte neu öffnen und erfassen.');
    const [,mid,entityID,rid,action]=match;
    const mod=fresh.modules.find(m=>m.id===mid),entity=mod?.entities.find(e=>e.id===entityID);
    if(!entity)throw new Error('Dieser Bereich ist nicht mehr verfügbar.');
    const read=async url=>{if(!cache.has(url))cache.set(url,fetch(url,{headers:{Accept:'application/json'}}).then(async response=>{if(!response.ok)throw new Error('Eine Zuordnung ist nicht mehr verfügbar. Bitte neu öffnen und erfassen.');return response.json();}));return cache.get(url);};
    const current=rid?await read('/api/v1/m/'+mid+'/'+entityID+'/'+rid):{};
    const operations={clone:'Neue Einkaufsliste aus Vorlage erstellen',check:entry.body.data.checked?'Artikel erledigen':'Artikel wieder öffnen',archive:entry.body.data.archived===true||entry.body.data.archived==='true'?'Liste archivieren':'Liste wieder öffnen',post:'Ausgabe verbindlich buchen',reverse:'Ausgleich erstellen',complete:'Aufgabe erledigen',expense:'Tankung als Ausgabe übernehmen',recipe:'Rezept verknüpfen',tags:'Tags zuordnen',transfer:'Bestand umlagern',shopping:mid==='pantry'?'Vorrat zum Einkauf ergänzen':'Zutaten zur Einkaufsliste übernehmen'};
    const title=document.createElement('h3');title.textContent=mod.title+' · '+(operations[action]||(entry.method==='PATCH'?'Eintrag ändern':'Eintrag anlegen'));
    const name=document.createElement('p');name.textContent=current[entity.primary]||entry.body.data[entity.primary]||entity.title;
    article.append(title,name);
    const extra={list_id:{label:'Einkaufsliste',reference:'shopping/lists'},category_id:{label:'Kategorie',reference:'expenses/categories'},recipe_id:{label:'Rezept',reference:'recipes/recipes'},location_id:{label:'Ziel-Lagerort',reference:'pantry/locations'},ingredient_ids:{label:'Ausgewählte Zutaten',reference:'recipes/ingredients'},tag_ids:{label:'Tags',reference:'recipes/tags'},servings:{label:'Portionen',kind:'quantity'},checked:{label:'Erledigt',kind:'boolean'},archived:{label:'Archiviert',kind:'boolean'},unit_overrides:{label:'Einheiten'}};
    const namedReference=async(reference,value)=>{
      const [referenceModule,referenceEntity]=reference.split('/');
      const target=fresh.modules.find(m=>m.id===referenceModule)?.entities.find(e=>e.id===referenceEntity);
      const rows=(await read('/api/v1/core/reference-options/'+reference+'/'+encodeURIComponent(value))).records;
      let row=rows.find(row=>row.id===value);
      if(!row&&referenceModule!=='core')row=await read('/api/v1/m/'+reference+'/'+value);
      const label=row?.label||row?.[target?.primary];
      if(!label)throw new Error('Eine Zuordnung konnte nicht benannt werden. Bitte neu erfassen.');
      return label;
    };
    const show=async(field,value)=>{
      if(value===null||value===undefined||value==='')return 'Nicht gesetzt';
      if(field.reference){const values=Array.isArray(value)?value:[value];return values.length?(await Promise.all(values.map(value=>namedReference(field.reference,value)))).join(', '):'Keine Auswahl';}
      if(field.kind==='boolean')return value===true||value==='true'?'Ja':'Nein';
      if(field.kind==='date'||field.kind==='time')return localDate(value,field.kind==='time');
      if(field.kind==='money'){const amount=typeof value==='number'?value/100:Number(String(value).replace(',','.'));return new Intl.NumberFormat('de-AT',{style:'currency',currency:'EUR'}).format(amount);}
      if(field.kind==='quantity')return String(value).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');
      if(typeof value==='object')throw new Error('Diese Änderung bitte im ursprünglichen Eintrag prüfen und neu erfassen.');
      return choiceText(value);
    };
    const values=document.createElement('dl');
    for(const [key,value]of Object.entries(entry.body.data)){
      const field=entity.fields.find(f=>f.name===key)||extra[key];
      if(!field)throw new Error('Ein Feld wird nicht mehr unterstützt. Bitte neu erfassen.');
      let after;
      if(key==='unit_overrides')after=(await Promise.all(Object.entries(value).map(async([id,unit])=>await namedReference('recipes/ingredients',id)+': '+choiceText(unit)))).join(', ');
      else after=await show(field,value);
      const before=key in current?await show(field,current[key]):null;
      if(before===after)continue;
      const label=document.createElement('dt');label.textContent=field.label;
      const description=document.createElement('dd');description.textContent=before?before+' → '+after:after;
      values.append(label,description);
    }
    article.append(values);
    if(!values.children.length){const note=document.createElement('p');note.textContent='Der angezeigte Vorgang wird nach deiner Bestätigung ausgeführt.';article.append(note);}
  }
  $('#review-queue')?.addEventListener('click',async()=>{
    const box=$('#queue-review');box.replaceChildren();box.textContent='Änderungen werden zur Prüfung geladen …';
    try{
      const bootstrap=await fetch('/api/v1/core/bootstrap',{headers:{Accept:'application/json'}});if(!bootstrap.ok)throw new Error('Bitte erneut anmelden.');
      const fresh=await bootstrap.json();
      if(fresh.user?.id!==user||fresh.user?.household_id!==household)throw new Error('Das angemeldete Konto hat gewechselt. Einstellungen nach der Anmeldung neu öffnen.');
      const entries=await queueOp('readonly',store=>store.getAll()),cache=new Map();box.replaceChildren();
      if(!entries.length){box.textContent='Keine wartenden Änderungen.';return;}
      for(const entry of entries){
        const article=document.createElement('article');article.className='session-row queue-entry';
        const actions=document.createElement('div');actions.className='actions';
        const discard=document.createElement('button');discard.textContent='Verwerfen';discard.addEventListener('click',async()=>{await queueOp('readwrite',store=>store.delete(entry.key));article.remove();});actions.append(discard);
        const mid=entry.url.match(/\/m\/([^/]+)\//)?.[1],targetsValid=actionTargets(entry.url).every(target=>fresh.states[target]?.state==='enabled'&&fresh.states[target]?.generation===entry.body.target_generations?.[target]);
        let valid=entry.user===user&&entry.household===household&&entry.format===1&&mid&&fresh.states[mid]?.state==='enabled'&&fresh.states[mid]?.generation===entry.body.generation&&targetsValid;
        let problem='Konto, Modul oder Datenstand geändert. Bitte verwerfen und neu erfassen.';
        if(valid){try{await describeDraft(entry,fresh,cache,article);}catch(error){valid=false;problem=error.message;}}
        if(valid&&financialMutation(entry.url)){
          valid=false;problem='Älterer lokaler Finanzentwurf: automatische Übertragung wird nicht mehr unterstützt. Werte prüfen und im gemeinsamen Editor ausdrücklich erfassen oder diesen Entwurf verwerfen.';
          const recovery=financialRecovery(entry.url),open=document.createElement('a');open.className='button';open.href=recovery.url;open.target='_blank';open.rel='noopener';open.textContent=recovery.label;actions.append(open);
        }
        if(valid){const send=document.createElement('button');send.className='primary';send.textContent='Geprüft übertragen';send.addEventListener('click',async()=>{send.disabled=true;try{await request(entry.url,entry.method,entry.body,entry.key);await queueOp('readwrite',store=>store.delete(entry.key));article.remove();toast('Änderung übernommen.');}catch(error){toast(error.message+' · Eintrag bleibt zur Prüfung erhalten.');send.disabled=false;}});actions.append(send);}
        else{const p=document.createElement('p');p.textContent=problem;article.append(p);}
        article.append(actions);box.append(article);
      }
    }catch(error){box.textContent=error.message;}
  });
  // Explicit recipe drafts share logout/account isolation with the offline queue.
  function clearRecipeDrafts(all=false){try{for(const key of Object.keys(localStorage)){if(!key.startsWith('household-recipe-draft:'))continue;let draft;try{draft=JSON.parse(localStorage.getItem(key));}catch{}if(all||!draft||(user&&(draft.owner!==user||draft.household!==document.body.dataset.household))||!Number.isFinite(draft.expires)||draft.expires<=Date.now())localStorage.removeItem(key);}for(const key of Object.keys(sessionStorage)){if(key==='household-recipe-substitution-v1'){if(all)sessionStorage.removeItem(key);continue;}if(!key.startsWith('household-pantry-pending:'))continue;let locator;try{locator=JSON.parse(sessionStorage.getItem(key));}catch{}if(all||!locator||user&&(locator.user!==user||locator.household!==household))sessionStorage.removeItem(key);}}catch{}}
  clearRecipeDrafts();
  $('#logout-form')?.addEventListener('submit',async event=>{event.preventDefault();clearRecipeDrafts(true);try{localStorage.removeItem('household-offline-owner');}catch{}try{await clearPrivateDeviceData(true);}catch{try{sessionStorage.setItem('household-device-cleanup-warning','1');}catch{}}event.target.submit();});
  if(user){try{const owner=localStorage.getItem('household-offline-owner');if(owner&&owner!==user){clearPrivateDeviceData(true).catch(error=>toast(error.message));localStorage.removeItem('household-offline-owner');}if(location.pathname==='/change-password')window.HouseholdRecipeDevice?.clear().catch(error=>toast(error.message));for(const device of [window.HouseholdPantryDevice,window.HouseholdShoppingDevice,window.HouseholdPushDevice,location.pathname==='/change-password'?null:window.HouseholdRecipeDevice])device?.observe(user,household).catch(error=>toast(error.message));}catch{}}
  if($('#queue-count'))queueOp('readonly',store=>store.count()).then(count=>$('#queue-count').textContent=count?`${count} Änderungen warten auf Prüfung nach der Anmeldung.`:'').catch(()=>{});
  const attachmentList=$('[data-attachments]');
  if(attachmentList)fetch(attachmentList.dataset.attachments,{headers:{Accept:'application/json'}}).then(r=>r.ok?r.json():[]).then(rows=>{for(const row of rows){const li=document.createElement('li');const a=document.createElement('a');a.href='/attachments/'+row.id;a.textContent=row.name;a.target='_blank';a.rel='noopener';if(row.preview){const img=document.createElement('img');img.src='/attachments/'+row.id+'/preview';img.alt='';img.width=96;img.height=96;img.loading='lazy';a.prepend(img);}li.append(a);attachmentList.append(li);}}).catch(()=>{});
  all('.ingredient-selection').forEach(group=>{const form=group.closest('form');const input=form.elements.servings;const update=()=>{const factor=Number(String(input.value).replace(',','.'))/Number(group.dataset.baseServings);all('.ingredient-choice',group).forEach(row=>{const label=$('[data-quantity]',row);if(!label?.dataset.quantity)return;const select=$('select',row);const from=label.dataset.unit,to=select?.value||from;const units={g:1,kg:1000,ml:1,l:1000,tsp:5,tbsp:15,piece:1};const quantity=Number(label.dataset.quantity)*factor*(units[from]||1)/(units[to]||1);label.textContent=Number.isFinite(quantity)?new Intl.NumberFormat('de-AT',{maximumFractionDigits:6}).format(quantity)+' '+to:'Menge prüfen';});};input.addEventListener('input',update);group.addEventListener('change',update);update();});
  all('[data-timer]').forEach(button=>button.addEventListener('click',()=>{if(button.dataset.running)return;button.dataset.running='true';const until=Date.now()+Number(button.dataset.timer)*1000;const refresh=()=>{const seconds=Math.max(0,Math.ceil((until-Date.now())/1000));button.textContent=seconds?`${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')} verbleibend`:'Timer fertig · neu starten';if(!seconds){clearInterval(tick);delete button.dataset.running;toast('Koch-Timer ist abgelaufen.');}};const tick=setInterval(refresh,1000);refresh();}));
  const report=$('#report');
  if(report)fetch(report.dataset.report,{headers:{Accept:'application/json'}}).then(async response=>{const data=await response.json();if(!response.ok)throw new Error(data.error||'Auswertung konnte nicht geladen werden.');return data;}).then(data=>{
    report.replaceChildren();
    const meterReport=report.dataset.report.includes('/meters/');
    const sections={months:{title:'Monatsausgaben',columns:['month','amount_minor']},products:{title:'Bestände',columns:['name','stock','unit','minimum']},fillups:{title:'Tankverlauf',columns:['vehicle_name','series_label','date','odometer','quantity','fuel_type','full','missing','total_minor']},consumption:{title:'Verbrauch',columns:meterReport?['name','device_label','from','to','consumption','unit','rollover_inferred']:['vehicle_name','series_label','from_date','to_date','distance','quantity','per_100km','unit']}};
    const labels={month:'Monat',amount_minor:'Betrag',name:'Name',device_label:'Gerät / Seriennummer',vehicle_name:'Fahrzeug',series_label:'Kilometerzähler',from:'Von',to:'Bis',from_date:'Von',to_date:'Bis',unit:'Einheit',stock:'Bestand',minimum:'Mindestbestand',date:'Datum',per_100km:'Verbrauch / 100 km',distance:'Strecke (km)',quantity:'Menge',consumption:'Verbrauch',rollover_inferred:'Überlauf abgeleitet',odometer:'Kilometerstand',total_minor:'Gesamtpreis',full:'Vollgetankt',missing:'Fehlende Tankung',fuel_type:'Kraftstoff'};
    for(const [name,section]of Object.entries(sections)){
      const rows=data[name];if(!Array.isArray(rows)||!rows.length)continue;
      const title=document.createElement('h2');title.textContent=section.title;
      const wrap=document.createElement('div');wrap.className='table-scroll';wrap.tabIndex=0;wrap.setAttribute('role','region');wrap.setAttribute('aria-label',section.title+' · Tabelle horizontal verschiebbar');
      const table=document.createElement('table'),thead=document.createElement('thead'),head=document.createElement('tr');
      for(const key of section.columns){const th=document.createElement('th');th.scope='col';th.textContent=labels[key];head.append(th);}thead.append(head);table.append(thead);
      const body=document.createElement('tbody');
      for(const row of rows){const tr=document.createElement('tr');for(const key of section.columns){const td=document.createElement('td');let value=row[key];if(value!==null&&value!==undefined){if(key.endsWith('_minor'))value=new Intl.NumberFormat('de-AT',{style:'currency',currency:'EUR'}).format(Number(value)/100);else if(['from','to','from_date','to_date','date'].includes(key))value=localDate(value,meterReport&&['from','to'].includes(key));else if(typeof value==='boolean')value=value?'Ja':'Nein';else if(['stock','minimum','distance','quantity','per_100km','consumption','odometer'].includes(key))value=String(value).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');else value=choiceText(value);}td.textContent=value??'–';tr.append(td);}body.append(tr);}table.append(body);wrap.append(table);report.append(title,wrap);
    }
    if(!report.children.length)report.textContent='Noch keine auswertbaren Daten.';
  }).catch(error=>report.textContent=error.message);
  addEventListener('beforeinstallprompt',event=>{event.preventDefault();deferredInstall=event;$('#install')?.removeAttribute('hidden');});
  $('#install')?.addEventListener('click',async()=>{await deferredInstall?.prompt();deferredInstall=null;$('#install').hidden=true;});
  if('serviceWorker'in navigator&&window.isSecureContext){navigator.serviceWorker.register('/sw.js',{scope:'/'}).then(registration=>{worker=registration.waiting;if(worker)$('#update').hidden=false;registration.addEventListener('updatefound',()=>{const installing=registration.installing;installing.addEventListener('statechange',()=>{if(installing.state==='installed'&&navigator.serviceWorker.controller){worker=registration.waiting;$('#update').hidden=false;}});});}).catch(()=>{});}
  $('#apply-update')?.addEventListener('click',()=>{worker?.postMessage({type:'ACTIVATE'});navigator.serviceWorker.addEventListener('controllerchange',()=>location.reload(),{once:true});});
})();
