'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const ui=require('../internal/web/static/notification-settings.js');
const id='11111111-1111-4111-8111-111111111111';
const pref=()=>({category:'test',enabled:false,timezone:'Europe/Vienna',quiet_start:null,quiet_end:null,version:0,enabled_since:null});
const delivery=()=>({id,state:'accepted',safe_code:'gateway_accepted',accepted_at:'2026-11-03T10:00:00Z',updated_at:'2026-11-03T10:00:00Z'});
test('quiet-hour controls preserve midnight and unset values without implicit conversion',()=>{
 assert.equal(ui.minute(''),null);assert.equal(ui.clock(null),'');
 for(let n=0;n<1440;n++)assert.equal(ui.minute(ui.clock(n)),n);
 for(const invalid of ['24:00','1:00','12:60','00:00:00','NaN',null,0])assert.throws(()=>ui.minute(invalid));
 for(const invalid of [-1,1440,1.5,'60',undefined])assert.throws(()=>ui.clock(invalid));
});
test('preference projection rejects duplicate categories, unsafe versions and incomplete quiet hours',()=>{
 const payload={preferences:[pref()],categories:[{category:'test',module:'',available:true}]};
 assert.equal(ui.preferences(payload),payload);
 assert.throws(()=>ui.preferences({...payload,preferences:[pref(),pref()]}));
 assert.throws(()=>ui.preferences({...payload,categories:[{category:'unknown',available:true}]}));
 assert.throws(()=>ui.preferences({...payload,categories:[{category:'test',available:'true'}]}));
 for(const invalid of [{version:Number.MAX_SAFE_INTEGER+1},{quiet_start:0},{quiet_start:0,quiet_end:0},{quiet_start:-1,quiet_end:60},{enabled:'false'},{enabled_since:'not-a-date'}])assert.throws(()=>ui.preference({...pref(),...invalid}));
 assert.doesNotThrow(()=>ui.preference({...pref(),quiet_start:22*60,quiet_end:7*60}));
});
test('gateway acceptance and browser display acknowledgement never claim user confirmation',()=>{
 const accepted=ui.deliveryText(delivery());assert.match(accepted,/Push-Dienst/);assert.match(accepted,/nicht bestätigt/);assert.doesNotMatch(accepted,/Von dir/);
 const display=ui.deliveryText({...delivery(),safe_code:'display_requested'});assert.match(display,/angefordert/);assert.match(display,/nicht bestätigt/);
 assert.match(ui.deliveryText({...delivery(),safe_code:'user_confirmed'}),/Von dir als gesehen bestätigt/);
 assert.match(ui.deliveryText({...delivery(),state:'expired',safe_code:''}),/abgelaufen/);
 assert.throws(()=>ui.delivery({...delivery(),id:'https://other.invalid'}));assert.throws(()=>ui.delivery({...delivery(),state:'seen'}));
 assert.throws(()=>ui.delivery({...delivery(),state:'pending',safe_code:'user_confirmed'}));
 assert.throws(()=>ui.delivery({...delivery(),accepted_at:null}));
});
test('device projection refuses unsafe identifiers, duplicate subscriptions and categories',()=>{
 const device={id,device_id:'22222222-2222-4222-8222-222222222222',state:'registered',registered_at:'2026-11-03T10:00:00Z',expires_at:'2026-12-03T10:00:00Z',name:'Kitchen',categories:[{category:'test',enabled:false,paused:false,name:'Kitchen',version:0,enabled_since:null}],last_test:null};
 assert.equal(ui.devices({devices:[device]})[0],device);
 assert.throws(()=>ui.devices({devices:[device,device]}));
 assert.throws(()=>ui.devices({devices:[{...device,id:'../../other-user'}]}));
 assert.throws(()=>ui.devices({devices:[{...device,categories:[...device.categories,...device.categories]}]}));
 assert.throws(()=>ui.devices({devices:[{...device,categories:[{...device.categories[0],enabled:1}]}]}));
 assert.throws(()=>ui.devices({devices:[{...device,last_test:{...delivery(),safe_code:'private endpoint: https://secret'}}]}));
});
test('pagehide prevents late bootstrap work and conceals cached settings until a fresh authenticated page',async()=>{
 const listeners=new Map(),requests=[];let releaseBootstrap,reloads=0;
 const refresh={disabled:false,addEventListener(){}},status={textContent:'',dataset:{}};
 const panel={hidden:false,querySelector(selector){
  if(selector==='[data-notification-refresh]')return refresh;
  if(selector==='[data-notification-status]')return status;
  throw Error('Late controller work tried to touch hidden account content');
 }};
 const sandbox={document:{querySelector:selector=>selector==='[data-notification-settings]'?panel:null,body:{dataset:{user:id,household:id}}},
  HouseholdShoppingUI:{raw:url=>{requests.push(url);return new Promise(resolve=>{releaseBootstrap=resolve;});}},
  addEventListener:(name,callback)=>listeners.set(name,callback),location:{reload(){reloads++;}},setTimeout,clearTimeout};
 vm.runInNewContext(fs.readFileSync(require.resolve('../internal/web/static/notification-settings.js'),'utf8'),sandbox);
 assert.deepEqual(requests,['/api/v1/core/bootstrap']);
 listeners.get('pageshow')({persisted:false});assert.equal(reloads,0,'ordinary page load must not reload itself');
 listeners.get('pagehide')({persisted:true});assert.equal(panel.hidden,true,'BFCache must not expose the prior account settings while revalidating');
 releaseBootstrap({user:{id,household_id:id}});await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(requests,['/api/v1/core/bootstrap'],'late bootstrap must not start category/device reads after leaving the page');
 assert.equal(refresh.disabled,true,'departed page cannot accept a new settings refresh');
 listeners.get('pageshow')({persisted:true});assert.equal(reloads,1,'BFCache restoration must request a fresh authenticated page');
 assert.equal(panel.hidden,true,'cached private content stays hidden while fresh navigation starts');
});

// Small DOM adapter runs the real settings controller and save/reload handlers.
// It is intentionally not evidence of browser rendering or server persistence.
async function shoppingController(options={}){
 class Node {
  constructor(tag){this.tag=tag;this.children=[];this.dataset={};this.attributes={};this.listeners=new Map();this.classList={add(){}};this.ownText='';}
  set textContent(value){this.ownText=String(value);this.children=[];}
  get textContent(){return this.ownText+this.children.map(child=>child.textContent).join('');}
  append(...children){this.children.push(...children);}
  replaceChildren(...children){this.ownText='';this.children=[...children];}
  setAttribute(key,value){this.attributes[key]=String(value);}
  addEventListener(name,handler){this.listeners.set(name,handler);}
  reportValidity(){return true;}
  querySelectorAll(selector){const tags=selector.split(',');return this.children.flatMap(child=>[...(tags.includes(child.tag)?[child]:[]),...child.querySelectorAll(selector)]);}
 }
 const nodes=Object.fromEntries(['refresh','status','preferences','devices'].map(name=>[name,new Node(name==='refresh'?'button':'div')]));
 const panel={querySelector(selector){return nodes[selector.match(/^\[data-notification-(.+)\]$/)?.[1]];},querySelectorAll(){return [];}};
 const category=options.category||'shopping';
 let saved={...pref(),category,version:options.version||0},mode='save';const writes=[],reads=[];
 const projection=()=>({preferences:[{...saved}],categories:[{category,available:true}]});
 const raw=async(url,input)=>{
  reads.push(url);
  if(url==='/api/v1/core/bootstrap')return {user:{id,household_id:id},states:{[category]:{state:options.disabledModule?'disabled':'enabled'}}};
  if(url==='/api/v1/core/push/device-preferences')return {devices:[]};
  assert.equal(url,'/api/v1/core/notification-preferences');
  if(input?.method!=='POST')return projection();
  writes.push({...input.body});
  if(mode==='fail')throw Object.assign(Error('Synthetic server failure'),{code:'HTTP',status:503});
  if(mode==='conflict'){saved={...saved,version:3,enabled:false};mode='save';throw Object.assign(Error('Changed'),{status:409});}
  assert.equal(input.body.version,saved.version);saved={...input.body,version:saved.version+1,enabled_since:input.body.enabled?'2026-11-03T12:00:00Z':null};return {...saved};
 };
 const sandbox={document:{querySelector:selector=>selector==='[data-notification-settings]'?panel:null,body:{dataset:{user:id,household:id}},createElement:tag=>new Node(tag),createTextNode:text=>{const n=new Node('#text');n.textContent=text;return n;}},HouseholdShoppingUI:{raw},addEventListener(){},setTimeout,clearTimeout,Event};
 vm.runInNewContext(fs.readFileSync(require.resolve('../internal/web/static/notification-settings.js'),'utf8'),sandbox);
 for(let turn=0;turn<10&&!nodes.status.textContent.includes('Aktueller Stand geladen');turn++)await new Promise(resolve=>setImmediate(resolve));
 assert.match(nodes.status.textContent,/Aktueller Stand geladen/);
 const all=node=>[node,...(node.children||[]).flatMap(all)],article=nodes.preferences.children[0],form=all(article).find(node=>node.tag==='form');
 return {writes,reads,article,form,all,control:()=>all(article).find(node=>Object.hasOwn(node.dataset,'shoppingControl')),checkbox:()=>all(form).find(node=>node.tag==='input'&&node.type==='checkbox'),setMode(value){mode=value;},get saved(){return saved;},submit:()=>form.listeners.get('submit')({preventDefault(){}})};
}
test('first explicit Shopping save as off selects central control without opting devices in',async()=>{
 const f=await shoppingController();assert.equal(f.control().dataset.shoppingControl,'legacy');assert.match(f.control().textContent,/auch, wenn du die Kategorie ausgeschaltet speicherst/);
 assert.equal(f.writes.length,0,'merely opening the row must not migrate consent');
 await f.submit();assert.deepEqual(f.writes.map(row=>[row.enabled,row.version]),[[false,0]]);assert.equal(f.control().dataset.shoppingControl,'central');
 assert.equal(f.saved.enabled,false);assert.equal(f.saved.version,1);assert.match(f.control().textContent,/frühere Listenversand aus/);
 assert.ok(f.reads.every(url=>!url.includes('/subscriptions/')),'category save must not create or migrate device permissions');
 f.checkbox().checked=true;await f.submit();f.checkbox().checked=false;await f.submit();assert.equal(f.saved.enabled,false);assert.equal(f.saved.version,3);assert.equal(f.control().dataset.shoppingControl,'central');
});
test('failed initial Shopping save preserves the legacy warning until the server commits',async()=>{
 const f=await shoppingController();f.setMode('fail');f.checkbox().checked=true;await f.submit();
 assert.equal(f.control().dataset.shoppingControl,'legacy');assert.equal(f.saved.version,0);assert.equal(f.checkbox().checked,true,'unconfirmed draft remains editable');
 f.setMode('save');await f.submit();assert.equal(f.control().dataset.shoppingControl,'central');assert.equal(f.saved.enabled,true);
});
test('conflicting or already-disabled Shopping preferences remain visibly central',async()=>{
 const existing=await shoppingController({version:2});assert.equal(existing.control().dataset.shoppingControl,'central');assert.equal(existing.checkbox().checked,false);
 const raced=await shoppingController();raced.setMode('conflict');raced.checkbox().checked=true;await raced.submit();
 assert.equal(raced.control().dataset.shoppingControl,'central');assert.equal(raced.checkbox().checked,false);assert.equal(raced.saved.version,3);assert.equal(raced.writes.length,1,'conflict reload never silently retries the draft');
});
test('Shopping list-permission link is offered only while its module is enabled',async()=>{
 for(const disabledModule of [false,true]){const f=await shoppingController({disabledModule}),links=f.all(f.article).filter(node=>node.tag==='a');assert.equal(links.length,disabledModule?0:1);if(links.length)assert.equal(links[0].href,'/settings#settings-notifications');}
});

test('Pantry consent starts off and saves independently without implicitly enrolling devices',async()=>{
 const f=await shoppingController({category:'pantry'});assert.equal(f.checkbox().checked,false);assert.equal(f.writes.length,0);
 assert.match(f.article.textContent,/Bestehende Geräte werden nicht automatisch freigegeben/);
 for(const enabled of [false,true,false]){f.checkbox().checked=enabled;await f.submit();assert.equal(f.saved.enabled,enabled);}
 assert.deepEqual(f.writes.map(row=>[row.category,row.enabled,row.version]),[['pantry',false,0],['pantry',true,1],['pantry',false,2]]);
 assert.ok(f.reads.every(url=>!url.includes('/subscriptions/')),'account consent never creates a device subscription');
});

test('Pantry failed save retains the explicit draft and conflict reload does not retry consent',async()=>{
 const f=await shoppingController({category:'pantry'});f.checkbox().checked=true;f.setMode('fail');await f.submit();
 assert.equal(f.checkbox().checked,true);assert.equal(f.saved.enabled,false);assert.equal(f.saved.version,0);
 f.setMode('conflict');await f.submit();assert.equal(f.checkbox().checked,false);assert.equal(f.saved.version,3);assert.equal(f.writes.length,2);
});

test('Pantry explains daily grouping and observation limits before consent without claiming retroactive alarms',async()=>{
 const f=await shoppingController({category:'pantry'}),details=f.all(f.article).find(node=>node.tag==='details');
 assert.match(f.article.textContent,/höchstens eine allgemeine Nachricht pro Tag/);assert.match(f.article.textContent,/ursprünglichen Ablauf/);
 assert.match(details.textContent,/Nach deiner Freigabe muss zuerst ein ausreichender Bestand erkannt werden/);
 assert.match(details.textContent,/Bestehende Engpässe|bestehende Engpässe/);assert.match(details.textContent,/zwischen zwei Prüfungen/);
 assert.match(details.textContent,/09:00 Uhr in der Haushaltszeitzone/);assert.match(details.textContent,/Verbrauchsdatum hat Vorrang/);
 assert.match(details.textContent,/Verbrauchte oder entfernte Chargen werden nicht gemeldet/);assert.match(details.textContent,/nach dem Öffnen sind keine Ablaufalarme/);
});

test('Pantry stock link is offered only while its module is enabled',async()=>{
 for(const disabledModule of [false,true]){const f=await shoppingController({category:'pantry',disabledModule}),links=f.all(f.article).filter(node=>node.tag==='a');assert.equal(links.length,disabledModule?0:1);if(links.length)assert.equal(links[0].href,'/m/pantry/stock');}
});

test('Fuel account consent requires explicit save and never changes device subscriptions',async()=>{
 const f=await shoppingController({category:'fuel'});assert.equal(f.writes.length,0);assert.equal(f.checkbox().checked,false);
 for(const enabled of [false,true,false]){f.checkbox().checked=enabled;await f.submit();assert.equal(f.saved.enabled,enabled);}
 assert.deepEqual(f.writes.map(row=>[row.category,row.enabled,row.version]),[['fuel',false,0],['fuel',true,1],['fuel',false,2]]);
 assert.ok(f.reads.every(url=>!url.includes('/subscriptions/')));
});

test('Fuel failed save keeps draft and conflicting consent reloads server state without retry',async()=>{
 const f=await shoppingController({category:'fuel'});f.checkbox().checked=true;f.setMode('fail');await f.submit();
 assert.equal(f.checkbox().checked,true);assert.equal(f.saved.enabled,false);assert.equal(f.saved.version,0);
 f.setMode('conflict');await f.submit();assert.equal(f.checkbox().checked,false);assert.equal(f.saved.version,3);assert.equal(f.writes.length,2);
});

test('Fuel guidance distinguishes recipients, freshness, initial mileage baseline and independent task alerts',async()=>{
 const f=await shoppingController({category:'fuel'}),details=f.all(f.article).find(node=>node.tag==='details');
 assert.match(f.article.textContent,/Wartungspläne verwenden ausschließlich ihre eigene Zuständigkeit/);
 for(const phrase of ['sieben Tage vorher','09:00 Uhr in der Haushaltszeitzone','24 Stunden','30 Kalendertage','beim ersten Prüfen bleibt ohne Hinweis','Archivierte Fahrzeuge','beide Hinweise erscheinen'])assert.ok(details.textContent.includes(phrase),phrase);
});

test('Fuel links require enabled module and resolve separate vehicle and maintenance workflows',async()=>{
 for(const disabledModule of [false,true]){const f=await shoppingController({category:'fuel',disabledModule}),links=f.all(f.article).filter(node=>node.tag==='a');assert.deepEqual(links.map(link=>link.href),disabledModule?[]:['/m/fuel/workspace/overview','/m/fuel/workspace/maintenance']);}
});

test('Finance consent saves off/on/off independently without implicit device enrollment',async()=>{
 const f=await shoppingController({category:'expenses'});assert.equal(f.writes.length,0);
 for(const enabled of [false,true,false]){f.checkbox().checked=enabled;await f.submit();assert.equal(f.saved.enabled,enabled);}
 assert.deepEqual(f.writes.map(row=>[row.category,row.enabled,row.version]),[['expenses',false,0],['expenses',true,1],['expenses',false,2]]);
 assert.ok(f.reads.every(url=>!url.includes('/subscriptions/')));
});

test('Finance failed consent preserves draft and conflict reloads without retry',async()=>{
 const f=await shoppingController({category:'expenses'});f.checkbox().checked=true;f.setMode('fail');await f.submit();assert.equal(f.checkbox().checked,true);assert.equal(f.saved.enabled,false);
 f.setMode('conflict');await f.submit();assert.equal(f.checkbox().checked,false);assert.equal(f.saved.version,3);assert.equal(f.writes.length,2);
});

test('Finance guidance distinguishes actual payment, private scope, no-history and budget hysteresis',async()=>{
 const f=await shoppingController({category:'expenses'}),details=f.all(f.article).find(node=>node.tag==='details');
 for(const text of ['keine Beträge, Kontonamen','80 % und 100 %','75 % beziehungsweise 95 %','nicht nachgemeldet','vollständige Monat','einschließlich Übertrag','null oder weniger','gebuchte Ausgabe allein'])assert.ok(f.article.textContent.includes(text),text);
 assert.match(details.textContent,/sieben Tage vorher/);assert.match(details.textContent,/24 Stunden/);assert.match(details.textContent,/geschützten Quellen/);
});

test('Finance links require enabled module and target actual entry and budget workspaces',async()=>{
 for(const disabledModule of [false,true]){const f=await shoppingController({category:'expenses',disabledModule}),links=f.all(f.article).filter(node=>node.tag==='a');assert.deepEqual(links.map(link=>link.href),disabledModule?[]:['/m/expenses/workspace/entries','/m/expenses/workspace/planning?section=budgets']);}
});
