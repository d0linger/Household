const _householdText_shopping_extension_ui_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root)=>{
 'use strict';
 const UUID=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
 const LIMITS=Object.freeze({lists:10,items:1000,bytes:2097152,age:7*86400000});
 const error=(code,message)=>Object.assign(Error(message),{code});
 const object=value=>!!value&&typeof value==='object'&&!Array.isArray(value);
 function keys(value,allowed,required=allowed){if(!object(value)||Object.keys(value).some(key=>!allowed.includes(key))||required.some(key=>!Object.hasOwn(value,key)))throw error('FORMAT',_householdText_shopping_extension_ui_js("Unbekanntes Datenformat. Aktuellen Stand erneut prüfen."));return value;}
 function text(value,max=LIMITS.bytes,nullable=false){if(nullable&&value===null)return value;if(typeof value!=='string'||new TextEncoder().encode(value).length>max||value.includes('\0'))throw error('FORMAT',_householdText_shopping_extension_ui_js("Textangabe hat ein unbekanntes Format."));return value;}
 const version=value=>typeof value==='string'&&/^(0|[1-9][0-9]{0,29})$/.test(value);
 const bytes=value=>new TextEncoder().encode(typeof value==='string'?value:goJSON(value)).length;
 const canonical=value=>Array.isArray(value)?'['+value.map(canonical).join(',')+']':object(value)?'{'+Object.keys(value).sort().map(key=>JSON.stringify(key)+':'+canonical(value[key])).join(',')+'}':JSON.stringify(value);
 function goJSON(value){return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g,ch=>'\\u'+ch.charCodeAt(0).toString(16).padStart(4,'0'));}
 async function hash(value){const encoded=new TextEncoder().encode(typeof value==='string'?value:canonical(value));return [...new Uint8Array(await root.crypto.subtle.digest('SHA-256',encoded))].map(n=>n.toString(16).padStart(2,'0')).join('');}
 function context(value){
  keys(value,['user_id','household_id','generation','session_scope','auth_scope','role','can_write','offline_allowed','server_time']);
  if(!UUID.test(value.user_id)||!UUID.test(value.household_id)||!Number.isSafeInteger(value.generation)||value.generation<1||!['admin','editor','reader'].includes(value.role)||typeof value.can_write!=='boolean'||typeof value.offline_allowed!=='boolean'||!Number.isFinite(Date.parse(value.server_time)))throw error('AUTH',_householdText_shopping_extension_ui_js("Aktueller Kontokontext ist unvollständig."));
  for(const field of ['session_scope','auth_scope'])if(!text(value[field],256)||/[\s\u0000-\u001f]/.test(value[field]))throw error('AUTH',_householdText_shopping_extension_ui_js("Aktuelle Gerätefreigabe erneut prüfen."));
  return value;
 }
 function scope(value){context(value);return {user_id:value.user_id,household_id:value.household_id,generation:value.generation,session_scope:value.session_scope,auth_scope:value.auth_scope};}
 const sameOwner=(a,b)=>!!a&&!!b&&a.user_id===b.user_id&&a.household_id===b.household_id;
 const sameScope=(a,b)=>sameOwner(a,b)&&a.generation===b.generation&&a.session_scope===b.session_scope&&a.auth_scope===b.auth_scope;
 function validateList(list){
  keys(list,['id','version','name','archived','is_template','items']);
  if(!UUID.test(list.id)||!version(list.version)||list.version==='0'||!text(list.name)||typeof list.archived!=='boolean'||typeof list.is_template!=='boolean'||!Array.isArray(list.items)||list.items.length>LIMITS.items)throw error('FORMAT',_householdText_shopping_extension_ui_js("Gespeicherte Liste ist unvollständig."));
  const seen=new Set();
  for(const item of list.items){
   keys(item,['id','version','name','quantity','unit','group','note','checked','availability','assignee_label','product_label','pack_label','replacement_label']);
   if(!UUID.test(item.id)||seen.has(item.id)||!version(item.version)||item.version==='0'||!text(item.name)||typeof item.checked!=='boolean'||!['available','unavailable','substituted'].includes(item.availability))throw error('FORMAT',_householdText_shopping_extension_ui_js("Artikel der Lesekopie ist unvollständig."));
   seen.add(item.id);
   if(item.quantity!==null&&(typeof item.quantity!=='string'||!/^(?:0|[1-9][0-9]{0,23})(?:\.[0-9]{1,6})?$/.test(item.quantity)))throw error('FORMAT',_householdText_shopping_extension_ui_js("Exakte Mengenangabe konnte nicht gelesen werden."));
   if(item.unit!==null&&!/^[a-zA-Z0-9_-]{1,32}$/.test(item.unit))throw error('FORMAT',_householdText_shopping_extension_ui_js("Einheit konnte nicht gelesen werden."));
   for(const field of ['group','note'])text(item[field]);
   for(const field of ['assignee_label','product_label','pack_label','replacement_label'])text(item[field],LIMITS.bytes,true);
  }
  return list;
 }
 function fingerprintJSON(envelope){const copy=JSON.parse(JSON.stringify(envelope));delete copy.fingerprint;delete copy.totals.utf8_bytes;return goJSON(copy);}
 async function envelope(value,{owner,ids,now=Date.now()}={}){
  keys(value,['schema','context','captured_at','expires_at','complete','lists','totals','fingerprint']);context(value.context);
  keys(value.totals,['lists','items','utf8_bytes']);
  const captured=Date.parse(value.captured_at),expires=Date.parse(value.expires_at);
  if(value.schema!==1||value.complete!==true||!Array.isArray(value.lists)||!value.lists.length||value.lists.length>LIMITS.lists||!Number.isFinite(captured)||!Number.isFinite(expires)||expires-captured!==LIMITS.age||expires<=now||captured>now+300000||!/^[0-9a-f]{64}$/.test(value.fingerprint))throw error('EXPIRED',_householdText_shopping_extension_ui_js("Die vollständige Lesekopie ist unbekannt oder abgelaufen. Datum und Verbindung prüfen."));
  const listIDs=new Set(),itemIDs=new Set();let count=0;
  for(const list of value.lists){validateList(list);if(listIDs.has(list.id))throw error('FORMAT',_householdText_shopping_extension_ui_js("Eine Liste wurde mehrfach geliefert."));listIDs.add(list.id);for(const item of list.items){if(itemIDs.has(item.id))throw error('FORMAT',_householdText_shopping_extension_ui_js("Ein Artikel wurde mehrfach geliefert."));itemIDs.add(item.id);count++;}}
  if(count>LIMITS.items||value.totals.lists!==value.lists.length||value.totals.items!==count||!Number.isSafeInteger(value.totals.utf8_bytes)||value.totals.utf8_bytes!==bytes(goJSON(value))||value.totals.utf8_bytes>LIMITS.bytes)throw error('BUDGET',_householdText_shopping_extension_ui_js("Nur vollständige Lesekopien bis 1.000 Artikel und 2 MiB können gespeichert werden."));
  if(owner&&!sameScope(scope(value.context),owner))throw error('AUTH',_householdText_shopping_extension_ui_js("Antwort gehört nicht mehr zur aktuellen Gerätefreigabe."));
  if(ids&&(ids.length!==listIDs.size||ids.some(id=>!listIDs.has(id))))throw error('FORMAT',_householdText_shopping_extension_ui_js("Antwort enthält andere Listen als ausdrücklich gewählt."));
  if(await hash(fingerprintJSON(value))!==value.fingerprint)throw error('FORMAT',_householdText_shopping_extension_ui_js("Die gelieferte Lesekopie ist nicht vollständig überprüfbar. Neu abrufen."));
  return value;
 }
 const api={UUID,LIMITS,error,object,keys,text,version,bytes,canonical,goJSON,hash,context,scope,sameOwner,sameScope,validateList,fingerprintJSON,envelope};
 if(typeof module==='object'&&module.exports){module.exports=api;return;}
 if(!root.document)return;
 const node=(tag,value,className)=>{const result=document.createElement(tag);if(value!==undefined)result.textContent=value;if(className)result.className=className;return result;};
 const date=value=>new Intl.DateTimeFormat((globalThis.HouseholdI18n?.locale() || 'de-AT'),{dateStyle:'medium',timeStyle:'short'}).format(new Date(value));
 const quantity=value=>value===null?_householdText_shopping_extension_ui_js("Menge unbekannt"):String(value).replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.', globalThis.HouseholdI18n?.language() === 'en' ? '.' : ',');
 const unit=value=>({piece:_householdText_shopping_extension_ui_js("Stück"),portion:_householdText_shopping_extension_ui_js("Portionen")})[value]||value||_householdText_shopping_extension_ui_js("Einheit unbekannt");
 async function raw(url,{method='GET',body,timeout=15000}={}){
  if(!url.startsWith('/api/v1/')||url.startsWith('//'))throw error('REQUEST',_householdText_shopping_extension_ui_js("Anfrage ist nicht zulässig."));
  const abort=new AbortController(),timer=setTimeout(()=>abort.abort(),timeout);
  try{
   const response=await fetch(url,{method,signal:abort.signal,cache:'no-store',headers:{Accept:'application/json',...(body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]')?.content||''})},body:body===undefined?undefined:JSON.stringify(body)});
   if(!response.body)throw error('RESPONSE',_householdText_shopping_extension_ui_js("Serverantwort enthält keine lesbaren Daten."));
   const reader=response.body.getReader(),decoder=new TextDecoder('utf-8',{fatal:true}),chunks=[];let consumed=0,textBody;
   try{
    for(;;){const part=await reader.read();if(part.done)break;consumed+=part.value.byteLength;if(consumed>LIMITS.bytes+65536){abort.abort();throw error('BUDGET',_householdText_shopping_extension_ui_js("Antwort überschreitet die sichere Gerätegrenze."));}chunks.push(decoder.decode(part.value,{stream:true}));}
    chunks.push(decoder.decode());textBody=chunks.join('');
   }catch(failure){try{await reader.cancel();}catch{}throw failure;}finally{reader.releaseLock();}
   let data;try{data=JSON.parse(textBody);}catch{throw error('RESPONSE',_householdText_shopping_extension_ui_js("Serverantwort ist nicht lesbar. Aktuellen Stand gesondert prüfen."));}
   if(!response.ok)throw Object.assign(error('HTTP',typeof data.error==='string'?data.error:_householdText_shopping_extension_ui_js("Anfrage nicht bestätigt.")),{status:response.status});
   return data;
  }finally{clearTimeout(timer);}
 }
 function client(panel,{onLock=()=>{},status=()=>{}}={}){
  let current=null,revision=0,locked=false;
  function lock(){locked=true;++revision;for(const element of panel.querySelectorAll('[data-shopping-private]'))element.hidden=true;onLock();}
  async function read(url,options){try{return await raw(url,options);}catch(failure){const removed=failure.status===401||failure.status===403,paused=failure.status===404&&url==='/api/v1/shopping/extensions/context';if(removed||paused){lock();status(_householdText_shopping_extension_ui_js("Aktuelle Berechtigung fehlt. Private Shopping-Ansichten sind gesperrt."));try{const method=removed?'reset':'suspend';if(!root.HouseholdShoppingDevice?.[method])throw error('STORAGE',_householdText_shopping_extension_ui_js("Gerätebereinigung fehlt."));await root.HouseholdShoppingDevice[method]();status(removed?_householdText_shopping_extension_ui_js("Aktuelle Berechtigung fehlt. Shopping-Lesekopien auf diesem Gerät wurden gelöscht."):_householdText_shopping_extension_ui_js("Einkaufsmodul derzeit nicht verfügbar. Lesekopien sind bis zum vollständigen neuen Abruf gesperrt."));}catch{status(_householdText_shopping_extension_ui_js("Private Shopping-Ansichten sind gesperrt. Die vollständige lokale Bereinigung ist nicht bestätigt. Gerätespeicher prüfen."));failure.cleanupFailed=true;}}throw failure;}}
  async function refresh(){
   const ticket=revision;
   try{
    const result=await read('/api/v1/shopping/extensions/context');context(result.context);
    const expected= document.body.dataset;
    if((expected.user&&expected.user!==result.context.user_id)||(expected.household&&expected.household!==result.context.household_id)){lock();throw error('AUTH',_householdText_shopping_extension_ui_js("Seitenkonto und aktuelle Anmeldung stimmen nicht überein. Seite erneut öffnen."));}
    const enforced=await root.HouseholdShoppingDevice?.enforceContext(result.context);
    if(enforced?.consent===false)status(_householdText_shopping_extension_ui_js("Keine Offline-Freigabe am Server. Gespeicherte Shopping-Kopien sind gesperrt; persönliche Hinweise bleiben unabhängig davon."));
    if(enforced?.changed&&!current)locked=false;
    if(ticket!==revision&&!enforced?.changed)throw error('AUTH',_householdText_shopping_extension_ui_js("Ansicht wurde geändert. Neu öffnen."));
    if(current&&!sameScope(scope(current),scope(result.context))){lock();throw error('AUTH',_householdText_shopping_extension_ui_js("Anmeldung oder Einkaufsstand geändert. Die Ansicht erneut öffnen."));}
    if(locked)throw error('AUTH',_householdText_shopping_extension_ui_js("Gesperrte Ansicht mit aktueller Anmeldung erneut öffnen."));
    current=result.context;return result;
   }catch(failure){throw failure;}
  }
  async function request(url,options){
   if(!current||locked)throw error('AUTH',_householdText_shopping_extension_ui_js("Aktuelle Anmeldung zuerst prüfen."));
   const ticket=revision,owner=scope(current),result=await read(url,options);
   if(ticket!==revision||locked||!sameScope(owner,scope(current)))throw error('AUTH',_householdText_shopping_extension_ui_js("Verspätete Antwort wurde verworfen. Aktuell erneut prüfen."));
   return result;
  }
  async function csrf(){
   await refresh();const before=scope(current),boot=await read('/api/v1/core/bootstrap');
   if(boot.user?.id!==before.user_id||boot.user?.household_id!==before.household_id||boot.user?.role!==current.role||typeof boot.csrf!=='string'||!boot.csrf){lock();throw error('AUTH',_householdText_shopping_extension_ui_js("Anmeldung hat sich geändert. Ansicht erneut öffnen."));}
   await refresh();if(!sameScope(before,scope(current)))throw error('AUTH',_householdText_shopping_extension_ui_js("Anmeldung hat sich während der Prüfung geändert."));
   document.querySelector('meta[name="csrf-token"]').content=boot.csrf;
  }
  const changed=()=>{lock();status(_householdText_shopping_extension_ui_js("Gerätestand in einer anderen Ansicht geändert. Ansicht ausdrücklich neu öffnen."));};
  root.addEventListener('household-shopping-lock',lock);
  root.addEventListener('household-shopping-device-change',changed);
  const dispose=()=>{root.removeEventListener('household-shopping-lock',lock);root.removeEventListener('household-shopping-device-change',changed);++revision;locked=true;};
  return {refresh,request,csrf,lock,dispose,get current(){return current;},get revision(){return revision;}};
 }
 root.HouseholdShoppingUI={...api,node,date,quantity,unit,raw,client};
})(globalThis);
