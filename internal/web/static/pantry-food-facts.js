((root)=>{
 'use strict';const D=typeof module==='object'&&module.exports?require('./pantry-device.js'):root.HouseholdPantryDevice;
 function gtin(raw){const code=D.code(raw);if(!/^(?:\d{8}|\d{12}|\d{13}|\d{14})$/.test(code))return false;let sum=0,weight=3;for(let i=code.length-2;i>=0;i--){sum+=Number(code[i])*weight;weight=weight===3?1:3;}return (10-sum%10)%10===Number(code.at(-1));}
 function sourceURL(value){try{const url=new URL(value);return url.protocol==='https:'&&!url.port&&!url.username&&!url.password&&['world.openfoodfacts.org','world.openfoodfacts.net'].includes(url.hostname)?url.href:null;}catch{return null;}}
 if(typeof module==='object'&&module.exports){module.exports={gtin,sourceURL};return;}if(!root.document)return;const panel=document.querySelector('#pantry-food-facts');if(!panel)return;const U=root.HouseholdPantryUI,{node,link}=U,$=s=>panel.querySelector(s),base='/api/v1/pantry/extensions/food-facts';let context=null,loaded=null,product=null,receiptProduct='',busy=false;
 const status=text=>$('#pf-status').textContent=text,error=text=>{const box=$('#pf-error');box.textContent=text;box.hidden=false;box.focus();};
 const client=U.client(panel,{status,onLock:()=>{loaded=product=null;$('#pf-form').reset();$('#pf-code-form').reset();for(const id of ['source-content','selection','review-content','receipt-content','provenance','local-result'])$('#pf-'+id).replaceChildren();picker.clear();}});
 const picker=U.picker($('#pf-product-picker'),'Vorhandenes Vorratsprodukt','pantry/products',{onSelect:choice=>{
  product=null;
  if(choice)void work(async()=>{
   const value=await client.request('/api/v1/m/pantry/products/'+choice.id);
   if(picker.value().id!==value.id)return;
   if(value.archived_at)throw Error('Ein aktives Produkt wählen.');
   product=value;$('#pf-product-state').textContent=value.name+' · '+value.unit_code+' · Stand '+value.version;
  });
 }});
 function renderSource(target,source){target.replaceChildren();const rows=[['Produktname',source.name||'Nicht angegeben'],['Marke',source.brand||'Nicht angegeben'],['Packungsbeschreibung',source.pack_description||'Nicht angegeben'],['Abgerufen',source.fetched_at?new Date(source.fetched_at).toLocaleString('de-AT'):'Unbekannt'],['Gültig bis',source.expires_at?new Date(source.expires_at).toLocaleString('de-AT'):'Unbekannt'],['Datenbanklizenz',source.database_license],['Inhaltslizenz',source.contents_license]];for(const[label,value]of rows)target.append(node('p',label+': '+value));const url=sourceURL(source.url);if(url){const a=link('Quelle: Open Food Facts',url);a.target='_blank';a.rel='noopener noreferrer';target.append(a);}}
 const flow=U.operation({kind:'food-facts',base,client,status,onReceipt:value=>{if(!value?.product_id)throw Error('Produktbeleg fehlt. Ausgang weiter prüfen.');receiptProduct=value.product_id;client.close($('#pf-uncertain'));client.close($('#pf-review'));const box=$('#pf-receipt-content');box.replaceChildren(node('p',value.message||'Lokale Produktzuordnung und ausgewählte Herkunft gespeichert. Kein Bestand gebucht.'),link('Produkt und Packungen öffnen','/m/pantry/products/'+value.product_id),node('p','Ausgewählte Quellenfelder: '+(value.selected_fields||[]).map(n=>({name:'Name',brand:'Marke',pack_description:'Packungsbeschreibung'})[n]).join(', ')));if(value.source){const own=node('div');renderSource(own,value.source);box.append(own);}client.open($('#pf-receipt'));$('#pf-new').hidden=false;$('#pf-receipt-title').focus();status('Produktzuordnung gespeichert. Kein Bestand oder Haltbarkeitsdatum wurde angelegt.');},onRefused:()=>{client.close($('#pf-uncertain'));$('#pf-new').hidden=false;status('Vorgang verbindlich abgebrochen. Unter dieser Kennung wird keine neue Zuordnung angelegt.');},onUncertain:()=>{client.close($('#pf-editor'));client.close($('#pf-review'));client.close($('#pf-lookup'));client.open($('#pf-uncertain'));},onReview:value=>{const box=$('#pf-review-content');box.replaceChildren(node('p',(value.product?.name||value.input?.product_name||'Vorhandenes Produkt')+' · '+(value.product?.unit_code||value.product?.unit||value.input?.unit||'aktuelle Basiseinheit')));if(value.pack)box.append(node('p','Neue Packung: '+value.pack.label+' · '+value.pack.quantity+' '+(value.pack.unit_code||value.pack.unit)));for(const effect of value.effects||[])box.append(node('p',effect));const s=node('div');renderSource(s,value.source);box.append(s,node('p','Keine Charge, Bestandsbewegung oder Haltbarkeit.'));$('#pf-expiry').textContent='Prüfvorschlag gültig bis '+new Date(value.expires*1000).toLocaleTimeString('de-AT');client.close($('#pf-editor'));client.close($('#pf-lookup'));client.open($('#pf-review'));$('#pf-review-title').focus();}});
 function locks(){panel.setAttribute('aria-busy',String(busy));$('#pf-fields').disabled=busy||flow.blocked||flow.terminal;$('#pf-code').disabled=busy||flow.blocked||flow.terminal;for(const id of ['local','external','confirm','edit','refresh','check','abandon','new','provenance-load'])$('#pf-'+id).disabled=busy;let eligible=false;try{eligible=gtin($('#pf-code').value);}catch{}$('#pf-external').disabled=busy||flow.blocked||flow.terminal||!eligible||loaded?.state==='local';}
 async function work(fn){if(busy)return;busy=true;locks();$('#pf-error').hidden=true;try{await fn();}catch(e){error(e.message);}finally{busy=false;locks();}}
 function selections(){return [...$('#pf-selection').querySelectorAll('input:checked')].map(input=>input.value);}
 async function lookup(external){if(flow.blocked||flow.terminal)throw Error('Bisherigen Vorgang zuerst klären.');await client.context();const code=D.code($('#pf-code').value);if(external&&!gtin(code))throw Error('Interne oder ungeeignete Codes bleiben lokal.');loaded=await client.request(base+'/lookup',{method:'POST',body:{generation:client.current.generation,code,allow_external:external},timeout:12000});client.close($('#pf-editor'));client.close($('#pf-source'));$('#pf-local-result').replaceChildren();$('#pf-state').textContent=(loaded.message||({local:'Eigene Zuordnung vorhanden.',found:'Unbestätigte Produktangaben gefunden.',not_found:'Kein Produkt gefunden.',stale:'Gespeicherte Quelle ist veraltet und kann nicht übernommen werden.',limited:'Abfragebudget erreicht. Später ausdrücklich erneut versuchen.',disabled:'Integration ausgeschaltet.',not_configured:'Integration noch nicht konfiguriert.',unsupported:'Dieser Haushaltscode bleibt lokal.',error:'Externe Produktabfrage derzeit nicht verfügbar.'})[loaded.state]||'Quelle erneut prüfen.')+(loaded.retry_at?' Frühestens erneut: '+new Date(loaded.retry_at).toLocaleString('de-AT'):'')+(loaded.from_cache?' · lokaler Cache':'');
  if(loaded.state==='local'){const row=loaded.local;$('#pf-local-result').append(node('p',row.name+(row.pack_label?' · '+row.pack_label:'')),link('Eigene Chargen und Produktzuordnung öffnen','/m/pantry/priority?product='+row.product_id));status('Eigene Zuordnung hat Vorrang. Nichts wurde überschrieben.');return;}
  if(loaded.source){renderSource($('#pf-source-content'),loaded.source);client.open($('#pf-source'));}if(loaded.state!=='found'||loaded.stale){status('Keine aktuell übernehmbare Quelle. Eingabe bleibt erhalten.');return;}
  const choices=$('#pf-selection');choices.replaceChildren();for(const[name,title]of [['name','Produktname'],['brand','Marke'],['pack_description','Packungsbeschreibung']]){const label=node('label',undefined,'check-label'),input=node('input');input.type='checkbox';input.value=name;input.disabled=!loaded.source[name];label.append(input,document.createTextNode(title+': '+(loaded.source[name]||'nicht angegeben')));choices.append(label);}$('#pf-form').reset();product=null;picker.clear();$('#pf-product-picker').hidden=true;$('#pf-pack-fields').hidden=true;$('#pf-new-fields').hidden=false;client.open($('#pf-editor'));status('Quellenfelder und lokale Produktwerte einzeln prüfen. Noch keine Produktzuordnung gespeichert. Bestand unverändert.');}
 function input(){if(!loaded?.source||loaded.state!=='found'||loaded.stale)throw Error('Eine aktuelle Quelle ausdrücklich laden.');const selected=selections();if(!selected.length)throw Error('Mindestens ein verfügbares Quellenfeld ausdrücklich auswählen.');const existing=$('#pf-mode').value==='existing',pack=$('#pf-pack').checked;if(existing&&!product)throw Error('Vorhandenes aktives Produkt aktuell wählen.');return {generation:client.current.generation,code:loaded.code,provider:loaded.source.provider,cache_fingerprint:loaded.source.fingerprint,selected_fields:selected,product_id:existing?product.id:'',product_version:existing?Number(product.version):0,product_name:existing?'':$('#pf-name').value.trim(),unit:existing?'':$('#pf-unit').value,integer_quantities:existing?false:$('#pf-integers').checked,create_pack:pack,pack_label:pack?$('#pf-pack-label').value.trim():'',pack_quantity:pack?D.decimal($('#pf-pack-quantity').value):'',pack_unit:pack?$('#pf-pack-unit').value:''};}
 async function initialize(){
  context=await client.context();
  if(!context.can_write)throw Error('Optionale Produktzuordnung benötigt aktuelle Schreibrechte.');
  if(await flow.restore()){$('#pf-new').hidden=!flow.retired;return;}
  client.open($('#pf-lookup'));
  const code=new URLSearchParams(location.search).get('code');if(code&&!$('#pf-code').value)$('#pf-code').value=code;
  status('Eigene Zuordnung zuerst prüfen. Externe Abfrage erfolgt ausschließlich nach deinem Klick.');
 }
 $('#pf-code-form').addEventListener('submit',event=>{event.preventDefault();void work(()=>lookup(false));});
 $('#pf-external').addEventListener('click',()=>void work(()=>lookup(true)));
 $('#pf-code').addEventListener('input',()=>{loaded=null;client.close($('#pf-editor'));client.close($('#pf-source'));flow.edit();locks();});
 $('#pf-mode').addEventListener('change',()=>{$('#pf-product-picker').hidden=$('#pf-mode').value!=='existing';$('#pf-new-fields').hidden=$('#pf-mode').value==='existing';product=null;flow.edit();});
 $('#pf-pack').addEventListener('change',()=>{$('#pf-pack-fields').hidden=!$('#pf-pack').checked;flow.edit();});
 $('#pf-use-name').addEventListener('click',()=>{
  if(!selections().includes('name')){error('Quellennamen zuerst ausdrücklich auswählen.');return;}
  $('#pf-name').value=loaded.source.name;flow.edit();
 });
 $('#pf-form').addEventListener('input',()=>flow.edit());
 $('#pf-form').addEventListener('submit',event=>{event.preventDefault();void work(async()=>{await client.context();await flow.review(input());});});
 $('#pf-confirm').addEventListener('click',()=>void work(()=>flow.execute()));
 $('#pf-edit').addEventListener('click',()=>{try{flow.edit();client.close($('#pf-review'));client.open($('#pf-editor'));client.open($('#pf-lookup'));}catch(e){error(e.message);}});
 $('#pf-check').addEventListener('click',()=>void work(()=>flow.status()));
 $('#pf-abandon').addEventListener('click',()=>{if(confirm('Diesen Vorgang verbindlich abbrechen? Eine schon angenommene Zuordnung wird stattdessen angezeigt.'))void work(()=>flow.abandon());});
 $('#pf-new').addEventListener('click',()=>void work(async()=>{
  flow.fresh();loaded=product=null;$('#pf-form').reset();client.close($('#pf-receipt'));client.close($('#pf-source'));$('#pf-new').hidden=true;await initialize();
 }));
 $('#pf-refresh').addEventListener('click',()=>void work(initialize));
 $('#pf-provenance-load').addEventListener('click',()=>void work(async()=>{
  if(!receiptProduct)throw Error('Produktbeleg zuerst laden.');
  const result=await client.request(base+'/provenance/'+receiptProduct+'?generation='+client.current.generation);
  if(!Array.isArray(result.sources)||typeof result.more!=='boolean')throw Error('Herkunftsübersicht konnte nicht vollständig gelesen werden.');
  const box=$('#pf-provenance');box.replaceChildren();
  for(const source of result.sources){const section=node('section',undefined,'pe-row');renderSource(section,source);box.append(section);}
  if(!result.sources.length)box.append(node('p','Keine aktuell verfügbare Herkunft.'));
  if(result.more)box.append(node('p','Die letzten 20 Herkunftseinträge werden angezeigt. Weitere ältere Einträge sind vorhanden. Dies ist keine vollständige Historie.'));
 }));
 root.addEventListener('beforeunload',event=>{if(flow.blocked){event.preventDefault();event.returnValue='';}});
 void work(initialize);
})(globalThis);
