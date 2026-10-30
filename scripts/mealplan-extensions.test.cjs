'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const H=require('../internal/web/static/planning-extensions.js');
const calendar=require('../internal/web/static/planning-calendar.js');
test('calendar expiry display names the device zone and handles Vienna daylight changes',()=>{
 assert.match(calendar.calendarDate('2026-03-29T00:30:00Z','Europe/Vienna'),/29\.03\.2026.*01:30.*\(Europe\/Vienna\)/);
 assert.match(calendar.calendarDate('2026-03-29T01:30:00Z','Europe/Vienna'),/29\.03\.2026.*03:30.*\(Europe\/Vienna\)/);
 assert.match(calendar.calendarDate('2026-10-25T02:30:00Z','Europe/Vienna'),/25\.10\.2026.*03:30.*\(Europe\/Vienna\)/);
});
test('calendar display preserves the actual instant in a different device zone',()=>{
 assert.match(calendar.calendarDate('2026-10-30T00:30:00Z','America/New_York'),/29\.10\.2026.*20:30.*\(America\/New_York\)/);
 assert.match(calendar.calendarDate('2026-10-30T00:30:00Z','UTC'),/30\.10\.2026.*00:30.*\(UTC\)/);
 assert.equal(typeof calendar.resolvedDeviceZone(),'string');assert.ok(calendar.resolvedDeviceZone().length>0);
});
test('calendar malformed timestamps and invalid zones display an explicit unknown',()=>{
 for(const raw of [null,12,{},'', 'not-a-date','x'.repeat(101)])assert.equal(calendar.calendarDate(raw),'Zeitangabe nicht lesbar');
 assert.equal(calendar.calendarDate('2026-10-30T00:30:00Z','not/a-zone'),'Zeitangabe nicht lesbar');
});
test('portions preserve exact significant digits, comma input and six-place canonical strings',()=>{
 assert.equal(H.portions('9007199254.740993'),'9007199254.740993');assert.equal(H.portions(' 1,25 '),'1.250000');assert.equal(H.portions('0.000001'),'0.000001');
 for(const value of ['0','-1','1e2','0.0000001','NaN','',1,null,{toString(){throw Error('must not coerce');}}])assert.throws(()=>H.portions(value));
});
test('EUR inputs retain zero and unknown independently and never pass through Number',()=>{
 assert.equal(H.majorToMinor(''),null);assert.equal(H.majorToMinor('0,00'),'0');assert.equal(H.majorToMinor('9007199254,74'),'900719925474');assert.equal(H.majorToMinor('10000000000.00'),'1000000000000');
 for(const value of ['10000000000.01','1.001','-1','Infinity','1e3',12,{toString(){return'12';}}])assert.throws(()=>H.majorToMinor(value));
 assert.equal(H.money(null),'Unbekannt');assert.equal(H.money('0'),'0,00 €');assert.equal(H.money('9007199254740993'),'90071992547409,93 €');assert.equal(H.money(0),'Unbekannt');
});
test('quantity display retains a positive microportion and never converts unknown to zero',()=>{
 assert.equal(H.quantity('0.000001'),'0,000001');assert.equal(H.quantity('100.000000'),'100');assert.equal(H.quantity(null),'Unbekannt');assert.equal(H.quantity(100),'Unbekannt');
});
test('versions and identifiers have a separate bounded structural contract',()=>{
 assert.equal(H.version(0),0);assert.equal(H.version(Number.MAX_SAFE_INTEGER),Number.MAX_SAFE_INTEGER);for(const value of ['1',-1,1.1,Infinity,Number.MAX_SAFE_INTEGER+1])assert.throws(()=>H.version(value));
 assert.ok(H.uuid('aaaaaaaa-0000-4000-8000-000000000001'));for(const value of ['../../secret','a',null,'AAAAAAAA-0000-4000-8000-000000000001'])assert.equal(H.uuid(value),false);
});
test('all five weights are explicit bounded integers and unknown rule keys are not emitted',()=>{
 const values={stock:'0',urgency:'100',effort:'42',cost:'12',repetition:'1',invented:'99'},fields={querySelector(selector){return{value:values[selector.match(/"(.*?)"/)[1]]};}};
 assert.deepEqual(H.rules(fields),{stock:0,urgency:100,effort:42,cost:12,repetition:1});for(const bad of ['101','-1','1.5','',null]){values.cost=bad;assert.throws(()=>H.rules(fields));}
});
function harness(fetcher,ask=async()=>false){
 const nodes=new Map(),node=()=>({hidden:true,disabled:false,textContent:'',events:{},addEventListener(name,fn){this.events[name]=fn;}}),root={dataset:{generation:'1',writable:'true',id:'aaaaaaaa-0000-4000-8000-000000000001'},querySelector(selector){if(!nodes.has(selector))nodes.set(selector,node());return nodes.get(selector);}},history={locks:[],refreshes:0,discards:0},state={dirty:false};let api,key=0;
 const window={HouseholdPlanningLeftovers:{create(_,value){api=value;return{update(){},lock(){},dirty(){return state.dirty;},discard(){history.discards++;state.dirty=false;}};}}};
 vm.runInNewContext(fs.readFileSync(require.resolve('../internal/web/static/planning-extensions.js'),'utf8'),{window,document:{querySelector(){return{content:'private-csrf-fixture'};}},fetch:fetcher,AbortController,TextEncoder,setTimeout,clearTimeout,crypto:{randomUUID(){return'bbbbbbbb-0000-4000-8000-'+String(++key).padStart(12,'0');}},URL,console});
 const hub=window.HouseholdPlanningExtensions.create(root,{lock(value){history.locks.push(value);},refresh:async()=>{history.refreshes++;},ask});hub.onWeek({plan:{version:3},days:['2026-10-26']});return{api,hub,nodes,history,state};
}
const response=(data,status=200)=>({ok:status>=200&&status<300,status,text:async()=>JSON.stringify(data)});
test('response loss retains exact body and key, blocks new writes, and same-key retry confirms once',async()=>{
 const calls=[];let first=true,accepted=0;const h=harness(async(url,options)=>{calls.push({url,...options});if(first){first=false;throw Error('response lost');}return response({accepted:true});});
 const body={generation:1,plan_version:3,week:'2026-10-26',budget_minor:'0'};
 await assert.rejects(h.api.mutate('/budget',body,()=>accepted++));assert.equal(h.hub.isPending(),true);await assert.rejects(h.api.mutate('/budget',{...body,budget_minor:'1'}));assert.equal(calls.length,1);
 await h.nodes.get('#planning-extension-retry').events.click();assert.equal(calls.length,2);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(calls[0].body,calls[1].body);assert.equal(calls[1].headers['X-CSRF-Token'],'private-csrf-fixture');assert.equal(accepted,1);assert.equal(h.history.refreshes,1);assert.equal(h.hub.isPending(),false);assert.equal(h.history.locks.at(-1),false);
});
test('definite version conflict does not become an uncertain accepted receipt',async()=>{
 const h=harness(async()=>response({error:'Stand geändert'},409));h.state.dirty=true;await assert.rejects(h.api.mutate('/leftovers',{action:'source_save'}),{status:409});assert.equal(h.hub.isPending(),false);assert.equal(h.hub.hasWork(),true);assert.match(h.nodes.get('#planning-extension-notice').textContent,/Entwurf bleibt erhalten/);assert.equal(h.history.refreshes,0);
});
test('known success followed by render failure stays confirmed and does not mint a retry',async()=>{
 const h=harness(async()=>response({accepted:true}));await h.api.mutate('/budget',{},()=>{throw Error('render failed');});assert.equal(h.hub.isPending(),false);assert.equal(h.history.refreshes,1);assert.match(h.nodes.get('#planning-extension-notice').textContent,/bestätigt/);
});
test('week change requires explicit discard and never rebinds an uncertain write',async()=>{
 const h=harness(async()=>{throw Error('lost');});h.state.dirty=true;assert.equal(await h.hub.canChangeWeek(),false);assert.equal(h.history.discards,0);await assert.rejects(h.api.mutate('/budget',{}));assert.equal(await h.hub.canChangeWeek(),false);assert.equal(h.history.discards,0);
 const permitted=harness(async()=>response({}),async()=>true);permitted.state.dirty=true;assert.equal(await permitted.hub.canChangeWeek(),true);assert.equal(permitted.history.discards,1);
});
test('source review uses the real global source route and previews carry CSRF without writing a receipt',async()=>{
 const calls=[],h=harness(async(url,options)=>{calls.push({url,...options});return response({});});await h.api.read('/source/cccccccc-0000-4000-8000-000000000003');await h.api.review('/autoplan/preview',{slots:[]});assert.equal(calls[0].url,'/api/v1/planning/source/cccccccc-0000-4000-8000-000000000003');assert.equal(calls[1].url,'/api/v1/planning/aaaaaaaa-0000-4000-8000-000000000001/autoplan/preview');assert.equal(calls[1].headers['X-CSRF-Token'],'private-csrf-fixture');assert.equal(h.hub.isPending(),false);assert.equal(h.history.refreshes,0);
});
