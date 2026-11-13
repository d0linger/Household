'use strict';
// Protocol/metadata source tests. Physical vendor delivery is a separate check.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {MessageChannel}=require('node:worker_threads');
const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/push-worker.js'),'utf8');
const uuid=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0');
const NOW=Date.UTC(2026,9,29,12);
const audience='a'.repeat(64);
const assetGo=fs.readFileSync(path.join(__dirname,'../internal/web/assets.go'),'utf8');
const cacheRuntime=assetGo.match(/const serviceWorkerRuntime = `([\s\S]*?)`/)[1];
// Derive the injected registry from the real server registry and reviewed
// catalogs. A test-local translated map could hide missing production copy.
const categoryRegistrySource=assetGo.match(/func pushCategoryMessages\(locale string\) map\[string\]string \{([\s\S]*?)\n\}/)?.[1];
assert.ok(categoryRegistrySource,'actual server category registry must exist');
const englishCatalog=Object.assign({},...fs.readdirSync(path.join(__dirname,'../internal/i18n')).filter(name=>name.endsWith('.json')).map(name=>JSON.parse(fs.readFileSync(path.join(__dirname,'../internal/i18n',name),'utf8'))));
const categoryMessages={de:{},en:{}};
for(const entry of categoryRegistrySource.matchAll(/"([a-z_]+)":\s*i18n\.Text\(locale,\s*("(?:[^"\\]|\\.)*")\)/g)){
  const [,,encoded]=entry,sourceText=JSON.parse(encoded);
  assert.equal(typeof englishCatalog[sourceText],'string','every server push template needs reviewed English copy');
  categoryMessages.de[entry[1]]=sourceText;categoryMessages.en[entry[1]]=englishCatalog[sourceText];
}
assert.equal(Object.keys(categoryMessages.de).length,13,'all compiled categories must have server templates');
function cacheEnvironment(options={}){
  const asset='/assets/0123456789ab/finance-workspace.js',shells=['/offline','/offline/scans','/offline/shopping','/offline/recipes'].flatMap(shell=>['de','en'].map(locale=>shell+'?lang='+locale));
  const handlers={},stored=new Map(shells.map(shell=>[shell,{shell}])),writes=[],fetches=[],installs=[],deleted=[];
  const response={status:200,type:'basic',redirected:false,...options.response,clone(){return this;}};
  const cache={async match(key){if(options.matchError)throw Error('cache read failed');return stored.get(key);},async put(key,value){if(options.putError)throw Error('quota exceeded');writes.push(key);stored.set(key,value);},async addAll(paths){installs.push(...paths);}};
  const caches={async open(){if(options.openError)throw Error('storage unavailable');return cache;},async keys(){return ['household-old','household-current','household-public-preferences-v1','unrelated'];},async delete(key){deleted.push(key);}};
  let claims=0,activations=0;
  const self={location:{origin:'https://household.example.invalid'},addEventListener(name,fn){handlers[name]=fn;},clients:{async claim(){claims++;}},skipWaiting(){activations++;}};
  vm.runInNewContext(cacheRuntime,{self,caches,URL,Set,Response,CACHE:'household-current',ASSETS:[...shells,asset],PRECACHE:shells,fetch:async request=>{fetches.push(request.url);if(options.networkError)throw Error('offline');return response;}});
  return {asset,shells,response,stored,writes,fetches,installs,deleted,get claims(){return claims;},get activations(){return activations;},
    async dispatch(name,data={}){let pending;handlers[name]({...data,waitUntil(value){pending=value;}});await pending;},
    async request(target,extra={}){let pending;handlers.fetch({request:{url:new URL(target,self.location.origin).href,method:'GET',mode:'cors',...extra},respondWith(value){pending=value;}});return pending===undefined?null:await pending;}};
}

test('service worker installs only the offline inventory and caches immutable assets on demand',async()=>{
  const env=cacheEnvironment();await env.dispatch('install');assert.deepEqual(env.installs,env.shells);
  assert.equal(await env.request(env.asset),env.response);assert.deepEqual(env.writes,[env.asset]);
  assert.equal(await env.request(env.asset),env.response);assert.equal(env.fetches.length,1);
  await env.dispatch('activate');assert.deepEqual(env.deleted,['household-old']);assert.equal(env.claims,1);
  await env.dispatch('message',{data:{type:'ACTIVATE'}});assert.equal(env.activations,1);
});

test('service worker never caches failed, partial, opaque, cross-origin or redirected responses',async()=>{
  for(const response of [{status:404},{status:503},{status:206},{type:'opaque'},{type:'cors'},{redirected:true}]){
    const env=cacheEnvironment({response});assert.equal(await env.request(env.asset),env.response);assert.deepEqual(env.writes,[]);
  }
  const env=cacheEnvironment({networkError:true});await assert.rejects(env.request(env.asset),/offline/);assert.deepEqual(env.writes,[]);
});

test('cache storage failure cannot discard an available online asset',async()=>{
  for(const options of [{openError:true},{matchError:true},{putError:true}]){
    const env=cacheEnvironment(options);assert.equal(await env.request(env.asset),env.response);assert.equal(env.fetches.length,1);
  }
});

test('only exact known public asset GETs enter runtime storage',async()=>{
  const env=cacheEnvironment();
  for(const [target,extra] of [[env.asset+'?variant=1',{}],['/assets/unknown/private.js',{}],['https://outside.invalid'+env.asset,{}],[env.asset,{method:'POST'}],['/api/v1/m/shopping/lists',{}],['/calendar/token/calendar.ics',{mode:'navigate'}],['/m/mealplan/calendar.ics',{mode:'navigate'}]]){
    assert.equal(await env.request(target,extra),null,target);
  }
  for(const target of ['/settings','/m/shopping/lists','/offline/shopping'])assert.equal(await env.request(target,{mode:'navigate'}),env.response);
  assert.deepEqual(env.writes,[]);
});

test('offline navigation only returns neutral shells and never stores private responses',async()=>{
  const env=cacheEnvironment({networkError:true});
  for(const [target,shell] of [['/settings','/offline'],['/m/pantry/scans','/offline/scans'],['/m/shopping/lists','/offline/shopping'],['/m/recipes/recipes','/offline/recipes']]){
    assert.deepEqual(await env.request(target,{mode:'navigate'}),{shell:shell+'?lang=de'});
  }
  assert.deepEqual(env.writes,[]);
});

test('offline locale is bounded, persists across activation, and selects only public variants',async()=>{
  const env=cacheEnvironment({networkError:true});
  for(const [locale,url] of [['fr','https://household.example.invalid/settings'],['en','https://outside.invalid/settings'],['en','']])await env.dispatch('message',{data:{type:'SET_LOCALE',locale},source:{url}});
  assert.deepEqual(env.writes,[]);
  await env.dispatch('message',{data:{type:'SET_LOCALE',locale:'en'},source:{url:'https://household.example.invalid/settings'}});
  assert.deepEqual(env.writes,['/__household_public_locale']);
  assert.equal(await env.stored.get('/__household_public_locale').clone().text(),'en');
  assert.deepEqual(await env.request('/m/recipes/recipes',{mode:'navigate'}),{shell:'/offline/recipes?lang=en'});
  assert.deepEqual(await env.request('/offline/recipes?lang=de',{mode:'navigate'}),{shell:'/offline/recipes?lang=de'});
  await env.dispatch('activate');assert.deepEqual(env.deleted,['household-old']);
});

const payload=()=>({schema:1,kind:'shopping_notice',id:uuid(1),expires_at:Math.floor(NOW/1000)+300,audience_scope:audience});
const metadata=()=>({schema:1,key:'device',epoch:uuid(2),revision:1,device_id:uuid(3),subscription_id:uuid(4),owner:{user_id:uuid(5),household_id:uuid(6),session_scope:'b'.repeat(64),auth_scope:audience},vapid_key_id:'c'.repeat(64),registered_at:new Date(NOW).toISOString(),state:'registered'});
function environment(options={}){
  const handlers={},shown=[],navigated=[],opened=[],closed=[],fetches=[],clientQueries=[];
  let opens=0,requests=0,now=NOW,inventories=0;
  const meta=options.meta===undefined?metadata():options.meta;
  const indexedDB={
    databases:async()=>{inventories++;if(options.inventoryPendingAfter!==undefined&&inventories>options.inventoryPendingAfter)return new Promise(()=>{});if(options.advance)now+=options.advance;return options.absent?[]:[{name:'household-push-device',version:options.version??1}];},
    open(){
      opens++;const opening={};
      queueMicrotask(()=>{
        if(options.upgrade){opening.transaction={abort(){closed.push('aborted-upgrade');}};opening.onupgradeneeded();return;}
        opening.result={version:1,objectStoreNames:{contains:name=>name==='meta'},close(){closed.push('db');},transaction(name,mode){
          assert.equal(name,'meta');assert.equal(mode,'readonly');
          const tx={objectStore(){return {get(key){assert.equal(key,'device');const query={};queueMicrotask(()=>{query.result=meta;query.onsuccess();tx.oncomplete();});return query;}};}};
          return tx;
        }};
        opening.onsuccess();
      });
      return opening;
    }
  };
  if(options.noInventory)delete indexedDB.databases;
  const clients=options.clients||[];
  const self={indexedDB,location:{origin:'https://household.example.invalid'},addEventListener(name,handler){handlers[name]=handler;},registration:{async showNotification(title,body){if(options.displayFailure)throw Error('OS refused display request');shown.push({title,...body});},async getNotifications(){return options.notices||[];}},clients:{async matchAll(query){clientQueries.push(query);return clients;},async openWindow(url){opened.push(url);}},fetch(url,init){requests++;fetches.push({url,init,shown:shown.length});if(options.fetch)return options.fetch(url,init);throw Error('No controlled network configured');}};
  if(options.locale)self.householdLocale=async()=>options.locale;
  class Clock extends Date {static now(){return now;}}
  vm.runInNewContext(source,{self,URL,Date:Clock,setTimeout,clearTimeout,AbortController,TextDecoder,Uint8Array,...(options.foreground?{MessageChannel}:{}),PUSH_ICON:'/assets/local/icon-192.png',...(options.noCategoryMessages?{}:{PUSH_CATEGORY_MESSAGES:categoryMessages}),...(options.locale?{PUSH_MESSAGES:{de:'Änderungen im Einkauf. Household öffnen.',en:'Shopping updates. Open Household.'}}:{})});
  async function dispatch(name,data){let pending;handlers[name]({...data,waitUntil(value){pending=value;}});await pending;}
  return {shown,navigated,opened,closed,fetches,clientQueries,get opens(){return opens;},get requests(){return requests;},advanceTime(ms){now+=ms;},
    push:raw=>dispatch('push',{data:{text:()=>JSON.stringify(raw),json:()=>raw}}),
    pushText:raw=>dispatch('push',{data:{text:()=>raw,json:()=>JSON.parse(raw)}}),
    click:raw=>dispatch('notificationclick',{notification:{data:raw,close(){closed.push('notification');}}}),
    reset:()=>dispatch('message',{data:{type:'PUSH_DEVICE_RESET'}})};
}

test('push displays only a fixed generic same-origin notice for the registered audience',async()=>{
  const env=environment();await env.push(payload());assert.equal(env.shown.length,1);
  const view=env.shown[0];assert.equal(view.title,'Household');assert.equal(view.body,'Änderungen im Einkauf. Household öffnen.');
  assert.equal(view.icon,'/assets/local/icon-192.png');assert.equal(view.tag,'household-shopping-'+uuid(1));
  assert.deepEqual(JSON.parse(JSON.stringify(view.data)),payload());assert.equal(env.requests,0);
});

const timerPayload=()=>({...payload(),schema:2,kind:'cooking_timer',expires_at:Math.floor(NOW/1000)+60});
function cookingClient(reply,override={}){
 return {url:'https://household.example.invalid/m/recipes/cooking/'+uuid(10),focused:true,visibilityState:'visible',postMessage(message,ports){reply(message,ports[0]);},...override};
}
test('a matching audible foreground timer still shows its required OS notice, requested silent',async()=>{
 let deliveries=0;
 const env=environment({foreground:true,clients:[cookingClient((message,port)=>{
  deliveries++;assert.equal(message.type,'COOKING_PUSH_FOREGROUND');assert.equal(message.id,uuid(1));
  port.postMessage({handled:true,id:message.id,auth_scope:message.auth_scope});
 })]});
 await env.push(timerPayload());assert.equal(deliveries,1);assert.equal(env.shown.length,1);assert.equal(env.shown[0].silent,true);
 assert.equal(env.shown[0].renotify,false);assert.equal(env.fetches[0].shown,1,'display acknowledgement occurs only after actual showNotification');
 assert.equal(env.clientQueries[0].includeUncontrolled,false);
});
test('foreground visual-only or rejected handling preserves normal OS sound policy',async()=>{
 for(const answer of [{handled:false},{handled:true,auth_scope:'d'.repeat(64)},{handled:true,id:uuid(99)},{handled:true,extra:'not allowed'}]){
  const env=environment({foreground:true,clients:[cookingClient((message,port)=>port.postMessage({id:message.id,auth_scope:message.auth_scope,...answer}))]});
  await env.push(timerPayload());assert.equal(env.shown.length,1);assert.equal(env.shown[0].silent,undefined);
 }
});
test('only focused visible same-origin cooking clients receive timer coordination',async()=>{
 for(const override of [{focused:false},{visibilityState:'hidden'},{url:'https://other.invalid/m/recipes/cooking/'+uuid(10)},{url:'https://household.example.invalid/settings'},{url:'https://household.example.invalid/m/recipes/cooking/history/'+uuid(10)}]){
  let contacted=0;const env=environment({foreground:true,clients:[cookingClient(()=>{contacted++;},override)]});
  await env.push(timerPayload());assert.equal(contacted,0);assert.equal(env.shown.length,1);assert.equal(env.shown[0].silent,undefined);
 }
});
test('foreground coordination timeout is bounded and contacts at most four clients',async()=>{
 let contacted=0;
 const env=environment({foreground:true,clients:Array.from({length:100},()=>cookingClient(()=>{contacted++;}))});
 const start=performance.now();await env.push(timerPayload());assert.equal(contacted,4);assert.equal(env.shown.length,1);assert.equal(env.shown[0].silent,undefined);
 assert.ok(performance.now()-start<1500,'single 500ms coordination deadline, not one timeout per window');
});
test('device withdrawal or expiry while coordinating prevents the later OS notice',async()=>{
 for(const change of ['expiry','withdrawal']){
  const meta=metadata();let env;
  env=environment({foreground:true,meta,clients:[cookingClient((message,port)=>{
   if(change==='expiry')env.advanceTime(61000);else meta.state='disabled';
   port.postMessage({handled:true,id:message.id,auth_scope:message.auth_scope});
  })]});
  await env.push(timerPayload());assert.equal(env.shown.length,0);assert.equal(env.requests,0);
 }
});

test('push locale changes only fixed generic copy and preserves audience and destination guards',async()=>{
  const env=environment({locale:'en'});await env.push(payload());assert.equal(env.shown[0].body,'Shopping updates. Open Household.');assert.deepEqual(JSON.parse(JSON.stringify(env.shown[0].data)),payload());assert.equal(env.requests,0);
});

test('unknown, oversized, expired and ambiguous payload shapes fail closed',async()=>{
  const variants=[null,[],{}, {...payload(),schema:2},{...payload(),kind:'open_url'},{...payload(),url:'https://outside.invalid'}, {...payload(),id:'bad'}, {...payload(),audience_scope:'bearer'}, {...payload(),expires_at:Math.floor(NOW/1000)}, {...payload(),expires_at:Math.floor(NOW/1000)+86401}, {...payload(),expires_at:1.5},{...payload(),unknown:'x'.repeat(3000)}];
  for(const value of variants){const env=environment();await env.push(value);assert.equal(env.shown.length,0);assert.equal(env.requests,0);}
});

test('missing, unknown-version or unsupported metadata does not create a database',async()=>{
  for(const options of [{absent:true},{version:2},{noInventory:true}]){const env=environment(options);await env.push(payload());assert.equal(env.opens,0);assert.equal(env.shown.length,0);}
});

test('a deletion race aborts accidental first database creation',async()=>{
  const env=environment({upgrade:true});await env.push(payload());assert.equal(env.shown.length,0);assert.ok(env.closed.includes('aborted-upgrade'));
});

test('withdrawn, different-audience and expired device metadata deny display',async()=>{
  const base=metadata();
  for(const meta of [null,{...base,state:'disabled'},{...base,schema:2},{...base,revision:0},{...base,subscription_id:'bad'},{...base,epoch:'bad'},{...base,vapid_key_id:'bad'},{...base,owner:{...base.owner,auth_scope:'d'.repeat(64)}},{...base,registered_at:new Date(NOW-30*86400000).toISOString()}]){
    const env=environment({meta});await env.push(payload());assert.equal(env.shown.length,0);
  }
});

test('payload expiry is checked again after the asynchronous device read',async()=>{
  const env=environment({advance:301000});await env.push(payload());assert.equal(env.shown.length,0);
});

test('notification click always opens the fixed local inbox',async()=>{
  const env=environment();await env.click(payload());assert.deepEqual(env.opened,['https://household.example.invalid/notifications']);assert.equal(env.requests,0);
});

test('notification click navigates and focuses an existing same-origin window',async()=>{
  const visited=[];let focused=0;
  const env=environment({clients:[{url:'https://outside.invalid/page',async navigate(){throw Error('foreign window');}},{url:'https://household.example.invalid/m/shopping/items',async navigate(url){visited.push(url);return {async focus(){focused++;}};}}]});
  await env.click(payload());assert.deepEqual(visited,['https://household.example.invalid/notifications']);assert.equal(focused,1);assert.deepEqual(env.opened,[]);
});

test('a closing existing window falls back to the same fixed inbox',async()=>{
  for(const navigate of [async()=>null,async()=>{throw Error('window closed');}]){
    const env=environment({clients:[{url:'https://household.example.invalid/',navigate}]});
    await env.click(payload());assert.deepEqual(env.opened,['https://household.example.invalid/notifications']);
  }
});

test('old audience and hostile click data cannot open any page',async()=>{
  const env=environment({meta:null});await env.click(payload());assert.deepEqual(env.opened,[]);
  const other=environment();await other.click({...payload(),url:'https://outside.invalid'});assert.deepEqual(other.opened,[]);
});

test('local withdrawal closes only the owned generic notification namespace',async()=>{
  const closed=[];
  const env=environment({notices:[{tag:'household-shopping-'+uuid(1),close(){closed.push('own');}},{tag:'unrelated',close(){closed.push('other');}}]});
  await env.reset();assert.deepEqual(closed,['own']);assert.equal(env.requests,0);
});

const devicePayload=(kind='test',seconds=60)=>({...payload(),schema:2,kind,expires_at:Math.floor(NOW/1000)+seconds});
function ackResponse(body,extra={}){
  const bytes=new TextEncoder().encode(typeof body==='string'?body:JSON.stringify(body));
  return {ok:true,status:200,redirected:false,body:new ReadableStream({start(controller){controller.enqueue(bytes);controller.close();}}),...extra};
}

test('schema 2 displays every compiled category with generic localized content',async()=>{
  const categories={chores_due:3600,chores_digest:14400,chores_escalation:3600,cooking_timer:60,shopping:86400,pantry:86400,fuel:86400,expenses:86400,meters:86400,documents:86400,mealplan:14400,security:3600,test:60};
  for(const [kind,seconds] of Object.entries(categories))for(const locale of ['de','en']){
    const env=environment({locale});const message=devicePayload(kind,seconds);await env.push(message);
    assert.equal(env.shown.length,1,kind+'/'+locale);const view=env.shown[0];
    assert.equal(view.title,'Household');assert.equal(view.tag,'household-notice-'+message.id);assert.equal(view.renotify,false);
    assert.equal(view.body.includes(message.id),false);assert.equal(view.body.includes(audience),false);
    assert.ok(locale==='en'?/[A-Za-z]/.test(view.body):view.body.includes('Household'));
    assert.deepEqual(JSON.parse(JSON.stringify(view.data)),message);
    assert.equal(env.fetches[0].shown,1,'display must happen before optional diagnostic I/O');
  }
  const en=environment({locale:'en'});await en.push(devicePayload('cooking_timer'));assert.equal(en.shown[0].body,'A cooking timer has ended. Open Household.');
  const de=environment();await de.push(devicePayload('cooking_timer'));assert.equal(de.shown[0].body,'Ein Kochtimer ist abgelaufen. Household öffnen.');
});

test('schema 2 rejects unknown fields, categories, excess category lifetime and wrong audiences',async()=>{
  for(const message of [
    {...devicePayload(),schema:3},{...devicePayload(),kind:'shopping_notice'},{...devicePayload(),kind:'__proto__'},
    {...devicePayload(),kind:'unknown'},devicePayload('cooking_timer',61),devicePayload('security',3601),devicePayload('mealplan',14401),
    {...devicePayload(),expires_at:Math.floor(NOW/1000)}, {...devicePayload(),url:'/logout'},
    {...devicePayload(),title:'Private content'},{...devicePayload(),id:'../../logout'},{...devicePayload(),audience_scope:'b'.repeat(64)},
  ]){
    const env=environment();await env.push(message);assert.equal(env.shown.length,0);assert.equal(env.requests,0);
  }
  const env=environment({advance:61000});await env.push(devicePayload());assert.equal(env.shown.length,0);
  const missing=environment({noCategoryMessages:true});await missing.push(devicePayload());assert.equal(missing.shown.length,0);assert.equal(missing.requests,0);
});

test('flat wire parser rejects duplicate and escaped duplicate keys in either schema',async()=>{
  for(const message of [payload(),devicePayload()]){
    const valid=JSON.stringify(message);
    for(const raw of [valid.replace('{','{"schema":'+message.schema+','),valid.replace('{','{"\\u0073chema":'+message.schema+','),valid+'{}',valid.replace('{','{"id":{},')]){
      const env=environment();await env.pushText(raw);assert.equal(env.shown.length,0);assert.equal(env.requests,0);
    }
    const env=environment();await env.pushText(' \n'+valid+'\n ');assert.equal(env.shown.length,1);
  }
});

test('schema 2 clicks use only the fixed authorized delivery route',async()=>{
  const message=devicePayload();const env=environment();await env.click(message);
  assert.deepEqual(env.opened,['https://household.example.invalid/notifications/device/'+message.id]);assert.equal(env.requests,0);
  const foreign=environment({meta:{...metadata(),owner:{...metadata().owner,auth_scope:'d'.repeat(64)}}});await foreign.click(message);assert.deepEqual(foreign.opened,[]);
});

test('reset closes legacy and current Household notices but preserves unrelated ones',async()=>{
  const closed=[];const env=environment({notices:['household-shopping-1','household-notice-2','other-app'].map(tag=>({tag,close(){closed.push(tag);}}))});
  await env.reset();assert.deepEqual(closed,['household-shopping-1','household-notice-2']);
});

test('display acknowledgment is bounded, same-origin, audience-checked and CSRF-protected',async()=>{
  const message=devicePayload(),csrf='x'.repeat(43);
  const env=environment({fetch:async url=>url.endsWith('/context')?ackResponse({id:message.id,auth_scope:audience,csrf}):ackResponse({accepted:true})});
  await env.push(message);assert.equal(env.shown.length,1);assert.equal(env.fetches.length,2);
  const [context,ack]=env.fetches;
  assert.equal(context.url,'/api/v1/core/push/deliveries/'+message.id+'/context');
  assert.equal(ack.url,'/api/v1/core/push/deliveries/'+message.id+'/displayed');
  for(const request of env.fetches){assert.equal(request.shown,1);assert.equal(request.init.credentials,'same-origin');assert.equal(request.init.redirect,'error');assert.equal(request.init.cache,'no-store');assert.ok(request.init.signal instanceof AbortSignal);}
  assert.equal(ack.init.method,'POST');assert.equal(ack.init.headers['X-CSRF-Token'],csrf);assert.equal(ack.init.body,'{}');
});

test('missing, hostile or oversized acknowledgment context never prevents display or posts',async()=>{
  const message=devicePayload(),csrf='x'.repeat(43);
  for(const body of [null,{}, {id:uuid(99),auth_scope:audience,csrf},{id:message.id,auth_scope:'b'.repeat(64),csrf},{id:message.id,auth_scope:audience,csrf:''},{id:message.id,auth_scope:audience,csrf,url:'/logout'},'x'.repeat(2049)]){
    const env=environment({fetch:async()=>ackResponse(body)});await env.push(message);assert.equal(env.shown.length,1);assert.equal(env.fetches.length,1);
  }
  for(const extra of [{ok:false},{redirected:true}]){const env=environment({fetch:async()=>ackResponse({},extra)});await env.push(message);assert.equal(env.shown.length,1);assert.equal(env.fetches.length,1);}
  const denied=environment({displayFailure:true});await denied.push(message);assert.equal(denied.fetches.length,0);
});

test('diagnostic timeout terminates the optional request after display',async()=>{
  let aborted=false;const env=environment({fetch:async(url,{signal})=>new Promise((resolve,reject)=>signal.addEventListener('abort',()=>{aborted=true;reject(Error('bounded timeout'));},{once:true}))});
  await env.push(devicePayload());assert.equal(env.shown.length,1);assert.equal(env.fetches.length,1);assert.equal(aborted,true);
});

test('early display conflict retries with fresh context and records only one visible notification',async()=>{
  const message=devicePayload();let posts=0,contexts=0;
  const env=environment({fetch:async url=>{
    if(url.endsWith('/context')){contexts++;return ackResponse({id:message.id,auth_scope:audience,csrf:(contexts===1?'x':'y').repeat(43)});}
    posts++;return ackResponse({},posts===1?{ok:false,status:409}:{});
  }});
  await env.push(message);
  assert.equal(env.shown.length,1);assert.equal(posts,2);assert.equal(contexts,2);
  assert.deepEqual(env.fetches.filter(value=>value.init.method==='POST').map(value=>value.init.headers['X-CSRF-Token']),['x'.repeat(43),'y'.repeat(43)]);
  assert.ok(env.fetches.every(value=>value.init.signal===env.fetches[0].init.signal),'one timeout budget must cover all attempts');
});

test('persistent display conflict stops after three attempts and other status codes never retry',async()=>{
  const message=devicePayload();
  for(const status of [409,400,401,403,404,429,500]){
    let posts=0,contexts=0;
    const env=environment({fetch:async url=>{
      if(url.endsWith('/context')){contexts++;return ackResponse({id:message.id,auth_scope:audience,csrf:'x'.repeat(43)});}
      posts++;return ackResponse({},{ok:false,status});
    }});
    await env.push(message);assert.equal(env.shown.length,1);assert.equal(posts,status===409?3:1);assert.equal(contexts,posts);
  }
});

test('expiry or a changed local audience during conflict retry prevents further requests',async()=>{
  const message=devicePayload();
  for(const mutation of ['expiry','audience','withdrawal']){
    let posts=0;const meta=metadata();
    const env=environment({meta,fetch:async url=>{
      if(url.endsWith('/context'))return ackResponse({id:message.id,auth_scope:audience,csrf:'x'.repeat(43)});
      posts++;
      if(mutation==='expiry')env.advanceTime(61000);
      if(mutation==='audience')meta.owner.auth_scope='d'.repeat(64);
      if(mutation==='withdrawal')meta.state='disabled';
      return ackResponse({},{ok:false,status:409});
    }});
    await env.push(message);assert.equal(env.shown.length,1);assert.equal(posts,1);assert.equal(env.fetches.length,2);
  }
});

test('changed server audience during conflict retry cannot post with the former context',async()=>{
  const message=devicePayload();let posts=0,contexts=0;
  const env=environment({fetch:async url=>{
    if(url.endsWith('/context')){contexts++;return ackResponse({id:message.id,auth_scope:contexts===1?audience:'d'.repeat(64),csrf:'x'.repeat(43)});}
    posts++;return ackResponse({},{ok:false,status:409});
  }});
  await env.push(message);assert.equal(env.shown.length,1);assert.equal(posts,1);assert.equal(contexts,2);
});

test('the total diagnostic deadline also bounds a stalled audience check on retry',async()=>{
  const message=devicePayload();let posts=0;
  const env=environment({inventoryPendingAfter:2,fetch:async url=>{
    if(url.endsWith('/context'))return ackResponse({id:message.id,auth_scope:audience,csrf:'x'.repeat(43)});
    posts++;return ackResponse({},{ok:false,status:409});
  }});
  await env.push(message);assert.equal(env.shown.length,1);assert.equal(posts,1);assert.equal(env.fetches.length,2);
  assert.equal(env.fetches[0].init.signal.aborted,true,'deadline must expire even while IndexedDB metadata is stalled');
});

test('controlled native registration requires both tags, actual mains and isolated authority',()=>{
  const {controlledMains,authority}=require('./run-shopping-push-testtools.cjs');
  const name='TestShoppingPushControlledExample';
  assert.deepEqual(controlledMains('//go:build integration && testtools\nfunc '+name+'(t *testing.T){}'),[name]);
  assert.deepEqual(controlledMains('//go:build integration && testtools\nfunc TestNotificationDeliveryEdge(t *testing.T){}'),['TestNotificationDeliveryEdge']);
  assert.throws(()=>controlledMains('//go:build integration\nfunc '+name+'(t *testing.T){}'));
  assert.throws(()=>controlledMains('//go:build integration && testtools\n'));
  const env={HOUSEHOLD_REQUIRE_INTEGRATION:'1',HOUSEHOLD_TEST_OWNER_URL:'postgres://household_owner:synthetic@127.0.0.1:55434/household_test?sslmode=disable',HOUSEHOLD_TEST_APP_URL:'postgres://household_app:synthetic@localhost:55434/household_test?sslmode=disable'};
  assert.doesNotThrow(()=>authority(env));assert.throws(()=>authority({...env,HOUSEHOLD_REQUIRE_INTEGRATION:'0'}));
  assert.throws(()=>authority({...env,HOUSEHOLD_TEST_APP_URL:'postgres://household_app:synthetic@outside.invalid:5432/household_test'}));
  assert.throws(()=>authority({...env,HOUSEHOLD_TEST_APP_URL:'postgres://household_app:synthetic@localhost:5432/household_test'}));
  const ci={...env,HOUSEHOLD_TEST_OWNER_URL:'postgres://household_owner:synthetic@localhost:5432/household_test?sslmode=disable',HOUSEHOLD_TEST_APP_URL:'postgres://household_app:synthetic@localhost:5432/household_test?sslmode=disable'};
  assert.throws(()=>authority(ci));assert.doesNotThrow(()=>authority({...ci,GITHUB_ACTIONS:'true'}));
  for(const extra of ['host=outside.invalid','port=5432','user=foreign','dbname=production','hostaddr=203.0.113.10','sslmode=disable'])assert.throws(()=>authority({...env,HOUSEHOLD_TEST_OWNER_URL:env.HOUSEHOLD_TEST_OWNER_URL+'&'+extra}));
  assert.throws(()=>authority({...env,HOUSEHOLD_TEST_APP_URL:'postgres://household_app@localhost:55434/household_test?sslmode=disable'}));
});

test('controlled native proof refuses zero runs, skips and unfinished subcases',()=>{
  const {terminalProof}=require('./run-shopping-push-testtools.cjs');
  const name='TestShoppingPushControlledExample',Package='household/internal/web';
  const events=[{Action:'start',Package},{Action:'run',Package,Test:name},{Action:'run',Package,Test:name+'/attempt'},{Action:'pass',Package,Test:name+'/attempt'},{Action:'pass',Package,Test:name},{Action:'pass',Package}];
  assert.deepEqual(terminalProof(events,[name]),{main_pass:1,main_fail:0,main_skip:0,cases:2});
  assert.throws(()=>terminalProof([],[name]));assert.throws(()=>terminalProof(events,[]));
  assert.throws(()=>terminalProof(events.filter(value=>value.Test!==name+'/attempt'||value.Action!=='pass'),[name]));
  assert.throws(()=>terminalProof(events.map(value=>value.Test===name+'/attempt'&&value.Action==='pass'?{...value,Action:'skip'}:value),[name]));
  assert.throws(()=>terminalProof(events.map(value=>value.Test?{...value,Test:value.Test.replace(name,'TestUnexpected')}:value),[name]),/Unexpected/);
});

test('focused controlled runs require explicit bounded selection instead of an implicit fifteen-minute full suite',()=>{
  const {profile}=require('./run-shopping-push-testtools.cjs');
  assert.throws(()=>profile([]),/Usage/);
  assert.throws(()=>profile(['--shard','6']),/Usage/);
  assert.throws(()=>profile(['--full','extra']),/Usage/);
  assert.equal(profile(['--full']).timeout,'60m');
  assert.equal(profile(['--full']).milliseconds,61*60*1000);
  for(let shard=0;shard<6;shard++)assert.equal(profile(['--shard',String(shard)]).shard,shard);
});

function notificationCIFixture(){
 const {PG_IMAGE}=require('./notification-browser-fixture.cjs');
 const id='a'.repeat(64),env={CI:'true',GITHUB_ACTIONS:'true',GITHUB_RUN_ID:'123',GITHUB_RUN_ATTEMPT:'2',HOUSEHOLD_NOTIFICATION_BROWSER_SUITE:'mealplan',HOUSEHOLD_NOTIFICATION_BROWSER_PG_ID:id,HOUSEHOLD_TEST_OWNER_URL:'postgres://household_owner:synthetic@localhost:55434/household_test?sslmode=disable',HOUSEHOLD_TEST_APP_URL:'postgres://household_app:synthetic@localhost:55434/household_test?sslmode=disable'};
 const manifest={schema:1,mode:'github-actions',run_id:'123',run_attempt:'2',suite:'mealplan',origin:'http://localhost:8101',container_id:id};
 const container={Id:id,State:{Running:true,OOMKilled:false},Config:{Image:PG_IMAGE,Labels:{'com.household.notification-browser-ci':'123-2-mealplan'}},HostConfig:{NanoCpus:2000000000,Memory:1<<30,MemorySwap:1<<30,PidsLimit:128,PortBindings:{'5432/tcp':[{HostIp:'127.0.0.1',HostPort:'55434'}]}}};
 return {env,manifest,container};
}
test('notification CI fixture requires actual owned service, exact run/image/loopback and both database roles',()=>{
 const {validateCI}=require('./notification-browser-fixture.cjs'),good=notificationCIFixture();
 assert.doesNotThrow(()=>validateCI(good.manifest,good.container,good.env));
 for(const mutate of [f=>{f.env.CI='false';},f=>{f.env.GITHUB_ACTIONS='false';},f=>{f.env.GITHUB_RUN_ATTEMPT='3';},f=>{f.container.Id='b'.repeat(64);},f=>{f.container.Config.Image='postgres:latest';},f=>{f.container.Config.Labels['com.household.notification-browser-ci']='foreign';},f=>{f.container.HostConfig.PortBindings['5432/tcp'][0].HostIp='0.0.0.0';},f=>{f.container.State.OOMKilled=true;},f=>{f.env.HOUSEHOLD_TEST_APP_URL=f.env.HOUSEHOLD_TEST_OWNER_URL;},f=>{f.env.HOUSEHOLD_TEST_APP_URL+='&host=outside.invalid';}]){
  const changed=structuredClone(good);mutate(changed);assert.throws(()=>validateCI(changed.manifest,changed.container,changed.env));
 }
});
test('notification CI authority rejects absent or changed CPU, memory, swap and process bounds',()=>{
 const {validateCI}=require('./notification-browser-fixture.cjs');
 for(const field of ['NanoCpus','Memory','MemorySwap','PidsLimit'])for(const value of [undefined,0,2,1000000000]){
  const changed=notificationCIFixture();changed.container.HostConfig[field]=value;
  assert.throws(()=>validateCI(changed.manifest,changed.container,changed.env),/resource bounds/);
 }
});
test('notification local fixture retains exact pinned database identity for Documents and Mealplan',()=>{
 const {validateLocal}=require('./notification-browser-fixture.cjs'),directory=path.resolve('.local/synthetic-review');
 const id='66be6de20312',containerID='664e69fc20765f38cbb344bca5ac690c09e30a2052ab258e58362414aa27a993';
 const resources={id,container_id:containerID,app_port:8101,port:55434,container_name:'household-ocean-app-review-'+id,directory};
 const container={Id:containerID,Name:'/'+resources.container_name,State:{Running:true},Config:{Labels:{'com.household.review-id':id}},HostConfig:{PortBindings:{'5432/tcp':[{HostIp:'127.0.0.1',HostPort:'55434'}]}}};
 assert.doesNotThrow(()=>validateLocal(resources,container,directory,true));
 assert.throws(()=>validateLocal({...resources,container_id:'b'.repeat(64)},{...container,Id:'b'.repeat(64)},directory,true),/Pinned/);
 assert.throws(()=>validateLocal({...resources,app_port:8100},container,directory,false));
});
test('notification browser acceptance rejects partial, failing, duplicated and unsafe report matrices',()=>{
 const {definitions,validateReport}=require('./run-notification-browser.cjs');
 assert.deepEqual(Object.keys(definitions),['settings','fuel','finance','meters','documents','mealplan']);
 for(const definition of Object.values(definitions)){
  const good={passed:true,physicalDeliveryTested:false,errors:[],externalRequests:[],checks:Array(definition.min).fill('check'),accessibility:Array.from({length:definition.axe},(_,n)=>({name:'scan'+n,violations:[]})),screenshots:Array.from({length:definition.captures},(_,n)=>'capture'+n+'.png')};
  assert.doesNotThrow(()=>validateReport(good,definition));
  for(const mutate of [r=>{r.passed=false;},r=>{r.failure='timeout';},r=>{r.checks.pop();},r=>{r.accessibility.pop();},r=>{r.accessibility[0].violations.push({id:'contrast'});},r=>{r.errors.push('error');},r=>{r.externalRequests.push('foreign');},r=>{r.screenshots[0]='../private.png';},r=>{r.screenshots[0]=r.screenshots[1];},r=>{r.physicalDeliveryTested=true;}]){const changed=structuredClone(good);mutate(changed);assert.throws(()=>validateReport(changed,definition));}
 }
});
test('notification runner rejects changed tool bytes and terminates only its owned timed-out child',async()=>{
 const {assertPins,runChild}=require('./run-notification-browser.cjs'),{EventEmitter}=require('node:events');
 assert.throws(()=>assertPins(path.resolve(__dirname,'..'),[{file:'scripts/notification-browser-fixture.cjs',sha256:'0'.repeat(64)}]),/bytes changed/);
 const child=new EventEmitter();child.stdout=new EventEmitter();child.stderr=new EventEmitter();let killed=0;
 await assert.rejects(runChild('synthetic-driver',{},0.01,{spawnProcess:()=>child,killProcess:owned=>{assert.equal(owned,child);killed++;setImmediate(()=>child.emit('close',null));}}),/deadline/);assert.equal(killed,1);
});
test('browser workflow requires baseline plus every isolated notification matrix result under the stable required name',()=>{
 const workflow=require('yaml').parse(fs.readFileSync(path.join(__dirname,'../.github/workflows/browser.yml'),'utf8'));
 assert.deepEqual(workflow.jobs.notifications.strategy.matrix.suite,['settings','fuel','finance','meters','documents','mealplan']);
 assert.equal(workflow.jobs.notifications.strategy['fail-fast'],false);assert.equal(workflow.jobs.notifications.strategy['max-parallel'],2);
 assert.equal(workflow.jobs.notifications['timeout-minutes'],20);assert.equal(workflow.jobs.baseline['timeout-minutes'],20);
 assert.deepEqual(workflow.jobs.notifications.services.postgres.ports,['127.0.0.1:55434:5432']);
 for(const bound of ['--cpus=2','--memory=1g','--memory-swap=1g','--pids-limit=128'])assert.ok(workflow.jobs.notifications.services.postgres.options.split(/\s+/).includes(bound));
 assert.equal(workflow.jobs.browser.name,'BrowserAccessibility');assert.deepEqual(workflow.jobs.browser.needs,['baseline','notifications']);assert.equal(workflow.jobs.browser.if,'always()');
 const gate=workflow.jobs.browser.steps[0];assert.equal(gate.env.BASELINE_RESULT,'${{ needs.baseline.result }}');assert.equal(gate.env.NOTIFICATION_RESULT,'${{ needs.notifications.result }}');
 assert.equal(gate.run.trim(),'test "$BASELINE_RESULT" = success\ntest "$NOTIFICATION_RESULT" = success');
 const artifact=workflow.jobs.notifications.steps.find(step=>step.uses?.startsWith('actions/upload-artifact@'));assert.equal(artifact.with['if-no-files-found'],'error');assert.equal(artifact.with.path,'outputs/notification-browser/${{ matrix.suite }}');
 for(const definition of Object.values(require('./run-notification-browser.cjs').definitions)){const raw=fs.readFileSync(path.join(__dirname,definition.script),'utf8');assert.match(raw,/notification-browser-fixture\.cjs/);assert.doesNotMatch(raw,/channel:'msedge'\}\)\}/);}
});

function cleanupHarness({queue=()=>Promise.resolve(),missing=false}={}){
  const app=fs.readFileSync(path.join(__dirname,'../internal/web/static/app.js'),'utf8');
  const start=app.indexOf('  async function clearPrivateDeviceData('),end=app.indexOf("  try{if(sessionStorage.getItem('household-device-cleanup-warning'))",start);
  assert.ok(start>=0&&end>start,'actual common privacy helper must be located');
  const calls=[],timers=new Map();let sequence=0;
  const window={HouseholdPantryDevice:{reset(){calls.push('pantry');return Promise.resolve();}},HouseholdShoppingDevice:missing?undefined:{reset(){calls.push('shopping');return Promise.resolve();}},HouseholdPushDevice:{reset(){calls.push('push');return Promise.resolve();}}};
  const translation=app.match(/^const _householdText_app_js = .*$/m)?.[0]||'';
  const reset=vm.runInNewContext(translation+'\n'+app.slice(start,end)+';clearPrivateDeviceData',{window,clearQueue(){calls.push('queue');return queue();},setTimeout(fn,ms){assert.equal(ms,6500);const id=++sequence;timers.set(id,fn);return id;},clearTimeout(id){timers.delete(id);}});
  return {reset,calls,expire(){for(const fn of [...timers.values()])fn();}};
}

test('common opt-out clears all offline namespaces while push withdrawal stays separate',async()=>{
  const env=cleanupHarness();await env.reset();assert.deepEqual(env.calls,['queue','pantry','shopping']);
  const logout=cleanupHarness();await logout.reset(true);assert.deepEqual(logout.calls,['queue','pantry','shopping','push']);
});

test('a blocked legacy store cannot hold logout cleanup indefinitely or claim success',async()=>{
  const env=cleanupHarness({queue:()=>new Promise(()=>{})});const result=env.reset(true);env.expire();
  await assert.rejects(result,/nicht vollständig gelöscht/);assert.deepEqual(env.calls,['queue','pantry','shopping','push']);
});

test('missing cleanup code is an honest failure after the remaining namespaces are attempted',async()=>{
  const env=cleanupHarness({missing:true});await assert.rejects(env.reset(true),/nicht vollständig gelöscht/);assert.deepEqual(env.calls,['queue','pantry','push']);
});
