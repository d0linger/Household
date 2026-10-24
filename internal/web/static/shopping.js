(() => {
  'use strict';
  const board = document.querySelector('[data-shopping-list]');
  if (!board) return;
  const $ = selector => board.querySelector(selector);
  const listID = board.dataset.shoppingList, generation = Number(board.dataset.generation);
  const canWrite = board.dataset.canWrite === 'true';
  const container = $('#shopping-items'), message = $('#shopping-message'), conflict = $('#shopping-conflict');
  const form = $('#shopping-quick-add'), mode = $('#shopping-mode'), filter = $('#shopping-filter');
  const more = $('#shopping-more'), refresh = $('#shopping-refresh');
  const previous = document.createElement('button'); previous.type = 'button'; previous.textContent = 'Vorherige Artikel'; previous.hidden = true; more.before(previous);
  let records = [], page = null, cursor = '', nextCursor = '', history = [], busy = false, loading = false;
  let poll, delay = 15000, queryTimer, revision = 0, controller, previousSignature = '', undo, completedOpen = true, resetRequested = false;
  const node = (tag, text, className) => {const el = document.createElement(tag); if(text !== undefined) el.textContent = text; if(className) el.className = className; return el;};
  const unit = code => code === 'piece' ? 'Stück' : code || '';
  const money = raw => {const cents=BigInt(raw||'0');return new Intl.NumberFormat('de-AT').format(cents/100n)+','+String(cents%100n).padStart(2,'0')+' €';};
  const variant = product => product?[product.name,product.brand,quantity(product.quantity)+' '+unit(product.unit)].filter(Boolean).join(' · '):'';
  const quantity = raw => String(raw ?? '').replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '').replace('.', ',');
  const announce = text => {message.textContent = text;};
  const actionURL = row => '/api/v1/m/shopping/items/' + row.id + '/actions/check';
  function focusPosition() {
    const active=document.activeElement;
    if(active?.dataset.shoppingCheck)return {id:active.dataset.shoppingCheck,kind:'check'};
    if(active?.dataset.shoppingLink)return {id:active.dataset.shoppingLink,kind:'link'};
    if(active?.hasAttribute('data-shopping-summary'))return {kind:'summary'};
    return null;
  }
  function restorePosition(position) {
    if(!position)return;
    const selector=position.kind==='link'?'[data-shopping-link]':'[data-shopping-check]';
    const element=position.kind==='summary'?container.querySelector('[data-shopping-summary]'):[...container.querySelectorAll(selector)].find(el=>(el.dataset.shoppingLink||el.dataset.shoppingCheck)===position.id);
    if(element&&!element.closest('details:not([open])')&&!element.disabled){element.focus();return;}
    const successor=[...container.querySelectorAll('[data-shopping-check]')].find(el=>!el.disabled&&!el.closest('details:not([open])'));
    (successor||container.querySelector('[data-shopping-summary]')||refresh).focus();
  }
  async function mutate(url, data, version, key) {
    const response = await fetch(url, {method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':key},body:JSON.stringify({data,version,generation})});
    const result = await response.json().catch(() => ({error:'Serverantwort konnte nicht gelesen werden.'}));
    if(!response.ok){const error = new Error(result.error || 'Änderung konnte nicht gespeichert werden.');error.status=response.status;throw error;}
    return result;
  }
  function render() {
    if (!page) return;
    const position = focusPosition();
    container.replaceChildren();
    board.classList.toggle('shopping-mode', mode.checked);
    $('#shopping-list-title').textContent=page.list.name;
    const ordered = records;
    const open = ordered.filter(row => !row.checked_at), done = ordered.filter(row => row.checked_at);
    function groups(rows, target) {
      let current = null, section;
      for (const row of rows) {
        const group = row.group_name || 'Ohne Gruppe';
        if (group !== current) {current = group; section=node('section',undefined,'shopping-group'); section.append(node('h3',group)); target.append(section);}
        const article=node('article',undefined,'record-row');article.dataset.shoppingItem=row.id;
        const check=node('button',row.checked_at?'✓':'', 'check-button'+(row.checked_at?' checked':''));
        check.type='button';check.dataset.shoppingCheck=row.id;check.setAttribute('aria-label',(row.checked_at?'Wieder öffnen: ':'Erledigen: ')+row.name);check.setAttribute('aria-pressed',String(Boolean(row.checked_at)));
        check.disabled=!canWrite||Boolean(page.list.archived_at)||Boolean(page.list.is_template)||row.availability==='unavailable'&&!row.checked_at;
        check.addEventListener('click',()=>void toggle(row,!row.checked_at));
        const text=node('div',undefined,'record-main'+(row.checked_at?' completed':''));
        const link=node('a',row.name);link.href='/m/shopping/items/'+row.id;link.dataset.shoppingLink=row.id;text.append(link);
        if(row.quantity)text.append(node('small',quantity(row.quantity)+' '+unit(row.unit_code)));
        if(row.note)text.append(node('small',row.note));
        if(row.product)text.append(node('small',variant(row.product)));
        if(row.availability==='unavailable')text.append(node('small','Nicht verfügbar · Details öffnen, um Verfügbarkeit oder Ersatz zu ändern.','shopping-availability'));
        if(row.replacement)text.append(node('small','Ersatz: '+variant(row.replacement),'shopping-availability'));
        if(row.assignee_name)text.append(node('small','Zuständig: '+row.assignee_name+(row.assignee_inactive?' (nicht mehr aktiv)':'')));
        article.append(check);
        if(row.image_id){const picture=node('img');picture.src='/attachments/'+encodeURIComponent(row.image_id)+'/preview';picture.alt='Produktbild: '+(row.replacement?.name||row.product?.name||row.name);picture.width=48;picture.height=48;picture.loading='lazy';picture.addEventListener('error',()=>picture.remove(),{once:true});article.append(picture);}
        article.append(text);section.append(article);
      }
    }
    groups(open,container);
    updateMergeChoices(open);
    if(done.length){const completed=node('details',undefined,'shopping-completed');completed.open=completedOpen;const summary=node('summary',done.length+' erledigt auf dieser Seite');summary.setAttribute('data-shopping-summary','');completed.append(summary);completed.addEventListener('toggle',()=>{if(completed.isConnected)completedOpen=completed.open;});groups(done,completed);container.append(completed);}
    if(!ordered.length)container.append(node('p',filter.value.trim()?'Keine passenden Artikel. Suche ändern oder leeren.':'Noch keine Artikel. Füge deinen ersten Artikel hinzu.','muted'));
    $('#shopping-progress').textContent=page.completed+' von '+page.total+' Artikeln erledigt';
    $('#shopping-progress-bar').max=Math.max(1,page.total);$('#shopping-progress-bar').value=page.completed;
    $('#shopping-page-note').textContent=records.length+' Artikel auf dieser Seite. Die Suche durchsucht die ganze Liste; der Fortschritt zählt auch ausgeblendete Artikel.';
    more.hidden=!nextCursor;previous.hidden=!history.length;
    const context=page.context;
    $('#shopping-context-title').textContent=(context.store_name||'Geschäft')+' · Schätzung';
    $('#shopping-store').textContent=context.store_name?'Geschäft: '+context.store_name+' · Artikel nach Gangreihenfolge.':'Noch kein Geschäft gewählt. Artikel nach Gruppe sortiert.';
    $('#shopping-estimate').textContent='Schätzung: '+money(context.estimate_minor)+' · '+context.unknown+' Positionen ohne passenden Preis oder Menge · '+context.unavailable+' nicht verfügbar.'+(context.oldest_price?' Preisbeobachtungen: '+context.oldest_price+' bis '+context.newest_price+'.':'')+' Gilt für offene und erledigte verfügbare Positionen; tatsächlicher Kassenpreis kann abweichen.';
    $('#shopping-shopper').textContent=context.shopper_name?context.shopper_name+' kauft ein bis '+new Date(context.shopper_until).toLocaleTimeString('de-AT',{hour:'2-digit',minute:'2-digit',timeZoneName:'short'})+'.':'Aktuell ist niemand als einkaufend gemeldet.';
    $('#shopping-shopper').hidden=!context.shopper_name;
    if($('#shopping-claim')){$('#shopping-claim').disabled=Boolean(context.shopper_name)&&!context.shopper_self;$('#shopping-claim').textContent=context.shopper_self?'Verlängern':'Ich kaufe jetzt ein';$('#shopping-claim').setAttribute('aria-label',context.shopper_self?'Einkaufsstatus um 30 Minuten verlängern':'Ich kaufe jetzt ein · 30 Minuten');$('#shopping-release').hidden=!context.shopper_name||!context.shopper_self&&board.dataset.isAdmin!=='true';}
    restorePosition(position);
  }
  function updateMergeChoices(rows) {
    if(!$('#shopping-merge'))return;
    for(const selector of ['#shopping-merge-keep','#shopping-merge-remove']){
      const select=$(selector),chosen=select.value;select.replaceChildren(new Option('Artikel auswählen',''));
      for(const row of rows)select.add(new Option(row.name+' · '+quantity(row.quantity)+' '+unit(row.unit_code)+(row.product?' · '+variant(row.product):' · Freier Artikel'),row.id));
      select.value=chosen;
      if(chosen&&!select.value)$('#shopping-merge-comparison').hidden=true;
    }
  }
  if($('#shopping-merge')){
    const keep=$('#shopping-merge-keep'),remove=$('#shopping-merge-remove'),comparison=$('#shopping-merge-comparison'),status=$('#shopping-merge-status'),review=$('#shopping-merge-review');
    let selectionRevision=0;
    for(const select of [keep,remove])select.addEventListener('change',()=>{++selectionRevision;comparison.hidden=true;status.textContent='';});
    review.addEventListener('click',async()=>{
      if(busy)return;
      if(!keep.value||!remove.value||keep.value===remove.value){status.textContent='Zwei unterschiedliche offene Artikel auswählen.';keep.focus();return;}
      const snapshot=++selectionRevision,selectedKeep=keep.value,selectedRemove=remove.value;
      review.disabled=true;status.textContent='Aktuelle Positionen werden verglichen …';comparison.hidden=true;
      try{
        const response=await fetch('/api/v1/shopping/items/'+selectedKeep+'/merge-preview?other='+selectedRemove,{headers:{Accept:'application/json'}}),result=await response.json();
        if(!response.ok)throw Error(result.error||'Vergleich konnte nicht geladen werden.');
        if(snapshot!==selectionRevision||keep.value!==selectedKeep||remove.value!==selectedRemove)return;
        const a=result.keep,b=result.other;comparison.replaceChildren(node('h3','Vor dem Zusammenführen prüfen'),node('p','Je Feld: Position behalten → Position dazugeben.'));
        const fields=[['name','Artikel',v=>v],['quantity','Menge',v=>quantity(v)],['unit_code','Einheit',v=>unit(v)],['product','Produktvariante',variant],['replacement','Ersatz',variant],['availability','Verfügbarkeit',v=>({available:'Verfügbar',unavailable:'Nicht verfügbar',substituted:'Ersatz gewählt'})[v]],['assignee_name','Zuständig',v=>v],['group_name','Gruppe',v=>v],['note','Notiz',v=>v]];
        for(const [field,label,format]of fields)comparison.append(node('p',label+': '+(format(a[field])||'Keine Angabe')+' → '+(format(b[field])||'Keine Angabe')));
        comparison.append(node('p',result.compatible?'Ergebnis: '+quantity(result.quantity)+' '+unit(a.unit_code)+' in „'+a.name+'“. Die zweite Position wird entfernt; Änderungen bleiben im Protokoll nachvollziehbar.':result.reason));
        const cancel=node('button','Auswahl ändern');cancel.type='button';cancel.addEventListener('click',()=>{++selectionRevision;comparison.hidden=true;keep.focus();});comparison.append(cancel);
        if(result.compatible){const confirm=node('button','Geprüft zusammenführen','primary');confirm.type='button';comparison.append(confirm);const key=crypto.randomUUID();
          confirm.addEventListener('click',async()=>{
            if(busy)return;busy=true;confirm.disabled=true;review.disabled=true;keep.disabled=true;remove.disabled=true;cancel.disabled=true;
            controller?.abort();++revision;loading=false;
            try{await mutate('/api/v1/m/shopping/items/'+a.id+'/actions/merge',{other_id:b.id,other_version:b.version},a.version,key);comparison.hidden=true;busy=false;await load(true);status.textContent='Artikel zusammengeführt.';}
            catch(error){status.textContent=error.message+' Ergebnis prüfen: Liste aktualisieren und Positionen erneut vergleichen.';comparison.hidden=true;}
            finally{busy=false;confirm.disabled=false;review.disabled=false;keep.disabled=false;remove.disabled=false;cancel.disabled=false;if(document.activeElement===confirm||document.activeElement===document.body)review.focus();schedule();}
          });
        }else{comparison.append(node('p','Unterschiede in den Artikeldetails prüfen, anschließend erneut vergleichen.'));for(const row of [a,b]){const link=node('a','Details: '+row.name);link.href='/m/shopping/items/'+row.id;comparison.append(link,node('br'));}}
        comparison.hidden=false;status.textContent='';comparison.focus();
      }catch(error){status.textContent=error.message;}finally{review.disabled=false;}
    });
  }
  function schedule() {clearTimeout(poll);poll=setTimeout(async()=>{if(!document.hidden&&navigator.onLine&&!busy&&!loading)await load(false,true);schedule();},delay);}
  async function load(reset=false,quiet=false) {
    if(reset){cursor='';history=[];resetRequested=true;}
    if(busy)return;
    resetRequested=false;
    controller?.abort();controller=new AbortController();const current=++revision;loading=true;
    refresh.disabled=true;more.disabled=true;previous.disabled=true;
    if(!quiet)announce('Liste wird aktualisiert …');
    try{
      const params=new URLSearchParams({cursor,q:filter.value.trim()});
      const response=await fetch('/api/v1/shopping/lists/'+listID+'/items?'+params,{headers:{Accept:'application/json'},signal:controller.signal});
      const result=await response.json();if(!response.ok)throw new Error(result.error||'Liste konnte nicht geladen werden.');
      if(result.generation!==generation)throw new Error('Der Modulstand hat sich geändert. Seite neu öffnen.');
      if(current!==revision)return;
      page=result;records=result.records;nextCursor=result.next_cursor;
      // Cursor signatures contain an expiry; compare business data only.
      const stable=JSON.stringify([records,result.total,result.completed,result.list,result.context]);
      if(stable!==previousSignature){previousSignature=stable;render();if(quiet)announce('Änderungen im Haushalt wurden übernommen.');}
      else {more.hidden=!nextCursor;previous.hidden=!history.length;}
      if(!quiet)announce('Liste ist aktuell.');
      delay=15000;
    }catch(error){if(error.name!=='AbortError'){announce(error.message+' Deine Eingaben bleiben erhalten.');delay=Math.min(delay*2,120000);}}
    finally{if(current===revision){loading=false;refresh.disabled=false;more.disabled=false;previous.disabled=false;}}
  }
  async function showConflict(row, checked) {
    conflict.replaceChildren();conflict.hidden=false;
    conflict.append(node('h3','Artikel wurde inzwischen geändert'));
    const keep=node('button','Aktuellen Stand behalten');keep.type='button';keep.addEventListener('click',async()=>{const retain=document.activeElement===keep;conflict.hidden=true;await load();if(retain&&(document.activeElement===keep||document.activeElement===document.body))restorePosition({id:row.id,kind:'check'});});
    try{
      const response=await fetch('/api/v1/shopping/items/'+row.id,{headers:{Accept:'application/json'}});
      if(!response.ok)throw new Error('Artikel ist nicht mehr verfügbar. Liste aktualisieren.');
      const current=await response.json();
      conflict.append(node('p','Deine Auswahl: '+(checked?'erledigt':'offen')+'. Aktuell: '+current.name+' · '+(current.checked_at?'erledigt':'offen')+(current.quantity?' · '+quantity(current.quantity)+' '+unit(current.unit_code):'')+'.'));
      for(const [field,label,format] of [['product','Produkt',variant],['replacement','Ersatz',variant],['assignee_name','Zuständig',value=>value||'Nicht zugewiesen'],['availability','Verfügbarkeit',value=>({available:'Verfügbar',unavailable:'Nicht verfügbar',substituted:'Ersatz gewählt'})[value]||'Verfügbar']]){const before=format(row[field])||'Keine Auswahl',after=format(current[field])||'Keine Auswahl';if(before!==after)conflict.append(node('p',label+': '+before+' → '+after));}
      if(['name','quantity','unit_code','note','group_name','availability','product_id','replacement_product_id','assignee_id'].some(field=>String(current[field]??'')!==String(row[field]??''))){conflict.append(node('p','Auch Artikeldetails wurden geändert. Prüfe den aktuellen Eintrag vor der Bestätigung.'));const link=node('a','Aktuelle Details öffnen');link.href='/m/shopping/items/'+row.id;conflict.append(link);}
      const apply=node('button',checked?'Geprüft als erledigt markieren':'Geprüft wieder öffnen');apply.type='button';apply.disabled=checked&&current.availability==='unavailable';apply.addEventListener('click',()=>void toggle(current,checked));conflict.append(keep,apply);
    }catch(error){conflict.append(node('p',error.message),keep);}
    conflict.focus();
  }
  async function toggle(row,checked) {
    if(busy)return;
    const startedFocus=document.activeElement;
    const intendedFocus=focusPosition()||((conflict.contains(startedFocus)||message.contains(startedFocus))?{id:row.id,kind:'check'}:null);
    busy=true;controller?.abort();++revision;loading=false;board.setAttribute('aria-busy','true');
    container.querySelectorAll('[data-shopping-check]').forEach(button=>button.setAttribute('aria-disabled','true'));
    try{
      const result=await mutate(actionURL(row),{checked},row.version,crypto.randomUUID());
      undo={row:result,checked:!checked,expires:Date.now()+10000};
      const retainFocus=document.activeElement===startedFocus;
      conflict.hidden=true;busy=false;await load();if(retainFocus)restorePosition(intendedFocus);
      announce(checked?'Artikel erledigt.':'Artikel wieder geöffnet.');
      const reverse=node('button','Rückgängig');reverse.type='button';const saved=undo;
      reverse.addEventListener('click',()=>{if(undo===saved&&Date.now()<saved.expires){undo=null;void toggle(saved.row,saved.checked);}});message.append(reverse);setTimeout(()=>reverse.remove(),10000);
    }catch(error){busy=false;if(error.status===409)await showConflict(row,checked);else announce(error.message+' Änderung wurde nicht bestätigt.');render();}
    finally{busy=false;board.removeAttribute('aria-busy');container.querySelectorAll('[data-shopping-check]').forEach(button=>button.removeAttribute('aria-disabled'));refresh.disabled=false;more.disabled=false;previous.disabled=false;if(resetRequested)void load();schedule();}
  }
  if(form){
    const input=form.elements.name,preview=$('#shopping-parse');let suggestion=null,pending=null;
    $('#shopping-use-favorite').addEventListener('click',async()=>{
      if(busy)return;
      const selected=$('#shopping-favorite'),productSelect=form.elements.product_id,button=$('#shopping-use-favorite');
      if(!selected.value||selected.dataset.referenceState!=='ready'||productSelect.dataset.referenceState!=='ready'){announce('Favorit auswählen und die Produktauswahl vollständig laden.');selected.focus();return;}
      const before=JSON.stringify(Object.fromEntries(new FormData(form))),id=selected.value;button.disabled=true;
      try{
        const response=await fetch('/api/v1/m/shopping/products/'+id,{headers:{Accept:'application/json'}});
        if(!response.ok)throw Error('Favorit ist nicht mehr verfügbar. Auswahl aktualisieren.');
        const product=await response.json();
        if(JSON.stringify(Object.fromEntries(new FormData(form)))!==before||selected.value!==id){announce('Dein Entwurf wurde inzwischen geändert. Favorit bei Bedarf erneut übernehmen.');return;}
        if(![...productSelect.options].some(option=>option.value===id)){const option=node('option',selected.selectedOptions[0].textContent);option.value=id;productSelect.append(option);}
        productSelect.value=id;input.value=product.name;form.elements.quantity.value='1';form.elements.unit_code.value='piece';preview.hidden=true;$('#shopping-details').open=true;
        announce('Favorit als Entwurf mit einer Packung übernommen. Menge und Details vor dem Hinzufügen prüfen.');input.focus();
      }catch(error){announce(error.message);}finally{button.disabled=false;}
    });
    input.addEventListener('input',()=>{suggestion=globalThis.HouseholdShoppingInput.parseShoppingInput(input.value);preview.hidden=!suggestion;if(suggestion)preview.querySelector('p').textContent='Erkannt: '+suggestion.name+' · '+quantity(suggestion.quantity)+' '+unit(suggestion.unit_code)+'. Vor dem Hinzufügen prüfen.';});
    $('#shopping-apply-parse').addEventListener('click',()=>{if(!suggestion)return;input.value=suggestion.name;form.elements.quantity.value=quantity(suggestion.quantity);form.elements.unit_code.value=suggestion.unit_code;$('#shopping-details').open=true;preview.hidden=true;form.elements.quantity.focus();});
    form.addEventListener('submit',async event=>{
      event.preventDefault();if(busy||!form.reportValidity())return;
      const errorBox=form.querySelector('.form-error');errorBox.hidden=true;
      if([...form.querySelectorAll('[data-reference]')].some(select=>select.value&&select.dataset.referenceState!=='ready')){errorBox.textContent='Gewählte Produktvariante oder Zuständigkeit wird noch geprüft. Auswahl zuerst laden oder erneut versuchen.';errorBox.hidden=false;errorBox.focus();return;}
      const data=Object.fromEntries(new FormData(form));delete data.csrf;
      const signature=JSON.stringify(data);if(!pending||pending.signature!==signature)pending={signature,key:crypto.randomUUID()};
      busy=true;controller?.abort();++revision;loading=false;form.querySelector('button.primary').disabled=true;
      try{
        await mutate(form.action,data,0,pending.key);pending=null;
        const current=Object.fromEntries(new FormData(form));delete current.csrf;
        const unchanged=JSON.stringify(current)===signature;
        if(unchanged){form.reset();preview.hidden=true;$('#shopping-details').open=false;}
        busy=false;await load(true);announce(unchanged?'Artikel hinzugefügt.':'Artikel hinzugefügt. Deine inzwischen geänderte Eingabe bleibt erhalten.');
        if(unchanged&&$('#shopping-entry').open)input.focus();
      }
      catch(error){errorBox.textContent=error.message+' Deine Eingaben bleiben erhalten.';errorBox.hidden=false;errorBox.focus();}
      finally{busy=false;form.querySelector('button.primary').disabled=false;refresh.disabled=false;more.disabled=false;previous.disabled=false;render();if(resetRequested)void load();schedule();}
    });
  }
  mode.addEventListener('change',()=>{completedOpen=!mode.checked;if(mode.checked){if($('#shopping-entry'))$('#shopping-entry').open=false;$('#shopping-filter-panel').open=false;}render();});
  async function activity(active){if(busy||!page)return;busy=true;controller?.abort();++revision;loading=false;try{await mutate('/api/v1/m/shopping/lists/'+listID+'/actions/activity',{active},page.list.version,crypto.randomUUID());busy=false;await load();$('#shopping-claim').focus();}catch(error){announce(error.message+' Liste aktualisieren und den Status erneut prüfen.');}finally{busy=false;refresh.disabled=false;more.disabled=false;previous.disabled=false;if(resetRequested)void load();schedule();}}
  $('#shopping-claim')?.addEventListener('click',()=>void activity(true));$('#shopping-release')?.addEventListener('click',()=>void activity(false));
  filter.addEventListener('input',()=>{clearTimeout(queryTimer);queryTimer=setTimeout(()=>void load(true),300);});
  refresh.addEventListener('click',()=>void load(true));
  more.addEventListener('click',()=>{if(!nextCursor||loading||busy)return;history.push(cursor);cursor=nextCursor;void load();});
  previous.addEventListener('click',()=>{if(loading||busy)return;cursor=history.pop()||'';void load();});
  document.addEventListener('visibilitychange',()=>{if(!document.hidden){void load(false,true);schedule();}});
  addEventListener('online',()=>{void load(false,true);schedule();});
  addEventListener('pagehide',()=>{clearTimeout(poll);controller?.abort();});
  void load();schedule();
})();
