/* Included in sw.js. Payloads cannot choose text, links, images or API requests. */
(() => {
  'use strict';
  const UUID=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  const HASH=/^[0-9a-f]{64}$/;
  const NAME='household-push-device';
  function notice(raw){
    const now=Math.floor(Date.now()/1000);
    if(!raw||typeof raw!=='object'||Array.isArray(raw)||Object.keys(raw).sort().join(',')!=='audience_scope,expires_at,id,kind,schema'||raw.schema!==1||raw.kind!=='shopping_notice'||!UUID.test(raw.id)||!HASH.test(raw.audience_scope)||!Number.isSafeInteger(raw.expires_at)||raw.expires_at<=now||raw.expires_at>now+86400)return null;
    return raw;
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
  self.addEventListener('push',event=>event.waitUntil((async()=>{
    let payload;
    try{if(!event.data||event.data.text().length>2048)return;payload=notice(event.data.json());}catch{return;}
    if(!payload||!await eligible(payload)||!notice(payload))return;
    await self.registration.showNotification('Household',{body:'Änderungen im Einkauf. Household öffnen.',icon:PUSH_ICON,badge:PUSH_ICON,tag:'household-shopping-'+payload.id,renotify:false,data:{schema:1,kind:payload.kind,id:payload.id,expires_at:payload.expires_at,audience_scope:payload.audience_scope}});
  })().catch(()=>{})));
  self.addEventListener('notificationclick',event=>{
    event.notification.close();
    event.waitUntil((async()=>{
      const payload=notice(event.notification.data);
      if(!payload||!await eligible(payload)||!notice(payload))return;
      const target=new URL('/notifications',self.location.origin).href;
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
      for(const value of notices)if(value.tag?.startsWith('household-shopping-'))value.close();
    }).catch(()=>{}));
  });
})();
