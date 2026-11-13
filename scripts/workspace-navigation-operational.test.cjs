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
