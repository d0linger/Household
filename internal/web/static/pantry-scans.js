((root)=>{
 'use strict';
 function camera({media,Detector,secure,video,onCode,onStatus,onActive,later=setTimeout,cancel=clearTimeout}){let sequence=0,stream,tick,lease;function stop(){++sequence;if(tick!==undefined)cancel(tick);if(lease!==undefined)cancel(lease);tick=lease=undefined;if(stream)for(const track of stream.getTracks())track.stop();stream=null;video.pause();video.srcObject=null;onActive(false);}async function start(){stop();const id=sequence;if(!secure||!media?.getUserMedia||!Detector?.getSupportedFormats){onStatus('Lokale Kameraerkennung nicht verfügbar. Barcode manuell oder per Handscanner eingeben.');return;}try{const formats=(await Detector.getSupportedFormats()).filter(f=>['ean_13','ean_8','upc_a','upc_e','code_128','code_39','itf','qr_code'].includes(f));if(id!==sequence)return;if(!formats.length)throw Error('unsupported');const detector=new Detector({formats});onActive(true);lease=later(()=>{if(id===sequence){stop();onStatus('Kamera nach einer Minute gestoppt.');}},60000);const acquired=await media.getUserMedia({audio:false,video:{facingMode:{ideal:'environment'},width:{ideal:1280,max:1280},height:{ideal:720,max:720}}});if(id!==sequence){for(const track of acquired.getTracks())track.stop();return;}stream=acquired;video.srcObject=stream;await video.play();if(id!==sequence)return;onStatus('Kamera aktiv. Treffer füllt nur das Codefeld.');async function detect(){if(id!==sequence)return;try{const found=await detector.detect(video);if(id!==sequence)return;if(found.length){const raw=found[0].rawValue;stop();onCode(raw);return;}tick=later(()=>void detect(),300);}catch{if(id===sequence){stop();onStatus('Erkennung nicht möglich. Manuelle Eingabe bleibt verfügbar.');}}}void detect();}catch{if(id===sequence){stop();onStatus('Kein Kamerazugriff. Manuelle Eingabe oder Handscanner verwenden.');}}}return {start,stop};}
 async function sendPending({device,client,lease,pending}){
  if(!pending||typeof pending.body!=='string')throw Error('Nach dem Neuladen zuerst denselben Serverentwurf suchen.');
  const saved={...pending,owner:{...pending.owner}},current=await client.context();
  if(current.can_write!==true)throw Error('Übertragung benötigt aktuelle Schreibrechte.');
  const owner=device.owner(current,await device.hash(current.auth_scope));
  if(!device.sameScope(owner,saved.owner))throw Error('Anmeldung oder Vorratsgeneration wurde geändert. Denselben Serverentwurf gesondert prüfen.');
  const body=JSON.parse(saved.body);
  if(body.version!==0||body.generation!==owner.generation||await device.hash(body)!==saved.digest)throw Error('Die ursprüngliche Anfrage stimmt nicht mehr mit ihrer gespeicherten Übergabekennung überein.');
  // One readonly transaction checks the current lease and exact draft before
  // any PUT. Renewing a device lease cannot revive an expired pending body.
  await device.pending(lease,saved);
  return client.request('/api/v1/pantry/batches/drafts/'+saved.id,{method:'PUT',body:saved.body,key:saved.key,timeout:25000});
 }
 async function readTransferredDraft(client,locator){
  if(!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(locator?.draft_id))throw Error('Gespeicherte Serverentwurfskennung prüfen.');
  // Historical status remains readable under current server authority even
  // when the local draft has expired. This does not authorize another PUT.
  const existing=await client.request('/api/v1/pantry/batches/drafts/'+locator.draft_id);
  if(existing.id!==locator.draft_id||existing.owner_id!==client.current.user_id||Number(existing.generation)!==client.current.generation||existing.kind!=='intake')throw Error('Antwort gehört nicht zur gespeicherten eigenen Einlagerungskennung.');
  return existing;
 }
 if(typeof module==='object'&&module.exports){module.exports={camera,sendPending,readTransferredDraft};return;}if(!root.document)return;
 const panel=document.querySelector('#pantry-scans');if(!panel)return;const D=root.HouseholdPantryDevice,U=root.HouseholdPantryUI,$=s=>panel.querySelector(s),{node,link}=U;
 let context=null,scope=null,lease=null,drafts=[],draft=null,busy=false,localRevision=0,pending=null,onlineRows=[],location=null,notArrived=false;
 const status=text=>$('#ps-status').textContent=text,error=message=>{const box=$('#ps-error');box.textContent=message;box.hidden=false;box.focus();};
 const privateSections=['local','editor','online','uncertain','saved'];function hide(){++localRevision;capture.stop();for(const id of privateSections)$('#ps-'+id).hidden=true;onlineRows=[];location=null;locationPicker.clear();$('#ps-review-lines').replaceChildren();$('#ps-lines').replaceChildren();$('#ps-drafts').replaceChildren();$('#ps-existing').replaceChildren();$('#ps-saved-content').replaceChildren();$('#ps-scan-form').reset();lease=null;draft=null;}
 const client=U.client(panel,{status,onLock:hide});
 const capture=camera({media:navigator.mediaDevices,Detector:root.BarcodeDetector,secure:root.isSecureContext,video:$('#ps-video'),onCode:raw=>{try{$('#ps-scan-form').elements.code.value=D.code(raw);status('Code erfasst. Menge selbst prüfen und die Zeile ausdrücklich speichern.');$('#ps-scan-form').elements.code.focus();}catch(e){error(e.message);}},onStatus:text=>$('#ps-camera-status').textContent=text,onActive:active=>{$('#ps-video').hidden=!active;$('#ps-camera-start').disabled=active;$('#ps-camera-stop').disabled=!active;}});
 const locationPicker=U.picker($('#ps-location'),'Ziel-Lagerort','pantry/locations');
 function locks(){
  panel.setAttribute('aria-busy',String(busy));
  $('#ps-scan-fields').disabled=busy||!!draft?.handoff;
  $('#ps-online-fields').disabled=busy;
  for(const id of ['new','resolve','close','delete','use-location','transfer','resume','discard-old','recheck'])$('#ps-'+id).disabled=busy;
  $('#ps-delete').disabled=busy||draft?.handoff?.phase==='uncertain';
  $('#ps-retry').disabled=busy||!pending;$('#ps-recover').disabled=busy;
 }
 async function work(fn){
  if(busy)return;busy=true;locks();$('#ps-error').hidden=true;let focus;
  try{focus=await fn();}catch(errorValue){error(errorValue.message);}
  finally{busy=false;locks();}
  if(focus instanceof HTMLElement)focus.focus();
 }
 async function connect(){context=await client.context();if(!context.can_write)throw Error('Scanübernahme benötigt aktuelle Schreibrechte.');const boot=await U.raw('/api/v1/core/bootstrap');if(boot.user?.id!==context.user_id||boot.user?.household_id!==context.household_id||boot.user?.session_id!==context.session_id||boot.user?.role!==context.role||!boot.csrf)throw Error('Anmeldung hat sich geändert. Ansicht neu öffnen.');document.querySelector('meta[name="csrf-token"]').content=boot.csrf;scope=D.owner(context,await D.hash(context.auth_scope));await D.observe(scope.user,scope.household);if(!D.opted(scope.user))throw Error('Offline-Speicherung zuerst ausdrücklich in den Geräteeinstellungen aktivieren.');const current=await D.inspect();if(current.meta?.owner&&current.meta.owner.generation!==scope.generation){$('#ps-generation').hidden=false;throw Error('Frühere Vorratsgeneration ausdrücklich verwerfen.');}if(current.meta?.owner&&!D.sameScope(current.meta.owner,scope)){$('#ps-session').hidden=false;throw Error('Eigene Entwürfe in dieser neuen Sitzung ausdrücklich wiederaufnehmen.');}lease=await D.activate(scope);await loadLocal();status('Aktuelle Anmeldung und Gerätebesitz geprüft. Bestand unverändert.');}
 async function loadLocal(){const data=await D.inspect();if(data.expired)throw Error('Lokale Gerätefreigabe ist abgelaufen. Zuerst online erneut anmelden und prüfen.');if(!data.meta)throw Error('Dieses Gerät hat keine aktive Freigabe. Online anmelden und Offline-Speicherung aktivieren.');if(scope&&!D.sameScope(data.meta.owner,scope))throw Error('Gerätebesitz oder Sitzung erneut prüfen.');lease=D.sameScope(data.meta.owner,lease?.owner)?{epoch:data.meta.epoch,revision:data.meta.revision,owner:data.meta.owner}:lease;if(!lease)lease={epoch:data.meta.epoch,revision:data.meta.revision,owner:data.meta.owner};drafts=data.drafts.filter(d=>d.expires>Date.now());const count=drafts.reduce((n,d)=>n+d.lines.length,0);$('#ps-budget').textContent=count+' von 100 Scanzeilen · maximal 256 KiB · sieben Tage ab Erstellung.';const box=$('#ps-drafts');box.replaceChildren();for(const item of drafts){const row=node('article',undefined,'pe-row');row.append(node('h3',item.lines.length+' Scanzeilen'),node('p','Verfügbar bis '+new Date(item.expires).toLocaleString('de-AT')),button(item.handoff?'Übergabe prüfen':'Entwurf öffnen',()=>work(()=>open(item.id))));box.append(row);}if(!drafts.length)box.append(node('p','Noch keine lokalen Scanentwürfe. Ein neuer Entwurf verändert keinen Bestand.'));client.open($('#ps-local'));}
 const button=(text,fn)=>{const b=node('button',text);b.type='button';b.addEventListener('click',fn);return b;};
 async function apply(result,id=draft?.id){lease=result.lease;drafts=result.drafts;draft=drafts.find(d=>d.id===id)||null;}
 async function open(id){
  await D.valid(lease);notArrived=false;if(pending?.localID!==id)pending=null;
  draft=drafts.find(d=>d.id===id);if(!draft)throw Error('Entwurf ist nicht mehr verfügbar.');
  for(const kind of privateSections)client.close($('#ps-'+kind));
  if(draft.handoff?.phase==='saved'){showSaved(draft.handoff.draft_id);return;}
  if(draft.handoff){client.open($('#ps-uncertain'));$('#ps-retry').hidden=!pending;status('Gespeicherte Übergabekennung geladen. Zuerst denselben Serverentwurf suchen.');return;}
  renderLines();$('#ps-expiry').textContent='Lokal verfügbar bis '+new Date(draft.expires).toLocaleString('de-AT');
  client.open($('#ps-editor'));$('#ps-editor-title').focus();
 }
 function renderLines(){const box=$('#ps-lines');box.replaceChildren();for(const [i,item]of draft.lines.entries()){const row=node('article',undefined,'pe-row');row.append(node('h3','Scan '+(i+1)+': '+item.code),node('p',(item.quantity?item.quantity.replace('.',','):'Menge noch unbekannt')+(item.label?' · '+item.label:'')),button('Diese Zeile entfernen',()=>work(async()=>{draft={...draft,lines:draft.lines.filter(l=>l.id!==item.id)};await apply(await D.save(lease,draft));renderLines();})));box.append(row);}if(!draft.lines.length)box.append(node('p','Noch kein Code gespeichert. Der Kameratreffer allein erzeugt keine Zeile.'));}
 $('#ps-scan-form').addEventListener('submit',event=>{
  event.preventDefault();
  const form=event.currentTarget;
  void work(async()=>{
   if(!draft||draft.handoff)throw Error('Neuen lokalen Entwurf öffnen.');
   const entry=D.line({id:crypto.randomUUID(),code:form.elements.code.value,quantity:form.elements.quantity.value,label:form.elements.label.value});
   if(draft.lines.some(l=>l.code===entry.code)&&!confirm('Dieser Code ist schon vorhanden. Bewusst als weitere getrennte Zeile erfassen?'))return;
   await apply(await D.save(lease,{...draft,lines:[...draft.lines,entry]}));form.reset();renderLines();
   status('Scanzeile lokal gespeichert. Kein Bestand gebucht.');return form.elements.code;
  });
 });
 async function resolve(){capture.stop();const id=draft?.id;if(!id)throw Error('Einen lokalen Entwurf öffnen.');await connect();draft=drafts.find(d=>d.id===id);if(!draft?.lines.length)throw Error('Mindestens eine lokale Scanzeile benötigt.');await D.valid(lease);onlineRows=draft.lines.map(line=>({line,product:null,state:null,alias:null,quantity:line.quantity,unit:'',pack_id:''}));location=null;$('#ps-reviewed').checked=false;$('#ps-review-lines').replaceChildren();for(const item of onlineRows){const row=node('article',undefined,'pe-row');row.append(node('h3',item.line.code));const notice=node('p','Lokale Zuordnung wird gesucht …');row.append(notice);item.row=row;item.notice=notice;const manual=node('div');row.append(manual);item.picker=U.picker(manual,'Vorratsprodukt','pantry/products',{onSelect:choice=>{item.product=null;item.state=null;if(choice)void work(async()=>{item.product=await client.request('/api/v1/m/pantry/products/'+choice.id);item.notice.textContent='Einmalig gewählt: '+item.product.name+'. Lagerort und Menge noch prüfen.';});}});$('#ps-review-lines').append(row);try{const alias=await client.request('/api/v1/pantry/lookup?'+new URLSearchParams({code:item.line.code}));await D.valid(lease);item.alias=alias;item.product=await client.request('/api/v1/m/pantry/products/'+alias.product_id);notice.textContent='Lokale Zuordnung: '+item.product.name+(alias.pack_label?' · '+alias.pack_label:'')+'. Noch keine Bestandsprüfung.';}catch(e){if(e.status!==404)throw e;notice.textContent='Kein lokales Produkt. Vorhandenes Produkt ausdrücklich wählen oder Produktinformationen separat prüfen.';const a=link('Optionale Produktinformationen in neuem Tab prüfen','/m/pantry/food-facts?code='+encodeURIComponent(item.line.code));a.target='_blank';a.rel='noopener';row.append(a);}}client.close($('#ps-editor'));client.close($('#ps-uncertain'));client.open($('#ps-online'));$('#ps-online-title').focus();status('Zuordnungen geladen. Zielort und jede tatsächliche Menge prüfen.');}
 async function useLocation(){
  const choice=locationPicker.value();location=await client.request('/api/v1/m/pantry/locations/'+choice.id);
  if(location.archived_at)throw Error('Aktiven Ziel-Lagerort auswählen.');
  if(locationPicker.value().id!==location.id)throw Error('Geänderten Lagerort erneut ausdrücklich laden.');
  $('#ps-reviewed').checked=false;
  for(const item of onlineRows){
   const selected=item.picker.element.querySelector('select').value;
   if(!item.product||item.product.archived_at||selected&&selected!==item.product.id)throw Error('Für jeden Code die tatsächlich gewählte aktuelle Produktzuordnung laden.');
   item.state=await client.request('/api/v1/pantry/batches/intake-state?'+new URLSearchParams({product:item.product.id,location:location.id}));await D.valid(lease);
   item.controls?.remove();
   const controls=node('div',undefined,'pe-grid'),quantityLabel=node('label','Tatsächliche Menge / Packungsanzahl'),q=node('input');
   q.inputMode='decimal';q.maxLength=40;q.value=item.quantity;quantityLabel.append(q);
   const unitLabel=node('label','Einheit oder Packung'),units=node('select');
   units.append(new Option('Bitte ausdrücklich wählen',''));units.append(new Option(item.state.product.unit_code==='portion'?'Portionen':item.state.product.unit_code,'base'));
   for(const pack of item.state.packs||[])if(!pack.disabled)units.append(new Option(pack.label+' · '+pack.quantity+' '+pack.unit_code,'pack:'+pack.id));
   unitLabel.append(units);
   const dateLabel=node('label','Erhalten am'),date=node('input');date.type='date';date.value=item.line.received_on;dateLabel.append(date);
   const labelLabel=node('label','Chargenbezeichnung'),label=node('input');label.maxLength=300;label.value=item.line.label;labelLabel.append(label);
   controls.append(quantityLabel,unitLabel,dateLabel,labelLabel,node('p','Ziel: '+location.name));item.row.append(controls);
   Object.assign(item,{controls,q,units,date,label});
   item.notice.textContent=item.state.product.name+' · '+location.name+' · neue Charge, keine vorhandenen Bestände übernommen.';
  }
 }
 function input(){if(!$('#ps-reviewed').checked||!location||!onlineRows.length)throw Error('Alle aktuellen Zuordnungen und Mengen ausdrücklich bestätigen.');const lines=onlineRows.map(item=>{if(!item.state||!item.controls)throw Error('Für alle Zeilen Produkt und Lagerort erneut prüfen.');const quantity=D.decimal(item.q.value),chosen=item.units.value;if(!chosen)throw Error('Einheit oder Packung ausdrücklich wählen.');const pack=chosen.startsWith('pack:')?item.state.packs.find(p=>p.id===chosen.slice(5)&&!p.disabled):null;if(chosen!=='base'&&!pack)throw Error('Aktive Packung erneut laden.');if(!item.date.value||!item.label.value.trim())throw Error('Erhalten-am-Datum und Chargenbezeichnung für jede Zeile angeben.');return {id:item.line.id,product_id:item.state.product.id,product_version:Number(item.state.product.version),location_id:location.id,location_version:Number(item.state.location.version),lot_id:'',lot_version:0,movement_count:0,balance:'',quantity,unit:pack?'':item.state.product.unit_code,pack_id:pack?.id||'',pack_version:Number(pack?.version||0),label:item.label.value.trim(),received_on:item.date.value,best_before_on:'',use_by_on:'',opened_on:'',after_open_days:null,after_open_source:'',after_open_note:'',reason:''};});return {generation:context.generation,version:0,kind:'intake',title:'Geprüfte Scanentwürfe',location_id:'',lines};}
 async function transfer(){
  await D.valid(lease);await client.context();
  const body=input(),encoded=JSON.stringify(body),metadata=draft.handoff||{draft_id:crypto.randomUUID(),key:crypto.randomUUID(),digest:''},digest=await D.hash(body);
  await apply(await D.handoff(lease,draft.id,{...metadata,digest,phase:'uncertain'}));
  pending={id:metadata.draft_id,key:metadata.key,body:encoded,digest,localID:draft.id,owner:{...draft.owner},epoch:draft.epoch,draftRevision:draft.revision,expires:draft.expires};
  client.close($('#ps-online'));client.open($('#ps-uncertain'));await send();
 }
 async function send(){
  if(!pending)throw Error('Nach dem Neuladen zuerst den gespeicherten Serverentwurf suchen oder aktuell neu prüfen.');
  const current=pending;
  try{
   const result=await sendPending({device:D,client,lease,pending:current});await D.valid(lease);
   if(result.id!==current.id)throw Error('Antwort gehört nicht zur gespeicherten Entwurfskennung.');
   await apply(await D.handoff(lease,current.localID,{draft_id:current.id,key:current.key,digest:current.digest,phase:'saved'}));
   pending=null;showSaved(current.id);status('Privater Serverentwurf gespeichert. Bestand weiterhin unverändert.');
  }catch(e){status('Übergabe ungewiss, abgelaufen oder konfliktbehaftet. Kennung bleibt erhalten. Denselben Serverentwurf ausdrücklich suchen.');throw e;}
 }
 function showSaved(id){for(const kind of privateSections)client.close($('#ps-'+kind));$('#ps-saved-content').replaceChildren(link('Diesen Serverentwurf öffnen und separat buchen','/m/pantry/batches?draft='+id));client.open($('#ps-saved'));$('#ps-saved-title').focus();}
 async function recover(){
  const remembered=pending?{draft_id:pending.id,key:pending.key,digest:pending.digest,localID:pending.localID,owner:pending.owner}:
   draft?.handoff?{...draft.handoff,localID:draft.id,owner:draft.owner}:null;
  await connect();
  const selectedID=remembered?.localID||draft?.id,local=selectedID?drafts.find(d=>d.id===selectedID):drafts.find(d=>d.handoff?.phase==='uncertain');
  const locator=local?.handoff?{...local.handoff,localID:local.id,owner:local.owner}:remembered;
  if(!locator||!D.sameScope(locator.owner,scope))throw Error('Keine eigene Übergabekennung im aktuellen Kontext verfügbar.');
  draft=local||null;const meta=locator,live=!!local&&local.expires>Date.now();
  try{
   const existing=await readTransferredDraft(client,meta);
   const reconstructed={generation:Number(existing.generation),version:0,kind:existing.kind,title:existing.title,location_id:existing.location_id||'',lines:existing.lines};
   const match=await D.hash(reconstructed)===meta.digest,box=$('#ps-existing');
   box.replaceChildren(node('p',match?'Der vorhandene Serverentwurf stimmt mit der gespeicherten Übergabe überein.':'Unter derselben Kennung liegt ein Serverentwurf mit anderem oder späterem Stand. Diesen aktuellen Stand ausdrücklich vergleichen; es wird nichts überschrieben.'),node('p',(existing.lines?.length||0)+' Zeilen · Version '+existing.version+' · Status '+existing.state),link('Vorhandenen privaten Serverentwurf zur Prüfung öffnen','/m/pantry/batches?draft='+meta.draft_id));
   for(const row of existing.lines||[])box.append(node('p',(row.label||'Ohne Bezeichnung')+' · '+row.quantity+' '+row.unit));
   if(live&&local.expires>Date.now()){
    try{await D.valid(lease);await apply(await D.handoff(lease,local.id,{draft_id:meta.draft_id,key:meta.key,digest:meta.digest,phase:'saved'}));}
    catch(error){if(local.expires>Date.now())throw error;draft=null;}
   }
   pending=null;notArrived=false;
   client.open($('#ps-uncertain'));$('#ps-recheck').hidden=true;$('#ps-retry').hidden=true;
   status('Vorhandener Serverentwurf gefunden. Aktuellen Inhalt vor jeder Buchung separat prüfen.');
  }catch(error){
   if(error.status!==404)throw error;
   notArrived=live;client.open($('#ps-uncertain'));$('#ps-recheck').hidden=!live;$('#ps-retry').hidden=!live||!pending||pending.draftRevision!==local.revision||pending.epoch!==local.epoch;
   status(live?'Unter derselben Kennung noch kein Serverentwurf sichtbar. Erneut suchen oder ausdrücklich neu prüfen; Kennung und Schlüssel bleiben erhalten.':'Unter derselben Kennung noch kein Serverentwurf sichtbar. Der lokale Entwurf ist abgelaufen oder entfernt; nur weiter nachsehen, keine alte Anfrage übertragen.');
  }
 }
 $('#ps-connect').addEventListener('click',()=>void work(connect));$('#ps-open-local').addEventListener('click',()=>void work(async()=>{if(navigator.onLine){await connect();return;}scope=null;await loadLocal();status('Lokale Entwürfe geöffnet. Serveranmeldung und Bestände sind offline nicht bestätigt.');}));
 $('#ps-resume').addEventListener('click',()=>void work(async()=>{if(!scope)throw Error('Zuerst aktuelle Anmeldung prüfen.');lease=await D.activate(scope,{resume:true});$('#ps-session').hidden=true;await loadLocal();}));$('#ps-discard-old').addEventListener('click',()=>void work(async()=>{if(!scope||!confirm('Alle alten lokalen Entwürfe endgültig verwerfen?'))return;lease=await D.activate(scope,{discard:true});$('#ps-generation').hidden=true;await loadLocal();}));
 $('#ps-new').addEventListener('click',()=>void work(async()=>{if(!lease)throw Error('Gerätebesitz zuerst prüfen.');const result=await D.create(lease);await apply(result,result.drafts.at(-1).id);await open(draft.id);}));$('#ps-close').addEventListener('click',()=>void work(async()=>{capture.stop();client.close($('#ps-editor'));await loadLocal();}));$('#ps-delete').addEventListener('click',()=>void work(async()=>{if(confirm('Diesen lokalen Entwurf endgültig verwerfen?')){await apply(await D.remove(lease,draft.id));client.close($('#ps-editor'));await loadLocal();}}));
 $('#ps-resolve').addEventListener('click',()=>void work(resolve));$('#ps-use-location').addEventListener('click',()=>void work(useLocation));$('#ps-transfer').addEventListener('click',()=>void work(transfer));$('#ps-retry').addEventListener('click',()=>void work(send));$('#ps-recover').addEventListener('click',()=>void work(recover));$('#ps-recheck').addEventListener('click',()=>void work(async()=>{if(!notArrived)throw Error('Zuerst denselben Serverentwurf suchen.');await resolve();status('Neue Prüfung behält die frühere Entwurfskennung und denselben Schlüssel. Ein vorhandener anderer Stand führt zum Vergleich statt Überschreiben.');}));
 $('#ps-online-fields').addEventListener('input',event=>{if(event.target.id!=='ps-reviewed')$('#ps-reviewed').checked=false;});
 $('#ps-online-fields').addEventListener('change',event=>{if(event.target.id!=='ps-reviewed')$('#ps-reviewed').checked=false;});
 $('#ps-saved-list').addEventListener('click',()=>void work(async()=>{client.close($('#ps-saved'));await loadLocal();}));
 $('#ps-saved-delete').addEventListener('click',()=>void work(async()=>{if(!draft||!confirm('Nur die lokale übertragene Kopie löschen? Der Serverentwurf und Bestand bleiben erhalten.'))return;await apply(await D.remove(lease,draft.id));client.close($('#ps-saved'));await loadLocal();}));
 $('#ps-reset').addEventListener('click',()=>void work(async()=>{
  if(!confirm('Alle lokalen Scanzeilen einschließlich ungeklärter Übergabekennungen löschen? Vorhandene private Serverentwürfe und Bestand bleiben erhalten und müssen dort geprüft werden.'))return;
  await D.reset();hide();pending=null;status('Lokale Scandaten gelöscht. Für neue Entwürfe aktuelle Anmeldung erneut prüfen.');
 }));
 $('#ps-camera-start').addEventListener('click',()=>{if(!busy&&lease&&draft&&!draft.handoff)void capture.start();});$('#ps-camera-stop').addEventListener('click',capture.stop);$('#ps-camera').addEventListener('toggle',e=>{if(!e.currentTarget.open)capture.stop();});root.addEventListener('pagehide',capture.stop);document.addEventListener('visibilitychange',()=>{if(document.hidden)capture.stop();});root.addEventListener('household-pantry-lock',event=>{capture.stop();if(event.detail?.reason!=='visibility')hide();});root.addEventListener('household-pantry-device-change',()=>{hide();status('Lokaler Entwurfsstand wurde in einer anderen Registerkarte geändert. Eigene Entwürfe ausdrücklich neu öffnen.');});root.addEventListener('storage',event=>{if(event.key==='household-offline-owner'){hide();status('Gerätefreigabe geändert. Ansicht gesperrt.');}});
 status('Offline nur lokale Entwürfe. Nach Verbindung aktuelle Anmeldung ausdrücklich prüfen.');locks();
})(globalThis);
