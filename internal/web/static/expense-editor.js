'use strict';
(() => {
  const root = document.getElementById('expense-editor');
  if (!root) return;
  const $ = id => document.getElementById(id), values = HouseholdExpenseValues;
  const form = $('expense-form'), fields = $('expense-fields'), picker = $('expense-category-dialog');
  const controls = {description:$('expense-description'), occurred_on:$('expense-date'), kind:$('expense-kind'), amount_eur:$('expense-amount'), payee:$('expense-payee'), paid_by_membership_id:$('expense-payer')};
  const state = {bundle:null, lines:[], removed:[], editable:false, busy:false, dirty:false, pending:null, review:null, fresh:null, categoryKey:null, returnFocus:null};
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(value);
  const node = (tag, text, className) => {const result=document.createElement(tag); if(text!==undefined)result.textContent=text; if(className)result.className=className; return result;};
  const money = minor => minor === null || minor === undefined ? 'Noch offen' : (values.minorText(String(minor)) ?? 'Nicht darstellbar') + ' €';
  const canEdit = () => state.editable && !state.busy && !state.pending && !state.review;
  function button(label, action) {const result=node('button',label);result.type='button';result.addEventListener('click',action);return result;}
  function status(text) {$('expense-status').textContent=text;$('expense-status').hidden=false;}
  function clearError() {$('expense-error').hidden=true;$('expense-error-fields').replaceChildren();root.querySelectorAll('[aria-invalid="true"]').forEach(input=>input.removeAttribute('aria-invalid'));}
  function errorTarget(path) {
    const header={'header.description':'description','header.occurred_on':'occurred_on','header.kind':'kind','header.amount':'amount_eur','header.payee':'payee','header.paid_by':'paid_by_membership_id'};
    if(header[path])return controls[header[path]];
    const match=path.match(/^lines\.(\d+)\.(amount|note|category)$/);
    return match ? $(`expense-line-${state.lines[Number(match[1])]?.key}-${match[2]}`) : null;
  }
  function showError(message, problems={}) {
    clearError();$('expense-error-text').textContent=message;
    for(const [path,text] of Object.entries(problems)) {
      const item=node('li'), target=errorTarget(path);
      if(target){target.setAttribute('aria-invalid','true');const link=node('a',text);link.href='#'+target.id;link.addEventListener('click',event=>{event.preventDefault();target.focus();});item.append(link);}else item.textContent=text;
      $('expense-error-fields').append(item);
    }
    $('expense-error').hidden=false;$('expense-error').focus();
  }
  async function request(url, options={}) {
    let response;
    try {response=await fetch(url,{cache:'no-store',credentials:'same-origin',...options,headers:{Accept:'application/json',...options.headers}});}
    catch {const error=new Error('Keine sichere Verbindung zum Haushalt. Bitte Verbindung prüfen.');error.status=0;throw error;}
    let result;
    try {result=await response.json();} catch {const error=new Error('Serverantwort konnte nicht sicher gelesen werden.');error.status=response.ok?0:response.status;throw error;}
    if(!response.ok){const error=new Error(result.error||'Vorgang fehlgeschlagen.');error.status=response.status;error.fields=result.fields||{};throw error;}
    return result;
  }
  async function actor(writing=false) {
    const current=await request('/api/v1/core/bootstrap');
    if(current.user?.id!==document.body.dataset.user || current.user?.household_id!==document.body.dataset.household) {
      const error=new Error('Das angemeldete Konto hat gewechselt. Seite nach Prüfung deiner offenen Eingaben neu öffnen.');error.status=403;throw error;
    }
    if(writing&&!['admin','editor'].includes(current.user.role)){const error=new Error('Du hast derzeit nur Lesezugriff. Deine Eingaben bleiben erhalten.');error.status=403;throw error;}
    if(typeof current.csrf!=='string')throw new Error('Anmeldung konnte nicht vollständig geprüft werden.');
    return current.csrf;
  }
  async function readBundle(entry) {
    await actor();
    const bundle=await request('/api/v1/expenses/editor'+(entry?'?entry='+encodeURIComponent(entry):''));
    if(!bundle?.header || !Array.isArray(bundle.lines) || !Array.isArray(bundle.baseline) || bundle.lines.length>100 || entry&&bundle.header.id!==entry)throw new Error('Vollständige Buchung konnte nicht gelesen werden.');
    if(!values.safeVersions(bundle)){bundle.editable=false;bundle.lock_reason='Gespeicherte Versionswerte liegen außerhalb der sicheren Browsergrenzen. Einzelverwaltung oder Export verwenden.';}
    return bundle;
  }
  function inactiveSelections() {
    return state.lines.some(line=>line.archived||line.missing) || Boolean(controls.paid_by_membership_id.value && controls.paid_by_membership_id.value===state.bundle?.header.paid_by_membership_id && !state.bundle.header.payer_active);
  }
  function totals() {return values.totals(controls.amount_eur.value,state.lines.map(line=>line.amount_eur),controls.kind.value);}
  function availability() {
    fields.disabled=!canEdit();
    $('expense-add-line').disabled=state.lines.length>=100;
    $('expense-review').disabled=!totals().canPost||inactiveSelections();
    $('expense-post-confirm').disabled=state.busy||Boolean(state.pending);
    $('expense-post-cancel').disabled=state.busy||Boolean(state.pending);
    $('expense-retry').disabled=state.busy;
    root.setAttribute('aria-busy',String(state.busy));
  }
  function updateTotals() {
    const current=totals();
    $('expense-total-value').textContent=money(current.total);$('expense-sum-value').textContent=money(current.sum);$('expense-rest-value').textContent=money(current.rest);
    $('expense-rest-status').textContent=!current.validHeader?'Positiven Ausgabenbetrag oder negativen Erstattungsbetrag eintragen.':!current.validLines?'Alle Teilbeträge als exakte, von null verschiedene Beträge prüfen.':inactiveSelections()?'Inaktive Zahlende oder archivierte Kategorien vor dem Buchen ausdrücklich ersetzen.':current.canPost?'Vollständig aufgeteilt. Du kannst die Buchung jetzt prüfen.':'Der Entwurf darf diese Restdifferenz behalten. Verbindliches Buchen benötigt mindestens eine Zeile und exakt 0,00 € Rest.';
    availability();
  }
  function title() {
    const text=!state.editable?'Buchung und Aufteilung':controls.kind.value==='refund'?'Erstattung aufteilen':state.bundle?.header.id?'Ausgabe aufteilen':'Neue Ausgabe aufteilen';
    root.querySelector('h1').textContent=text;document.title=text+' · Household';
  }
  function dirty() {state.dirty=true;$('expense-saved-state').textContent='Ungespeicherte Eingaben';title();updateTotals();}
  function lineInput(line, name, label) {
    const wrapper=node('label',label), input=node('input');input.type='text';input.id=`expense-line-${line.key}-${name==='amount_eur'?'amount':name}`;
    input.value=line[name]||'';input.maxLength=name==='note'?300:32;input.autocomplete='off';
    if(name==='amount_eur'){input.inputMode='decimal';input.required=true;}
    input.addEventListener('input',()=>{line[name]=input.value;dirty();});wrapper.append(input);return wrapper;
  }
  function chooseCategory(key, trigger) {
    if(!canEdit())return;state.categoryKey=key;state.returnFocus=trigger;picker.showModal();$('expense-category-choice').focus();
  }
  function renderLines(focusID) {
    const list=$('expense-lines');list.replaceChildren();
    state.lines.forEach((line,index)=>{
      const row=node('fieldset',undefined,'expense-line');row.dataset.lineKey=line.key;
      const legend=node('legend',`Kategoriezeile ${index+1}`,'sr-only');row.append(legend);
      const heading=node('div',undefined,'expense-line-heading'), title=node('h3',`${index+1}. ${line.label}${line.archived?' · archiviert':''}${line.missing?' · nicht verfügbar':''}`);
      title.id=`expense-line-${line.key}-title`;heading.append(title);
      const category=button('Kategorie ändern',()=>chooseCategory(line.key,category));category.id=`expense-line-${line.key}-category`;category.setAttribute('aria-label',`Kategorie von Zeile ${index+1} ändern`);heading.append(category);row.append(heading);
      const grid=node('div',undefined,'form-grid');grid.append(lineInput(line,'amount_eur','Teilbetrag in EUR'),lineInput(line,'note','Bezeichnung / Notiz'));row.append(grid);
      const actions=node('div',undefined,'actions');
      const rest=button('Rest hier zuordnen',()=>{
        if(!canEdit())return;
        const other=values.totals(controls.amount_eur.value,state.lines.filter(item=>item!==line).map(item=>item.amount_eur),controls.kind.value);
        if(!other.validHeader||!other.validLines||other.rest===0n||values.euroMinor(values.minorText(String(other.rest)))===null){showError('Den Gesamtbetrag und die übrigen Teilbeträge prüfen. Der verbleibende Teil muss von null verschieden sein und in eine Zeile passen.');return;}
        line.amount_eur=values.minorText(String(other.rest));$(`expense-line-${line.key}-amount`).value=line.amount_eur;dirty();$(`expense-line-${line.key}-amount`).focus();
      });
      const move=direction=>{if(!canEdit())return;const next=index+direction;if(next<0||next>=state.lines.length)return;[state.lines[index],state.lines[next]]=[state.lines[next],state.lines[index]];dirty();renderLines(`expense-line-${line.key}-amount`);};
      const up=button('Nach oben',()=>move(-1)), down=button('Nach unten',()=>move(1));up.disabled=index===0;down.disabled=index===state.lines.length-1;
      const remove=button('Entfernen',()=>{if(!canEdit())return;state.lines.splice(index,1);state.removed.push({...line,index});dirty();renderLines(state.lines[index]?`expense-line-${state.lines[index].key}-amount`:'expense-add-line');});
      for(const action of [rest,up,down,remove])action.setAttribute('aria-label',`${action.textContent} · Zeile ${index+1}`);
      actions.append(rest,up,down,remove);row.append(actions);list.append(row);
    });
    $('expense-lines-empty').hidden=state.lines.length!==0;$('expense-removed').hidden=state.removed.length===0;$('expense-removed-lines').replaceChildren();
    state.removed.forEach((line,index)=>{
      const item=node('li',`${line.label} · ${line.amount_eur||'Teilbetrag offen'}`), undo=button('Entfernung zurücknehmen',()=>{
        if(!canEdit()||state.lines.length>=100)return;state.removed.splice(index,1);state.lines.splice(Math.min(line.index,state.lines.length),0,line);dirty();renderLines(`expense-line-${line.key}-amount`);
      });undo.disabled=state.lines.length>=100;item.append(undo);$('expense-removed-lines').append(item);
    });
    updateTotals();if(focusID)$(focusID)?.focus();
  }
  function facts(bundle, container) {
    container.replaceChildren();const h=bundle.header, details=node('dl',undefined,'expense-facts');
    const rows=[['Beschreibung',h.description||'Noch offen'],['Datum',h.occurred_on],['Art',h.kind==='refund'?'Erstattung':h.kind==='reversal'?'Storno':'Ausgabe'],['Gesamtbetrag',h.amount_minor?money(h.amount_minor):'Noch offen'],['Empfänger / Geschäft',h.payee||'Nicht angegeben'],['Bezahlt von',h.payer_label?(h.payer_label+(h.payer_active?'':' · inaktiv')):'Nicht bekannt / offen']];
    for(const [label,text] of rows)details.append(node('dt',label),node('dd',text));container.append(details);
    const lines=node('ul',undefined,'expense-review-lines');
    for(const line of bundle.lines)lines.append(node('li',`${line.category_label}${line.category_archived?' · archiviert':''} · ${money(line.amount_minor)}${line.note?'\n'+line.note:''}`));
    container.append(node('h3','Aufteilung'),lines);if(!bundle.lines.length)container.append(node('p','Noch keine Kategoriezeilen.'));
  }
  function applyBundle(bundle) {
    state.bundle=bundle;state.editable=Boolean(bundle.editable);state.removed=[];state.fresh=null;state.review=null;state.dirty=false;
    const h=bundle.header;
    state.lines=bundle.lines.map(line=>({key:line.id,id:line.id,version:line.version,category_id:line.category_id,label:line.category_label,archived:line.category_archived,missing:line.category_missing,amount_eur:values.minorText(line.amount_minor)||'',note:line.note}));
    for(const name of ['description','occurred_on','kind','payee'])controls[name].value=h[name]||'';
    controls.amount_eur.value=h.amount_minor?values.minorText(h.amount_minor)||'':'';
    const payer=controls.paid_by_membership_id;
    if(h.paid_by_membership_id&&!Array.from(payer.options).some(option=>option.value===h.paid_by_membership_id)){
      const option=node('option',h.payer_label||'Gespeicherte Mitgliedschaft');option.value=h.paid_by_membership_id;payer.append(option);
    }
    payer.value=h.paid_by_membership_id||'';
    const selected=payer.selectedOptions[0];if(h.paid_by_membership_id&&selected)selected.textContent=(h.payer_label||'Gespeicherte Mitgliedschaft')+(h.payer_active?'':' · inaktiv');
    $('expense-form').hidden=!state.editable;$('expense-readonly').hidden=state.editable;
    if(!state.editable){$('expense-readonly-reason').textContent=bundle.lock_reason||'Diese Buchung ist schreibgeschützt.';facts(bundle,$('expense-readonly-content'));}
    $('expense-saved-state').textContent=!h.id?'Neuer, noch ungespeicherter Entwurf':h.state==='posted'?'Gebucht · unveränderlich':'Gespeicherter Entwurf';
    $('expense-detail-link').hidden=!h.id;if(h.id)$('expense-detail-link').href='/m/expenses/entries/'+h.id;
    root.dataset.entry=h.id||'';$('expense-conflict').hidden=true;$('expense-post-review').hidden=true;
    if(h.id){const url=new URL(location.href);url.searchParams.set('entry',h.id);history.replaceState(null,'',url);}
    title();renderLines();
  }
  function input(action) {
    if(!state.bundle)throw new Error('Aktuellen Buchungsstand zuerst laden.');
    if(controls.paid_by_membership_id.value&&controls.paid_by_membership_id.dataset.referenceState!=='ready')throw new Error('Die Auswahl der Zahlenden zuerst vollständig laden oder erneut versuchen.');
    const h=state.bundle.header;
    return {entry_id:h.id,generation:state.bundle.generation,version:h.version,action,
      header:Object.fromEntries(Object.entries(controls).map(([name,control])=>[name,control.value])),
      lines:state.lines.map(line=>({key:line.key,id:line.id||'',version:line.version||0,category_id:line.category_id,amount_eur:line.amount_eur,note:line.note})),
      baseline:state.bundle.baseline.map(line=>({...line})),removed:state.removed.filter(line=>line.id).map(line=>({id:line.id,version:line.version}))};
  }
  async function showConflict() {
    if(!state.bundle.header.id)return;
    try {
      state.fresh=await readBundle(state.bundle.header.id);
      const box=$('expense-conflict-content');facts(state.fresh,box);
      $('expense-conflict').hidden=false;$('expense-conflict').focus();
    }catch(error){showError('Deine Eingaben bleiben erhalten. Der aktuelle Stand konnte nicht zum Vergleich geladen werden: '+error.message);}
  }
  function pendingNotice(text) {$('expense-pending-text').textContent=text;$('expense-pending').hidden=false;$('expense-pending').focus();availability();}
  async function resolveAccepted() {
    const accepted=state.pending.receipt;
    try {
      const bundle=await readBundle(accepted.entry_id);
      state.pending=null;applyBundle(bundle);$('expense-pending').hidden=true;clearError();
      status((accepted.action==='post'?'Buchung bestätigt.':'Entwurf gespeichert.')+' Der aktuelle vollständige Stand ist geladen.');
      $('expense-status').tabIndex=-1;$('expense-status').focus();
    }catch(error){pendingNotice('Speichern ist bestätigt. Die aktuelle Buchung konnte noch nicht geladen werden. Deine Eingaben bleiben gesperrt, bis der aktuelle Stand geprüft ist. '+error.message);}
  }
  async function sendPending() {
    if(!state.pending||state.busy)return;
    state.busy=true;availability();
    try {
      if(state.pending.receipt){await resolveAccepted();return;}
      const csrf=await actor(true);
      const receipt=await request('/api/v1/expenses/editor',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':state.pending.key},body:state.pending.body});
      if(!uuid(receipt?.entry_id)||!uuid(receipt?.id)||!['save','post'].includes(receipt.action))throw new Error('Vorgangsbestätigung konnte nicht sicher gelesen werden.');
      state.pending.receipt=receipt;await resolveAccepted();
    }catch(error){
      if(!error.status||error.status>=500||state.pending?.uncertain){
        if(state.pending)state.pending.uncertain=true;
        pendingNotice('Der Vorgang könnte bereits gespeichert sein. Eingaben bleiben unverändert gesperrt. Prüfe denselben Vorgang erneut, sobald Verbindung und Anmeldung funktionieren; schließe diesen Tab bis dahin nicht. '+error.message);
      }else{
        state.pending=null;state.review=null;$('expense-post-review').hidden=true;$('expense-pending').hidden=true;
        showError(error.message,error.fields);if(error.status===409)await showConflict();
      }
    }finally{state.busy=false;availability();}
  }
  function begin(body) {state.pending={key:crypto.randomUUID(),body:JSON.stringify(body),receipt:null,uncertain:false};clearError();void sendPending();}
  form.addEventListener('submit',event=>{
    event.preventDefault();if(!canEdit()||!form.reportValidity())return;
    try {begin(input('save'));}catch(error){showError(error.message);}
  });
  for(const control of Object.values(controls))control.addEventListener('input',dirty);
  $('expense-add-line').addEventListener('click',()=>chooseCategory(null,$('expense-add-line')));
  $('expense-category-cancel').addEventListener('click',()=>picker.close());
  picker.addEventListener('close',()=>{const focus=state.returnFocus;state.returnFocus=null;if(focus?.isConnected)focus.focus();});
  $('expense-category-form').addEventListener('submit',event=>{
    event.preventDefault();const choice=$('expense-category-choice');
    if(!canEdit()||choice.dataset.referenceState!=='ready'||!uuid(choice.value)||!event.currentTarget.reportValidity())return;
    let line=state.lines.find(line=>line.key===state.categoryKey);
    if(!line){if(state.lines.length>=100)return;line={key:crypto.randomUUID(),id:'',version:0,amount_eur:'',note:''};state.lines.push(line);}
    line.category_id=choice.value;line.label=choice.selectedOptions[0].textContent;line.archived=false;line.missing=false;
    state.returnFocus=null;picker.close();dirty();renderLines(`expense-line-${line.key}-amount`);
  });
  $('expense-review').addEventListener('click',()=>{
    if(!canEdit()||!totals().canPost||inactiveSelections()||!form.reportValidity())return;
    try {
      state.review=input('post');clearError();
      const review=$('expense-post-content');review.replaceChildren(node('p',`${state.review.header.description} · ${state.review.header.occurred_on} · ${money(totals().total)}`));
      const list=node('ul',undefined,'expense-review-lines');for(const line of state.lines)list.append(node('li',`${line.label} · ${money(values.euroMinor(line.amount_eur))}${line.note?'\n'+line.note:''}`));review.append(list);
      $('expense-post-review').hidden=false;availability();$('expense-post-review').focus();
    }catch(error){showError(error.message);}
  });
  $('expense-post-cancel').addEventListener('click',()=>{if(state.busy||state.pending)return;state.review=null;$('expense-post-review').hidden=true;availability();$('expense-review').focus();});
  $('expense-post-confirm').addEventListener('click',()=>{if(state.review&&!state.pending&&!state.busy)begin(state.review);});
  $('expense-retry').addEventListener('click',()=>void sendPending());
  $('expense-reload-current').addEventListener('click',()=>{
    if(!state.fresh||state.pending||state.busy)return;const fresh=state.fresh;applyBundle(fresh);clearError();status('Aktuellen Stand übernommen. Eigene ungespeicherte Eingaben wurden ausdrücklich verworfen.');
  });
  addEventListener('beforeunload',event=>{if(state.dirty||state.pending){event.preventDefault();event.returnValue='';}});
  async function load() {
    state.busy=true;availability();
    try {const bundle=await readBundle(root.dataset.entry);applyBundle(bundle);status(bundle.editable?'Buchung geladen. Entwurf und Aufteilung werden gemeinsam gespeichert.':'Gespeicherte Buchung geladen.');}
    catch(error){showError(error.message);status('Buchung konnte nicht vollständig geladen werden. Eingaben bleiben gesperrt.');}
    finally{state.busy=false;availability();}
  }
  void load();
})();
