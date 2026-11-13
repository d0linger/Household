'use strict';
const assert=require('node:assert/strict');
const {test}=require('node:test');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
function helper(file,name){
 const source=fs.readFileSync(path.join(__dirname,'../internal/web/static',file),'utf8');
 const context={URLSearchParams};vm.createContext(context);
 const start=source.search(/\(\(\)\s*=>\s*\{/);assert.ok(start>=0,'Workspace entry point exists');
 vm.runInContext(source.slice(0,start)+'\nthis.result='+name,context);
 return context.result;
}
test('Vehicle workspace links retain only the selected vehicle across every destination',()=>{
 const link=helper('fuel-module.js','fuelWorkspaceHref'),id='11111111-1111-4111-8111-111111111111';
 for(const pane of['overview','capture','history','reports','costs','maintenance','charging','trips','imports','documents','odometer','settings']){
  const url=new URL(link(pane,id),'https://household.invalid');
  assert.equal(url.pathname,'/m/fuel/workspace/'+pane);
  assert.deepEqual([...url.searchParams],[['vehicle',id]]);
  assert.equal(link(pane),'/m/fuel/workspace/'+pane);
 }
 assert.equal(link('manage',id),'/m/fuel/manage?vehicle='+id);
});
test('Vehicle navigation refuses invalid identity and destinations instead of broadening selection',()=>{
 const link=helper('fuel-module.js','fuelWorkspaceHref');
 for(const bad of['../settings','https://evil.invalid','unknown',''])assert.throws(()=>link(bad));
 for(const bad of['00000000-0000-0000-0000-000000000000','foo','../admin','11111111-1111-4111-8111-111111111111&record=x'])assert.throws(()=>link('history',bad));
});
test('Vehicle leaving guard protects uncertain work but accepts a confirmed receipt without discarding it',()=>{
 const guard=helper('fuel-module.js','fuelWorkspaceUnsaved');
 assert.equal(guard(null,null,false,false),false);
 assert.equal(guard(null,null,false,true),true);
 assert.equal(guard({key:'pending'},null,false,false),true);
 assert.equal(guard({key:'accepted'},{state:'accepted'},false,true),false);
 assert.equal(guard(null,{state:'accepted'},true,false),true);
});
test('Task context stays on supported area-filtered views without inventing filters on planning or management',()=>{
 const link=helper('chores-workspace.js','choresWorkspaceHref'),area='22222222-2222-4222-8222-222222222222';
 for(const pane of['today','history'])assert.equal(link(pane,area),'/m/chores/workspace/'+pane+'?area='+area);
 for(const pane of['areas','series','plan','settings'])assert.equal(link(pane,area),'/m/chores/workspace/'+pane);
 assert.equal(link('today'),'/m/chores/workspace/today');
 assert.throws(()=>link('history','invalid'));assert.throws(()=>link('today','00000000-0000-0000-0000-000000000000'));assert.throws(()=>link('../admin',area));
});
test('Meter workspace and management retain only the selected register, including reading plans',()=>{
 const link=helper('meters-workspace.js','metersWorkspaceHref'),register='33333333-3333-4333-8333-333333333333';
 for(const pane of['overview','capture','history','costs','plans','imports','settings']){
  const url=new URL(link(pane,register),'https://household.invalid');
  assert.equal(url.pathname,'/m/meters/workspace/'+pane);
  assert.deepEqual([...url.searchParams],[['register',register]]);
 }
 assert.equal(link('manage',register),'/m/meters/manage?register='+register);
 assert.equal(link('overview'),'/m/meters/workspace/overview');
 for(const bad of['00000000-0000-0000-0000-000000000000','x',register+'&record=other'])assert.throws(()=>link('history',bad));
 for(const pane of['../settings','unknown','https://evil.invalid'])assert.throws(()=>link(pane,register));
});

test('Meter browser commands retain actual upstream bytes without replay or CDP body reads',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'browser-meters-workspace-check.cjs'),'utf8');
 const start=source.indexOf('async function post(action,click){'),end=source.indexOf("  await pane('settings');",start);
 assert.ok(start>=0&&end>start);
 for(const action of ['meter.save','reading.save','import.preview'])for(const failure of ['', 'fetch', 'body', 'fulfill', 'status', 'json']){
  const url='http://localhost:8101/api/v1/meters/commands',bytes=Buffer.from(failure==='json'?'invalid JSON':JSON.stringify({id:'actual-record',value:'125.123456',state:'preview',rows:1}));
  const events=[];let handler,match,resolve,reject;
  const request={method:()=> 'POST',postDataJSON:()=>({action})};
  const upstream={body:async()=>{events.push('body');if(failure==='body')throw Error('upstream body failed');return bytes;}};
  const response={url:()=>url,request:()=>request,status:()=>failure==='status'?409:200,json:()=>{throw Error('CDP body unavailable');}};
  const route={request:()=>request,fetch:async options=>{events.push('fetch');assert.deepEqual(JSON.parse(JSON.stringify(options)),{maxRedirects:0,maxRetries:0,timeout:15000});if(failure==='fetch')throw Error('upstream fetch failed');return upstream;},fulfill:async value=>{events.push('fulfill');assert.equal(value.response,upstream);assert.equal(value.body,bytes);if(failure==='fulfill')throw Error('upstream fulfill failed');resolve(response);},abort:async reason=>{events.push('abort');assert.equal(reason,'failed');reject(Error('request aborted'));},fallback:async()=>{throw Error('expected command was not captured');}};
  const page={route:async(m,h)=>{match=m;handler=h;},waitForResponse:predicate=>{assert.equal(predicate(response),true);return new Promise((ok,bad)=>{resolve=ok;reject=bad;});},unroute:async(m,h)=>{assert.equal(m,match);assert.equal(h,handler);events.push('cleanup');}};
  const scope={page,URL,origin:new URL(url),must:assert.ok,ready:async()=>{events.push('ready');}};
  vm.runInNewContext(source.slice(start,end),scope);
  const result=scope.post(action,async()=>{
   assert.equal(match(new URL(url)),true);assert.equal(match(new URL('https://foreign.invalid/api/v1/meters/commands')),false);assert.equal(match(new URL('http://localhost:8101/api/v1/meters/history')),false);
   let fellBack=false;await handler({request:()=>({method:()=> 'POST',postDataJSON:()=>({action:'unrelated'})}),fallback:async()=>{fellBack=true;}});assert.equal(fellBack,true);
   await handler(route);
  });
  if(failure)await assert.rejects(result);else assert.equal((await result).value,'125.123456');
  assert.equal(events.filter(e=>e==='fetch').length,1,'Never replay the actual command');assert.equal(events.at(-1),'cleanup');
  if(['fetch','body','fulfill'].includes(failure)){assert.ok(events.includes('abort'));assert.ok(!events.includes('ready'));}
  if(failure==='status')assert.ok(!events.includes('ready'),'Reject failed HTTP before accepting UI state');
 }
});
