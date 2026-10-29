((root)=>{
 'use strict';const D=typeof module==='object'&&module.exports?require('./pantry-device.js'):root.HouseholdPantryDevice;
 function total(lines,cap=''){if(!lines.length||lines.length>20)throw Error('Ein bis 20 Behälter ausdrücklich angeben.');let sum=0n;const seen=new Set();for(const line of lines){if(seen.has(line.id))throw Error('Behälterkennung ist doppelt.');seen.add(line.id);sum+=BigInt(D.decimal(line.quantity).replace('.',''));}if(sum>=10n**18n)throw Error('Gemeinsame Restmenge überschreitet den unterstützten Bereich.');if(cap&&sum>BigInt(D.decimal(cap).replace('.','')))throw Error('Restmenge übersteigt die bekannte tatsächlich zubereitete Portionsmenge.');return (sum/1000000n)+'.'+(sum%1000000n).toString().padStart(6,'0');}
 if(typeof module==='object'&&module.exports){module.exports={total};return;}if(!root.document)return;const panel=document.querySelector('#pantry-leftovers');if(!panel)return;const U=root.HouseholdPantryUI,{node,link}=U,$=s=>panel.querySelector(s),base='/api/v1/pantry/leftovers';let busy=false,context=null,sourceView=null,product=null,containers=[],historyCursor='',historyItems=new Map(),sourceID='';
 const status=text=>$('#pl-status').textContent=text,error=text=>{const n=$('#pl-error');n.textContent=text;n.hidden=false;n.focus();};
 const client=U.client(panel,{status,onLock:()=>{
  $('#pl-form').reset();
  for(const id of ['containers','review-content','receipt-content','detail-content'])$('#pl-'+id).replaceChildren();
  recipePicker.clear();productPicker.clear();containers=[];sourceView=product=null;
 }});
 const recipePicker=U.picker($('#pl-recipe-picker'),'Rezept','recipes/recipes');
 const productPicker=U.picker($('#pl-product-picker'),'Portionsprodukt','pantry/products',{onSelect:choice=>{
  product=null;
  if(choice)void work(async()=>{
   const value=await client.request('/api/v1/m/pantry/products/'+choice.id);
   if(productPicker.value().id!==value.id)return;
   if(value.archived_at||value.unit_code!=='portion')throw Error('Ein aktives Produkt mit Basiseinheit Portion auswählen.');
   product=value;$('#pl-product-state').textContent=value.name+' · Portionen · Produktstand '+value.version;
  });
 }});
 function showReceipt(value){if(!value)throw Error('Belegantwort fehlt.');client.close($('#pl-uncertain'));client.close($('#pl-review'));const box=$('#pl-receipt-content');box.replaceChildren();if(value.details_available===false)box.append(node('p','Vorgang ist bereits angenommen. Details sind in der aktuellen Vorratsgeneration nicht verfügbar; keine neue Buchung wurde ausgelöst.'));else{box.append(node('p',(value.name||'Reste')+' · '+value.total_portions+' Portionen · zubereitet am '+value.prepared_on),node('p','Gebucht am '+new Date(value.created_at).toLocaleString('de-AT')));for(const line of value.lines||[]){const row=node('p',line.label+' · '+line.quantity+' Portionen · '+line.location+' ');if(line.lot_id)row.append(link('Zubereitungsangaben dieser Restcharge öffnen','/m/pantry/leftovers/'+line.lot_id),document.createTextNode(' · '),link('Charge und aktuelle Bestandsaktionen öffnen','/m/pantry/consume/'+line.lot_id));box.append(row);}}client.open($('#pl-receipt'));$('#pl-new').hidden=false;$('#pl-receipt-title').focus();status('Gespeicherter Zugangsbeleg geladen. Zutaten wurden nicht erneut entnommen.');}
 const flow=U.operation({kind:'leftovers',base,client,status,onReceipt:showReceipt,onRefused:()=>{client.close($('#pl-uncertain'));$('#pl-new').hidden=false;status('Vorgang verbindlich abgebrochen. Unter dieser Kennung wird nichts eingelagert.');},onUncertain:()=>{client.close($('#pl-editor'));client.close($('#pl-review'));client.open($('#pl-uncertain'));},onReview:value=>{const after=value.after||{},box=$('#pl-review-content');box.replaceChildren(node('p',after.name+' · insgesamt '+after.total_portions+' Portionen'),node('p','Zubereitet am '+after.prepared_on+' · '+(after.portions_unknown?'Zubereitete Gesamtmenge ausdrücklich unbekannt':'Zubereitet: '+after.prepared_portions+' Portionen')));for(const row of after.containers||[])box.append(node('p',row.label+' · '+row.quantity+' Portionen · '+(row.location||row.location_id)+' · erhalten am '+row.received_on+' · zu verbrauchen bis '+(row.use_by_on||'unbekannt')+' · mindestens haltbar bis '+(row.best_before_on||'unbekannt')));for(const effect of value.effects||[])box.append(node('p',typeof effect==='string'?effect:'Neue Charge mit positivem Zugang'));$('#pl-expiry').textContent='Prüfvorschlag gültig bis '+new Date(value.expires*1000).toLocaleTimeString('de-AT');client.close($('#pl-editor'));client.open($('#pl-review'));$('#pl-review-title').focus();}});
 function locks(){panel.setAttribute('aria-busy',String(busy));$('#pl-fields').disabled=busy||flow.blocked||flow.terminal||!!panel.dataset.lot;for(const id of ['confirm','edit','refresh','check','abandon','new'])$('#pl-'+id).disabled=busy;}
 async function work(fn){if(busy)return;busy=true;locks();$('#pl-error').hidden=true;try{await fn();}catch(e){error(e.message);}finally{busy=false;locks();}}
 function changed(){if(flow.blocked||flow.terminal)return;flow.edit();client.close($('#pl-review'));}
 async function loadSource(){
  const kind=$('#pl-source-kind').value;
  if(kind==='manual')sourceID='';
  else if(kind==='cooking'){
   sourceID=$('#pl-history').value||sourceID;
   if(!sourceID)throw Error('Eine abgeschlossene Zubereitung auswählen.');
  }else{
   const choice=recipePicker.value(),source=await client.request('/api/v1/cooking/source/'+choice.id);
   sourceID=source.source_version_id;
  }
  sourceView=await client.request(base+'/context?'+new URLSearchParams({source_kind:kind,source_id:sourceID}));
  if(sourceView.already_recorded){sourceView=null;throw Error('Diese bestätigte Zubereitung besitzt bereits eine Resteeinlagerung. Vorhandene Chargen und Beleg prüfen; nicht erneut buchen.');}
  const source=sourceView.source,known=kind==='cooking';
  $('#pl-source').textContent=kind==='manual'?'Manuelle Einlagerung ohne Rezeptbezug.':source.title+(source.actual_servings?' · tatsächlich gekocht: '+source.actual_servings+' Portionen':' · gesamte Zubereitungsmenge hier selbst angeben');
  $('#pl-use-name').hidden=kind==='manual';
  $('#pl-produced').disabled=false;$('#pl-produced').readOnly=known;
  $('#pl-unknown').disabled=known;$('#pl-unknown').checked=false;
  if(known){$('#pl-produced').value=source.actual_servings;$('#pl-prepared').value=source.cooked_on;$('#pl-prepared').readOnly=true;}
  else{$('#pl-produced').value='';$('#pl-prepared').readOnly=false;}
  changed();
 }
 function makeContainer(){if(containers.length>=20)throw Error('Höchstens 20 Behälter pro Einlagerung.');const line={id:crypto.randomUUID()},row=node('article',undefined,'pe-row'),heading=node('h3','Behälter '+(containers.length+1));row.append(heading);const grid=node('div',undefined,'pe-grid');for(const [key,title,type]of [['label','Eigene Behälterbezeichnung','text'],['quantity','Tatsächlich übrige Portionen','text'],['received_on','Eingelagert / erhalten am','date'],['best_before_on','Mindestens haltbar bis · leer = unbekannt','date'],['use_by_on','Zu verbrauchen bis · leer = unbekannt','date']]){const label=node('label',title),input=node('input');input.type=type;input.maxLength=key==='label'?200:40;if(key==='quantity')input.inputMode='decimal';input.required=['label','quantity','received_on'].includes(key);input.addEventListener('input',()=>{changed();updateTotal();});label.append(input);grid.append(label);line[key]=input;}row.append(grid);const place=node('div');row.append(place);line.picker=U.picker(place,'Aktiver Lagerort','pantry/locations');const remove=node('button','Diesen Behälter entfernen');remove.type='button';remove.addEventListener('click',()=>{if(containers.length===1){error('Mindestens einen Behälter behalten.');return;}containers=containers.filter(c=>c.id!==line.id);row.remove();changed();updateTotal();$('#pl-add').focus();});row.append(remove);line.row=row;containers.push(line);$('#pl-containers').append(row);updateTotal();}
 function updateTotal(){try{$('#pl-total').textContent='Gemeinsam übrig: '+total(containers.map(c=>({id:c.id,quantity:c.quantity.value})),!$('#pl-unknown').checked?$('#pl-produced').value:'').replace('.',',')+' Portionen.';}catch(e){$('#pl-total').textContent='Noch offen: '+e.message;}}
 async function input(){if(!sourceView)throw Error('Bezug zuerst aktuell prüfen.');const kind=$('#pl-source-kind').value;if(sourceView.source.kind!==kind)throw Error('Geänderten Bezug ausdrücklich laden.');if($('#pl-existing').checked&&!product)throw Error('Vorhandenes Portionsprodukt aktuell auswählen.');const rows=[];for(const c of containers){const choice=c.picker.value(),location=await client.request('/api/v1/m/pantry/locations/'+choice.id);if(location.archived_at)throw Error('Für jeden Behälter einen aktiven Lagerort wählen.');rows.push({id:c.id,label:c.label.value.trim(),quantity:D.decimal(c.quantity.value),location_id:location.id,location_version:Number(location.version),received_on:c.received_on.value,best_before_on:c.best_before_on.value,use_by_on:c.use_by_on.value});}const unknown=$('#pl-unknown').checked,cap=unknown?'':D.decimal($('#pl-produced').value);total(rows,cap);return {generation:Number(sourceView.generation),recipes_generation:kind==='manual'?0:Number(sourceView.recipes_generation),source_kind:kind,source_id:kind==='manual'?'':sourceID,source_version_id:sourceView.source.source_version_id||'',product_id:$('#pl-existing').checked?product.id:'',product_version:$('#pl-existing').checked?Number(product.version):0,name:$('#pl-name').value.trim(),prepared_on:$('#pl-prepared').value,prepared_portions:cap,portions_unknown:unknown,containers:rows,fingerprint:''};}
 async function detail(){
  const id=panel.dataset.lot;
  $('#pl-title').textContent='Zubereitungsangaben zur Charge';
  $('#pl-intro').textContent='Eigene bestätigte Angaben bleiben Pantry-Daten. Der aktuelle Bestand folgt dem unveränderlichen Journal; historische Zugänge sind keine erneute Einlagerung.';
  const box=$('#pl-detail-content');box.replaceChildren();
  try{
   const value=await client.request('/api/v1/pantry/lots/'+encodeURIComponent(id)+'/prepared'),lot=value.lot,prepared=value.prepared;
   box.append(node('p','Zubereitetes Gericht: '+prepared.dish_name),
    node('p',(lot.product_name||'Portionsprodukt')+' · '+(lot.label||prepared.initial_label||'Charge')),
    node('p','Aktueller Lagerort: '+lot.location+' · aktueller Bestand: '+lot.stock+' Portionen'),
    node('p','Zubereitet am '+prepared.prepared_on));
   if(prepared.transferred){
    box.append(node('p','Diese Charge entstand durch Umlagerung. Anfänglich hierher übertragen: '+prepared.initial_portions+' Portionen.'),
     node('p','Ursprünglich aus dieser Zubereitung eingelagert: '+prepared.root_initial_portions+' Portionen. Die Umlagerung erzeugt keinen zweiten Zubereitungszugang.'));
   }else box.append(node('p','Ursprünglicher Zugang dieser Charge: '+prepared.initial_portions+' Portionen'));
   box.append(node('p','Damals insgesamt zubereitet: '+(prepared.portions_unknown?'ausdrücklich unbekannt':prepared.prepared_portions+' Portionen')),
    node('p','Ursprüngliche Einlagerungsangaben: erhalten am '+prepared.received_on+' · zu verbrauchen bis '+(prepared.use_by_on||'unbekannt')+' · mindestens haltbar bis '+(prepared.best_before_on||'unbekannt')));
   if(value.source?.available)box.append(node('p','Aktuell erlaubter Bezug: '+value.source.title));
   else if(prepared.source_kind!=='manual')box.append(node('p','Optionaler Rezept- oder Kochbezug ist aktuell nicht verfügbar. Eigene Zubereitungsangaben bleiben erhalten.'));
   box.append(link('Vollständige Charge und Journal öffnen','/m/pantry/lots/'+id));
   if(context.can_write)box.append(document.createTextNode(' · '),link('Verbrauch und weitere Bestandsaktionen prüfen','/m/pantry/consume/'+id));
   status('Eigene Zubereitungsdaten und aktuellen Journalbestand geladen.');
  }catch(error){
   if(error.status!==404)throw error;
   box.append(node('p','Für diese Charge sind keine aktuell verfügbaren Zubereitungsangaben gespeichert.'),link('Normale Chargendetails öffnen','/m/pantry/lots/'+id));
   status('Zubereitungsbezug nicht verfügbar; es wurde nichts verändert.');
  }
  client.open($('#pl-detail'));
 }
 async function initialize(){
  context=await client.context();
  if(panel.dataset.lot){await detail();return;}
  if(!context.can_write)throw Error('Einlagerung benötigt aktuelle Schreibrechte.');
  if(await flow.restore()){$('#pl-new').hidden=!flow.retired;return;}
  sourceView=await client.request(base+'/context');
  if(!containers.length)makeContainer();client.open($('#pl-editor'));
  const params=new URLSearchParams(location.search),kind=params.get('source_kind'),id=params.get('source_id');
  if(kind==='cooking'&&id){
   $('#pl-source-kind').value='cooking';sourceID=id;
   $('#pl-history').append(new Option('Verknüpfte bestätigte Zubereitung',id));$('#pl-history').value=id;
   $('#pl-cooking-picker').hidden=false;await loadSource();
  }
  status('Aktuellen Vorratskontext geladen. Restmengen bleiben bis zur Eingabe unbekannt.');
 }
 $('#pl-form').addEventListener('submit',event=>{event.preventDefault();if(!event.currentTarget.reportValidity())return;void work(async()=>{await client.context();await flow.review(await input());});});$('#pl-form').addEventListener('input',changed);$('#pl-add').addEventListener('click',()=>{try{makeContainer();changed();containers.at(-1).label.focus();}catch(e){error(e.message);}});$('#pl-source-kind').addEventListener('change',()=>{sourceView=null;sourceID='';$('#pl-cooking-picker').hidden=$('#pl-source-kind').value!=='cooking';$('#pl-recipe-picker').hidden=$('#pl-source-kind').value!=='recipe_version';$('#pl-source').textContent='Gewählten Bezug ausdrücklich neu prüfen.';changed();});$('#pl-source-load').addEventListener('click',()=>void work(loadSource));$('#pl-use-name').addEventListener('click',()=>{$('#pl-name').value=sourceView?.source?.title||'';changed();});$('#pl-unknown').addEventListener('change',()=>{$('#pl-produced').disabled=$('#pl-unknown').checked;updateTotal();});$('#pl-existing').addEventListener('change',()=>{$('#pl-product-picker').hidden=!$('#pl-existing').checked;if(!$('#pl-existing').checked)product=null;changed();});
 async function history(append){const value=await client.request('/api/v1/cooking/history'+(append&&historyCursor?'?before='+encodeURIComponent(historyCursor):''));if(!append){historyItems.clear();$('#pl-history').replaceChildren(new Option('Bitte ausdrücklich auswählen',''));}for(const row of value.items||[]){historyItems.set(row.id,row);$('#pl-history').append(new Option(row.title+' · '+row.cooked_on+' · '+row.actual_servings+' Portionen',row.id));}historyCursor=value.next||'';$('#pl-history-more').hidden=!historyCursor;}
 $('#pl-history-load').addEventListener('click',()=>void work(()=>history(false)));$('#pl-history-more').addEventListener('click',()=>void work(()=>history(true)));$('#pl-confirm').addEventListener('click',()=>void work(()=>flow.execute()));$('#pl-edit').addEventListener('click',()=>{try{flow.edit();client.close($('#pl-review'));client.open($('#pl-editor'));}catch(e){error(e.message);}});$('#pl-check').addEventListener('click',()=>void work(()=>flow.status()));$('#pl-abandon').addEventListener('click',()=>{if(confirm('Diesen Vorgang verbindlich abbrechen? Eine bereits angenommene Einlagerung wird stattdessen angezeigt.'))void work(()=>flow.abandon());});$('#pl-new').addEventListener('click',()=>void work(async()=>{flow.fresh();$('#pl-form').reset();sourceView=product=null;containers=[];$('#pl-containers').replaceChildren();client.close($('#pl-receipt'));$('#pl-new').hidden=true;await initialize();}));$('#pl-refresh').addEventListener('click',()=>void work(initialize));root.addEventListener('beforeunload',event=>{if(flow.blocked){event.preventDefault();event.returnValue='';}});void work(initialize);
})(globalThis);
