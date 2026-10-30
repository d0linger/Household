'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const crypto=require('node:crypto');

const staticFile=name=>fs.readFileSync(path.join(__dirname,'../internal/web/static',name),'utf8');
const uuid=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0');
const copy=value=>structuredClone(value);

function actualFunctions(filename,start,end,sandbox,names){
 const source=staticFile(filename),from=source.indexOf(start),to=source.indexOf(end,from);
 assert.ok(from>=0&&to>from,filename+' real function boundary');
 return vm.runInNewContext('(()=>{'+source.slice(from,to)+';return{'+names.join(',')+'}})()',sandbox,{filename});
}

test('Discover submits only bounded title, all selected references, duration and history criteria',()=>{
 const entries={q:'  Suppe  ',tags:[uuid(2),uuid(1),uuid(2)],catalog:[uuid(3),uuid(4)],max_minutes:'45',include_unknown_duration:'on',cooked_after:'2026-10-01',never_cooked:null};
 class FakeFormData{constructor(form){this.values=form.entries;}get(name){const value=this.values[name];return Array.isArray(value)?value[0]:value??null;}getAll(name){const value=this.values[name];return Array.isArray(value)?value:value==null?[]:[value];}}
 const form={entries},sandbox={form,FormData:FakeFormData,URLSearchParams,TextEncoder,text:value=>value==null?'':String(value),bytes:value=>new TextEncoder().encode(JSON.stringify(value)).length,fields:['q','tags','catalog','max_minutes','include_unknown_duration','cooked_after','never_cooked']};
 const {values,parameters}=actualFunctions('recipe-find.js',' function values(){','\n function renderChips(',sandbox,['values','parameters']);
 const selected=values(),query=parameters(selected);
 assert.equal(selected.q,'Suppe');assert.deepEqual([...selected.tags],[uuid(1),uuid(2)]);assert.equal(query.getAll('tags').length,2);assert.equal(query.getAll('catalog').length,2);
 assert.deepEqual([...query.keys()].sort(),['catalog','catalog','cooked_after','include_unknown_duration','max_minutes','q','tags','tags'].sort());
 assert.equal(query.get('q'),'Suppe');assert.equal(query.get('max_minutes'),'45');
 entries.max_minutes='0';assert.throws(()=>values(),/1 bis 10080/);
 entries.max_minutes='10081';assert.throws(()=>values(),/10080/);
 entries.max_minutes='10';entries.never_cooked='on';assert.throws(()=>values(),/getrennt/);
 entries.cooked_after='';entries.tags=[uuid(1),uuid(2),uuid(3),uuid(4),uuid(5),uuid(6)];assert.throws(()=>values(),/fünf/);
});

test('Discover treats a single stored zero duration as unknown and ignores superseded searches',async()=>{
 const nodes=new Map();const element=id=>{if(!nodes.has(id))nodes.set(id,{textContent:'',hidden:false,children:[],replaceChildren(){this.children=[];},append(child){this.children.push(child);}});return nodes.get(id);};
 const item=(tag,value)=>({tag,textContent:value??'',children:[],append(child){this.children.push(child);}});
 const sandbox={$:element,item,text:value=>value==null?'':String(value),serial:0,context:null,active:null,page:0,next:'',clearError(){},cancelPendingSave(){},parameters:()=>'',validContext:()=>true,same:()=>true,renderChips(){},json:null};
 const functions=actualFunctions('recipe-find.js',' function renderResults(', '\n function renderViews(',sandbox,['renderResults','search']);
 functions.renderResults({items:[{id:uuid(1),title:'Zeit offen',prep_minutes:0,cook_minutes:15}],next:''});
 const duration=element('rf-results').children[0].children.find(child=>child.textContent==='Dauer nicht eindeutig erfasst');assert.ok(duration,'A single zero duration stays visibly unknown');
 const pending=[];sandbox.json=()=>new Promise(resolve=>pending.push(resolve));const criteria={q:'Suppe'};
 const old=functions.search(criteria),newer=functions.search(criteria);assert.equal(pending.length,2);
 pending[1]({context:{},items:[],next:''});assert.equal(await newer,true);
 pending[0]({context:{},items:[],next:''});assert.equal(await old,false,'Superseded result cannot authorize saving a view');
});

test('Substitution handoff requires current scopes and source unit before navigation',()=>{
 const stored=new Map(),navigations=[],errors=[],recipeID=uuid(10),root={dataset:{generation:'3'}};
 const data={recipe:{content_fingerprint:'a'.repeat(64)},context:{session_scope:'b'.repeat(64),auth_scope:'c'.repeat(64),recipes_generation:3}};
 const sandbox={data,recipeID,root,TextEncoder,sessionStorage:{setItem:(key,value)=>stored.set(key,value)},location:{assign:url=>navigations.push(url)},error:message=>errors.push(message)};
 const {handoff}=actualFunctions('recipe-insights.js',' function handoff(', '\n function renderCost(',sandbox,['handoff']);
 const group={catalog_id:uuid(11),catalog_name:'Reis',catalog_version:4};
 const rule={target_catalog_id:uuid(12),target_name:'Hirse',target_version:2,ratio:'2.5',source_unit_code:'g',unit_code:'g',source:'Kochbuch',source_date:'2026-10-01',status:'current'};
 handoff(group,{...rule,source_unit_code:''});assert.equal(navigations.length,0);assert.equal(stored.size,0);
 handoff(group,rule);assert.equal(navigations[0],'/m/recipes/recipes/'+recipeID+'/workspace');
 const saved=JSON.parse(stored.get('household-recipe-substitution-v1'));assert.equal(saved.source_unit_code,'g');assert.equal(saved.rule_version,4);assert.equal(saved.recipe_fingerprint,'a'.repeat(64));
 data.context.auth_scope='';handoff(group,rule);assert.equal(navigations.length,1);assert.ok(errors.length>=2);
});

test('Substitution selection rejects a locally edited source quantity before applying a ratio',()=>{
 const id=uuid(30),source={ingredients:[{id,name:'Hirse',quantity:'100',unit_code:'g',catalog_id:uuid(31)}]};
 const {unchangedSubstitutionSource}=actualFunctions('recipe-workspace.js',' function unchangedSubstitutionSource(', '\n async function consumeSubstitution(',{source,text:value=>value==null?'':String(value)},['unchangedSubstitutionSource']);
 const row={id,data:{name:'Hirse',quantity:'100',unit_code:'g',catalog_id:uuid(31)}};
 assert.equal(unchangedSubstitutionSource(row,uuid(31),'g'),true);
 assert.equal(unchangedSubstitutionSource({...row,data:{...row.data,quantity:'125'}},uuid(31),'g'),false);
 assert.equal(unchangedSubstitutionSource({...row,data:{...row.data,unit_code:'kg'}},uuid(31),'g'),false);
 assert.equal(unchangedSubstitutionSource({...row,id:uuid(32)},uuid(31),'g'),false);
});

test('Allergen view separates current unknown from historical stale evidence',()=>{
 const box={prepend(value){this.intro=value;}};const drawn=[];
 const profile={allergens:{milk:'reviewed_absent'},source:'Beleg A',source_date:'2026-10-01'};
 const data={allergens:{status_by_code:{milk:'unknown'},stale:false}};
 const sandbox={data,profile:()=>profile,ALLERGENS:[['milk','Milch']],$:id=>id==='rx-allergen-rows'?box:null,el:(tag,value)=>({tag,value,children:[],append(...values){this.children.push(...values);}}),line:(label,value)=>label+': '+value,state:message=>message,list:(_box,rows,render,empty)=>drawn.push({rows,rendered:rows.map(render),empty})};
 const {renderAllergens}=actualFunctions('recipe-insights.js',' function renderAllergens(', '\n function renderConversions(',sandbox,['renderAllergens']);
 renderAllergens();assert.equal(drawn[0].rows.length,0);assert.match(drawn[0].empty,/unbekannt/);
 data.allergens.stale=true;renderAllergens();assert.equal(drawn[1].rows.length,1);assert.match(drawn[1].rendered[0].children.join(' '),/Aktuell unbekannt.*früherer Beleg/);assert.match(box.intro,/historische, veraltete Belege/);
});

// The adapter only schedules IndexedDB transactions. All authorization,
// projection, expiry and deletion logic executes in the actual PWA module.
function memoryIDB(){
 const databases=new Map();let commitHook=null,openGate=null,openCalls=0;
 const database=name=>{
  const stores=new Map();
  return {objectStoreNames:{contains:key=>stores.has(key)},createObjectStore:key=>stores.set(key,new Map()),close(){},transaction(names,mode){
   names=Array.isArray(names)?names:[names];const working=new Map(names.map(key=>[key,new Map([...stores.get(key)].map(([id,row])=>[id,copy(row)]))]));
   let pending=0,done=false,aborted=false;
   const tx={oncomplete:null,onerror:null,onabort:null,abort(){aborted=true;queueMicrotask(finish);},objectStore(key){
    const rows=working.get(key);if(!rows)throw Error('Unknown test object store');
    const request=(operation)=>{const result={};pending++;queueMicrotask(()=>{try{if(!aborted){result.result=copy(operation());result.onsuccess?.();}}catch(error){aborted=true;tx.onerror?.(error);}finally{pending--;queueMicrotask(finish);}});return result;};
    return {get:id=>request(()=>rows.get(id)),getAll:()=>request(()=>[...rows.values()]),put:row=>request(()=>{rows.set(row.key||row.id,copy(row));}),clear:()=>request(()=>{rows.clear();})};
   }};
   function finish(){if(done||pending)return;done=true;if(aborted){tx.onabort?.();return;}if(mode==='readwrite'){for(const [key,rows]of working)stores.set(key,rows);const hook=commitHook;commitHook=null;hook?.();}tx.oncomplete?.();}
   queueMicrotask(finish);return tx;
  },raw(){return copy(Object.fromEntries([...stores].map(([key,rows])=>[key,[...rows.values()]])));}};
 };
 return {open(name){openCalls++;const gate=openGate;openGate=null;const request={};Promise.resolve(gate).then(()=>queueMicrotask(()=>{if(!databases.has(name)){databases.set(name,database(name));request.result=databases.get(name);request.onupgradeneeded?.();}else request.result=databases.get(name);request.onsuccess?.();}));return request;},deleteDatabase(name){const request={};queueMicrotask(()=>{databases.delete(name);request.onsuccess?.();});return request;},databases:async()=>[...databases.keys()].map(name=>({name})),raw:name=>databases.get(name)?.raw()||null,setCommitHook:hook=>{commitHook=hook;},setOpenGate:gate=>{openGate=gate;},openCalls:()=>openCalls};
}

test('A newer search at the local transaction commit boundary rolls back only its stale view',async()=>{
 const disk=memoryIDB(),scope={owner_scope:'owner',session_scope:'session',auth_scope:'auth',generation:1,role:'reader'};
 const open=()=>new Promise((resolve,reject)=>{const request=disk.open('household-recipe-views');request.onupgradeneeded=()=>request.result.createObjectStore('records');request.onsuccess=()=>resolve(request.result);request.onerror=()=>reject(Error('open failed'));});
 const sandbox={authorityEpoch:0,serial:0,context:scope,pendingSaveTransaction:null,open,same:(a,b)=>!!a&&!!b&&a.owner_scope===b.owner_scope&&a.session_scope===b.session_scope&&a.auth_scope===b.auth_scope&&a.generation===b.generation&&a.role===b.role,bytes:value=>new TextEncoder().encode(JSON.stringify(value)).length,crypto:crypto.webcrypto,clearViews:async()=>{throw Error('Unrelated views must not be deleted');},$:()=>({textContent:''})};
 const {modify}=actualFunctions('recipe-find.js',' function cancelPendingSave()', '\n async function loadViews()',sandbox,['modify']);
 const view={id:uuid(50),name:'Überholte Suche',criteria:{q:'alt'}};
 disk.setCommitHook(()=>{sandbox.serial=1;});
 await assert.rejects(modify(row=>{row.views=[view];row.revision++;return{views:row.views,revision:row.revision};},()=>sandbox.serial===0,async()=>{await modify(row=>{row.views=row.views.filter(item=>item.id!==view.id);row.revision++;return row;});}));
 assert.equal(disk.raw('household-recipe-views').records[0].views.length,0,'Stale view was removed after commit without clearing the whole view database');
});

function deviceRuntime(){
 const disk=memoryIDB(),values=new Map([['household-offline-owner',uuid(1)]]),events=new Map();
 const current={owner_scope:'a'.repeat(64),session_scope:'b'.repeat(64),auth_scope:'c'.repeat(64),role:'reader',generation:3,offline_allowed:false,server_time:new Date().toISOString()};
 const recipeID=uuid(20),captured=new Date(),expiry=new Date(captured.getTime()+24*3600000);
 const recipe={id:recipeID,title:'Reis <script>',summary:'Nur Lesetext',source_url:'',base_servings:'2',prep_minutes:10,cook_minutes:15,notes:'Privater Hinweis',secret:'server-only'};
 const snapshot={recipe,ingredients:[{name:'Reis',quantity:'100',unit_code:'g',section_name:'',quantity_note:'',preparation_note:''}],steps:[{instruction:'Kochen',timer_seconds:300}],tags:['Einfach'],profiles:{nutrition:{basis:'per_recipe',source:'Kochbuch',source_date:'2026-10-01',stale:false,nutrients:{energy_kcal:'100'}},allergens:{source:'Kochbuch',source_date:'2026-10-01',stale:false,status_by_code:{milk:'reviewed_absent'}}},images:[],version:'1',captured_at:captured.toISOString(),expires_at:expiry.toISOString(),private_token:'never-persist'};
 let authStatus=200,authJSON=true,fetches=0,now=Date.now();
 const sandbox={TextEncoder,Blob,Date:class extends Date{static now(){return now;}},createImageBitmap:async()=>({width:1,height:1,close(){}}),crypto:crypto.webcrypto,indexedDB:disk,localStorage:{getItem:key=>values.get(key)||null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)},document:{body:{dataset:{user:uuid(1)}},getElementById:()=>null},navigator:{onLine:true},atob:value=>Buffer.from(value,'base64').toString('binary'),fetch:async url=>{fetches++;if(authStatus===0)throw Error('Transport offline');if(authStatus!==200)return{ok:false,status:authStatus,json:async()=>{if(!authJSON)throw Error('Not JSON');return{error:'Denied'};}};if(url==='/api/v1/recipes/device-context')return{ok:true,status:200,json:async()=>copy(current)};if(url==='/api/v1/recipes/'+recipeID+'/read-snapshot')return{ok:true,status:200,json:async()=>({context:copy(current),snapshot:copy(snapshot)})};throw Error('Unexpected request '+url);},addEventListener:(type,listener)=>{events.set(type,listener);}};
 sandbox.window=sandbox;vm.runInNewContext(staticFile('recipe-offline.js'),sandbox,{filename:'recipe-offline.js'});
 return{device:sandbox.HouseholdRecipeDevice,disk,current,snapshot,values,recipeID,setAuth:(status,json=true)=>{authStatus=status;authJSON=json;},setNow:value=>{now=value;},setOnline:value=>{sandbox.navigator.onLine=value;},fetches:()=>fetches,emitStorage:key=>events.get('storage')?.({key})};
}

async function runCommonDeviceObservation(f,pathname){
 const app=staticFile('app.js'),start=app.indexOf('  if(user){try{const owner=localStorage.getItem('),end=app.indexOf("  if($('#queue-count'))",start);
 assert.ok(start>=0&&end>start,'actual common app device startup is present');
 const calls=[],pending=[],messages=[];
 const observe=(name,action=()=>Promise.resolve())=>({observe(...args){calls.push({name,args});const result=Promise.resolve().then(()=>action(...args));pending.push(result);return result;}});
 const recipe=observe('recipe',()=>f.device.observe());recipe.clear=()=>{calls.push({name:'recipe-clear',args:[]});const result=f.device.clear();pending.push(result);return result;};
 const sandbox={user:uuid(1),household:uuid(2),location:{pathname},localStorage:{getItem:key=>f.values.get(key)||null,removeItem:key=>f.values.delete(key)},window:{HouseholdPantryDevice:observe('pantry'),HouseholdShoppingDevice:observe('shopping'),HouseholdPushDevice:observe('push'),HouseholdRecipeDevice:recipe},clearPrivateDeviceData:()=>{throw Error('Matching owner must not be cleared by startup');},toast:message=>messages.push(message)};
 vm.runInNewContext(app.slice(start,end),sandbox,{filename:'app.js'});
 await Promise.allSettled(pending);
 return{calls,messages};
}

test('Mandatory password page physically purges imaged Recipe copies and views without a protected API call',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;f.snapshot.images=[{mime_type:'image/png',base64:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y8Za3cAAAAASUVORK5CYII=',alt:'Privates Rezeptbild',caption:''}];await f.device.save(f.recipeID);f.setAuth(403);
 const view=await new Promise(resolve=>{const request=f.disk.open('household-recipe-views');request.onupgradeneeded=()=>request.result.createObjectStore('records');request.onsuccess=()=>resolve(request.result);});const saved=view.transaction('records','readwrite');saved.objectStore('records').put({id:'saved',views:[{name:'Private Suchansicht'}]});await new Promise(resolve=>{saved.oncomplete=resolve;});view.close();
 const initialFetches=f.fetches(),initial=f.disk.raw('household-recipe-readcopies');assert.equal(initial.copies.length,1);assert.equal(initial.copies[0].images.length,1);assert.equal(f.disk.raw('household-recipe-views').records[0].views.length,1);
 const mandatory=await runCommonDeviceObservation(f,'/change-password');
 assert.deepEqual(mandatory.calls.map(row=>row.name),['recipe-clear','pantry','shopping','push']);
 assert.ok(mandatory.calls.slice(1).every(row=>row.args[0]===uuid(1)&&row.args[1]===uuid(2)));
 assert.equal(f.fetches(),initialFetches,'Mandatory page does not fetch an API that must return 403');
 assert.equal(f.disk.raw('household-recipe-readcopies'),null,'Known password gate physically deletes copy and image bytes');
 assert.equal(f.disk.raw('household-recipe-views'),null,'Known password gate deletes private saved views');
 assert.equal(f.values.get('household-recipe-device-authorized'),undefined,'Old offline read grant is revoked');
 assert.deepEqual(mandatory.messages,[]);
 f.setAuth(200);const normal=await runCommonDeviceObservation(f,'/settings');
 assert.deepEqual(normal.calls.map(row=>row.name),['pantry','shopping','push','recipe']);
 assert.equal(f.fetches(),initialFetches+1,'Normal authenticated page resumes Recipe context verification');
 assert.equal(f.disk.raw('household-recipe-readcopies'),null,'Normal observer cannot restore purged image bytes');
 assert.deepEqual(normal.messages,[]);
});

test('Offline copy requires explicit consent and stores only bounded reading projection',async()=>{
 const f=deviceRuntime();await assert.rejects(f.device.save(f.recipeID),failure=>failure.code==='CONSENT');assert.equal(f.disk.raw('household-recipe-readcopies'),null);
 f.current.offline_allowed=true;await f.device.save(f.recipeID);const raw=f.disk.raw('household-recipe-readcopies');assert.equal(raw.copies.length,1);assert.equal(raw.copies[0].recipe.title,'Reis <script>');assert.equal(raw.copies[0].recipe.secret,undefined);assert.equal(raw.copies[0].private_token,undefined);assert.equal(raw.copies[0].profiles.allergens.status_by_code.milk,'reviewed_absent');
 assert.equal((await f.device.list()).copies.length,1);assert.ok(f.fetches()>=3);
});

test('Disallowed or ownerless observation never opens a Recipe IndexedDB',async()=>{
 const denied=deviceRuntime();const view=await new Promise(resolve=>{const request=denied.disk.open('household-recipe-views');request.onupgradeneeded=()=>request.result.createObjectStore('records');request.onsuccess=()=>resolve(request.result);});const saved=view.transaction('records','readwrite');saved.objectStore('records').put({id:'saved',views:[{name:'Meine Rezepte'}]});await new Promise(resolve=>{saved.oncomplete=resolve;});view.close();
 const opens=denied.disk.openCalls();assert.equal(await denied.device.observe(),false);assert.equal(denied.disk.openCalls(),opens);assert.equal(denied.disk.raw('household-recipe-readcopies'),null);assert.equal(denied.disk.raw('household-recipe-views').records[0].views[0].name,'Meine Rezepte','Saved Discover criteria survive ordinary disallowed observation');
 const ownerless=deviceRuntime();ownerless.current.offline_allowed=true;ownerless.values.delete('household-offline-owner');assert.equal(await ownerless.device.observe(),false);assert.equal(ownerless.disk.openCalls(),0);assert.equal(ownerless.disk.raw('household-recipe-readcopies'),null);
});

test('A routine consent denial purges old Recipe copies but preserves separately saved Discover views',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);
 const view=await new Promise(resolve=>{const request=f.disk.open('household-recipe-views');request.onupgradeneeded=()=>request.result.createObjectStore('records');request.onsuccess=()=>resolve(request.result);});const saved=view.transaction('records','readwrite');saved.objectStore('records').put({id:'saved',views:[{name:'Mein Filter'}]});await new Promise(resolve=>{saved.oncomplete=resolve;});view.close();
 f.current.offline_allowed=false;assert.equal(await f.device.observe(),false);assert.equal(f.disk.raw('household-recipe-readcopies'),null);assert.equal(f.disk.raw('household-recipe-views').records[0].views[0].name,'Mein Filter');
});

test('An observation whose IndexedDB open completes after opt-out cannot recreate a copy store',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);
 let release;const gate=new Promise(resolve=>{release=resolve;});f.disk.setOpenGate(gate);const opens=f.disk.openCalls(),pending=f.device.observe();
 for(let i=0;i<20&&f.disk.openCalls()===opens;i++)await Promise.resolve();
 assert.equal(f.disk.openCalls(),opens+1,'Observation has begun its asynchronous read');
 f.current.offline_allowed=false;f.values.delete('household-offline-owner');await f.device.clear();assert.equal(f.disk.raw('household-recipe-readcopies'),null);
 release();assert.equal(await pending,false);assert.equal(f.disk.raw('household-recipe-readcopies'),null,'Late read must be followed by physical deletion');
});

test('A newer server denial fences an older observation before its first database open completes',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;
 let release;const gate=new Promise(resolve=>{release=resolve;});f.disk.setOpenGate(gate);
 const pending=f.device.observe();for(let i=0;i<20&&f.disk.openCalls()===0;i++)await Promise.resolve();
 assert.equal(f.disk.openCalls(),1);
 f.current.offline_allowed=false;assert.equal(await f.device.observe(),false);
 assert.equal(f.disk.raw('household-recipe-readcopies'),null);
 release();assert.equal(await pending,false);
 assert.equal(f.disk.raw('household-recipe-readcopies'),null,'Older positive response cannot recreate storage after a newer denial');
});

test('Expired consented copy prunes its image bytes before its reader authorization expires',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;f.snapshot.images=[{mime_type:'image/png',base64:'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y8Za3cAAAAASUVORK5CYII=',alt:'Testbild',caption:''}];
 await f.device.save(f.recipeID);assert.equal(f.disk.raw('household-recipe-readcopies').copies[0].images.length,1);
 f.setNow(Date.parse(f.snapshot.expires_at)+1000);f.setOnline(false);
 await assert.rejects(f.device.list(),failure=>failure.code==='EMPTY');
 const raw=f.disk.raw('household-recipe-readcopies');assert.equal(raw.copies.length,0);assert.equal(raw.meta[0].authorized_until,0);
});

test('New recipe session suspends old copies until explicit full refetch',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);const prior=f.disk.raw('household-recipe-readcopies').copies[0].captured_at;
 f.current.session_scope='d'.repeat(64);f.current.auth_scope='e'.repeat(64);await f.device.observe();assert.equal(f.disk.raw('household-recipe-readcopies').meta[0].suspended,true);
 await assert.rejects(f.device.list(),failure=>failure.code==='SESSION');const result=await f.device.reauthorize();assert.equal(result.count,1);assert.equal((await f.device.list()).copies.length,1);assert.equal(f.disk.raw('household-recipe-readcopies').meta[0].context.session_scope,'d'.repeat(64));assert.equal(f.disk.raw('household-recipe-readcopies').copies[0].captured_at,prior);
});

test('Known denial and explicit deletion physically remove recipe and view stores',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);f.setAuth(401);await f.device.observe();assert.equal(f.disk.raw('household-recipe-readcopies'),null);await assert.rejects(f.device.list());
 const second=deviceRuntime();second.current.offline_allowed=true;await second.device.save(second.recipeID);await second.device.clear();assert.equal(second.disk.raw('household-recipe-readcopies'),null);assert.equal(second.disk.raw('household-recipe-views'),null);await assert.rejects(second.device.list());
});

test('Known 4xx including non-JSON module404 deny copies; transport and 5xx preserve offline fallback',async()=>{
 for(const [status,json]of [[401,true],[403,true],[404,false],[422,false]]){
  const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);f.setAuth(status,json);await assert.rejects(f.device.list());assert.equal(f.disk.raw('household-recipe-readcopies'),null,'Known '+status+' must physically clear prior copies');
 }
 for(const status of [0,500]){
  const f=deviceRuntime();f.current.offline_allowed=true;await f.device.save(f.recipeID);f.setAuth(status,false);const result=await f.device.list();assert.equal(result.copies.length,1,'Transport/5xx may read explicitly consented unexpired copy');
 }
});

test('Logout at the IndexedDB commit boundary physically removes the just saved copy',async()=>{
 const f=deviceRuntime();f.current.offline_allowed=true;
 f.disk.setCommitHook(()=>{f.values.set('household-recipe-device-denied','logout-race');f.emitStorage('household-recipe-device-denied');});
 await assert.rejects(f.device.save(f.recipeID),failure=>failure.code==='STALE');
 assert.equal(f.disk.raw('household-recipe-readcopies'),null,'A late logout must delete the committed copy');
});
