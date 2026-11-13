'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {accountNavigationHref,navigationDecision}=require('../internal/web/static/finance-workspace.js');
const account='00000000-0000-4000-8000-000000000001';

test('Finance carries an explicit account only across read panes supporting that filter',()=>{
 for(const pane of ['entries','overview','accounts','reports']){
  const url=new URL(accountNavigationHref('/m/expenses/workspace/'+pane+'?from=2026-11-01&section=ledger',account),'https://household.invalid');
  assert.equal(url.searchParams.get('account'),account);assert.equal(url.searchParams.get('from'),'2026-11-01');assert.equal(url.searchParams.get('section'),'ledger');
 }
});
test('Finance never silently introduces account scope into budgets, imports, people or Manage',()=>{
 for(const path of ['/m/expenses/workspace/planning','/m/expenses/workspace/planning?section=budgets','/m/expenses/workspace/import','/m/expenses/workspace/people','/m/expenses/manage','https://other.invalid/m/expenses/workspace/entries'])assert.equal(accountNavigationHref(path,account),path);
});
test('Finance cleared or malformed account identity cannot survive in a read link',()=>{
 for(const id of ['',null,'../other','<script>','not-a-uuid']){
  const url=new URL(accountNavigationHref('/m/expenses/workspace/entries?account='+account+'&month=2026-11',id),'https://household.invalid');
  assert.equal(url.searchParams.has('account'),false);assert.equal(url.searchParams.get('month'),'2026-11');
 }
});
test('Finance navigation blocks unknown outcomes before considering explicit dirty draft discard',()=>{
 assert.equal(navigationDecision(false,{unknown:true}),'blocked');assert.equal(navigationDecision(true,{unknown:true}),'blocked');
 assert.equal(navigationDecision(true,null),'confirm');assert.equal(navigationDecision(true,{unknown:false}),'confirm');
 assert.equal(navigationDecision(false,null),'allow');
});

test('Finance actual private clear retains only scope-required boolean and retry cannot widen it',async()=>{
 const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/finance-workspace.js'),'utf8');
 const S={initialAccount:account,query:{account},view:{generation:1},epoch:1,pending:null};
 const elements=new Map(),element=id=>{if(!elements.has(id))elements.set(id,{dataset:{},textContent:'',hidden:false,replaceChildren(){},removeAttribute(){},setAttribute(){}});return elements.get(id);};
 let requests=0,clearedLinks=false;
 const environment={S,$:element,root:{querySelectorAll:()=>[]},reminders:null,refCache:new Map([['accounts/'+account,{name:'private name'}]]),URL,location:{href:'https://household.invalid/m/expenses/workspace/entries'},
  updateAccountLinks:value=>{clearedLinks=value==='';},revokeImages(){},locks(){},node:()=>({}),_householdText_finance_workspace_js:text=>text,
  activeFilters:()=>['account'],fail:message=>{throw Error(message);},request:()=>{requests++;throw Error('No request is allowed before explicit scope selection');}};
 const extract=(start,end)=>{const first=source.indexOf(start),last=source.indexOf(end,first);assert.ok(first>=0&&last>first);return vm.runInNewContext('('+source.slice(first,last).trim()+')',environment);};
 const clear=extract('function clearPrivate(){','async function bounded(');clear();
 assert.equal(S.accountScopeLost,true);assert.equal(S.initialAccount,'');assert.equal(S.query.account,undefined);assert.equal(environment.refCache.size,0);assert.equal(clearedLinks,true);assert.equal(S.denied,true);
 const retry=extract('async function prepareInitialAccount(epoch){','function clearAccountSelection(){');
 await assert.rejects(retry(S.epoch),/Kontofilter.*entfernt/);assert.equal(requests,0);assert.equal(S.initialAccount,'');
});
