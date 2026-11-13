const _householdText_shopping_device_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root)=>{
 'use strict';
 const U=typeof module==='object'&&module.exports?require('./shopping-extension-ui.js'):root.HouseholdShoppingUI;
 const DB='household-shopping-readlists',clone=value=>JSON.parse(JSON.stringify(value));
 function owner(value){U.keys(value,['user_id','household_id','generation','session_scope','auth_scope']);if(!U.UUID.test(value.user_id)||!U.UUID.test(value.household_id)||!Number.isSafeInteger(value.generation)||value.generation<1)throw U.error('FORMAT',_householdText_shopping_device_js("Unbekannter Besitzer der Lesekopie."));for(const key of ['session_scope','auth_scope'])if(!U.text(value[key],256))throw U.error('FORMAT',_householdText_shopping_device_js("Ungültige Gerätefreigabe."));return value;}
 function meta(value){U.keys(value,['key','schema','epoch','revision','owner','authorized_until']);if(value.key!=='device'||value.schema!==1||!U.UUID.test(value.epoch)||!Number.isSafeInteger(value.revision)||value.revision<0||!Number.isFinite(value.authorized_until))throw U.error('FORMAT',_householdText_shopping_device_js("Gerätestand ist unbekannt. Gespeicherte Lesekopien ausdrücklich löschen."));if(value.owner!==null)owner(value.owner);return value;}
 function row(value,device){
  U.keys(value,['schema','id','owner','epoch','revision','captured_at','expires_at','source_fingerprint','list','content_hash']);owner(value.owner);U.validateList(value.list);
  if(value.schema!==1||!U.UUID.test(value.id)||value.id!==value.list.id||value.epoch!==device.epoch||!U.sameScope(value.owner,device.owner)||!Number.isSafeInteger(value.revision)||value.revision<1||!/^[0-9a-f]{64}$/.test(value.source_fingerprint)||!/^[0-9a-f]{64}$/.test(value.content_hash)||!Number.isFinite(Date.parse(value.captured_at))||Date.parse(value.expires_at)-Date.parse(value.captured_at)!==U.LIMITS.age)throw U.error('FORMAT',_householdText_shopping_device_js("Lesekopie gehört nicht zum aktuellen Gerätestand. Nur löschen oder neu laden."));
  return value;
 }
 function budget(rows){const count=rows.reduce((n,item)=>n+item.list.items.length,0),size=U.bytes(U.goJSON(rows));if(rows.length>U.LIMITS.lists||count>U.LIMITS.items||size>U.LIMITS.bytes)throw U.error('BUDGET',_householdText_shopping_device_js("Gerätegrenze erreicht: höchstens 10 Listen, 1.000 Artikel und 2 MiB. Andere Kopien gezielt löschen."));return {lists:rows.length,items:count,utf8_bytes:size};}
 function content(value){const copy=clone(value);delete copy.content_hash;return U.canonical(copy);}
 const token=device=>({epoch:device.epoch,revision:device.revision,owner:clone(device.owner)});
 function assertToken(device,lease,{now=Date.now(),allowExpired=false}={}){meta(device);if(!lease||device.epoch!==lease.epoch||device.revision!==lease.revision||!U.sameScope(device.owner,lease.owner)||!allowExpired&&device.authorized_until<=now)throw U.error('STALE',_householdText_shopping_device_js("Gerätefreigabe oder gespeicherter Stand wurde geändert. Ansicht erneut öffnen."));}
 const pure={DB,owner,meta,row,budget,content,token,assertToken};
 if(typeof module==='object'&&module.exports){module.exports=pure;return;}
 if(!root.document||!U)return;
 let opening,serial=0,denied=false;const DENIED=DB+'-denied',AUTHORIZED=DB+'-authorized';
 const channel=typeof BroadcastChannel==='function'?new BroadcastChannel(DB):null;
 const globalOpted=user=>{try{return !!user&&localStorage.getItem('household-offline-owner')===user;}catch{return false;}};
 const denialNonce=()=>localStorage.getItem(DENIED)||'';
 const opted=user=>{try{return !denied&&denialNonce()===(localStorage.getItem(AUTHORIZED)||'')&&globalOpted(user);}catch{return false;}};
 function deny(){denied=true;try{localStorage.setItem(DENIED,crypto.randomUUID());}catch{try{localStorage.removeItem('household-offline-owner');}catch{}}}
 function invalidate(message=_householdText_shopping_device_js("Gerätestand wurde geändert.")){++serial;root.dispatchEvent(new CustomEvent('household-shopping-lock',{detail:{message}}));}
 function announce(kind){channel?.postMessage({kind});try{localStorage.setItem('household-shopping-device-event',JSON.stringify({kind,nonce:crypto.randomUUID()}));}catch{}}
 function changed(event){if(['changed','reset','lock'].includes(event?.kind)){invalidate();root.dispatchEvent(new CustomEvent('household-shopping-device-change',{detail:{kind:event.kind}}));}}
 if(channel)channel.onmessage=event=>changed(event.data);
 root.addEventListener('storage',event=>{if(event.key==='household-offline-owner'||event.key===DENIED||event.key===AUTHORIZED){invalidate();return;}if(event.key==='household-shopping-device-event')try{changed(JSON.parse(event.newValue));}catch{}});
 function database(){
  if(opening)return opening;
  opening=new Promise((resolve,reject)=>{
   if(!root.indexedDB){reject(U.error('STORAGE',_householdText_shopping_device_js("Lokaler Speicher ist nicht verfügbar.")));return;}
   let ended=false;const request=indexedDB.open(DB,1),timer=setTimeout(()=>fail(_householdText_shopping_device_js("Lokaler Speicher ist blockiert. Andere Ansichten schließen und erneut versuchen.")),5000);
   function fail(message){if(ended)return;ended=true;clearTimeout(timer);opening=undefined;reject(U.error('STORAGE',message));}
   request.onupgradeneeded=()=>{if(ended){request.transaction.abort();return;}request.result.createObjectStore('meta',{keyPath:'key'});request.result.createObjectStore('snapshots',{keyPath:'id'});};
   request.onblocked=()=>fail(_householdText_shopping_device_js("Eine andere Ansicht blockiert den Gerätespeicher."));request.onerror=()=>fail(_householdText_shopping_device_js("Gerätespeicher konnte nicht geöffnet werden."));
   request.onsuccess=()=>{const db=request.result;if(ended){db.close();return;}ended=true;clearTimeout(timer);db.onversionchange=()=>{db.close();opening=undefined;invalidate(_householdText_shopping_device_js("Speicherformat geändert. Ansicht neu öffnen."));};resolve(db);};
  });return opening;
 }
 async function transaction(mode,fn){
  const db=await database();return new Promise((resolve,reject)=>{
   let value,failure,finished=false;const tx=db.transaction(['meta','snapshots'],mode),timer=setTimeout(()=>{failure=U.error('STORAGE',_householdText_shopping_device_js("Geräteprüfung dauerte zu lange. Unbestätigten Stand nicht übernehmen."));try{tx.abort();}catch{}},10000);
   const api={meta:tx.objectStore('meta'),snapshots:tx.objectStore('snapshots'),set:result=>{value=result;},fail:error=>{failure=error;try{tx.abort();}catch{}}};
   const finish=error=>{if(finished)return;finished=true;clearTimeout(timer);if(error)reject(error);else resolve(value);};
   tx.oncomplete=()=>finish();tx.onabort=()=>finish(failure||U.error('STORAGE',_householdText_shopping_device_js("Geräteänderung wurde nicht gespeichert.")));tx.onerror=()=>{failure=U.error('STORAGE',_householdText_shopping_device_js("Gerätespeicher ist voll oder nicht verfügbar. Bisheriger vollständiger Stand bleibt erhalten."));};
   try{fn(api);}catch(error){api.fail(error);}
  });
 }
 function withData(api,fn){const request=api.meta.get('device');request.onsuccess=()=>{const all=api.snapshots.getAll(undefined,U.LIMITS.lists+1);all.onsuccess=()=>{try{if(all.result.length>U.LIMITS.lists)throw U.error('BUDGET',_householdText_shopping_device_js("Lokaler Speicher enthält mehr als zehn Listen. Gerätebereich ausdrücklich löschen."));fn(request.result,all.result);}catch(error){api.fail(error);}};};}
 function next(device){if(device.revision>=Number.MAX_SAFE_INTEGER)throw U.error('FORMAT',_householdText_shopping_device_js("Geräterevision erschöpft. Lesekopien ausdrücklich löschen."));return device.revision+1;}
 function freshMeta(){return {key:'device',schema:1,epoch:crypto.randomUUID(),revision:0,owner:null,authorized_until:0};}
 async function inspect(){
  let pruned=false;
  const result=await transaction('readwrite',api=>withData(api,(device,stored)=>{
   if(!device){if(stored.length)throw U.error('FORMAT',_householdText_shopping_device_js("Lesekopien haben keine gültige Gerätezuordnung."));api.set({meta:null,snapshots:[],totals:{lists:0,items:0,utf8_bytes:0}});return;}
   meta(device);const alive=[];
   for(const item of stored){row(item,device);if(Date.parse(item.expires_at)<=Date.now()){api.snapshots.delete(item.id);pruned=true;}else alive.push(item);}
   if(pruned){device.revision=next(device);api.meta.put(device);}
   const allowed=device.owner&&opted(device.owner.user_id)&&device.authorized_until>Date.now();
   api.set({meta:device,snapshots:allowed?alive:[],list_ids:alive.map(item=>item.id),expired:!!device.owner&&device.authorized_until<=Date.now(),totals:allowed?budget(alive):{lists:0,items:0,utf8_bytes:0}});
  }));if(pruned)announce('changed');return result;
 }
 async function prepare(context,ids,{reauthorize=false}={}){
  U.context(context);const target=U.scope(context);
  if(!context.offline_allowed||!globalOpted(target.user_id))throw U.error('CONSENT',_householdText_shopping_device_js("Offline-Speicherung zuerst ausdrücklich in den Geräteeinstellungen aktivieren."));
  if(!Array.isArray(ids)||!ids.length||ids.length>U.LIMITS.lists||ids.some(id=>!U.UUID.test(id))||new Set(ids).size!==ids.length)throw U.error('FORMAT',_householdText_shopping_device_js("Ein bis zehn unterschiedliche Listen ausdrücklich wählen."));
  const ticketSerial=serial;
  return transaction('readwrite',api=>withData(api,(existing,stored)=>{
   const device=existing||freshMeta();meta(device);if(!existing)api.meta.put(device);
   if(ticketSerial!==serial)throw U.error('STALE',_householdText_shopping_device_js("Gerätefreigabe wurde geändert."));
   if(device.owner&&!U.sameOwner(device.owner,target))throw U.error('OWNER',_householdText_shopping_device_js("Anderes Konto: gespeicherte Lesekopien zuerst vollständig löschen."));
   if(device.owner&&device.owner.generation!==target.generation)throw U.error('GENERATION',_householdText_shopping_device_js("Frühere Einkaufsgeneration. Alte Kopien ausdrücklich löschen und neu auswählen."));
   const alive=stored.map(item=>row(item,device)).filter(item=>Date.parse(item.expires_at)>Date.now()),changedSession=!!device.owner&&!U.sameScope(device.owner,target),suspended=!!device.owner&&(device.authorized_until<=Date.now()||!opted(target.user_id)),fullRefetch=changedSession||suspended;
   if(fullRefetch&&alive.length&&!reauthorize)throw Object.assign(U.error('SESSION',_householdText_shopping_device_js("Neue oder gesperrte Freigabe: alle eigenen Lesekopien ausdrücklich vollständig neu abrufen.")),{list_ids:alive.map(item=>item.id)});
   const requested=[...new Set([...ids,...(fullRefetch?alive.map(item=>item.id):[])])];if(requested.length>U.LIMITS.lists)throw U.error('BUDGET',_householdText_shopping_device_js("Für die neue Freigabe höchstens zehn Listen wählen."));
   api.set({epoch:device.epoch,revision:device.revision,owner:clone(device.owner),target_owner:target,ids:requested,full_refetch:fullRefetch,serial:ticketSerial,denial_nonce:denialNonce()});
  }));
 }
 async function save(ticket,response){
  if(!ticket||ticket.serial!==serial||ticket.denial_nonce!==denialNonce())throw U.error('STALE',_householdText_shopping_device_js("Frühere Geräteanfrage wurde verworfen."));
  const received=clone(await U.envelope(response,{owner:ticket.target_owner,ids:ticket.ids}));
  if(!globalOpted(ticket.target_owner.user_id))throw U.error('CONSENT',_householdText_shopping_device_js("Gerätefreigabe wurde aufgehoben."));
  const epoch=ticket.full_refetch?crypto.randomUUID():ticket.epoch,prepared=[];
  for(const list of received.lists){const item={schema:1,id:list.id,owner:clone(ticket.target_owner),epoch,revision:1,captured_at:received.captured_at,expires_at:received.expires_at,source_fingerprint:received.fingerprint,list:clone(list),content_hash:''};item.content_hash=await U.hash(content(item));prepared.push(item);}
  const result=await transaction('readwrite',api=>withData(api,(device,stored)=>{
   meta(device);
   if(ticket.serial!==serial||ticket.denial_nonce!==denialNonce()||device.epoch!==ticket.epoch||device.revision!==ticket.revision||(device.owner===null?ticket.owner!==null:!U.sameScope(device.owner,ticket.owner))||!globalOpted(ticket.target_owner.user_id))throw U.error('STALE',_householdText_shopping_device_js("Eine andere Ansicht hat den Gerätestand geändert. Neu abrufen statt alte Antworten wiederverwenden."));
   if(prepared.some(item=>Date.parse(item.expires_at)<=Date.now()))throw U.error('EXPIRED',_householdText_shopping_device_js("Antwort ist vor dem Speichern abgelaufen. Neu abrufen."));
   const alive=stored.map(item=>row(item,device)).filter(item=>Date.parse(item.expires_at)>Date.now());
   if(ticket.full_refetch&&alive.some(item=>!ticket.ids.includes(item.id)))throw U.error('SESSION',_householdText_shopping_device_js("Alle früheren Kopien müssen vollständig neu geprüft werden."));
   const nextRows=[...(ticket.full_refetch?[]:alive.filter(item=>!ticket.ids.includes(item.id))),...prepared];budget(nextRows);
   const updated={...device,owner:clone(ticket.target_owner),epoch,revision:next(device),authorized_until:Date.parse(received.context.server_time)+U.LIMITS.age};
   if(updated.authorized_until<=Date.now())throw U.error('EXPIRED',_householdText_shopping_device_js("Die aktuelle Gerätefreigabe ist abgelaufen."));
   api.snapshots.clear();for(const item of nextRows)api.snapshots.put(item);api.meta.put(updated);api.set({lease:token(updated),snapshots:nextRows,totals:budget(nextRows)});
  }));if(ticket.serial!==serial||ticket.denial_nonce!==denialNonce())throw U.error('STALE',_householdText_shopping_device_js("Gerätefreigabe wurde nach dem Speichern erneut gesperrt."));try{localStorage.setItem(AUTHORIZED,ticket.denial_nonce);denied=false;}catch{throw U.error('STORAGE',_householdText_shopping_device_js("Kopie gespeichert, lokale Lesefreigabe aber nicht bestätigt. Gerätestand erneut prüfen."));}announce('changed');return result;
 }
 async function valid(lease){const currentSerial=serial;return transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{if(currentSerial!==serial||!opted(lease?.owner?.user_id))throw U.error('CONSENT',_householdText_shopping_device_js("Offline-Freigabe wurde geändert."));assertToken(request.result,lease);api.set(true);}catch(error){api.fail(error);}};});}
 async function read(lease,id){
  const currentSerial=serial;
  const item=await transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{assertToken(request.result,lease);if(!opted(lease.owner.user_id)||currentSerial!==serial)throw U.error('CONSENT',_householdText_shopping_device_js("Gerätefreigabe wurde aufgehoben."));const selected=api.snapshots.get(id);selected.onsuccess=()=>{try{if(!selected.result)throw U.error('MISSING',_householdText_shopping_device_js("Diese Gerätekopie ist nicht mehr vorhanden."));row(selected.result,request.result);if(Date.parse(selected.result.expires_at)<=Date.now())throw U.error('EXPIRED',_householdText_shopping_device_js("Diese Lesekopie ist abgelaufen."));api.set(selected.result);}catch(error){api.fail(error);}};}catch(error){api.fail(error);}};});
  if(await U.hash(content(item))!==item.content_hash)throw U.error('FORMAT',_householdText_shopping_device_js("Gespeicherte Angaben sind beschädigt. Kopie löschen und neu laden."));
  await valid(lease);if(currentSerial!==serial)throw U.error('STALE',_householdText_shopping_device_js("Gerätestand wurde während der Prüfung geändert."));return item;
 }
 async function remove(lease,id){const result=await transaction('readwrite',api=>withData(api,(device,stored)=>{assertToken(device,lease);if(!opted(lease.owner.user_id))throw U.error('CONSENT',_householdText_shopping_device_js("Gerätefreigabe wurde aufgehoben."));if(!stored.some(item=>item.id===id))throw U.error('MISSING',_householdText_shopping_device_js("Kopie ist nicht mehr vorhanden."));api.snapshots.delete(id);device.revision=next(device);api.meta.put(device);api.set({lease:token(device)});}));announce('changed');return result;}
 async function suspend(){deny();invalidate(_householdText_shopping_device_js("Frühere Lesekopien sind bis zum vollständigen aktuellen Abruf gesperrt."));announce('lock');await transaction('readwrite',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{if(request.result){const value=meta(request.result);value.authorized_until=0;value.revision=next(value);api.meta.put(value);}api.set(true);}catch(failure){api.fail(failure);}};});return true;}
 async function enforceContext(context){U.context(context);const current=await transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{api.set(request.result?meta(request.result):null);}catch(failure){api.fail(failure);}};});if(!context.offline_allowed){if(current?.owner){await reset();return {consent:false,changed:true};}return {consent:false,changed:false};}if(!current?.owner)return {changed:false};if(!U.sameOwner(current.owner,U.scope(context))){await reset();return {changed:true};}if(!U.sameScope(current.owner,U.scope(context))){if(current.authorized_until!==0||opted(current.owner.user_id)){await suspend();return {suspended:true,changed:true};}return {suspended:true,changed:false};}return {suspended:current.authorized_until===0||!opted(current.owner.user_id),changed:false};}
 async function reset(){deny();invalidate(_householdText_shopping_device_js("Lokale Lesekopien werden gesperrt."));announce('lock');await transaction('readwrite',api=>{api.snapshots.clear();api.meta.clear();api.meta.put(freshMeta());api.set(true);});announce('reset');return true;}
 async function observe(user,household){if(!user||!household)return;const foreign=await transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{const device=request.result;api.set(!!device?.owner&&(device.owner.user_id!==user||device.owner.household_id!==household));};});if(foreign)await reset();}
 root.HouseholdShoppingDevice={...pure,opted,consented:globalOpted,inspect,prepare,save,valid,read,remove,reset,suspend,enforceContext,observe};
})(globalThis);
