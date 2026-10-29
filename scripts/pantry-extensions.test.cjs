'use strict';
// Pure tests for the implemented Pantry module; browser execution is separate.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const D=require('../internal/web/static/pantry-device.js');
const {total}=require('../internal/web/static/pantry-leftovers.js');
const {gtin,sourceURL}=require('../internal/web/static/pantry-food-facts.js');
const {camera,sendPending,readTransferredDraft}=require('../internal/web/static/pantry-scans.js');
const {operation,client}=require('../internal/web/static/pantry-extension-ui.js');
const batches=require('../internal/web/static/pantry-batches.js');
const daily=require('../internal/web/static/pantry-daily.js');
const cooking=require('../internal/web/static/cooking-stock.js');
const id=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0');
const scope={user:id(1),household:id(2),generation:7,session_hash:'a'.repeat(64)};
function draft(){return {format:1,id:id(3),owner:{...scope},epoch:id(4),namespace:D.namespace(scope,id(4)),revision:1,created:1000,expires:1000+D.LIMITS.age,lines:[D.line({id:id(5),code:'000AbC',quantity:'0,125',label:'Eigene Dose'})],handoff:null};}

test('real attribution CSV acceptance preserves quoted source JSON and refuses ambiguous rows',()=>{
 const source=require('node:fs').readFileSync(require('node:path').join(__dirname,'browser-pantry-extensions-check.cjs'),'utf8');
 const start=source.indexOf('function parseCSV('),end=source.indexOf('\n(async()=>{',start);
 assert.ok(start>=0&&end>start);
 const parse=require('node:vm').runInNewContext(source.slice(start,end)+';parseCSV',{Buffer});
 const values=text=>JSON.parse(JSON.stringify(parse(text)));
 assert.deepEqual(values('name,source_provenance\r\n"Food, ""own""","[{""source"":{""name"":""Milk""}}]"\r\n'),[{name:'Food, "own"',source_provenance:'[{"source":{"name":"Milk"}}]'}]);
 assert.deepEqual(values('name,notes\n"Ä","line one\r\nline two"\n'),[{name:'Ä',notes:'line one\r\nline two'}]);
 for(const bad of['name,name\na,b\n','name,source_provenance\nonly-one\n','name\n"unfinished','name\n"closed"suffix\n'])assert.throws(()=>parse(bad));
 const bounded=require('node:vm').runInNewContext(source.slice(start,end)+';parseCSV',{Buffer:{byteLength:()=>16*1024*1024+1}});
 assert.throws(()=>bounded('name\nx\n'),/export bound/);
});

test('actual OFF acceptance chooses only the exact fixed provider enum',()=>{
 const source=require('node:fs').readFileSync(require('node:path').join(__dirname,'browser-pantry-extensions-check.cjs'),'utf8');
 const start=source.indexOf('function foodFactsTarget('),end=source.indexOf('\nconst expectedFoodFacts=',start);
 assert.ok(start>=0&&end>start);
 const target=require('node:vm').runInNewContext(source.slice(start,end)+';foodFactsTarget');
 const value=input=>JSON.parse(JSON.stringify(target(input)));
 assert.deepEqual(value(),{provider:'off-staging',host:'world.openfoodfacts.net'});
 assert.deepEqual(value('off-production'),{provider:'off-production',host:'world.openfoodfacts.org'});
 for(const input of['','off-production ','off-staging.evil','https://world.openfoodfacts.org','__proto__','toString'])assert.throws(()=>target(input));
});

test('scan identity preserves local codes and exact decimal proposals',()=>{
 assert.equal(D.code('  000AbC\n'),'000AbC');assert.equal(D.code('https://example.invalid/a'),'https://example.invalid/a');
 for(const code of['','two codes','ä','x\0','x\x7f','x'.repeat(65)])assert.throws(()=>D.code(code));
 assert.equal(D.decimal('0001,000001'),'1.000001');assert.equal(D.decimal('999999999999.999999'),'999999999999.999999');
 for(const value of['','0','-1','0.0000001','1e3','1 000','1000000000000'])assert.throws(()=>D.decimal(value));
 assert.equal(D.line({id:id(6),code:'00012'}).quantity,'');
});
test('draft schema rejects hidden authority, credentials and executable request material',()=>{
 const value=draft(),meta={epoch:id(4),owner:scope};assert.equal(D.validateDraft(value,meta),value);
 for(const extra of[{body:{data:{quantity_delta:'5'}}},{stock:'8'},{csrf:'secret'},{cookie:'secret'},{image:'blob:private'}])assert.throws(()=>D.validateDraft({...value,...extra},meta));
 for(const extra of[{stock:'8'},{url:'/api/v1/m/pantry/stock_movements'},{method:'POST'}])assert.throws(()=>D.validateDraft({...value,lines:[{...value.lines[0],...extra}]},meta));
 assert.throws(()=>D.validateDraft({...value,expires:value.created+D.LIMITS.age+1},meta));
 assert.throws(()=>D.validateDraft({...value,lines:[value.lines[0],value.lines[0]]},meta));
});
test('draft namespaces and leases isolate household, generation, session, epoch and revision',()=>{
 const meta={epoch:id(4),revision:2,owner:scope,authorized_until:9999},lease={epoch:id(4),revision:2,owner:scope};D.assertLease(meta,lease,1000);
 for(const changed of[{epoch:id(7)},{revision:3},{owner:{...scope,household:id(8)}},{owner:{...scope,generation:8}},{owner:{...scope,session_hash:'b'.repeat(64)}},{authorized_until:1000}])assert.throws(()=>D.assertLease({...meta,...changed},lease,1000));
 assert.notEqual(D.namespace(scope,id(4)),D.namespace({...scope,session_hash:'b'.repeat(64)},id(4)));
 assert.notEqual(D.namespace(scope,id(4)),D.namespace(scope,id(7)));
});
test('global scan limits apply across drafts and use UTF-8 bytes',()=>{
 const value=draft(),now=2000;value.lines=Array.from({length:100},(_,i)=>D.line({id:id(100+i),code:'SCAN'+i,label:'ä'}));D.budget([value],now);
 assert.throws(()=>D.budget([value,{...draft(),id:id(9)}],now),/100/);
 assert.throws(()=>D.budget([{...draft(),lines:[{id:id(10),code:'A',label:'界'.repeat(90000)}]}],now),/256/);
 assert.equal(D.LIMITS.age,7*24*60*60*1000);
 assert.equal(D.canonical({b:1,a:[{z:3,y:2}]}),D.canonical({a:[{y:2,z:3}],b:1}));
});
test('leftover totals preserve micro-portions, empty unknowns and the complete cooking cap',()=>{
 assert.equal(total([{id:id(1),quantity:'0.000001'},{id:id(2),quantity:'1.249999'}],'1.25'),'1.250000');
 assert.throws(()=>total([{id:id(1),quantity:''}]));assert.throws(()=>total([{id:id(1),quantity:'1.250001'}],'1.25'));
 assert.throws(()=>total(Array.from({length:21},(_,i)=>({id:id(i),quantity:'1'}))));
 assert.throws(()=>total([{id:id(1),quantity:'1'},{id:id(1),quantity:'1'}]));
});
test('portion is a separate serving dimension in all reused physical helpers',()=>{
 assert.equal(batches.intake('0.5','portion','portion',null,false),'0.500000');
 assert.equal(batches.intake('2','portion','portion',{quantity:'0.25',unit_code:'portion'},false),'0.500000');
 for(const unit of['piece','g','kg','ml','l']){assert.throws(()=>batches.intake('1',unit,'portion',null,false));assert.throws(()=>batches.intake('1','portion',unit,null,false));}
 assert.deepEqual(daily.amount('half','','1.25',null,'portion',false),{quantity:'0.625000',after:'0.625000'});
 assert.throws(()=>daily.amount('pack','1','5',{quantity:'1',unit:'piece'},'portion',false));
 assert.equal(cooking.suggestion({quantity:'1',unit_code:'portion'},'piece','1','1'),null);
 assert.equal(cooking.suggestion({quantity:'1',unit_code:'portion'},'portion','1','0.5').value,'0.500000');
});
test('OFF eligibility uses original GTIN digits and check digit, never household-code coercion',()=>{
 for(const code of['96385074','036000291452','4006381333931'])assert.equal(gtin(code),true,code);
 for(const code of['4006381333932','ABC123','03600029145','+4006381333931'])assert.equal(gtin(code),false,code);
 assert.equal(sourceURL('https://world.openfoodfacts.org/product/036000291452'),'https://world.openfoodfacts.org/product/036000291452');
 for(const url of['javascript:alert(1)','https://world.openfoodfacts.org.evil.invalid/a','https://user:pass@world.openfoodfacts.org/a','https://world.openfoodfacts.org:444/a','http://world.openfoodfacts.org/a'])assert.equal(sourceURL(url),null);
});

function flowFixture({executeError=false,expires=20,receiptError=false}={}){
 const calls=[],events=[],values=new Map(),client={current:{user_id:id(1),household_id:id(2),generation:7},request:async(url,options)=>{calls.push({url,options});if(url.endsWith('/preview'))return {review:'signed-private-review',expires};if(url.endsWith('/execute')){if(executeError)throw Error('response lost');return {id:id(40)};}return {state:'unknown'};}};
 const storage={setItem:(k,v)=>values.set(k,v),getItem:k=>values.get(k)||null,removeItem:k=>values.delete(k)};
 const config={kind:'leftovers',base:'/api/v1/pantry/leftovers',client,status:m=>events.push(m),onReceipt:r=>{if(receiptError)throw Error('invalid receipt');events.push(r);},onRefused:r=>events.push(r),onReview:r=>events.push(r),onUncertain:()=>events.push('uncertain'),storage,random:()=>id(30),now:()=>1000};
 return {flow:operation(config),calls,events,values,client,config};
}
test('review and execution preserve one key and persist only an actor-bound locator',async()=>{
 const f=flowFixture();await f.flow.review({generation:7,name:'Private dish'});await f.flow.execute();
 assert.equal(f.calls[0].options.key,f.calls[1].options.key);assert.deepEqual(f.calls[1].options.body,{review:'signed-private-review'});
 assert.equal(f.flow.terminal,true);assert.equal(f.values.size,0);
 await assert.rejects(f.flow.review({generation:7}));f.flow.fresh();assert.equal(f.flow.terminal,false);
});
test('unknown execution never silently retries, rekeys or edits',async()=>{
 const f=flowFixture({executeError:true});await f.flow.review({generation:7,name:'Private dish'});await assert.rejects(f.flow.execute());
 assert.equal(f.flow.blocked,true);assert.equal(f.calls.length,2);assert.throws(()=>f.flow.fresh());assert.throws(()=>f.flow.edit());await assert.rejects(f.flow.review({generation:7}));
 const locator=[...f.values.values()][0];assert.ok(locator.includes(id(30)));assert.ok(!locator.includes('signed-private-review'));assert.ok(!locator.includes('Private dish'));assert.ok(!locator.includes('csrf'));
 await f.flow.status();assert.equal(f.flow.blocked,true);assert.match(f.calls.at(-1).url,new RegExp(id(30)+'\\?generation=7$'));
});
test('preview expiry sends no execution and malformed accepted reply stays recoverable',async()=>{
 const expired=flowFixture({expires:1});await expired.flow.review({generation:7});await assert.rejects(expired.flow.execute(),/abgelaufen/);assert.equal(expired.calls.length,1);
 const malformed=flowFixture({receiptError:true});await malformed.flow.review({generation:7});await assert.rejects(malformed.flow.execute());assert.equal(malformed.flow.blocked,true);assert.equal(malformed.values.size,1);
});
test('reload restores metadata only and refuses another actor or malformed locator',async()=>{
 const f=flowFixture({executeError:true});await f.flow.review({generation:7});await assert.rejects(f.flow.execute());
 const restored=operation(f.config);assert.equal(await restored.restore(),true);assert.equal(restored.blocked,true);assert.equal(f.calls.length,2);
 f.client.current={...f.client.current,user_id:id(99)};assert.equal(await operation(f.config).restore(),false);assert.equal(f.values.size,0);
 f.values.set('household-pantry-pending:leftovers',JSON.stringify({key:'../other',generation:7,user:id(99),household:id(2)}));assert.equal(await operation(f.config).restore(),false);
});
test('only actual terminal status or explicit retired-generation reset permits a new operation',async()=>{
 const f=flowFixture({executeError:true});await f.flow.review({generation:7});await assert.rejects(f.flow.execute());
 f.client.request=async()=>({state:'refused'});await f.flow.abandon();assert.equal(f.flow.terminal,true);f.flow.fresh();assert.equal(f.flow.blocked,false);
 const old=flowFixture({executeError:true});await old.flow.review({generation:7});await assert.rejects(old.flow.execute());old.client.current.generation=8;assert.equal(old.flow.retired,true);old.flow.fresh();assert.equal(old.flow.blocked,false);
});

test('same actor with generation+1 followed by focus never reopens a purged private section',async()=>{
 const section={hidden:true,dataset:{open:'false'}},events={addEventListener(){}},pageDocument={hidden:false,addEventListener(){}};
 const panel={querySelectorAll:selector=>selector.includes('data-open')?(section.dataset.open==='true'?[section]:[]):[section]};
 let generation=7,cleared=0;
 const scope=client(panel,{events,pageDocument,dispatchLock(){},onLock(){cleared++;},read:async()=>({context:{auth_scope:'unchanged-identity',generation,user_id:id(1),household_id:id(2),server_time:'2026-10-28T12:00:00Z'}})});
 await scope.context();scope.open(section);assert.equal(section.hidden,false);scope.assert();
 generation=8;await scope.revalidate();
 assert.equal(section.hidden,true);assert.equal(cleared,1);assert.throws(()=>scope.assert());
 await scope.revalidate();assert.equal(section.hidden,true);assert.throws(()=>scope.assert());
});

// Unit-only IndexedDB request/commit adapter. It implements generic storage
// operations, not Pantry expiry/ownership rules. The real device implementation
// below performs activate, prune, lease and atomic pending checks unchanged.
function deviceStorage(){
 const stores=new Map(),reads=[];let sequence=0;
 const copy=value=>structuredClone(value);
 const db={createObjectStore(name){stores.set(name,new Map());},close(){},transaction(names,mode){
  const number=++sequence,working=new Map(names.map(name=>[name,new Map([...stores.get(name)].map(([key,value])=>[key,copy(value)]))]));
  let outstanding=0,aborted=false,completed=false;
  const tx={abort(){if(aborted||completed)return;aborted=true;queueMicrotask(()=>tx.onabort?.());},objectStore(name){
   const values=working.get(name),write=operation=>{if(mode!=='readwrite')throw Error('Readonly unit transaction attempted a write');return operation();};
   function request(action,operation,key){
    const result={};outstanding++;
    queueMicrotask(()=>{
     if(aborted)return;
     try{reads.push({transaction:number,mode,store:name,action,key});result.result=copy(operation());result.onsuccess?.();}
     catch(error){tx.error=error;tx.abort();}
     finally{outstanding--;queueMicrotask(()=>{if(!aborted&&!completed&&outstanding===0){completed=true;if(mode==='readwrite')for(const [store,rows]of working)stores.set(store,rows);tx.oncomplete?.();}});}
    });return result;
   }
   return {
    get:key=>request('get',()=>values.get(key),key),
    getAll:()=>request('getAll',()=>[...values.entries()].sort(([a],[b])=>a.localeCompare(b)).map(([,value])=>value)),
    put:value=>request('put',()=>write(()=>{const key=name==='meta'?value.key:value.id;values.set(key,copy(value));return key;})),
    delete:key=>request('delete',()=>write(()=>values.delete(key)),key),
    clear:()=>request('clear',()=>write(()=>values.clear()))
   };
  }};return tx;
 }};
 const indexedDB={open(){const pending={result:db};queueMicrotask(()=>{if(!stores.size)pending.onupgradeneeded?.();pending.onsuccess?.();});return pending;}};
 return {indexedDB,reads,mutate(name,key,fn){const value=copy(stores.get(name).get(key));fn(value);stores.get(name).set(key,value);}};
}
async function pendingFixture(){
 const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),crypto=require('node:crypto'),storage=deviceStorage(),clock={now:1000},values=new Map();let uuidCalls=0;
 const context={user_id:id(1),household_id:id(2),generation:7,auth_scope:'unit-current-authority',can_write:true,role:'admin'};
 values.set('household-offline-owner',context.user_id);
 const sandbox={document:{},Date:{now:()=>clock.now},TextEncoder,TextDecoder,crypto:{subtle:crypto.webcrypto.subtle,randomUUID:()=>{uuidCalls++;return crypto.randomUUID();}},indexedDB:storage.indexedDB,
  localStorage:{getItem:key=>values.get(key)||null,setItem:(key,value)=>values.set(key,value)},addEventListener(){},dispatchEvent(){},CustomEvent:class {constructor(type){this.type=type;}}};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../internal/web/static/pantry-device.js'),'utf8'),sandbox);
 const device=sandbox.HouseholdPantryDevice,owner=device.owner(context,await device.hash(context.auth_scope));
 let lease=await device.activate(owner),changed=await device.create(lease),draft=changed.drafts.at(-1);lease=changed.lease;
 changed=await device.save(lease,{...draft,lines:[device.line({id:id(50),code:'UNIT-001',quantity:'1'})]});lease=changed.lease;draft=changed.drafts.find(row=>row.id===draft.id);
 const body=JSON.stringify({generation:7,version:0,kind:'intake',title:'Unit request retained in memory',location_id:'',lines:[]}),digest=await device.hash(JSON.parse(body));
 changed=await device.handoff(lease,draft.id,{draft_id:id(60),key:id(61),digest,phase:'uncertain'});lease=changed.lease;draft=changed.drafts.find(row=>row.id===draft.id);
 const pending={id:id(60),key:id(61),digest,body,localID:draft.id,owner:{...draft.owner},epoch:draft.epoch,draftRevision:draft.revision,expires:draft.expires},calls=[];
 const client={current:{...context},async context(){return this.current;},async request(url,options){calls.push({url,options});return options?.method==='PUT'?{id:pending.id}:{id:pending.id,owner_id:this.current.user_id,generation:this.current.generation,kind:'intake'};}};
 return {device,clock,storage,values,owner,pending,client,calls,lease,get uuidCalls(){return uuidCalls;}};
}
test('expired unknown pending survives memory but renewed actual activation prunes it before any PUT',async()=>{
 const f=await pendingFixture(),original={body:f.pending.body,id:f.pending.id,key:f.pending.key,digest:f.pending.digest};
 f.clock.now=f.pending.expires;f.lease=await f.device.activate(f.owner);
 assert.equal((await f.device.inspect()).drafts.length,0);
 await assert.rejects(sendPending(f));assert.equal(f.calls.length,0);
 assert.deepEqual({body:f.pending.body,id:f.pending.id,key:f.pending.key,digest:f.pending.digest},original);
 const found=await readTransferredDraft(f.client,{draft_id:f.pending.id});assert.equal(found.id,f.pending.id);
 assert.equal(f.calls.length,1);assert.equal(f.calls[0].options,undefined);assert.ok(f.calls[0].url.endsWith(f.pending.id));
});
test('unexpired stable-key retry checks meta and exact draft in one transaction and sends unchanged body once',async()=>{
 const f=await pendingFixture(),uuids=f.uuidCalls;f.storage.reads.length=0;
 const result=await sendPending(f);assert.equal(result.id,f.pending.id);assert.equal(f.calls.length,1);
 assert.deepEqual(f.calls[0].options,{method:'PUT',body:f.pending.body,key:f.pending.key,timeout:25000});assert.equal(f.uuidCalls,uuids);
 const reads=f.storage.reads.filter(row=>row.mode==='readonly');assert.equal(reads.length,2);assert.equal(reads[0].transaction,reads[1].transaction);
 assert.deepEqual(reads.map(row=>[row.store,row.action,row.key]),[['meta','get','device'],['drafts','get',f.pending.localID]]);
});
test('current reader, changed authority, household or generation cannot transfer retained pending bytes',async()=>{
 for(const changes of [{can_write:false,role:'reader'},{auth_scope:'different-session'},{household_id:id(9)},{generation:8}]){
  const f=await pendingFixture();Object.assign(f.client.current,changes);await assert.rejects(sendPending(f));assert.equal(f.calls.length,0);
 }
});
test('new epoch, stale device revision and renewed draft revision refuse old pending without rebasing',async()=>{
 const reset=await pendingFixture();await reset.device.reset();reset.lease=await reset.device.activate(reset.owner);await assert.rejects(sendPending(reset));assert.equal(reset.calls.length,0);
 const stale=await pendingFixture();await stale.device.activate(stale.owner);await assert.rejects(sendPending(stale));assert.equal(stale.calls.length,0);
 const renewed=await pendingFixture();renewed.lease=await renewed.device.activate(renewed.owner);await assert.rejects(sendPending(renewed));assert.equal(renewed.calls.length,0);
});
test('atomic pending gate refuses changed handoff key, ID, digest, phase or expected draft version',async()=>{
 for(const change of [row=>row.handoff.key=id(70),row=>row.handoff.draft_id=id(71),row=>row.handoff.digest='a'.repeat(64),row=>row.handoff.phase='saved']){
  const f=await pendingFixture();f.storage.mutate('drafts',f.pending.localID,change);await assert.rejects(sendPending(f));assert.equal(f.calls.length,0);
 }
 const version=await pendingFixture();version.pending.draftRevision++;await assert.rejects(sendPending(version));assert.equal(version.calls.length,0);
 const body=await pendingFixture();body.pending.body=body.pending.body.replace('retained in memory','changed after review');await assert.rejects(sendPending(body));assert.equal(body.calls.length,0);
});

const settle=async()=>{for(let i=0;i<10;i++)await Promise.resolve();};
function cameraFixture({acquire,detect,play,secure=true}={}){let stops=0,requests=0,n=0;const hits=[],tasks=new Map(),stream={getTracks:()=>[{stop:()=>stops++},{stop:()=>stops++}]},video={pause(){},play:async()=>play?.(),srcObject:null};class Detector{static async getSupportedFormats(){return ['ean_13'];}async detect(){return detect?detect():[];}}const control=camera({secure,Detector,media:{getUserMedia:async()=>{requests++;return acquire?acquire():stream;}},video,onCode:value=>hits.push(value),onStatus(){},onActive(){},later:(fn,delay)=>{tasks.set(++n,{fn,delay});return n;},cancel:id=>tasks.delete(id)});return {control,stream,video,hits,tasks,get stops(){return stops;},get requests(){return requests;}};}
test('camera cancellation closes all late tracks while permission or playback waits',async()=>{
 let release;const f=cameraFixture({acquire:()=>new Promise(resolve=>release=resolve)}),starting=f.control.start();await settle();f.control.stop();release(f.stream);await starting;assert.equal(f.stops,2);assert.equal(f.video.srcObject,null);assert.equal(f.hits.length,0);
 let play;const p=cameraFixture({play:()=>new Promise(resolve=>play=resolve)}),waiting=p.control.start();await settle();[...p.tasks.values()].find(t=>t.delay===60000).fn();play();await waiting;assert.equal(p.stops,2);assert.equal(p.hits.length,0);
});
test('camera hit is one inert code draft and insecure mode never requests media',async()=>{
 const f=cameraFixture({detect:async()=>[{rawValue:'000012345'}]});await f.control.start();await settle();assert.deepEqual(f.hits,['000012345']);assert.equal(f.stops,2);assert.equal(f.tasks.size,0);
 const unsupported=cameraFixture({secure:false});await unsupported.control.start();assert.equal(unsupported.requests,0);
});
