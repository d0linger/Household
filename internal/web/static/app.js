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
  function connection() {const box = $('#connection'); box.hidden = navigator.onLine; box.textContent = 'Offline · Änderungen erst nach erneuter Verbindung und Prüfung übertragen.';}
  addEventListener('online', connection); addEventListener('offline', connection); connection();
  try {const theme = localStorage.getItem('household-theme'); if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme;} catch {}
  const nav = $('.mobile-nav');
  if (nav) {const links = all('a', nav); if (links.length > 5) {links.slice(4).forEach(a => a.hidden = true); $('.more-button', nav).hidden = false;} if (!links.length) nav.hidden = true;}
  all('[data-open]').forEach(button => button.addEventListener('click', () => {const dialog = document.getElementById(button.dataset.open); dialog.showModal(); $('input:not([type=hidden]),select,textarea', dialog)?.focus();}));
  all('[data-close]').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
  all('[data-back]').forEach(button => button.addEventListener('click', () => history.length > 1 ? history.back() : location.assign('/')));
  all('[data-time]').forEach(input => {if (input.value && input.value.includes('Z')) {const d = new Date(input.value); if (!isNaN(d)) {d.setMinutes(d.getMinutes() - d.getTimezoneOffset()); input.value = d.toISOString().slice(0,16);}}});
  all('[data-reference]').forEach(async select => {
    try {const response = await fetch('/api/v1/core/references/' + select.dataset.reference, {headers:{Accept:'application/json'}}); if (!response.ok) {select.disabled = true; select.closest('form').querySelector('button[type=submit],button:not([type])')?.setAttribute('disabled',''); return;} for (const row of await response.json()) {const option = document.createElement('option'); option.value = row.id; option.textContent = row.label || row.id; select.append(option);}}
    catch {select.disabled = true;}
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
  async function queueMutation(url, method, body, key) {
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
  function actionTargets(url){const match=url.match(/\/m\/([^/]+)\/([^/]+)\/[^/]+\/actions\/([^/]+)$/);if(!match)return [];return ({'recipes/recipes/shopping':['shopping'],'pantry/products/shopping':['shopping'],'fuel/fillups/expense':['expenses'],'mealplan/meals/recipe':['recipes'],'mealplan/plans/shopping':['recipes','shopping']})[match.slice(1).join('/')]||[];}
  all('[data-record-form]').forEach(form => form.addEventListener('submit', async event => {
    event.preventDefault(); if (!form.reportValidity()) return;
    const button = $('button:not([type=button])',form); const errorBox = $('.form-error',form); if (errorBox) errorBox.hidden = true;
    const body = formBody(form);const key = form.elements.idempotency_key.value || crypto.randomUUID();const method = form.dataset.method || 'POST';
    if (button) button.disabled = true;
    try {const result=await request(form.getAttribute('action'),method,body,key);if(result.url)location.assign(result.url);else location.reload();}
    catch(error) {
      if (!error.status && !navigator.onLine) {try {await queueMutation(form.getAttribute('action'),method,body,key);form.closest('dialog')?.close();}catch(queueError){toast(queueError.message);}}
      else {if(errorBox){errorBox.textContent=error.message;errorBox.hidden=false;errorBox.focus();}else toast(error.message);}
    } finally {if(button) button.disabled=false;}
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
      try{const response=await fetch('/api/v1/core/preferences',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({theme,offline})});if(!response.ok)throw new Error('Einstellungen konnten nicht gespeichert werden.');localStorage.setItem('household-theme',theme);if(theme==='system')delete document.documentElement.dataset.theme;else document.documentElement.dataset.theme=theme;if(offline)localStorage.setItem('household-offline-owner',user);else{localStorage.removeItem('household-offline-owner');await clearQueue();}toast('Geräteeinstellungen gespeichert.');}catch(error){toast(error.message);}
    });
  }
  const choiceText=value=>({piece:'Stück',diesel:'Diesel',petrol:'Benzin',lpg:'LPG',cng:'CNG',receive:'Zugang',consume:'Verbrauch',waste:'Entsorgung',adjust:'Korrektur',expense:'Ausgabe',refund:'Erstattung',dinner:'Abendessen',lunch:'Mittagessen',breakfast:'Frühstück',snack:'Zwischenmahlzeit',actual:'Abgelesen',estimated:'Geschätzt'})[value]||String(value);
  function localDate(value,time=false){const date=new Date(value);return isNaN(date)?String(value):new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',...(time?{timeStyle:'short'}:{}),timeZone:'Europe/Vienna'}).format(date);}
  async function describeDraft(entry,fresh,cache,article){
    const match=entry.url.match(/^\/api\/v1\/m\/([^/]+)\/([^/]+)(?:\/([0-9a-f-]{36}))?(?:\/actions\/([^/]+))?$/);
    if(!match)throw new Error('Diesen Vorgang bitte neu öffnen und erfassen.');
    const [,mid,entityID,rid,action]=match;
    const mod=fresh.modules.find(m=>m.id===mid),entity=mod?.entities.find(e=>e.id===entityID);
    if(!entity)throw new Error('Dieser Bereich ist nicht mehr verfügbar.');
    const read=async url=>{if(!cache.has(url))cache.set(url,fetch(url,{headers:{Accept:'application/json'}}).then(async response=>{if(!response.ok)throw new Error('Eine Zuordnung ist nicht mehr verfügbar. Bitte neu öffnen und erfassen.');return response.json();}));return cache.get(url);};
    const current=rid?await read('/api/v1/m/'+mid+'/'+entityID+'/'+rid):{};
    const operations={check:entry.body.data.checked?'Artikel erledigen':'Artikel wieder öffnen',archive:entry.body.data.archived===true||entry.body.data.archived==='true'?'Liste archivieren':'Liste wieder öffnen',post:'Ausgabe verbindlich buchen',reverse:'Ausgleich erstellen',complete:'Aufgabe erledigen',expense:'Tankung als Ausgabe übernehmen',recipe:'Rezept verknüpfen',tags:'Tags zuordnen',transfer:'Bestand umlagern',shopping:mid==='pantry'?'Vorrat zum Einkauf ergänzen':'Zutaten zur Einkaufsliste übernehmen'};
    const title=document.createElement('h3');title.textContent=mod.title+' · '+(operations[action]||(entry.method==='PATCH'?'Eintrag ändern':'Eintrag anlegen'));
    const name=document.createElement('p');name.textContent=current[entity.primary]||entry.body.data[entity.primary]||entity.title;
    article.append(title,name);
    const extra={list_id:{label:'Einkaufsliste',reference:'shopping/lists'},category_id:{label:'Kategorie',reference:'expenses/categories'},recipe_id:{label:'Rezept',reference:'recipes/recipes'},location_id:{label:'Ziel-Lagerort',reference:'pantry/locations'},ingredient_ids:{label:'Ausgewählte Zutaten',reference:'recipes/ingredients'},tag_ids:{label:'Tags',reference:'recipes/tags'},servings:{label:'Portionen',kind:'quantity'},checked:{label:'Erledigt',kind:'boolean'},archived:{label:'Archiviert',kind:'boolean'},unit_overrides:{label:'Einheiten'}};
    const namedReference=async(reference,value)=>{
      const [referenceModule,referenceEntity]=reference.split('/');
      const target=fresh.modules.find(m=>m.id===referenceModule)?.entities.find(e=>e.id===referenceEntity);
      const rows=await read('/api/v1/core/references/'+reference);
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
      const fresh=await bootstrap.json(),entries=await queueOp('readonly',store=>store.getAll()),cache=new Map();box.replaceChildren();
      if(!entries.length){box.textContent='Keine wartenden Änderungen.';return;}
      for(const entry of entries){
        const article=document.createElement('article');article.className='session-row queue-entry';
        const actions=document.createElement('div');actions.className='actions';
        const discard=document.createElement('button');discard.textContent='Verwerfen';discard.addEventListener('click',async()=>{await queueOp('readwrite',store=>store.delete(entry.key));article.remove();});actions.append(discard);
        const mid=entry.url.match(/\/m\/([^/]+)\//)?.[1],targetsValid=actionTargets(entry.url).every(target=>fresh.states[target]?.state==='enabled'&&fresh.states[target]?.generation===entry.body.target_generations?.[target]);
        let valid=entry.user===user&&entry.household===household&&entry.format===1&&mid&&fresh.states[mid]?.state==='enabled'&&fresh.states[mid]?.generation===entry.body.generation&&targetsValid;
        let problem='Konto, Modul oder Datenstand geändert. Bitte verwerfen und neu erfassen.';
        if(valid){try{await describeDraft(entry,fresh,cache,article);}catch(error){valid=false;problem=error.message;}}
        if(valid){const send=document.createElement('button');send.className='primary';send.textContent='Geprüft übertragen';send.addEventListener('click',async()=>{send.disabled=true;try{await request(entry.url,entry.method,entry.body,entry.key);await queueOp('readwrite',store=>store.delete(entry.key));article.remove();toast('Änderung übernommen.');}catch(error){toast(error.message+' · Eintrag bleibt zur Prüfung erhalten.');send.disabled=false;}});actions.append(send);}
        else{const p=document.createElement('p');p.textContent=problem;article.append(p);}
        article.append(actions);box.append(article);
      }
    }catch(error){box.textContent=error.message;}
  });
  $('#logout-form')?.addEventListener('submit',async event=>{event.preventDefault();try{await clearQueue();localStorage.removeItem('household-offline-owner');}catch{}event.target.submit();});
  if(user){try{const owner=localStorage.getItem('household-offline-owner');if(owner&&owner!==user){clearQueue().catch(()=>{});localStorage.removeItem('household-offline-owner');}}catch{}}
  if($('#queue-count'))queueOp('readonly',store=>store.count()).then(count=>$('#queue-count').textContent=count?`${count} Änderungen warten auf Prüfung nach der Anmeldung.`:'').catch(()=>{});
  const attachmentList=$('[data-attachments]');
  if(attachmentList)fetch(attachmentList.dataset.attachments,{headers:{Accept:'application/json'}}).then(r=>r.ok?r.json():[]).then(rows=>{for(const row of rows){const li=document.createElement('li');const a=document.createElement('a');a.href='/attachments/'+row.id;a.textContent=row.name;a.target='_blank';a.rel='noopener';if(row.preview){const img=document.createElement('img');img.src='/attachments/'+row.id+'/preview';img.alt='';img.width=96;img.height=96;img.loading='lazy';a.prepend(img);}li.append(a);attachmentList.append(li);}}).catch(()=>{});
  all('.ingredient-selection').forEach(group=>{const form=group.closest('form');const input=form.elements.servings;const update=()=>{const factor=Number(String(input.value).replace(',','.'))/Number(group.dataset.baseServings);all('.ingredient-choice',group).forEach(row=>{const label=$('[data-quantity]',row);if(!label?.dataset.quantity)return;const select=$('select',row);const from=label.dataset.unit,to=select?.value||from;const units={g:1,kg:1000,ml:1,l:1000,piece:1};const quantity=Number(label.dataset.quantity)*factor*(units[from]||1)/(units[to]||1);label.textContent=Number.isFinite(quantity)?new Intl.NumberFormat('de-AT',{maximumFractionDigits:6}).format(quantity)+' '+to:'Menge prüfen';});};input.addEventListener('input',update);group.addEventListener('change',update);update();});
  all('[data-timer]').forEach(button=>button.addEventListener('click',()=>{if(button.dataset.running)return;button.dataset.running='true';const until=Date.now()+Number(button.dataset.timer)*1000;const refresh=()=>{const seconds=Math.max(0,Math.ceil((until-Date.now())/1000));button.textContent=seconds?`${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')} verbleibend`:'Timer fertig · neu starten';if(!seconds){clearInterval(tick);delete button.dataset.running;toast('Koch-Timer ist abgelaufen.');}};const tick=setInterval(refresh,1000);refresh();}));
  const report=$('#report');
  if(report)fetch(report.dataset.report,{headers:{Accept:'application/json'}}).then(async response=>{if(!response.ok)throw new Error('Auswertung konnte nicht geladen werden.');return response.json();}).then(data=>{
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
