'use strict';
// Real unit/VM checks; standalone browser and vendor delivery are separate gates.
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),crypto=require('node:crypto');
const U=require('../internal/web/static/shopping-extension-ui.js'),D=require('../internal/web/static/shopping-device.js'),P=require('../internal/web/static/push-device.js'),N=require('../internal/web/static/shopping-notifications.js'),O=require('../internal/web/static/shopping-offline.js');
const id=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0'),copy=value=>structuredClone(value),now=Date.parse('2026-10-29T12:00:00Z');
function context(changes={}){return {user_id:id(1),household_id:id(2),generation:3,session_scope:'a'.repeat(64),auth_scope:'b'.repeat(64),role:'reader',can_write:false,offline_allowed:true,server_time:new Date(now).toISOString(),...changes};}
function item(n,changes={}){return {id:id(10000+n),version:'9007199254740993',name:'Artikel '+n,quantity:null,unit:null,group:'',note:'',checked:false,availability:'available',assignee_label:null,product_label:null,pack_label:null,replacement_label:null,...changes};}
function list(n,count=1,changes={}){return {id:id(100+n),version:'9007199254740993',name:'Liste '+n,archived:false,is_template:false,items:Array.from({length:count},(_,i)=>item(n*1001+i)),...changes};}
async function envelope(lists=[list(1)],ctx=context()){
 const value={schema:1,context:copy(ctx),captured_at:ctx.server_time,expires_at:new Date(Date.parse(ctx.server_time)+U.LIMITS.age).toISOString(),complete:true,lists:copy(lists),totals:{lists:lists.length,items:lists.reduce((n,row)=>n+row.items.length,0)}};
 value.fingerprint=crypto.createHash('sha256').update(U.goJSON(value)).digest('hex');value.totals.utf8_bytes=0;
 for(let i=0;i<10;i++){const size=Buffer.byteLength(U.goJSON(value));if(size===value.totals.utf8_bytes)return value;value.totals.utf8_bytes=size;}throw Error('Unit envelope byte fixed point did not settle');
}
test('Go JSON framing preserves HTML escaping, Unicode separators and exact string quantities',async()=>{
 const value=await envelope([list(1,1,{name:'<>&\u2028\u2029',items:[item(1,{quantity:'9007199254740993.000001',note:'界\n<>&'})]})]);
 assert.ok(U.goJSON(value).includes('\\u003c\\u003e\\u0026\\u2028\\u2029'));assert.equal(value.totals.utf8_bytes,Buffer.byteLength(U.goJSON(value)));
 await U.envelope(value,{now});assert.equal(value.lists[0].items[0].quantity,'9007199254740993.000001');
 assert.equal(await U.hash(U.fingerprintJSON(value)),value.fingerprint);
});
test('complete snapshot rejects missing totals, partial lists, stale scopes and damaged fingerprint',async()=>{
 const original=await envelope();for(const mutate of [v=>v.complete=false,v=>v.totals.items++,v=>v.lists.push(copy(v.lists[0])),v=>v.fingerprint='0'.repeat(64),v=>v.expires_at=new Date(now).toISOString(),v=>delete v.totals.utf8_bytes]){const value=copy(original);mutate(value);await assert.rejects(U.envelope(value,{now}));}
 await assert.rejects(U.envelope(original,{now,owner:U.scope(context({generation:4}))}));await assert.rejects(U.envelope(original,{now,ids:[id(999)]}));
});
test('closed list codec denies authority, images, typed money and mutation material',()=>{
 for(const extra of [{description:'private list prose'},{image_id:id(9)},{csrf:'secret'},{total_minor:'5'},{body:{method:'POST'}}])assert.throws(()=>U.validateList({...list(1),...extra}));
 for(const extra of [{assigned_to:id(9)},{endpoint:'https://private.invalid'},{price:'1.2'},{image:'blob:private'}])assert.throws(()=>U.validateList({...list(1),items:[{...item(1),...extra}]}));
 assert.throws(()=>U.validateList({...list(1),items:[item(1),item(1)]}));
});
test('unknown quantity remains distinct from exact zero and display labels never become foreign IDs',()=>{
 assert.equal(U.validateList({...list(1),items:[item(1,{quantity:null}),item(2,{quantity:'0.000000',unit:'g',assignee_label:'Person <own>'})]}).items[0].quantity,null);
 for(const quantity of [0,1.25,'1e3','-1','0.0000001'])assert.throws(()=>U.validateList({...list(1),items:[item(1,{quantity})]}));
});
test('snapshot producer whole-batch boundaries are 10 lists and 1000 items without truncation',async()=>{
 await U.envelope(await envelope(Array.from({length:10},(_,i)=>list(i,100))),{now});
 await assert.rejects(U.envelope(await envelope(Array.from({length:11},(_,i)=>list(i,1))),{now}));
 await assert.rejects(U.envelope(await envelope([list(1,1000),list(2,1)]),{now}));
});
test('device byte budget includes UTF8 storage metadata and rejects one byte beyond bound',()=>{
 const rows=[{list:list(1)}];const overhead=U.bytes(U.goJSON(rows));rows[0].list.items[0].note='x'.repeat(U.LIMITS.bytes-overhead);assert.equal(D.budget(rows).utf8_bytes,U.LIMITS.bytes);rows[0].list.items[0].note+='界';assert.throws(()=>D.budget(rows));
});
test('device leases isolate owner household generation session epoch revision and absolute expiry',()=>{
 const owner=U.scope(context()),meta={key:'device',schema:1,epoch:id(5),revision:7,owner,authorized_until:now+1000},lease=D.token(meta);D.assertToken(meta,lease,{now});
 for(const changes of [{epoch:id(6)},{revision:8},{authorized_until:now},{owner:{...owner,user_id:id(9)}},{owner:{...owner,household_id:id(9)}},{owner:{...owner,generation:4}},{owner:{...owner,session_scope:'c'.repeat(64)}}])assert.throws(()=>D.assertToken({...meta,...changes},lease,{now}));
});
test('preference body has every explicit member and reader permission does not disable personal controls',()=>{
 const view={context:context(),list:{id:id(7),name:'Own',archived:false,is_template:false},revision:'9007199254740993',preference:null};
 assert.deepEqual(N.input(view,{enabled:true,push_enabled:false,mute:'0'}),{generation:3,revision:'9007199254740993',enabled:true,push_enabled:false,mute_for_seconds:0});
 assert.deepEqual(N.input(view,{enabled:false,push_enabled:true,mute:''}),{generation:3,revision:'9007199254740993',enabled:false,push_enabled:false,mute_for_seconds:0});
 for(const mute of ['',null,'keep','60','86401'])assert.throws(()=>N.input(view,{enabled:true,push_enabled:false,mute}));
 assert.throws(()=>N.input({...view,list:{...view.list,archived:true}},{enabled:true,push_enabled:false,mute:'0'}));
});
test('mute is a server-time deadline and cannot silently be preserved or extended by an invented wire value',()=>{
 const pref={enabled:true,push_enabled:false,muted_until:new Date(now+3600000).toISOString(),enabled_since:new Date(now-1000).toISOString(),push_enabled_since:null};N.preference(pref);assert.equal(N.muted(pref,now),true);assert.equal(N.muted(pref,now+3600000),false);
 assert.throws(()=>N.preference({...pref,revision:0}));assert.throws(()=>N.preference({...pref,muted_until:'not a date'}));
});
test('local filter searches complete saved notes and labels while paging remains readonly and bounded',()=>{
 const rows=Array.from({length:251},(_,n)=>item(n,{note:n===250?'später Treffer':'',assignee_label:n===150?'Eigene Person':null}));const before=U.canonical(rows);assert.equal(O.filtered(rows,'SPÄTER')[0].id,rows[250].id);assert.equal(O.filtered(rows,'person')[0].id,rows[150].id);
 assert.equal(O.page(rows,0).items.length,100);assert.equal(O.page(rows,1).next,true);assert.equal(O.page(rows,2).items.length,51);assert.equal(O.page(rows,2).next,false);assert.equal(U.canonical(rows),before);assert.throws(()=>O.page(rows,-1));
});
test('Push uses independent Core scope and strips Native expirationTime and unknown members from the closed request',()=>{
 const own={user_id:id(1),household_id:id(2),session_scope:'a'.repeat(64),auth_scope:'f'.repeat(64)},key='c'.repeat(64);
 P.core({context:{...own,role:'reader'},state:'ready',application_server_key:'B'+'a'.repeat(86),vapid_key_id:key,device_limit:5});
 const body=P.subscriptionBody(id(3),key,{endpoint:'https://push.invalid/private',expirationTime:null,keys:{p256dh:'opaque-key',auth:'opaque-auth'},extra:'not public'});
 assert.deepEqual(Object.keys(body.subscription).sort(),['endpoint','keys']);assert.deepEqual(Object.keys(body).sort(),['device_id','subscription','vapid_key_id']);assert.equal(P.same(own,{...own,auth_scope:'b'.repeat(64)}),false);
 assert.equal(P.permissionState(true,'granted'),'granted');assert.equal(P.permissionState(true,'denied'),'denied');assert.equal(P.permissionState(false,'granted'),'unsupported');
});
test('Push metadata contains no endpoint or receiver key and registered is distinct from server delivery',()=>{
 const row={schema:1,key:'device',epoch:id(4),revision:1,device_id:id(5),owner:{user_id:id(1),household_id:id(2),session_scope:'a'.repeat(64),auth_scope:'b'.repeat(64)},subscription_id:id(6),vapid_key_id:'c'.repeat(64),registered_at:new Date(now).toISOString(),state:'registered'};P.metadata(row);
 for(const extra of [{endpoint:'https://private.invalid'},{p256dh:'key'},{auth:'receiver'},{cookie:'secret'}])assert.throws(()=>P.metadata({...row,...extra}));assert.throws(()=>P.metadata({...row,state:'delivered'}));assert.throws(()=>P.metadata({...row,owner:{...row.owner,session_scope:'raw-session-uuid'}}));
});

// Generic serialized IndexedDB adapter; ownership/expiry/quota decisions run in
// the unchanged actual browser modules, never in this storage adapter.
function storage(){
 const stores=new Map(),queue=[];let active=false,number=0,failWrite=false,afterCommit;const reads=[];
 const db={createObjectStore(name){stores.set(name,new Map());},close(){},transaction(names,mode){
  names=typeof names==='string'?[names]:names;const transaction=++number;let working,started=false,aborted=false,ended=false,outstanding=0;const jobs=[];
  const finish=()=>{if(ended||!started)return;if(aborted){ended=true;tx.onabort?.();active=false;next();return;}if(outstanding)return;ended=true;if(mode==='readwrite'){for(const [name,rows]of working)stores.set(name,rows);const hook=afterCommit;afterCommit=undefined;hook?.();}tx.oncomplete?.();active=false;next();};
  const tx={abort(){if(ended)return;aborted=true;queueMicrotask(finish);},objectStore(name){if(!names.includes(name))throw Error('Unknown unit store');const request=(action,key,operation)=>{const result={};outstanding++;const job=()=>{if(aborted){outstanding--;queueMicrotask(finish);return;}try{reads.push({transaction,mode,name,action,key});if(failWrite&&mode==='readwrite'&&['put','clear','delete'].includes(action)){failWrite=false;throw Error('Unit storage failure');}result.result=copy(operation(working.get(name)));result.onsuccess?.();}catch{aborted=true;tx.onerror?.();}finally{outstanding--;queueMicrotask(finish);}};if(started)queueMicrotask(job);else jobs.push(job);return result;};return {get:key=>request('get',key,rows=>rows.get(key)),getAll:()=>request('getAll',null,rows=>[...rows.values()]),put:value=>{const saved=copy(value);return request('put',saved.key||saved.id,rows=>{if(mode!=='readwrite')throw Error('Readonly mutation');rows.set(saved.key||saved.id,saved);});},clear:()=>request('clear',null,rows=>{if(mode!=='readwrite')throw Error('Readonly mutation');rows.clear();}),delete:key=>request('delete',key,rows=>{if(mode!=='readwrite')throw Error('Readonly mutation');rows.delete(key);})};}};
  queue.push(()=>{started=true;working=new Map(names.map(name=>[name,new Map([...stores.get(name)].map(([key,value])=>[key,copy(value)]))]));for(const job of jobs)queueMicrotask(job);queueMicrotask(finish);});next();return tx;
 }};
 function next(){if(active||!queue.length)return;active=true;queueMicrotask(queue.shift());}
 return {reads,raw:()=>copy(Object.fromEntries([...stores].map(([name,rows])=>[name,[...rows.values()]]))),failNextWrite(){failWrite=true;},afterNextCommit(fn){afterCommit=fn;},indexedDB:{open(){const request={result:db};queueMicrotask(()=>{if(!stores.size)request.onupgradeneeded?.();request.onsuccess?.();});return request;}}};
}
function runtime(name='shopping-device.js',existing){
 const disk=existing?.disk||storage(),clock=existing?.clock||{now},values=existing?.values||new Map([['household-offline-owner',id(1)]]),events=new Map(),messages=[],native={unsubscribe:0};
 const sandbox={HouseholdShoppingUI:{...U,envelope:(value,options={})=>U.envelope(value,{now:clock.now,...options})},document:{querySelectorAll:()=>[]},Date:class extends Date{static now(){return clock.now;}},TextEncoder,TextDecoder,crypto:crypto.webcrypto,indexedDB:disk.indexedDB,setTimeout,clearTimeout,queueMicrotask,structuredClone,
  localStorage:{getItem:key=>values.get(key)||null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)},addEventListener(type,fn){if(!events.has(type))events.set(type,[]);events.get(type).push(fn);},dispatchEvent(event){for(const fn of events.get(event.type)||[])fn(event);},CustomEvent:class{constructor(type,options){this.type=type;this.detail=options?.detail;}},navigator:{serviceWorker:{getRegistration:async()=>({pushManager:{getSubscription:async()=>({unsubscribe:async()=>{native.unsubscribe++;return true;}})}}),controller:{postMessage:value=>messages.push(value)}}}};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../internal/web/static',name),'utf8'),sandbox,{filename:name});return {disk,clock,values,sandbox,messages,native,device:sandbox.HouseholdShoppingDevice||sandbox.HouseholdPushDevice};
}
async function savedRuntime(){const f=runtime(),ticket=await f.device.prepare(context(),[list(1).id]),receipt=await f.device.save(ticket,await envelope());return {...f,receipt};}
test('actual device saves complete reader snapshots and verifies stored hash before display',async()=>{
 const f=await savedRuntime(),state=await f.device.inspect(),lease=f.device.token(state.meta),saved=await f.device.read(lease,list(1).id);assert.equal(saved.list.items.length,1);assert.equal(state.totals.lists,1);assert.equal(f.disk.raw().snapshots.length,1);assert.equal(state.meta.owner.generation,3);
});
test('actual read never extends expiry and fresh access prunes an expired snapshot',async()=>{
 const f=await savedRuntime(),before=f.disk.raw().snapshots[0].expires_at;f.clock.now=now+U.LIMITS.age-1;await f.device.inspect();assert.equal(f.disk.raw().snapshots[0].expires_at,before);f.clock.now++;await assert.rejects(f.device.read(f.receipt.lease,list(1).id));const state=await f.device.inspect();assert.equal(state.snapshots.length,0);assert.equal(f.disk.raw().snapshots.length,0);
});
test('new session requires explicit full refetch and old generation cannot be automatically rebound',async()=>{
 const f=await savedRuntime(),fresh=context({session_scope:'d'.repeat(64),auth_scope:'e'.repeat(64)});await assert.rejects(f.device.prepare(fresh,[list(2).id]),error=>error.code==='SESSION');const ticket=await f.device.prepare(fresh,[list(2).id],{reauthorize:true});assert.deepEqual([...ticket.ids].sort(),[list(1).id,list(2).id].sort());const result=await f.device.save(ticket,await envelope([list(1),list(2)],fresh));assert.equal(result.snapshots.length,2);await assert.rejects(f.device.prepare(context({generation:4}),[list(1).id],{reauthorize:true}),error=>error.code==='GENERATION');
});
test('two prepared tabs cannot both commit against the same device revision',async()=>{
 const f=await savedRuntime(),a=await f.device.prepare(context(),[list(2).id]),b=await f.device.prepare(context(),[list(3).id]);const results=await Promise.allSettled([f.device.save(a,await envelope([list(2)])),f.device.save(b,await envelope([list(3)]))]);assert.equal(results.filter(row=>row.status==='fulfilled').length,1);assert.equal(f.disk.raw().snapshots.length,2);
});
test('reset physically clears all snapshots and owner and prevents a late pending response from resurrecting them',async()=>{
 const f=await savedRuntime(),pending=await f.device.prepare(context(),[list(2).id]);await f.device.reset();await assert.rejects(f.device.save(pending,await envelope([list(2)])));const raw=f.disk.raw();assert.equal(raw.snapshots.length,0);assert.equal(raw.meta.length,1);assert.equal(raw.meta[0].owner,null);assert.notEqual(raw.meta[0].epoch,pending.epoch);
});
test('actual account observation physically clears a foreign household owner without rebinding content',async()=>{
 const f=await savedRuntime();await f.device.observe(id(9),id(10));assert.equal(f.disk.raw().snapshots.length,0);assert.equal(f.disk.raw().meta[0].owner,null);await assert.rejects(f.device.read(f.receipt.lease,list(1).id));
});
test('failed atomic write preserves the previous complete snapshot and cannot report successful reset',async()=>{
 const f=await savedRuntime(),before=U.canonical(f.disk.raw()),pending=await f.device.prepare(context(),[list(2).id]);f.disk.failNextWrite();await assert.rejects(f.device.save(pending,await envelope([list(2)])));assert.equal(U.canonical(f.disk.raw()),before);f.disk.failNextWrite();await assert.rejects(f.device.reset());assert.equal(f.disk.raw().snapshots.length,1);
});
test('explicit snapshot read keeps exact closed POST body and scope change before send makes zero requests',async()=>{
 const calls=[],ctx=context(),device={observe:async()=>{},prepare:async()=>({target_owner:U.scope(ctx),ids:[id(8)]}),save:async(ticket,response)=>({ticket,response})},client={current:ctx,refresh:async()=>{},csrf:async()=>{},request:async(url,options)=>{calls.push({url,options});return {complete:true};}};
 await O.saveCopy({device,client,ids:[id(8)]});assert.deepEqual(calls[0].options,{method:'POST',body:{generation:3,list_ids:[id(8)]}});calls.length=0;client.csrf=async()=>{client.current=context({generation:4});};await assert.rejects(O.saveCopy({device,client,ids:[id(8)]}));assert.equal(calls.length,0);
});
test('explicit Push reset unsubscribes native capability and clears raw owner metadata plus visible SW notices',async()=>{
 const f=runtime('push-device.js');await f.device.reset();const raw=f.disk.raw();assert.equal(raw.meta.length,1);assert.equal(raw.meta[0].owner,null);assert.equal(raw.meta[0].state,'disabled');assert.equal(f.native.unsubscribe,1);assert.deepEqual(copy(f.messages),[{type:'PUSH_DEVICE_RESET'}]);
});
function readerRuntime(fetch,device){const sandbox={HouseholdShoppingDevice:device,document:{body:{dataset:{}},querySelector:()=>({content:''})},fetch,AbortController,TextEncoder,TextDecoder,crypto:crypto.webcrypto,setTimeout,clearTimeout,addEventListener(){},removeEventListener(){}};vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../internal/web/static/shopping-extension-ui.js'),'utf8'),sandbox);return sandbox.HouseholdShoppingUI;}
test('HTTP reader stops at the byte cap and cancels the stream before materializing an unbounded body',async()=>{
 let reads=0,cancelled=0;const chunk=new Uint8Array(65536),client=readerRuntime(async()=>({ok:true,body:{getReader(){return {async read(){reads++;return {done:false,value:chunk};},async cancel(){cancelled++;},releaseLock(){}};}}}));
 await assert.rejects(client.raw('/api/v1/shopping/extensions/context'),failure=>failure.code==='BUDGET');assert.equal(cancelled,1);assert.equal(reads,34);
});
test('HTTP streaming decoder preserves split multibyte UTF8 and refuses malformed bytes',async()=>{
 const encoded=new TextEncoder().encode('{"label":"界"}');let at=0;const valid=readerRuntime(async()=>({ok:true,body:{getReader(){return {async read(){return at<encoded.length?{done:false,value:encoded.slice(at,++at)}:{done:true};},async cancel(){},releaseLock(){}};}}}));assert.equal((await valid.raw('/api/v1/shopping/extensions/context')).label,'界');
 let done=false;const broken=readerRuntime(async()=>({ok:true,body:{getReader(){return {async read(){if(done)return {done:true};done=true;return {done:false,value:Uint8Array.of(255)};},async cancel(){},releaseLock(){}};}}}));await assert.rejects(broken.raw('/api/v1/shopping/extensions/context'));
});
test('observed new session suspends raw lease durably across reload and requires refetch of every retained copy',async()=>{
 const f=await savedRuntime(),next=context({session_scope:'d'.repeat(64),auth_scope:'e'.repeat(64)});await f.device.enforceContext(next);assert.equal(f.disk.raw().snapshots.length,1);assert.equal(f.disk.raw().meta[0].authorized_until,0);
 const reopened=runtime('shopping-device.js',f),state=await reopened.device.inspect();assert.equal(state.snapshots.length,0);assert.deepEqual([...state.list_ids],[list(1).id]);await assert.rejects(reopened.device.read(f.receipt.lease,list(1).id));await assert.rejects(reopened.device.prepare(next,[list(2).id]),failure=>failure.code==='SESSION');const ticket=await reopened.device.prepare(next,[list(2).id],{reauthorize:true});assert.equal(ticket.ids.length,2);await reopened.device.save(ticket,await envelope([list(1),list(2)],next));assert.equal((await reopened.device.inspect()).snapshots.length,2);
});
test('context404 suspension and same-scope recovery cannot revive other rows by saving only one list',async()=>{
 const f=await savedRuntime(),ui=readerRuntime(async()=>new Response('{"error":"paused"}',{status:404}),f.device),panel={querySelectorAll:()=>[]};await assert.rejects(ui.client(panel).refresh());const reopened=runtime('shopping-device.js',f);await assert.rejects(reopened.device.prepare(context(),[list(2).id]),failure=>failure.code==='SESSION');const ticket=await reopened.device.prepare(context(),[list(2).id],{reauthorize:true});assert.equal(ticket.full_refetch,true);assert.equal(ticket.ids.length,2);assert.equal((await reopened.device.inspect()).snapshots.length,0);
});
test('actual shared refresh and resource request401/403 physically reset instead of leaving an offline-readable lease',async()=>{
 for(const status of [401,403])for(const method of ['refresh','request']){const f=await savedRuntime();let deny=false;const ui=readerRuntime(async()=>new Response(JSON.stringify(deny?{error:'denied'}:{context:context()}),{status:deny?status:200}),f.device),section={hidden:false},client=ui.client({querySelectorAll:()=>[section]});await client.refresh();deny=true;await assert.rejects(method==='refresh'?client.refresh():client.request('/api/v1/shopping/lists/'+list(1).id+'/notification-preferences?generation=3'));assert.equal(section.hidden,true);assert.equal(f.disk.raw().snapshots.length,0);assert.equal(f.disk.raw().meta[0].owner,null);const reopened=runtime('shopping-device.js',f);assert.equal((await reopened.device.inspect()).snapshots.length,0);await assert.rejects(reopened.device.read(f.receipt.lease,list(1).id));}
});
test('server offline consent withdrawal clears raw copies while independent personal notification context remains available',async()=>{
 const f=await savedRuntime(),ctx=context({offline_allowed:false}),ui=readerRuntime(async()=>new Response(JSON.stringify({context:ctx}),{status:200}),f.device),client=ui.client({querySelectorAll:()=>[]});const result=await client.refresh();assert.equal(result.context.offline_allowed,false);assert.equal(f.disk.raw().snapshots.length,0);assert.equal(f.disk.raw().meta[0].owner,null);await assert.rejects(O.readCopies({device:f.device,client,online:true}),failure=>failure.code==='CONSENT');
});
test('online private-copy opening checks current authority first and never falls back on a known denied or mismatched scope',async()=>{
 const f=await savedRuntime(),order=[],client={current:context({generation:4}),async refresh(){order.push('authority');},};const observed={...f.device,async inspect(){order.push('storage');return f.device.inspect();}};await assert.rejects(O.readCopies({device:observed,client,online:true}),failure=>failure.code==='GENERATION');assert.deepEqual(order,['authority','storage']);await assert.rejects(O.readCopies({device:observed,client,online:false,blocked:true}));assert.deepEqual(order,['authority','storage']);
});
test('cross-tab denial between commit and authorization cannot be erased by an older save even if reset fails',async()=>{
 const f=await savedRuntime(),other=runtime('shopping-device.js',f),pending=await f.device.prepare(context(),[list(2).id]);let denial;
 f.disk.afterNextCommit(()=>{f.disk.failNextWrite();denial=other.device.reset();denial.catch(()=>{});});await assert.rejects(f.device.save(pending,await envelope([list(2)])),failure=>failure.code==='STALE');await assert.rejects(denial);
 const denied=f.values.get('household-shopping-readlists-denied'),authorized=f.values.get('household-shopping-readlists-authorized')||'';assert.notEqual(denied,authorized);assert.equal(f.disk.raw().snapshots.length,2);const reopened=runtime('shopping-device.js',f);assert.equal((await reopened.device.inspect()).snapshots.length,0);await assert.rejects(reopened.device.read(f.receipt.lease,list(1).id));
});
test('actual Push API wrapper discards visible old authority and withdraws native capability on401/403 only',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/push-device.js'),'utf8'),start=source.indexOf('  async function read(url,options)'),end=source.indexOf('\n  function locks()',start);assert.ok(start>=0&&end>start);
 for(const statusCode of [401,403,500]){const f=runtime('push-device.js');let clears=0;const failure=Object.assign(Error('safe refusal'),{status:statusCode}),harness=vm.runInNewContext('let revision=0,current={ready:true},rows=[{}],ticket={pending:true};'+source.slice(start,end)+';({read,state:()=>({revision,current,rows,ticket})})',{U:{...U,raw:async()=>{throw failure;}},$:()=>({replaceChildren(){clears++;}}),locks(){},reset:f.device.reset,status(){}});await assert.rejects(harness.read('/api/v1/core/push/context'));const state=harness.state();if(statusCode===500){assert.equal(state.rows.length,1);assert.equal(f.native.unsubscribe,0);}else{assert.equal(state.current,null);assert.equal(state.ticket,null);assert.equal(state.rows.length,0);assert.equal(clears,1);assert.equal(f.native.unsubscribe,1);assert.equal(f.disk.raw().meta[0].owner,null);}}
});
function browserWait(){
 const source=fs.readFileSync(path.join(__dirname,'browser-shopping-extensions-check.cjs'),'utf8'),start=source.indexOf('async function waitForActual('),end=source.indexOf('\nasync function rawDevice(',start);assert.ok(start>=0&&end>start);
 const timers=new Set(),later=(fn,delay)=>{const handle=setTimeout(()=>{timers.delete(handle);fn();},delay);timers.add(handle);return handle;},cancel=handle=>{timers.delete(handle);clearTimeout(handle);};
 return {wait:vm.runInNewContext(source.slice(start,end)+';waitForActual',{performance:require('node:perf_hooks').performance,setTimeout:later,clearTimeout:cancel}),timers};
}
test('actual browser wait awaits false Promise results and retries until the real predicate is true',async()=>{
 const helper=browserWait();let attempts=0;await helper.wait(async()=>{attempts++;await Promise.resolve();return attempts===3;},{timeout:2000,interval:1,message:'predicate never became true'});assert.equal(attempts,3);assert.equal(helper.timers.size,0);
});
test('actual browser wait bounds both false and unresolved predicates without leaking a deadline timer',async()=>{
 for(const predicate of [async()=>false,()=>new Promise(()=>{})]){const helper=browserWait();await assert.rejects(helper.wait(predicate,{timeout:5,interval:1,message:'bounded actual-state deadline'}),/bounded actual-state deadline/);assert.equal(helper.timers.size,0);}
});

function browserDiagnostics(){
 const source=fs.readFileSync(path.join(__dirname,'browser-shopping-extensions-check.cjs'),'utf8'),start=source.indexOf('async function failureDiagnostics('),end=source.indexOf('\nconst uuid=',start);assert.ok(start>=0&&end>start);
 const timers=new Set(),later=fn=>{const handle=setTimeout(()=>{timers.delete(handle);fn();},1);timers.add(handle);return handle;},cancel=handle=>{timers.delete(handle);clearTimeout(handle);};
 return {read:vm.runInNewContext(source.slice(start,end)+';failureDiagnostics',{safe:value=>String(value).replaceAll('test-secret','[redacted]'),setTimeout:later,clearTimeout:cancel}),timers};
}
test('shopping browser failure evidence redacts text and releases its diagnostic timer',async()=>{
 const helper=browserDiagnostics(),result=await helper.read({evaluate:async()=>({online:false,status:'test-secret',stored_list_buttons:0})});assert.equal(result.online,false);assert.equal(result.status,'[redacted]');assert.equal(result.stored_list_buttons,0);assert.equal(helper.timers.size,0);
});
test('shopping browser failure diagnostics cannot hang or replace the original failure',async()=>{
 for(const evaluate of [async()=>{throw Error('test-secret');},()=>new Promise(()=>{})]){const helper=browserDiagnostics(),result=await helper.read({evaluate});assert.equal(typeof result.unavailable,'string');assert.doesNotMatch(result.unavailable,/test-secret/);assert.equal(helper.timers.size,0);}
});
