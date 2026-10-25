(() => {
 'use strict';
 const qty=value=>String(value??'').replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');
 const amount=value=>{
  if(!value)return'Offen';
  const text=value.below_precision?(String(value.rational).startsWith('-')?'> −0,000001':'< 0,000001'):(value.approximate?'≈ ':'')+qty(value.value);
  return value.approximate?text+' (exakt '+value.rational+')':text;
 };
 const decisionKey=choice=>choice.action==='reactivate'?'meal/'+choice.meal_id:choice.requirement_id;
 if(typeof module==='object'&&module.exports){module.exports={amount,decisionKey};return;}
 const root=document.querySelector('#planning-purchases');if(!root)return;
 const $=selector=>root.querySelector(selector),writable=root.dataset.writable==='true',pid=root.dataset.plan;
 const el=(tag,text)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
 const unit=value=>value==='piece'?'Stück':value||'';
 const reasons={source_missing:'Gespeicherte Quelle fehlt. Betroffenen Mahlzeitenbedarf beenden und neue Quelle gesondert aufnehmen.',source_changed:'Portionen oder Quellenstand wurden geändert.',mapping_changed:'Produktzuordnung oder Produkteinheit wurde geändert.',target_missing:'Vorratsprodukt fehlt. Produktzuordnung zuerst neu bestätigen.',pantry_purged:'Vorratsdaten wurden gelöscht. Produktzuordnung zuerst neu bestätigen.',shopping_purged:'Einkaufsdaten wurden gelöscht.',item_missing:'Frühere Einkaufsposition fehlt.',item_changed:'Die offene Einkaufsposition wurde von Hand geändert.',unresolved_checkout:'Früherer Einkaufsabschluss deckt den Bedarf nicht nachweisbar.',renewal_required:'Erneute Einkaufsentscheidung erforderlich.',reactivation_required:'Bedarf dieser ganzen Mahlzeit wurde zuvor beendet.',checkout_required:'Erledigte Position zuerst im Einkauf abschließen oder ausdrücklich entkoppeln.',destination_closed:'Die bisherige Liste ist archiviert oder bereits abgeschlossen. Zuordnung lösen und neu prüfen.',destination_changed:'Einkaufsziel wurde geändert. Zuordnung zuerst lösen.',item_unit_changed:'Die offene Position hat eine andere Einheit. Zuordnung lösen und Position gesondert prüfen.',quantity_decision_required:'Exakte Menge ist nicht direkt buchbar. Portionen, Quelle oder Einheit zuerst ausdrücklich korrigieren.',product_unassigned:'Produktzuordnung fehlt.',quantity_unknown:'Quellmenge ist unbekannt.',unit_incompatible:'Quell- und Produkteinheit sind nicht umrechenbar.',product_archived:'Vorratsprodukt ist archiviert.',ingredients_missing:'Rezeptstand enthält keine Zutaten.',components_missing:'Rezeptkomponenten fehlen.'};
 const actions={none:'Unverändert',create:'Neue Position',update:'Menge derselben Position ändern',remove:'Position entfernen',blocked:'Entscheidung offen'};
 const dispositions={none:'Noch kein Einkauf nötig',managed:'Offene zugeordnete Position',stored:'Eingelagert',no_stock:'Gekauft ohne Einlagerung',skipped:'Nicht gekauft',missing:'Position fehlt',detached:'Einkaufszuordnung gelöst',unresolved:'Abschluss gesondert klären'};
 let scope=null,preview=null,target=null,control=null,pending=null,busy=false,uncertain=false,dirty=false;
 let selected=new Set(root.dataset.selected.split(',').filter(Boolean)),choices=new Map();
 function row(parent,label,value){const p=el('p');p.append(el('strong',label+': '),document.createTextNode(String(value)));parent.append(p);}
 function button(text,fn){const node=el('button',text);node.type='button';node.addEventListener('click',fn);return node;}
 function error(message){$('#purchase-error').textContent=message;$('#purchase-error').hidden=false;$('#purchase-error').focus();}
 async function api(url,options={}){
  let response;try{response=await fetch(url,options);}catch{throw Error('Verbindung unterbrochen.');}
  let data;try{data=await response.json();}catch{const e=Error('Antwort konnte nicht gelesen werden.');e.status=response.ok?0:response.status;throw e;}
  if(!response.ok){const e=Error(data.error||'Anfrage fehlgeschlagen.');e.status=response.status;throw e;}return data;
 }
 const enabled=()=>scope?.mealplan_enabled&&scope?.pantry_enabled&&scope?.shopping_enabled;
 const creates=()=>!!preview?.lines.some(line=>line.action==='create');
 function freeze(){
  for(const node of root.querySelectorAll('button,fieldset,input,select'))node.disabled=busy||uncertain;
  $('#purchase-compare').disabled=busy||uncertain||!enabled()||!selected.size&&!scope?.requirements.length;
  $('#purchase-selection').disabled=busy||uncertain||!scope?.mealplan_enabled;
  for(const node of $('#purchase-week-form').elements)node.disabled=busy||uncertain||!scope?.mealplan_enabled;
  if(writable){
   $('#purchase-retry').hidden=!uncertain;$('#purchase-retry').disabled=busy;
   $('#purchase-confirmation').disabled=busy||uncertain||dirty;
   $('#purchase-save').disabled=busy||uncertain||dirty||!preview?.ready||!$('#purchase-confirmation').checked||creates()&&!target;
  }
 }
 function invalidate(message,keepPreview=false){dirty=true;target=null;if(writable){$('#purchase-confirmation').checked=false;$('#purchase-commit').hidden=true;}if(!keepPreview){preview=null;$('#purchase-preview').hidden=true;}$('#purchase-status').textContent=message;freeze();}
 function renderScope(){
  const list=$('#purchase-scope');list.replaceChildren();const groups=new Map();
  for(const r of scope.requirements){if(!groups.has(r.meal_id))groups.set(r.meal_id,[]);groups.get(r.meal_id).push(r);}
  $('#purchase-scope-summary').textContent=groups.size?groups.size+(groups.size===1?' aktive Mahlzeit':' aktive Mahlzeiten')+' · '+scope.requirements.length+' gespeicherte Zutatenbedarfe.':'Noch kein aktiver Bedarf. Wähle ganze Mahlzeiten aus einer Arbeitswoche und vergleiche sie vor der Aufnahme.';
  if(!enabled())list.append(el('p','Für neue oder geänderte Einkäufe müssen Essensplanung, Vorrat und Einkauf aktiviert sein. Aktiven Bedarf kannst du weiterhin prüfen, beenden und von bestehenden Einkaufspositionen lösen.'));
  for(const [mealID,rows]of groups){
   const section=el('section');section.className='purchase-source';section.dataset.scopeMeal=mealID;section.append(el('h3',rows[0].meal_name||'Gespeicherte Mahlzeit · '+mealID.slice(0,8)));
   if(rows[0].plan_name)section.append(el('p',rows[0].plan_name));
   const details=el('details');details.append(el('summary',rows.length+' Zutaten und Einkaufszuordnungen'));
   for(const r of rows){const entry=el('div');entry.className='purchase-scope-row';row(entry,r.item_name||'Zutat · '+r.ingredient_id.slice(0,8),dispositions[r.disposition]||r.disposition);if(r.review_reason)entry.append(el('p',reasons[r.review_reason]||'Quelle erneut prüfen.'));if(writable&&r.item_id)entry.append(button('Zuordnung dieser Einkaufsposition lösen',()=>reviewControl('detach',[r],mealID)));details.append(entry);}
   section.append(details);if(writable)section.append(button('Bedarf dieser ganzen Mahlzeit beenden',()=>reviewControl('end',rows,mealID)));list.append(section);
  }
 }
 function reviewControl(action,rows,mealID){
  if(busy||uncertain)return;const view=scope;control={generation:view.generation,pantry_generation:view.pantry_generation,shopping_generation:view.shopping_generation,fingerprint:view.fingerprint,action,meal_ids:action==='end'?[mealID]:[],requirement_ids:action==='detach'?rows.map(r=>r.id):[]};
  const box=$('#purchase-control-content');box.replaceChildren();$('#purchase-control-title').textContent=action==='end'?'Ganzen Mahlzeitenbedarf beenden':'Einkaufszuordnung lösen';row(box,'Mahlzeit',rows[0].meal_name||mealID);row(box,'Umfang',rows.length+(rows.length===1?' Zutatenbedarf':' Zutatenbedarfe'));box.append(el('p',action==='end'?'Diese Mahlzeit wird aus dem aktiven Bedarf entfernt. Ihr bisher zugeteilter Bestand steht im nächsten Vergleich anderen Mahlzeiten zur Verfügung. Eine spätere Wiederaufnahme erfordert eine ausdrückliche Entscheidung für die ganze Mahlzeit.':'Der Bedarf bleibt aktiv. Die bestehende Einkaufsposition wird eigenständig und zählt danach nicht mehr als zugeordneter Einkauf. Eine erneute Übernahme kann zusätzliche Einkäufe erzeugen und muss neu geprüft werden.'));
  if(action==='detach')for(const r of rows){row(box,'Einkaufsposition',r.item_name||'Position · '+r.item_id);row(box,'Aktuelle Liste',r.list_name||(r.item_list_id?'Liste · '+r.item_list_id:'Nicht verfügbar; gespeicherte Zuordnung · '+r.list_id));if(r.item_quantity!==null&&r.item_quantity!==undefined)row(box,'Aktuelle Menge',qty(r.item_quantity)+' '+unit(r.item_unit));else box.append(el('p','Artikeldetails sind im aktuellen Modulstand nicht verfügbar. Die bestehende Position bleibt erhalten.'));}
  $('#purchase-control').hidden=false;$('#purchase-control-title').focus();freeze();
 }
 async function loadWeek(){
  $('#purchase-selection-section').hidden=true;if(!pid||!scope.mealplan_enabled)return;
  try{
   let start=$('#purchase-week-form').elements.week.value;
   if(!start){const plan=await api('/api/v1/m/mealplan/plans/'+pid);start=plan.starts_on;$('#purchase-week-form').elements.week.value=start;}
   const week=await api('/api/v1/planning/'+pid+'?week='+encodeURIComponent(start)),list=$('#purchase-meals');list.replaceChildren();
   $('#purchase-week-link').href='/m/mealplan/workspace/'+pid+'?week='+encodeURIComponent(start);
   $('#purchase-mapping-link').href='/m/mealplan/demand/'+pid+'?week='+encodeURIComponent(start)+[...selected].map(mid=>'&meal='+mid).join('');
   if(week.plan.is_template){$('#purchase-status').textContent='Diese Woche ist eine Vorlage. Zuerst eine unabhängige Arbeitswoche erstellen.';return;}
   const meals=week.meals.filter(meal=>meal.kind==='recipe'&&['open','planned'].includes(meal.status));
   for(const meal of meals){const label=el('label'),input=el('input');label.className='check';input.type='checkbox';input.value=meal.id;input.checked=selected.has(meal.id);input.addEventListener('change',()=>{selected=new Set([...list.querySelectorAll('input:checked')].map(n=>n.value));choices.clear();$('#purchase-mapping-link').href='/m/mealplan/demand/'+pid+'?week='+encodeURIComponent(start)+[...selected].map(mid=>'&meal='+mid).join('');invalidate('Auswahl geändert. Den vollständigen Umfang erneut vergleichen.');});label.append(input,el('span',meal.planned_on+' · '+meal.title));list.append(label);}
   $('#purchase-no-meals').hidden=!!meals.length;$('#purchase-selection-section').hidden=false;
  }catch(e){error(e.message+' Die aktive Bedarfsverwaltung bleibt erreichbar.');}
 }
 async function load(initial=false){
  if(busy||uncertain)return;busy=true;scope=null;preview=null;target=null;control=null;dirty=true;$('#purchase-preview').hidden=true;$('#purchase-scope').replaceChildren();$('#purchase-scope-summary').textContent='Aktiver Bedarf wird geladen …';$('#purchase-status').textContent='Aktiver Bedarf wird geladen …';if(writable){$('#purchase-commit').hidden=true;$('#purchase-control').hidden=true;}freeze();$('#purchase-error').hidden=true;
  try{scope=await api('/api/v1/planning-purchases/scope');renderScope();if(initial)await loadWeek();invalidate(enabled()?'Aktuellen vollständigen Umfang vergleichen.':'Deaktivierte Module zuerst aktivieren oder aktiven Bedarf unten verwalten.');}
  catch(e){error(e.message+' Aktuellen Umfang erneut laden.');}
  finally{busy=false;freeze();}
  if(scope&&enabled()&&(selected.size||scope.requirements.length))await compare(false);
 }
 function selectedSources(){return selected.size?[{plan_id:pid,meal_ids:[...selected].sort()}]:[];}
 function chosenDecisions(){return [...choices.values()];}
 function renderPreview(){
  $('#purchase-preview').hidden=false;$('#purchase-preview-summary').textContent=preview.demand.meals.length+(preview.demand.meals.length===1?' ganze Mahlzeit':' ganze Mahlzeiten')+' aus '+preview.plans.length+(preview.plans.length===1?' Plan. ':' Plänen. ')+(preview.ready?'Alle benötigten Angaben und Entscheidungen sind geprüft.':'Offene Mengen, Zuordnungen oder Entscheidungen zuerst klären.');
  $('#purchase-read-at').textContent='Gemeinsamer Datenstand: '+new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',timeStyle:'medium',timeZone:preview.demand.timezone}).format(new Date(preview.read_at));
  const issues=$('#purchase-issues');issues.replaceChildren();for(const issue of preview.issues)issues.append(el('p',reasons[issue.reason]||'Quelle oder Produktzuordnung in der Bestandsprüfung erneut prüfen.'));
  const list=$('#purchase-lines');list.replaceChildren();const decisionShown=new Set();
  for(const line of preview.lines){
   const source=preview.demand.lines.find(r=>r.component_id===line.component_id&&r.ingredient_id===line.ingredient_id),meal=preview.demand.meals.find(r=>r.id===line.meal_id),requirement=preview.requirements.find(r=>r.id===line.requirement_id);
   const section=el('section');section.className='purchase-line';section.dataset.ingredient=line.ingredient_id;section.append(el('h4',source.name),el('p',(meal?.title||'Mahlzeit')+' · '+source.component_title+' · '+qty(source.servings)+' Portionen'));
   row(section,'Bestätigtes Vorratsprodukt',source.binding.product_name+' · '+unit(source.binding.unit));
   const numbers=el('dl');numbers.className='purchase-amounts';for(const [label,value]of [['Bedarf',amount(source.demand)],['Bestandsdeckung',amount(line.stock_coverage)],['Neue Einkaufs-Sollmenge',amount(line.desired)],['Bisher zugeordnet',qty(requirement?.pending_quantity||'0')],['Differenz zum bisherigen Einkauf',amount(line.delta)],['Zusätzlicher Bedarf',amount(line.additional)]]){numbers.append(el('dt',label),el('dd',value+' '+unit(source.binding.unit)));}section.append(numbers);
   row(section,'Geprüfte Wirkung',actions[line.action]||line.action);
   if(line.current_item){const item=line.current_item;const details=el('details');details.append(el('summary','Vorhandene Einkaufsposition prüfen'));row(details,'Liste',item.list_name);row(details,'Aktueller Artikel',item.name+' · '+qty(item.quantity)+' '+unit(item.unit_code));row(details,'Notiz',item.note||'Keine Notiz vorhanden.');row(details,'Stand',item.version+' · '+(item.checked?'Erledigt':'Offen'));section.append(details);}
   for(const reason of line.review_reasons)section.append(el('p',reasons[reason]||reason));
   if(writable&&line.required_decision){const choice=line.required_decision==='reactivate'?{requirement_id:'',meal_id:line.meal_id,action:'reactivate'}:{requirement_id:line.requirement_id,meal_id:'',action:line.required_decision};const key=decisionKey(choice);
    if(!decisionShown.has(key)){decisionShown.add(key);const label=el('label'),input=el('input');label.className='check';input.type='checkbox';input.checked=choices.has(key);input.dataset.decision=key;input.addEventListener('change',()=>{if(input.checked)choices.set(key,choice);else choices.delete(key);invalidate('Entscheidung gewählt. Den vollständigen Umfang vor dem Speichern erneut vergleichen.',true);});const text=choice.action==='reactivate'?'Den Bedarf dieser ganzen Mahlzeit ausdrücklich wieder aufnehmen. Bisher behaltene Einkäufe bleiben eigenständig.':choice.action==='renew'?'Erneuten Einkauf ausdrücklich zulassen. Frühere Käufe, ausgelassene oder gelöste Positionen bleiben erhalten; sie sind keine nachgewiesene Vorratsdeckung.':'Aktuellen Quellen- und Produktstand sowie die vorhandene offene Einkaufsposition ausdrücklich für die oben gezeigte Wirkung übernehmen.';label.append(input,el('span',text));section.append(label);}
   }
   list.append(section);
  }
  if(!preview.lines.length)list.append(el('p','Noch kein ausführbarer Vergleich. Prüfe fehlende Quellen oder Produktzuordnungen; verwalte aktive alte Quellen im Abschnitt „Aktiver Bedarf“.'));
  if(writable){$('#purchase-commit').hidden=!preview.ready;$('#purchase-destination-form').hidden=!creates();$('#purchase-destination-info').textContent=creates()?'Eine offene, noch nicht abgeschlossene Zielliste für neue Positionen prüfen.':'Keine neue Position benötigt eine Zielliste. Vorhandene Positionen werden an ihrem bisherigen Ort abgeglichen.';const counts={create:0,update:0,remove:0};for(const line of preview.lines)if(line.action in counts)counts[line.action]++;$('#purchase-effects').textContent=counts.create+' neue Positionen · '+counts.update+' Mengenänderungen · '+counts.remove+' entfernte Positionen.';$('#purchase-confirmation').checked=false;}
 }
 async function compare(focus=true){
  if(busy||uncertain||!enabled())return;busy=true;freeze();$('#purchase-error').hidden=true;target=null;
  const query=new URLSearchParams();for(const meal of selected)query.append('source',pid+'/'+meal);for(const choice of choices.values())query.append('decision',(choice.meal_id||choice.requirement_id)+'/'+choice.action);
  try{preview=await api('/api/v1/planning-purchases/preview?'+query);dirty=false;renderPreview();$('#purchase-status').textContent=preview.ready?'Vergleich bereit. Erst die ausdrückliche Bestätigung speichert Änderungen.':'Vergleich zeigt offene Entscheidungen.';if(focus)$('#purchase-preview-title').focus();}
  catch(e){invalidate('Vergleich nicht mehr aktuell. Auswahl und Entscheidungen prüfen.');error(e.message);}
  finally{busy=false;freeze();}
 }
 function receipt(result){
  const box=$('#purchase-receipt-content');box.replaceChildren();row(box,'Historischer Vorgang',({sync:'Einkaufsabgleich',end:'Ganzer Bedarf beendet',detach:'Einkaufszuordnung gelöst',checkout:'Einkaufsabschluss'})[result.kind]||result.kind);row(box,'Gespeicherte Wirkung',result.source_count+' geprüfte Zutaten · '+result.counts.changes+' geänderte Beiträge · '+result.counts.creates+' neue, '+result.counts.updates+' geänderte, '+result.counts.removes+' entfernte Einkaufspositionen');row(box,'Zeitpunkt',new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',timeStyle:'medium'}).format(new Date(result.created_at)));
  box.append(el('p','Dieser unveränderliche Beleg beschreibt den damaligen Vorgang. Der heutige aktive Bedarf wird getrennt angezeigt.'));
  if(!result.details_available)box.append(el('p','Detailbeleg nicht verfügbar: Einkauf ist deaktiviert, der Datenstand wurde gelöscht oder die Detailaufbewahrung ist abgelaufen. Der dauerhafte Nachweis bleibt erhalten.'));
  else for(const source of result.snapshot.sources){const item=source.item_after||source.item_before;const p=el('p',(item?.name||'Zutat · '+source.ingredient_id.slice(0,8))+': '+(actions[source.item_action]||source.item_action));box.append(p);}
  $('#purchase-receipt-link').href='/planning-purchases?operation='+encodeURIComponent(result.id);$('#purchase-receipt').hidden=false;
 }
 async function send(kind,retry=false){
  if(busy||uncertain&&!retry)return;
  if(!retry){let input;if(kind==='control'){if(!control)return;input=control;}else{if(dirty||!preview?.ready||!$('#purchase-confirmation').checked||creates()&&!target)return;input={generation:preview.generation,pantry_generation:preview.pantry_generation,shopping_generation:preview.shopping_generation,fingerprint:preview.fingerprint,selected:selectedSources(),decisions:chosenDecisions(),list_id:creates()?target.id:'',list_version:creates()?target.version:0};}pending={key:crypto.randomUUID(),url:'/api/v1/planning-purchases/'+(kind==='control'?'control':'sync'),body:JSON.stringify(input)};}
  if(!pending)return;busy=true;freeze();$('#purchase-error').hidden=true;
  try{const result=await api(pending.url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':pending.key},body:pending.body});pending=null;uncertain=false;control=null;choices.clear();receipt(result);if(writable)$('#purchase-control').hidden=true;busy=false;await load();$('#purchase-receipt-title').focus();}
  catch(e){uncertain=!e.status||e.status>=500;if(!uncertain){pending=null;control=null;choices.clear();if(writable)$('#purchase-control').hidden=true;invalidate('Der Vergleich muss vor einer neuen Bestätigung erneut geladen werden.',true);}error(e.message+(uncertain?' Der Ausgang ist ungewiss. Genau denselben Vorgang mit derselben Kennung erneut prüfen.':' Aktuellen Umfang laden und erneut prüfen.'));}
  finally{busy=false;freeze();}
 }
 $('#purchase-refresh').addEventListener('click',()=>{choices.clear();load();});$('#purchase-compare').addEventListener('click',()=>compare());
 $('#purchase-week-form').addEventListener('submit',async event=>{event.preventDefault();if(busy||uncertain)return;selected.clear();choices.clear();busy=true;freeze();await loadWeek();busy=false;invalidate('Woche geändert. Ganze Mahlzeiten auswählen und vollständigen Umfang vergleichen.');});
 if(writable){
  $('#purchase-confirmation').addEventListener('change',freeze);$('#purchase-save').addEventListener('click',()=>send('sync'));$('#purchase-retry').addEventListener('click',()=>send('',true));$('#purchase-control-confirm').addEventListener('click',()=>send('control'));$('#purchase-control-cancel').addEventListener('click',()=>{control=null;$('#purchase-control').hidden=true;$('#purchase-scope-title').setAttribute('tabindex','-1');$('#purchase-scope-title').focus();});
  $('#purchase-destination').addEventListener('change',()=>{target=null;$('#purchase-confirmation').checked=false;$('#purchase-destination-info').textContent='Geänderte Zielliste vor dem Speichern erneut prüfen.';freeze();});
  $('#purchase-destination-form').addEventListener('submit',async event=>{event.preventDefault();if(busy||uncertain)return;const select=$('#purchase-destination');if(!select.value||select.dataset.referenceState!=='ready'){error('Eine geladene Zielliste auswählen.');return;}busy=true;freeze();try{const result=await api('/api/v1/planning-purchases/destinations/'+select.value);if(!result.open)throw Error('Diese Liste ist eine Vorlage, archiviert oder bereits abgeschlossen. Eine andere offene Liste wählen.');if(result.generation!==preview.shopping_generation)throw Error('Einkaufsdatenstand geändert. Vollständigen Umfang erneut laden.');target=result;$('#purchase-destination-info').textContent='Geprüfte Zielliste: '+target.name+' · Stand '+target.version;}catch(e){target=null;error(e.message);}finally{busy=false;freeze();if(target)$('#purchase-confirmation').focus();}});
 }
 window.addEventListener('beforeunload',event=>{if(pending){event.preventDefault();event.returnValue='';}});
 (async()=>{await load(true);if(root.dataset.operation){try{receipt(await api('/api/v1/planning-purchases/operations/'+encodeURIComponent(root.dataset.operation)));$('#purchase-receipt-title').focus();}catch(e){error(e.message);}}})();
})();
