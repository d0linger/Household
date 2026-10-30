((root)=>{
 'use strict';
 const U=typeof module==='object'&&module.exports?require('./shopping-extension-ui.js'):root.HouseholdShoppingUI;
 const DB='household-push-device',HEX=/^[0-9a-f]{64}$/,clone=value=>JSON.parse(JSON.stringify(value));
 const same=(a,b)=>!!a&&!!b&&['user_id','household_id','session_scope','auth_scope'].every(key=>a[key]===b[key]);
 function owner(value){U.keys(value,['user_id','household_id','session_scope','auth_scope']);if(!U.UUID.test(value.user_id)||!U.UUID.test(value.household_id)||!HEX.test(value.session_scope)||!HEX.test(value.auth_scope))throw U.error('AUTH','Aktuelle Geräteberechtigung konnte nicht geprüft werden.');return value;}
 function core(value){
  U.keys(value,['context','state','application_server_key','vapid_key_id','device_limit']);U.keys(value.context,['user_id','household_id','session_scope','auth_scope','role']);owner({user_id:value.context.user_id,household_id:value.context.household_id,session_scope:value.context.session_scope,auth_scope:value.context.auth_scope});
  if(!['reader','editor','admin'].includes(value.context.role)||!['disabled','not_configured','ready'].includes(value.state)||value.device_limit!==5)throw U.error('FORMAT','Gerätehinweise haben einen unbekannten Konfigurationsstand.');
  if(value.state==='ready'){if(!HEX.test(value.vapid_key_id)||typeof value.application_server_key!=='string'||!/^[A-Za-z0-9_-]{87}$/.test(value.application_server_key))throw U.error('FORMAT','Öffentlicher Geräteschlüssel ist ungültig.');}
  else if(value.vapid_key_id!==null||value.application_server_key!==null)throw U.error('FORMAT','Nicht verfügbare Gerätehinweise enthalten unerwartete Schlüsselangaben.');
  return value;
 }
 const scope=value=>({user_id:value.user_id,household_id:value.household_id,session_scope:value.session_scope,auth_scope:value.auth_scope});
 function metadata(value){
  U.keys(value,['schema','key','epoch','revision','device_id','owner','subscription_id','vapid_key_id','registered_at','state']);
  if(value.schema!==1||value.key!=='device'||!U.UUID.test(value.epoch)||!U.UUID.test(value.device_id)||!Number.isSafeInteger(value.revision)||value.revision<0||!['disabled','pending','registered'].includes(value.state))throw U.error('FORMAT','Lokale Geräteverbindung ist unbekannt. Ausdrücklich entfernen.');
  if(value.owner!==null)owner(value.owner);
  if(value.state==='registered'&&(!value.owner||!U.UUID.test(value.subscription_id)||!HEX.test(value.vapid_key_id)||!Number.isFinite(Date.parse(value.registered_at))))throw U.error('FORMAT','Lokale Geräteverbindung ist unvollständig.');
  if(value.state==='pending'&&(!value.owner||!HEX.test(value.vapid_key_id)))throw U.error('FORMAT','Unbestätigte Geräteverbindung ist unvollständig.');
  if(value.state==='disabled'&&(value.owner!==null||value.subscription_id!==''||value.vapid_key_id!==''||value.registered_at!==''))throw U.error('FORMAT','Deaktivierte Geräteverbindung enthält frühere Zuordnungen.');
  return value;
 }
 function receipt(value){U.keys(value,['id','device_id','state','vapid_key_id','registered_at']);if(!U.UUID.test(value.id)||!U.UUID.test(value.device_id)||!['registered','disabled','renewal_required','expired'].includes(value.state)||!HEX.test(value.vapid_key_id)||!Number.isFinite(Date.parse(value.registered_at)))throw U.error('FORMAT','Serververbindung ist nicht vollständig lesbar.');return value;}
 const permissionState=(available,permission)=>!available?'unsupported':permission==='denied'?'denied':permission==='granted'?'granted':'default';
 function subscriptionBody(device,key,native){if(!U.UUID.test(device)||!HEX.test(key)||!U.object(native)||typeof native.endpoint!=='string'||!U.object(native.keys)||typeof native.keys.p256dh!=='string'||typeof native.keys.auth!=='string')throw U.error('FORMAT','Browserverbindung enthält unvollständige Angaben.');return {device_id:device,vapid_key_id:key,subscription:{endpoint:native.endpoint,keys:{p256dh:native.keys.p256dh,auth:native.keys.auth}}};}
 const pure={DB,owner,core,scope,same,metadata,receipt,permissionState,subscriptionBody};
 if(typeof module==='object'&&module.exports){module.exports=pure;return;}
 if(!root.document||!U)return;
 let opening,serial=0;const channel=typeof BroadcastChannel==='function'?new BroadcastChannel(DB):null;
 function invalidate(){++serial;root.dispatchEvent(new CustomEvent('household-push-lock'));}
 function announce(){channel?.postMessage({kind:'changed'});try{localStorage.setItem('household-push-device-event',crypto.randomUUID());}catch{}}
 if(channel)channel.onmessage=()=>invalidate();root.addEventListener('storage',event=>{if(event.key==='household-push-device-event')invalidate();});
 function bounded(promise,message,ms=5000){let timer;return Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(U.error('DEVICE',message)),ms);})]).finally(()=>clearTimeout(timer));}
 function database(){if(opening)return opening;opening=new Promise((resolve,reject)=>{
  if(!root.indexedDB){reject(U.error('STORAGE','Gerätespeicher ist nicht verfügbar.'));return;}
  let ended=false;const request=indexedDB.open(DB,1),timer=setTimeout(()=>fail(),5000);
  function fail(){if(ended)return;ended=true;clearTimeout(timer);opening=undefined;reject(U.error('STORAGE','Geräteverbindung konnte nicht lokal geprüft werden. Andere Ansichten schließen.'));}
  request.onupgradeneeded=()=>{if(ended){request.transaction.abort();return;}request.result.createObjectStore('meta',{keyPath:'key'});};request.onblocked=request.onerror=fail;
  request.onsuccess=()=>{const db=request.result;if(ended){db.close();return;}ended=true;clearTimeout(timer);db.onversionchange=()=>{db.close();opening=undefined;invalidate();};resolve(db);};
 });return opening;}
 async function transaction(mode,fn){const db=await database();return new Promise((resolve,reject)=>{
  let result,failure;const tx=db.transaction('meta',mode),timer=setTimeout(()=>{failure=U.error('STORAGE','Lokale Geräteprüfung ist abgelaufen.');try{tx.abort();}catch{}},5000),store=tx.objectStore('meta');
  tx.oncomplete=()=>{clearTimeout(timer);resolve(result);};tx.onabort=()=>{clearTimeout(timer);reject(failure||U.error('STORAGE','Lokaler Gerätestand wurde nicht gespeichert.'));};tx.onerror=()=>{failure=U.error('STORAGE','Lokaler Gerätestand ist nicht verfügbar.');};
  const request=store.get('device');request.onsuccess=()=>{try{fn(request.result,store,value=>{result=value;});}catch(error){failure=error;tx.abort();}};
 });}
 function empty(){return {schema:1,key:'device',epoch:crypto.randomUUID(),revision:0,device_id:crypto.randomUUID(),owner:null,subscription_id:'',vapid_key_id:'',registered_at:'',state:'disabled'};}
 const available=()=>root.isSecureContext&&'Notification'in root&&'serviceWorker'in navigator&&'PushManager'in root;
 async function nativeSubscription(){if(!('serviceWorker'in navigator))return null;const registration=await bounded(navigator.serviceWorker.getRegistration('/'),'Geräteverbindung ist nicht erreichbar.');return registration?.pushManager?bounded(registration.pushManager.getSubscription(),'Geräteverbindung konnte nicht geprüft werden.'):null;}
 async function inspect(){return transaction('readonly',(value,_store,set)=>{set(value?metadata(value):null);});}
 async function stage(context,key){owner(context);if(!HEX.test(key))throw U.error('FORMAT','Aktuellen öffentlichen Schlüssel erneut prüfen.');const ticketSerial=serial;return transaction('readwrite',(value,store,set)=>{
  if(ticketSerial!==serial)throw U.error('STALE','Gerätefreigabe wurde vor dem Speichern aufgehoben.');const current=value?metadata(value):empty();if(current.owner&&!same(current.owner,context))throw U.error('AUTH','Frühere Gerätefreigabe erst ausdrücklich entfernen.');if(current.revision>=Number.MAX_SAFE_INTEGER)throw U.error('FORMAT','Gerätestand erschöpft. Verbindung entfernen.');
  const next={...current,owner:clone(context),state:'pending',vapid_key_id:key,subscription_id:'',registered_at:'',revision:current.revision+1};store.put(next);set({epoch:next.epoch,revision:next.revision,device_id:next.device_id,owner:clone(context),vapid_key_id:key,serial:ticketSerial});
 });}
 async function registered(ticket,result){receipt(result);if(result.state!=='registered'||result.device_id!==ticket.device_id||result.vapid_key_id!==ticket.vapid_key_id)throw U.error('FORMAT','Serverantwort gehört nicht zur aktuellen Geräteanfrage.');return transaction('readwrite',(value,store,set)=>{
  const current=metadata(value);if(serial!==ticket.serial||current.epoch!==ticket.epoch||current.revision!==ticket.revision||!same(current.owner,ticket.owner))throw U.error('STALE','Gerätefreigabe wurde während der Anfrage geändert. Aktuellen Status prüfen.');
  const next={...current,state:'registered',subscription_id:result.id,registered_at:result.registered_at,revision:current.revision+1};metadata(next);store.put(next);set(next);
 });}
 async function reset(){
  invalidate();announce();navigator.serviceWorker?.controller?.postMessage({type:'PUSH_DEVICE_RESET'});
  const tasks=await Promise.allSettled([transaction('readwrite',(_value,store,set)=>{store.clear();store.put(empty());set(true);}),nativeSubscription().then(async subscription=>{if(subscription){const removed=await bounded(subscription.unsubscribe(),'Browserverbindung konnte nicht entfernt werden.');if(!removed)throw U.error('DEVICE','Browserverbindung wurde nicht bestätigt entfernt.');}})]);
  if(tasks.some(result=>result.status==='rejected'))throw U.error('DEVICE','Gerätehinweise sind gesperrt; die vollständige lokale Bereinigung konnte nicht bestätigt werden. Erneut entfernen.');return true;
 }
 async function observe(user,household){if(!user||!household)return;const current=await inspect();if(current?.owner&&(current.owner.user_id!==user||current.owner.household_id!==household))await reset();}
 root.HouseholdPushDevice={...pure,inspect,reset,observe,nativeSubscription};
 function initialize(panel){
  const $=selector=>panel.querySelector(selector),status=text=>{$('[data-push-status]').textContent=text;};let current=null,rows=[],busy=false,ticket=null,revision=0;
  const supported=()=>permissionState(available(),root.Notification?.permission),safeFailure=failure=>typeof failure?.code==='string'?failure.message:'Geräteverbindung konnte nicht bestätigt werden. Aktuellen Status gesondert prüfen.';
  async function read(url,options){try{return await U.raw(url,options);}catch(failure){if(failure.status===401||failure.status===403){++revision;current=null;rows=[];ticket=null;$('[data-push-devices]').replaceChildren();locks();try{await reset();}catch{status('Gerätehinweise sind gesperrt. Die vollständige lokale Bereinigung ist nicht bestätigt.');throw U.error('DEVICE','Berechtigung fehlt; lokale Bereinigung gesondert erneut prüfen.');}}throw failure;}}
  function locks(){panel.setAttribute('aria-busy',String(busy));for(const button of panel.querySelectorAll('button'))button.disabled=busy;$('[data-push-enable]').disabled=busy||!current||current.state!=='ready'||!available()||supported()==='denied';}
  function render(local){
   const permission=supported();$('[data-push-permission]').textContent=({unsupported:'Dieser Browser stellt Gerätehinweise nicht bereit.',denied:'Gerätehinweise sind im Browser blockiert. Die Freigabe kann nur in den Browser-Einstellungen geändert werden.',default:'Browserfreigabe wurde noch nicht angefragt.',granted:'Browserfreigabe ist erteilt. Das bestätigt keine Zustellung.'})[permission];
   $('[data-push-server]').textContent=current?.state==='ready'?'Server ist für optionale Gerätehinweise eingerichtet.':current?.state==='disabled'?'Gerätehinweise sind am Server ausgeschaltet. In-App-Hinweise bleiben verfügbar.':'Gerätehinweise sind am Server noch nicht eingerichtet. In-App-Hinweise bleiben verfügbar.';
   const box=$('[data-push-devices]');box.replaceChildren();for(const item of rows){const article=U.node('div',undefined,'se-row'),own=local?.device_id===item.device_id;article.append(U.node('p',(own?'Dieses Gerät':'Andere eigene Geräteverbindung')+' · '+({registered:'am Server registriert',disabled:'deaktiviert',renewal_required:'Schlüssel geändert, neu verbinden',expired:'Freigabe abgelaufen'})[item.state]+' · eingerichtet '+U.date(item.registered_at)));if(item.state!=='disabled'){const remove=U.node('button','Diese Verbindung entfernen');remove.type='button';remove.addEventListener('click',()=>void work(()=>withdraw(item)));article.append(remove);}box.append(article);}if(!rows.length)box.append(U.node('p','Noch keine eigenen Geräteverbindungen.'));
   $('[data-push-local]').textContent=local?.state==='registered'?'Lokale Geräteverbindung gespeichert. Die Annahme durch einen Push-Dienst bestätigt keine Anzeige.':local?.state==='pending'?'Ausgang einer Geräteanfrage noch nicht bestätigt. Aktuellen Serverstatus prüfen.':'Dieses Gerät ist nicht bestätigt verbunden.';locks();
  }
  async function load(){const turn=revision,result=core(await read('/api/v1/core/push/context'));if(turn!==revision)throw U.error('AUTH','Geräteansicht wurde geändert.');const expected=document.body.dataset;if((expected.user&&expected.user!==result.context.user_id)||(expected.household&&expected.household!==result.context.household_id)){await reset();throw U.error('AUTH','Seitenkonto und aktuelle Geräteanmeldung stimmen nicht überein. Seite erneut öffnen.');}const own=scope(result.context),local=await inspect();if(local?.owner&&!same(local.owner,own)){await reset();throw U.error('AUTH','Frühere Gerätefreigabe wurde gesperrt. Ansicht neu öffnen und neu verbinden.');}current=result;const view=await read('/api/v1/core/push/subscriptions');U.keys(view,['subscriptions']);if(!Array.isArray(view.subscriptions)||view.subscriptions.length>128)throw U.error('FORMAT','Geräteübersicht überschreitet ihre Grenze.');rows=view.subscriptions.map(receipt);if(turn!==revision)throw U.error('AUTH','Verspätete Geräteantwort wurde verworfen.');render(local);return local;}
  async function csrf(){const expected=scope(current.context),boot=await read('/api/v1/core/bootstrap');if(boot.user?.id!==expected.user_id||boot.user?.household_id!==expected.household_id||boot.user?.role!==current.context.role||!boot.csrf)throw U.error('AUTH','Aktuelle Anmeldung erneut prüfen.');const latest=core(await read('/api/v1/core/push/context'));if(!same(scope(latest.context),expected)||latest.vapid_key_id!==current.vapid_key_id)throw U.error('AUTH','Anmeldung oder Geräteschlüssel wurde geändert.');document.querySelector('meta[name="csrf-token"]').content=boot.csrf;current=latest;}
  async function work(fn){if(busy)return;busy=true;locks();try{await fn();}catch(failure){status(safeFailure(failure));}finally{busy=false;locks();}}
  async function connect(permission){
   if(await permission!=='granted'){status('Keine Browserfreigabe. In-App-Hinweise bleiben verfügbar.');return;}
   await load();if(current.state!=='ready')throw U.error('DEVICE','Server ist nicht bereit. Es wurde keine neue Verbindung angelegt.');await csrf();
   const own=scope(current.context),keyID=current.vapid_key_id,local=await inspect();let subscription=await nativeSubscription();
   if(subscription&&(!local?.owner||local.vapid_key_id!==keyID)){if(!confirm('Eine frühere Browserverbindung verwerfen und dieses Gerät ausdrücklich neu verbinden?'))return;await reset();await load();if(!same(scope(current.context),own)||current.vapid_key_id!==keyID)throw U.error('AUTH','Anmeldung oder Geräteschlüssel wurde geändert. Erneut ausdrücklich verbinden.');subscription=null;}
   ticket=await stage(own,current.vapid_key_id);const registration=await bounded(navigator.serviceWorker.ready,'Service Worker ist noch nicht bereit.');
   if(!subscription){const encoded=current.application_server_key.replace(/-/g,'+').replace(/_/g,'/'),key=Uint8Array.from(atob(encoded+'='.repeat((4-encoded.length%4)%4)),ch=>ch.charCodeAt(0));if(key.length!==65||key[0]!==4)throw U.error('FORMAT','Öffentlicher Geräteschlüssel ist ungültig.');const pendingTicket=ticket;subscription=await bounded(registration.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:key}).then(async value=>{if(pendingTicket.serial!==serial){await bounded(value.unsubscribe(),'Verspätete Browserverbindung konnte nicht entfernt werden.');throw U.error('STALE','Verspätete Browserverbindung wurde verworfen.');}return value;}),'Browserverbindung nicht bestätigt.',15000);}
   if(ticket.serial!==serial){await bounded(subscription.unsubscribe(),'Browserverbindung konnte nicht entfernt werden.');throw U.error('STALE','Gerätefreigabe wurde inzwischen aufgehoben.');}
   try{
    const body=subscriptionBody(ticket.device_id,ticket.vapid_key_id,subscription.toJSON());
    const result=await read('/api/v1/core/push/subscriptions',{method:'POST',body,timeout:15000}),latest=core(await read('/api/v1/core/push/context'));
    if(!same(scope(latest.context),ticket.owner)||latest.vapid_key_id!==ticket.vapid_key_id)throw U.error('AUTH','Antwort gehört nicht mehr zur aktuellen Gerätefreigabe. Status nach erneuter Anmeldung prüfen.');
    await registered(ticket,result);ticket=null;await load();status('Eigene Geräteverbindung am Server registriert. Eine spätere Anzeige ist damit noch nicht bestätigt.');
   }
   catch(failure){status('Registrierungsausgang ungewiss. Dieselbe Gerätekennung bleibt erhalten; aktuellen Status prüfen statt neue Verbindung anlegen.');throw failure;}
  }
  async function recover(){const local=await load();if(local?.state==='pending'){const found=rows.filter(item=>item.device_id===local.device_id&&item.vapid_key_id===local.vapid_key_id&&item.state==='registered');if(found.length===1){await registered({epoch:local.epoch,revision:local.revision,device_id:local.device_id,owner:local.owner,vapid_key_id:local.vapid_key_id,serial},found[0]);await load();status('Dieselbe eigene Geräteverbindung am Server gefunden.');}else status('Keine passende bestätigte Verbindung gefunden. Nur ausdrücklich erneut verbinden oder entfernen.');}else status('Aktuellen eigenen Gerätestand geladen.');}
  async function withdraw(item){if(!confirm('Diese eigene Geräteverbindung deaktivieren? Listenhinweise in der App bleiben unabhängig davon.'))return;await csrf();await read('/api/v1/core/push/subscriptions/'+item.id,{method:'DELETE'});const local=await inspect();if(local?.device_id===item.device_id)await reset();await load();status('Eigene Serververbindung deaktiviert.');}
  $('[data-push-enable]').addEventListener('click',()=>{if(busy||!current||current.state!=='ready'||!available())return;const permission=root.Notification.permission==='default'?root.Notification.requestPermission():Promise.resolve(root.Notification.permission);void work(()=>connect(permission));});
  $('[data-push-refresh]').addEventListener('click',()=>void work(recover));
  $('[data-push-clear]').addEventListener('click',()=>void work(async()=>{if(!confirm('Lokale Gerätefreigabe und Browserverbindung entfernen? Eine unbekannte Serverregistrierung kann danach in der Übersicht gesondert deaktiviert werden.'))return;await reset();status('Lokale Browserverbindung vollständig entfernt. Ansicht neu öffnen, bevor du erneut verbindest.');}));
  root.addEventListener('household-push-lock',()=>{++revision;current=null;ticket=null;$('[data-push-devices]').replaceChildren();status('Gerätefreigabe wurde geändert. Ansicht erneut öffnen.');locks();});
  root.addEventListener('focus',()=>{if(!document.hidden)void work(load);});void work(load);
 }
 document.querySelectorAll('[data-shopping-push]').forEach(initialize);
})(globalThis);
