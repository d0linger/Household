'use strict';
// Protocol/metadata source tests. Physical vendor delivery is a separate check.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/push-worker.js'),'utf8');
const uuid=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0');
const NOW=Date.UTC(2026,9,29,12);
const audience='a'.repeat(64);
const payload=()=>({schema:1,kind:'shopping_notice',id:uuid(1),expires_at:Math.floor(NOW/1000)+300,audience_scope:audience});
const metadata=()=>({schema:1,key:'device',epoch:uuid(2),revision:1,device_id:uuid(3),subscription_id:uuid(4),owner:{user_id:uuid(5),household_id:uuid(6),session_scope:'b'.repeat(64),auth_scope:audience},vapid_key_id:'c'.repeat(64),registered_at:new Date(NOW).toISOString(),state:'registered'});
function environment(options={}){
  const handlers={},shown=[],navigated=[],opened=[],closed=[];
  let opens=0,requests=0,now=NOW;
  const meta=options.meta===undefined?metadata():options.meta;
  const indexedDB={
    databases:async()=>{if(options.advance)now+=options.advance;return options.absent?[]:[{name:'household-push-device',version:options.version??1}];},
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
  const self={indexedDB,location:{origin:'https://household.example.invalid'},addEventListener(name,handler){handlers[name]=handler;},registration:{async showNotification(title,body){shown.push({title,...body});},async getNotifications(){return options.notices||[];}},clients:{async matchAll(){return clients;},async openWindow(url){opened.push(url);}},fetch(){requests++;throw Error('Push must never fetch');}};
  class Clock extends Date {static now(){return now;}}
  vm.runInNewContext(source,{self,URL,Date:Clock,setTimeout,clearTimeout,PUSH_ICON:'/assets/local/icon-192.png'});
  async function dispatch(name,data){let pending;handlers[name]({...data,waitUntil(value){pending=value;}});await pending;}
  return {shown,navigated,opened,closed,get opens(){return opens;},get requests(){return requests;},
    push:raw=>dispatch('push',{data:{text:()=>JSON.stringify(raw),json:()=>raw}}),
    click:raw=>dispatch('notificationclick',{notification:{data:raw,close(){closed.push('notification');}}}),
    reset:()=>dispatch('message',{data:{type:'PUSH_DEVICE_RESET'}})};
}

test('push displays only a fixed generic same-origin notice for the registered audience',async()=>{
  const env=environment();await env.push(payload());assert.equal(env.shown.length,1);
  const view=env.shown[0];assert.equal(view.title,'Household');assert.equal(view.body,'Änderungen im Einkauf. Household öffnen.');
  assert.equal(view.icon,'/assets/local/icon-192.png');assert.equal(view.tag,'household-shopping-'+uuid(1));
  assert.deepEqual(JSON.parse(JSON.stringify(view.data)),payload());assert.equal(env.requests,0);
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

test('controlled native registration requires both tags, actual mains and isolated authority',()=>{
  const {controlledMains,authority}=require('./run-shopping-push-testtools.cjs');
  const name='TestShoppingPushControlledExample';
  assert.deepEqual(controlledMains('//go:build integration && testtools\nfunc '+name+'(t *testing.T){}'),[name]);
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
});

function cleanupHarness({queue=()=>Promise.resolve(),missing=false}={}){
  const app=fs.readFileSync(path.join(__dirname,'../internal/web/static/app.js'),'utf8');
  const start=app.indexOf('  async function clearPrivateDeviceData('),end=app.indexOf("  try{if(sessionStorage.getItem('household-device-cleanup-warning'))",start);
  assert.ok(start>=0&&end>start,'actual common privacy helper must be located');
  const calls=[],timers=new Map();let sequence=0;
  const window={HouseholdPantryDevice:{reset(){calls.push('pantry');return Promise.resolve();}},HouseholdShoppingDevice:missing?undefined:{reset(){calls.push('shopping');return Promise.resolve();}},HouseholdPushDevice:{reset(){calls.push('push');return Promise.resolve();}}};
  const reset=vm.runInNewContext(app.slice(start,end)+';clearPrivateDeviceData',{window,clearQueue(){calls.push('queue');return queue();},setTimeout(fn,ms){assert.equal(ms,6500);const id=++sequence;timers.set(id,fn);return id;},clearTimeout(id){timers.delete(id);}});
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
