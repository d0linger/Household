const _householdText_push_worker_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
/* Included in sw.js. Payloads cannot choose text, links, images or API requests. */
(() => {
  'use strict';
  const UUID=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  const HASH=/^[0-9a-f]{64}$/;
  const NAME='household-push-device';
  const LIFETIMES=Object.freeze({chores_due:3600,chores_digest:14400,chores_escalation:3600,cooking_timer:60,shopping:86400,pantry:86400,fuel:86400,expenses:86400,meters:86400,documents:86400,mealplan:14400,security:3600,test:60});
  function notice(raw){
    const now=Math.floor(Date.now()/1000);
    if(!raw||typeof raw!=='object'||Array.isArray(raw)||Object.keys(raw).sort().join(',')!=='audience_scope,expires_at,id,kind,schema'||typeof raw.kind!=='string'||typeof raw.id!=='string'||typeof raw.audience_scope!=='string'||!UUID.test(raw.id)||!HASH.test(raw.audience_scope)||!Number.isSafeInteger(raw.expires_at)||raw.expires_at<=now)return null;
    const lifetime=raw.schema===1&&raw.kind==='shopping_notice'?86400:raw.schema===2&&Object.hasOwn(LIFETIMES,raw.kind)?LIFETIMES[raw.kind]:0;
    if(!lifetime||raw.expires_at>now+lifetime)return null;
    return raw;
  }
  // JSON.parse alone discards duplicate keys. The wire format is deliberately
  // flat, so accept only five unique string/number pairs, including escaped keys.
  function parseNotice(text){
    if(typeof text!=='string'||text.length>2048)return null;
    const parsed=notice(JSON.parse(text));
    if(!parsed)return null;
    const pair=/\s*("(?:\\.|[^"\\])*")\s*:\s*("(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)\s*/y;
    const source=text.trim(),keys=new Set();
    if(source[0]!=='{'||source.at(-1)!=='}')return null;
    let position=1;
    while(position<source.length-1){
      pair.lastIndex=position;const match=pair.exec(source);
      if(!match)return null;
      const key=JSON.parse(match[1]);
      if(keys.has(key))return null;
      keys.add(key);position=pair.lastIndex;
      if(source[position]==='}')break;
      if(source[position]!==',')return null;
      position++;
    }
    return keys.size===5&&position===source.length-1?parsed:null;
  }
  async function device(){
    if(!self.indexedDB||typeof self.indexedDB.databases!=='function')return null;
    try{
      const databases=await self.indexedDB.databases();
      if(!databases.some(value=>value.name===NAME&&value.version===1))return null;
      return await new Promise(resolve=>{
        let db,done=false;
        const finish=value=>{if(done)return;done=true;clearTimeout(timeout);db?.close();resolve(value);};
        const timeout=setTimeout(()=>finish(null),3000);
        const opening=self.indexedDB.open(NAME);
        opening.onupgradeneeded=()=>{opening.transaction.abort();finish(null);};
        opening.onerror=()=>finish(null);
        opening.onblocked=()=>finish(null);
        opening.onsuccess=()=>{
          db=opening.result;
          if(done){db.close();return;}
          db.onversionchange=()=>{db.close();finish(null);};
          if(db.version!==1||!db.objectStoreNames.contains('meta')){finish(null);return;}
          const tx=db.transaction('meta','readonly');
          const query=tx.objectStore('meta').get('device');
          let result=null;
          query.onsuccess=()=>{result=query.result;};
          tx.oncomplete=()=>finish(result);
          tx.onerror=()=>finish(null);tx.onabort=()=>finish(null);
        };
      });
    }catch{return null;}
  }
  async function eligible(payload){
    const current=await device();
    const registered=Date.parse(current?.registered_at);
    return !!current&&current.schema===1&&current.key==='device'&&current.state==='registered'&&UUID.test(current.device_id)&&UUID.test(current.subscription_id)&&UUID.test(current.epoch)&&HASH.test(current.vapid_key_id)&&Number.isSafeInteger(current.revision)&&current.revision>=1&&UUID.test(current.owner?.user_id)&&UUID.test(current.owner?.household_id)&&HASH.test(current.owner?.session_scope)&&current.owner?.auth_scope===payload.audience_scope&&Number.isFinite(registered)&&registered<=Date.now()+60000&&Date.now()-registered<30*86400000;
  }
  async function contextBody(response){
    const reader=response.body?.getReader();
    if(!reader)return null;
    const bytes=new Uint8Array(2048);let length=0;
    try{
      for(;;){
        const chunk=await reader.read();
        if(chunk.done)break;
        if(length+chunk.value.byteLength>bytes.length){void reader.cancel().catch(()=>{});return null;}
        bytes.set(chunk.value,length);length+=chunk.value.byteLength;
      }
      return JSON.parse(new TextDecoder().decode(bytes.subarray(0,length)));
    }finally{reader.releaseLock();}
  }
  // This acknowledges a fulfilled showNotification request, never that the OS
  // rendered it or a person saw it. Display itself has no network dependency.
  function retryPause(signal,delay){
    if(signal.aborted)return Promise.resolve(false);
    return new Promise(resolve=>{
      let finished=false;
      const finish=value=>{if(finished)return;finished=true;clearTimeout(timer);signal.removeEventListener('abort',abort);resolve(value);};
      const abort=()=>finish(false),timer=setTimeout(()=>finish(true),delay);
      signal.addEventListener('abort',abort,{once:true});
    });
  }
  async function acknowledge(payload){
    if(payload.schema!==2||typeof self.fetch!=='function'||typeof AbortController!=='function')return;
    const controller=new AbortController();let timeout;
    const deadline=new Promise(resolve=>{timeout=setTimeout(()=>{controller.abort();resolve();},1500);});
    const base='/api/v1/core/push/deliveries/'+payload.id;
    const options={credentials:'same-origin',cache:'no-store',redirect:'error',signal:controller.signal};
    const work=(async()=>{
      try{
        for(let attempt=0;attempt<3;attempt++){
          if(attempt>0&&(!await retryPause(controller.signal,attempt===1?100:250)||!await eligible(payload)))return;
          if(controller.signal.aborted||!notice(payload))return;
          const response=await self.fetch(base+'/context',options);
          if(!response.ok||response.redirected){if(response.body)void response.body.cancel().catch(()=>{});return;}
          const context=await contextBody(response);
          if(!context||Object.keys(context).sort().join(',')!=='auth_scope,csrf,id'||context.id!==payload.id||context.auth_scope!==payload.audience_scope||typeof context.csrf!=='string'||!/^[A-Za-z0-9_-]{43}$/.test(context.csrf)||controller.signal.aborted||!notice(payload))return;
          const ack=await self.fetch(base+'/displayed',{...options,method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':context.csrf},body:'{}'});
          if(ack.body)void ack.body.cancel().catch(()=>{});
          // A device can request display before the worker commits gateway
          // acceptance. Retry only that conflict, within this single deadline.
          if(ack.status!==409)return;
        }
      }catch{ /* Offline, expired and signed-out clients simply lack an ACK. */ }
    })();
    try{await Promise.race([work,deadline]);}
    finally{clearTimeout(timeout);controller.abort();}
  }
  // Keep the visible OS notice for portable userVisibleOnly push. A focused
  // page can handle the matching timer and request a silent notice; offline,
  // uncertain or late replies leave ordinary push behavior intact.
  function foregroundTimer(payload){
    if(typeof MessageChannel!=='function')return Promise.resolve(false);
    return new Promise(resolve=>{
      const channels=[];let done=false,pending=0;
      const finish=value=>{if(done)return;done=true;clearTimeout(timeout);for(const channel of channels){try{channel.port1.postMessage({cancel:true});}catch{}channel.port1.close();channel.port2.close();}resolve(value);};
      const timeout=setTimeout(()=>finish(false),500);
      void self.clients.matchAll({type:'window',includeUncontrolled:false}).then(windows=>{
        if(done)return;
        const candidates=[],prefix='/m/recipes/cooking/';
        for(const client of windows.slice(0,64)){
          let url;try{url=new URL(client.url);}catch{continue;}
          if(client.focused&&client.visibilityState==='visible'&&url.origin===self.location.origin&&url.pathname.startsWith(prefix)&&UUID.test(url.pathname.slice(prefix.length)))candidates.push(client);
          if(candidates.length===4)break;
        }
        pending=candidates.length;if(!pending){finish(false);return;}
        for(const client of candidates){
          const channel=new MessageChannel();channels.push(channel);let replied=false;
          channel.port1.onmessage=event=>{
            if(done||replied)return;replied=true;const answer=event.data;
            if(answer&&Object.keys(answer).sort().join(',')==='auth_scope,handled,id'&&answer.handled===true&&answer.id===payload.id&&answer.auth_scope===payload.audience_scope){finish(true);return;}
            if(--pending===0)finish(false);
          };
          try{client.postMessage({type:'COOKING_PUSH_FOREGROUND',id:payload.id,auth_scope:payload.audience_scope,expires_at:payload.expires_at},[channel.port2]);}
          catch{replied=true;if(--pending===0)finish(false);}
        }
      }).catch(()=>finish(false));
    });
  }
  self.addEventListener('push',event=>event.waitUntil((async()=>{
    let payload;
    try{if(!event.data)return;payload=parseNotice(event.data.text());}catch{return;}
    if(!payload||!await eligible(payload)||!notice(payload))return;
    const locale=typeof self.householdLocale==='function'&&await self.householdLocale()==='en'?'en':'de';
    const body=payload.schema===1?(typeof PUSH_MESSAGES==='object'?PUSH_MESSAGES[locale]:_householdText_push_worker_js("Änderungen im Einkauf. Household öffnen.")):(typeof PUSH_CATEGORY_MESSAGES==='object'?PUSH_CATEGORY_MESSAGES[locale]?.[payload.kind]:null);
    if(typeof body!=='string'||!body||!await eligible(payload)||!notice(payload))return;
    let handled=false;
    if(payload.schema===2&&payload.kind==='cooking_timer'){
      handled=await foregroundTimer(payload);
      if(!await eligible(payload)||!notice(payload))return;
    }
    await self.registration.showNotification(_householdText_push_worker_js("Household"),{body,icon:PUSH_ICON,badge:PUSH_ICON,tag:(payload.schema===1?'household-shopping-':'household-notice-')+payload.id,renotify:false,...(handled?{silent:true}:{}),data:{schema:payload.schema,kind:payload.kind,id:payload.id,expires_at:payload.expires_at,audience_scope:payload.audience_scope}});
    await acknowledge(payload);
  })().catch(()=>{})));
  self.addEventListener('notificationclick',event=>{
    event.notification.close();
    event.waitUntil((async()=>{
      const payload=notice(event.notification.data);
      if(!payload||!await eligible(payload)||!notice(payload))return;
      const target=new URL(payload.schema===1?'/notifications':'/notifications/device/'+payload.id,self.location.origin).href;
      const windows=await self.clients.matchAll({type:'window',includeUncontrolled:true});
      for(const client of windows){
        try{
          if(new URL(client.url).origin!==self.location.origin)continue;
          const opened=await client.navigate(target);
          if(opened){await opened.focus();return;}
        }catch{ /* A closed or navigating window must not consume the click. */ }
      }
      await self.clients.openWindow(target);
    })().catch(()=>{}));
  });
  self.addEventListener('message',event=>{
    if(event.data?.type!=='PUSH_DEVICE_RESET')return;
    event.waitUntil(self.registration.getNotifications().then(notices=>{
      for(const value of notices)if(value.tag?.startsWith('household-'))value.close();
    }).catch(()=>{}));
  });
})();
