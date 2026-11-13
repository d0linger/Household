const _householdText_pantry_device_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
/* Explicit device drafts only. This store never contains stock, credentials or API bodies. */
((root) => {
  'use strict';
  const LIMITS = Object.freeze({lines:100, bytes:256*1024, age:7*24*60*60*1000});
  const UUID=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  const code = raw => {if(typeof raw!=='string'||raw.length>256)throw Error(_householdText_pantry_device_js("Barcode erneut eingeben."));const value=raw.trim();if(!/^[\x21-\x7e]{1,64}$/.test(value))throw Error(_householdText_pantry_device_js("1 bis 64 sichtbare ASCII-Zeichen ohne Leerzeichen eingeben."));return value;};
  function decimal(raw,positive=true){const match=String(raw).trim().replace(',','.').match(/^(\d{1,12})(?:\.(\d{1,6}))?$/);if(!match)throw Error(_householdText_pantry_device_js("Menge mit höchstens sechs Nachkommastellen eingeben."));const n=BigInt(match[1])*1000000n+BigInt((match[2]||'').padEnd(6,'0'));if(n>=10n**18n||positive&&n===0n)throw Error(_householdText_pantry_device_js("Eine positive, unterstützte Menge eingeben."));return (n/1000000n)+'.'+(n%1000000n).toString().padStart(6,'0');}
  function canonical(value){if(Array.isArray(value))return '['+value.map(canonical).join(',')+']';if(value&&typeof value==='object')return '{'+Object.keys(value).sort().map(k=>JSON.stringify(k)+':'+canonical(value[k])).join(',')+'}';return JSON.stringify(value);}
  function owner(context,sessionHash){if(!UUID.test(context.user_id)||!UUID.test(context.household_id)||!Number.isSafeInteger(context.generation)||context.generation<1||!/^[0-9a-f]{64}$/.test(sessionHash))throw Error(_householdText_pantry_device_js("Aktuellen Besitzerkontext neu laden."));return {user:context.user_id,household:context.household_id,generation:context.generation,session_hash:sessionHash};}
  const sameOwner=(a,b)=>!!a&&!!b&&a.user===b.user&&a.household===b.household;
  const sameScope=(a,b)=>sameOwner(a,b)&&a.generation===b.generation&&a.session_hash===b.session_hash;
  const namespace=(scope,epoch)=>[scope.user,scope.household,scope.generation,epoch,scope.session_hash].join('/');
  function line(input){if(!UUID.test(input.id))throw Error(_householdText_pantry_device_js("Scanzeile erneut erfassen."));const result={id:input.id,code:code(input.code),quantity:input.quantity?decimal(input.quantity):'',label:String(input.label||'').trim(),received_on:String(input.received_on||'')};if(new TextEncoder().encode(result.label).length>300||result.received_on&&!/^\d{4}-\d{2}-\d{2}$/.test(result.received_on))throw Error(_householdText_pantry_device_js("Bezeichnung oder Datum prüfen."));return result;}
  function validateDraft(draft,meta){const keys=['format','id','owner','epoch','namespace','revision','created','expires','lines','handoff'];if(!draft||Object.keys(draft).some(k=>!keys.includes(k))||draft.format!==1||!UUID.test(draft.id)||draft.epoch!==meta.epoch||!sameScope(draft.owner,meta.owner)||draft.namespace!==namespace(meta.owner,meta.epoch)||!Number.isSafeInteger(draft.revision)||draft.revision<1||!Number.isFinite(draft.created)||!Number.isFinite(draft.expires)||draft.expires<=draft.created||draft.expires-draft.created>LIMITS.age||!Array.isArray(draft.lines)||draft.lines.length>LIMITS.lines)throw Error(_householdText_pantry_device_js("Lokaler Entwurf hat ein unbekanntes Format oder einen anderen Besitzer. Nur ausdrücklich löschen oder online neu prüfen."));const ids=new Set();for(const value of draft.lines){if(ids.has(value.id)||canonical(line(value))!==canonical(value))throw Error(_householdText_pantry_device_js("Lokale Scanzeile ist beschädigt oder enthält nicht unterstützte Daten."));ids.add(value.id);}if(draft.handoff){const h=draft.handoff;if(Object.keys(h).some(k=>!['draft_id','key','digest','phase'].includes(k))||!UUID.test(h.draft_id)||!UUID.test(h.key)||!/^[0-9a-f]{64}$/.test(h.digest)||!['uncertain','saved'].includes(h.phase))throw Error(_householdText_pantry_device_js("Gespeicherte Übergabekennung ist ungültig."));}return draft;}
  function budget(drafts,now=Date.now()){let count=0;for(const draft of drafts){if(!Number.isFinite(draft.expires)||draft.expires<=now)continue;if(!Array.isArray(draft.lines))throw Error(_householdText_pantry_device_js("Unbekanntes Entwurfsformat."));count+=draft.lines.length;}if(count>LIMITS.lines||new TextEncoder().encode(canonical(drafts)).length>LIMITS.bytes)throw Error(_householdText_pantry_device_js("Gerätegrenze erreicht: höchstens 100 Scanzeilen und 256 KiB. Zuerst Entwürfe prüfen oder verwerfen."));}
  function token(meta){return {epoch:meta.epoch,revision:meta.revision,owner:meta.owner};}
  function assertLease(meta,lease,now=Date.now()){if(!meta||!lease||meta.epoch!==lease.epoch||meta.revision!==lease.revision||!sameScope(meta.owner,lease.owner)||!Number.isFinite(meta.authorized_until)||meta.authorized_until<=now)throw Error(_householdText_pantry_device_js("Gerätebesitz, Freigabeablauf oder Entwurfsstand wurde geändert. Ansicht online neu prüfen."));}
  function assertPendingDraft(meta,draft,lease,pending,now=Date.now()){
    assertLease(meta,lease,now);
    if(!pending||!UUID.test(pending.localID)||!UUID.test(pending.id)||!UUID.test(pending.key)||!/^[0-9a-f]{64}$/.test(pending.digest)||
      !sameScope(pending.owner,meta.owner)||pending.epoch!==meta.epoch||!Number.isSafeInteger(pending.draftRevision)||pending.draftRevision<1||!Number.isFinite(pending.expires))throw Error(_householdText_pantry_device_js("Die gespeicherte Übergabe gehört nicht zum aktuellen Gerätebesitz. Denselben Serverentwurf gesondert prüfen."));
    validateDraft(draft,meta);
    if(draft.id!==pending.localID||draft.expires<=now||draft.expires!==pending.expires||draft.revision!==pending.draftRevision||
      draft.handoff?.phase!=='uncertain'||draft.handoff.draft_id!==pending.id||draft.handoff.key!==pending.key||draft.handoff.digest!==pending.digest)throw Error(_householdText_pantry_device_js("Der lokale Entwurf ist abgelaufen oder geändert. Denselben Serverentwurf suchen; keine alte Anfrage übertragen."));
    return true;
  }
  const pure={LIMITS,code,decimal,canonical,owner,sameOwner,sameScope,namespace,line,budget,assertLease,assertPendingDraft,validateDraft};
  if(typeof module==='object'&&module.exports){module.exports=pure;return;}
  if(!root.document)return;
  const name='household-pantry-scans',channel=typeof BroadcastChannel==='function'?new BroadcastChannel(name):null;
  let opening;
  function database(){if(!opening)opening=new Promise((resolve,reject)=>{if(!root.indexedDB){reject(Error(_householdText_pantry_device_js("Lokaler Gerätespeicher ist nicht verfügbar.")));return;}const request=indexedDB.open(name,1);request.onupgradeneeded=()=>{request.result.createObjectStore('meta',{keyPath:'key'});request.result.createObjectStore('drafts',{keyPath:'id'});};request.onerror=()=>reject(Error(_householdText_pantry_device_js("Gerätespeicher konnte nicht geöffnet werden.")));request.onblocked=()=>reject(Error(_householdText_pantry_device_js("Eine andere Registerkarte blockiert den Gerätespeicher.")));request.onsuccess=()=>{const db=request.result;db.onversionchange=()=>{db.close();opening=undefined;};resolve(db);};});return opening;}
  async function transaction(mode,operation){const db=await database();return new Promise((resolve,reject)=>{const tx=db.transaction(['meta','drafts'],mode);let result,failure;const api={meta:tx.objectStore('meta'),drafts:tx.objectStore('drafts'),set:value=>{result=value;},fail:error=>{failure=error;tx.abort();}};tx.oncomplete=()=>resolve(result);tx.onabort=()=>reject(failure||Error(_householdText_pantry_device_js("Lokaler Entwurf konnte nicht atomar gespeichert werden.")));tx.onerror=()=>{failure=Error(_householdText_pantry_device_js("Gerätespeicher ist voll oder nicht verfügbar. Eingaben bleiben im Formular."));};try{operation(api);}catch(error){api.fail(error);}});}
  const opted=user=>{try{return !!user&&localStorage.getItem('household-offline-owner')===user;}catch{return false;}};
  function announce(kind='changed'){channel?.postMessage({kind});try{localStorage.setItem('household-pantry-device-event',JSON.stringify({kind,nonce:crypto.randomUUID()}));}catch{}if(kind==='reset')root.dispatchEvent(new CustomEvent('household-pantry-lock'));}
  function external(message){if(message?.kind==='reset'||message?.kind==='changed')root.dispatchEvent(new CustomEvent('household-pantry-device-change',{detail:{kind:message.kind}}));}
  if(channel)channel.onmessage=event=>external(event.data);
  root.addEventListener('storage',event=>{if(event.key==='household-pantry-device-event'){try{external(JSON.parse(event.newValue));}catch{}}if(event.key==='household-offline-owner')root.dispatchEvent(new CustomEvent('household-pantry-lock'));});
  async function reset(){root.dispatchEvent(new CustomEvent('household-pantry-lock'));await transaction('readwrite',api=>{api.drafts.clear();api.meta.put({key:'device',epoch:crypto.randomUUID(),revision:0,owner:null});api.set(true);});announce('reset');}
  async function observe(user,household){return transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{const meta=request.result;api.set(!!meta?.owner&&(meta.owner.user!==user||meta.owner.household!==household));};}).then(changed=>changed?reset():undefined);}
  async function inspect(){
    return transaction('readonly',api=>{
      const request=api.meta.get('device');
      request.onsuccess=()=>{
        const meta=request.result;
        if(!meta?.owner||!opted(meta.owner.user)){api.set({meta:null,drafts:[]});return;}
        if(!Number.isFinite(meta.authorized_until)||meta.authorized_until<=Date.now()){api.set({meta,drafts:[],expired:true});return;}
        const all=api.drafts.getAll();
        all.onsuccess=()=>{
          try{api.set({meta,drafts:all.result.filter(d=>d.epoch===meta.epoch&&sameOwner(d.owner,meta.owner)).map(d=>validateDraft(d,meta))});}
          catch(error){api.fail(error);}
        };
      };
    });
  }
  async function activate(scope,{resume=false,discard=false}={}){if(!opted(scope.user))throw Error(_householdText_pantry_device_js("Offline-Speicherung zuerst in den Geräteeinstellungen ausdrücklich aktivieren."));const result=await transaction('readwrite',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{let meta=request.result||{key:'device',epoch:crypto.randomUUID(),revision:0,owner:null};if(meta.owner&&!sameOwner(meta.owner,scope)){api.fail(Error(_householdText_pantry_device_js("Anderes Konto: lokale Entwürfe zunächst vollständig löschen.")));return;}if(meta.owner&&meta.owner.generation!==scope.generation&&!discard){api.fail(Error(_householdText_pantry_device_js("Alte Vorratsgeneration: Entwürfe ausdrücklich verwerfen; keine automatische Neuzuordnung.")));return;}if(meta.owner&&meta.owner.session_hash!==scope.session_hash&&!resume&&!discard){api.fail(Error(_householdText_pantry_device_js("Neue Sitzung: eigene Entwürfe ausdrücklich wiederaufnehmen.")));return;}const all=api.drafts.getAll();all.onsuccess=()=>{try{if(discard){api.drafts.clear();meta={key:'device',epoch:crypto.randomUUID(),revision:0,owner:scope};}else for(const draft of all.result){if(draft.expires<=Date.now())api.drafts.delete(draft.id);else if(meta.owner&&sameOwner(draft.owner,scope)&&draft.owner.generation===scope.generation){draft.owner=scope;draft.namespace=namespace(scope,meta.epoch);draft.revision++;api.drafts.put(draft);}}meta.owner=scope;meta.authorized_until=Date.now()+LIMITS.age;meta.revision++;api.meta.put(meta);api.set(token(meta));}catch(error){api.fail(error);}};}catch(error){api.fail(error);}};});announce();return result;}
  async function change(lease,operation){if(!opted(lease?.owner?.user))throw Error(_householdText_pantry_device_js("Gerätefreigabe wurde aufgehoben."));const result=await transaction('readwrite',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{const meta=request.result;assertLease(meta,lease);const all=api.drafts.getAll();all.onsuccess=()=>{try{const current=all.result.filter(d=>d.expires>Date.now()).map(d=>validateDraft(d,meta));const next=operation(current,meta);for(const item of next)validateDraft(item,meta);budget(next);api.drafts.clear();for(const draft of next)api.drafts.put(draft);meta.revision++;api.meta.put(meta);api.set({lease:token(meta),drafts:next});}catch(error){api.fail(error);}};}catch(error){api.fail(error);}};});announce();return result;}
  async function create(lease){const id=crypto.randomUUID(),now=Date.now();return change(lease,(drafts,meta)=>[...drafts,{format:1,id,owner:meta.owner,epoch:meta.epoch,namespace:namespace(meta.owner,meta.epoch),revision:1,created:now,expires:now+LIMITS.age,lines:[],handoff:null}]);}
  async function save(lease,draft){return change(lease,drafts=>{const prior=drafts.find(d=>d.id===draft.id);if(!prior||prior.revision!==draft.revision||prior.handoff)throw Error(_householdText_pantry_device_js("Entwurf geändert oder bereits übergeben. Gespeicherten Stand neu laden."));return drafts.map(d=>d.id===draft.id?{...d,revision:d.revision+1,lines:draft.lines.map(line)}:d);});}
  async function handoff(lease,id,metadata){if(!metadata||!UUID.test(metadata.draft_id)||!UUID.test(metadata.key)||!/^[0-9a-f]{64}$/.test(metadata.digest)||!['uncertain','saved'].includes(metadata.phase))throw Error(_householdText_pantry_device_js("Übergabekennung prüfen."));return change(lease,drafts=>{if(!drafts.some(d=>d.id===id))throw Error(_householdText_pantry_device_js("Lokaler Entwurf ist abgelaufen oder nicht mehr vorhanden. Übergabekennung im Serverentwurf prüfen."));return drafts.map(d=>d.id===id?{...d,revision:d.revision+1,handoff:{draft_id:metadata.draft_id,key:metadata.key,digest:metadata.digest,phase:metadata.phase}}:d);});}
  async function remove(lease,id){return change(lease,drafts=>{if(drafts.find(d=>d.id===id)?.handoff?.phase==='uncertain')throw Error(_householdText_pantry_device_js("Ungewisse Übergabe zuerst unter derselben Kennung klären."));return drafts.filter(d=>d.id!==id);});}
  async function valid(lease){return transaction('readonly',api=>{const request=api.meta.get('device');request.onsuccess=()=>{try{assertLease(request.result,lease);if(!opted(lease.owner.user))throw Error(_householdText_pantry_device_js("Gerätefreigabe aufgehoben."));api.set(true);}catch(error){api.fail(error);}};});}
  async function pending(lease,expected){
    if(!opted(lease?.owner?.user))throw Error(_householdText_pantry_device_js("Gerätefreigabe wurde aufgehoben."));
    return transaction('readonly',api=>{
      const metaRequest=api.meta.get('device');
      metaRequest.onsuccess=()=>{
        const meta=metaRequest.result;
        try{assertLease(meta,lease);if(!UUID.test(expected?.localID))throw Error(_householdText_pantry_device_js("Gespeicherte Entwurfskennung prüfen."));}
        catch(error){api.fail(error);return;}
        const draftRequest=api.drafts.get(expected.localID);
        draftRequest.onsuccess=()=>{
          try{if(!opted(lease.owner.user))throw Error(_householdText_pantry_device_js("Gerätefreigabe wurde aufgehoben."));api.set(assertPendingDraft(meta,draftRequest.result,lease,expected));}
          catch(error){api.fail(error);}
        };
      };
    });
  }
  async function hash(value){const bytes=new TextEncoder().encode(typeof value==='string'?value:canonical(value));return [...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(n=>n.toString(16).padStart(2,'0')).join('');}
  root.HouseholdPantryDevice={...pure,inspect,activate,create,save,handoff,remove,valid,pending,reset,observe,hash,opted};
})(globalThis);
