'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require.resolve('../internal/web/static/security.js'),'utf8');

// Executes the shipped controller. These DOM/protocol adapters are unit inputs,
// not evidence of real authentication, WebAuthn hardware or browser focus.
function fixture({target='/settings/security',modal=false,protectedForm=false,rejected=false}={}){
 let document;
 class Element {
  constructor(){this.listeners=new Map();this.attributes=new Map();this.hidden=false;this.textContent='';this.disabled=false;this.dataset={};this.classList={toggle:(name,enabled)=>this.attributes.set('class:'+name,enabled)};}
  addEventListener(name,callback){this.listeners.set(name,callback);}
  setAttribute(name,value){this.attributes.set(name,String(value));}
  getAttribute(name){return this.attributes.get(name)??null;}
  hasAttribute(name){return this.attributes.has(name);}
  focus(){document.activeElement=this;}
  append(){}
  querySelector(){return null;}
  querySelectorAll(){return [];}
 }
 class Form extends Element {
  constructor(action,returnTo){super();this.action=action;this.submitButton=new Element();this.output=new Element();this.password=new Element();this.password.value='unit-fixture-password';this.target=returnTo===null?null:{value:returnTo};this.resets=0;this.nativeSubmits=0;}
  querySelector(selector){
   if(selector==='[name="return_to"]')return this.target;
   if(selector==='[data-security-status], [data-passkey-status]')return this.output;
   if(selector==='input[name="password"]')return this.password;
   if(selector==='button:not([type="button"]):not([type="reset"])')return this.submitButton;
   return null;
  }
  querySelectorAll(selector){return selector==='button'?[this.submitButton]:[];}
  reset(){this.resets++;this.password.value='';}
  contains(element){return element===this.submitButton||element===this.password;}
 }
 const language=new Form('/settings/language','/settings/security/reauth');
 const reauth=new Form('/settings/security/reauth',target);
 const original=new Form('/settings/users',null);original.attributes.set('data-security-document','');original.draft='Pending new user draft';
 const trigger=new Element(),freshness=new Element(),passkeyScope=new Form('',null),passkey=new Element();passkey.parentElement=passkeyScope;
 passkeyScope.querySelectorAll=selector=>selector==='button'?[passkey]:[];
 let dialog=null;
 if(modal){
  dialog=new Element();dialog.open=false;
  dialog.querySelector=selector=>selector==='form'?reauth:selector==='input[name="password"]'?reauth.password:null;
  dialog.querySelectorAll=selector=>selector==='[role="status"]'?[reauth.output]:[];
  dialog.showModal=()=>{dialog.open=true;};
  dialog.close=()=>{dialog.open=false;dialog.listeners.get('close')?.();};
 }
 // The language form precedes the standalone reauth form in the real shared
 // header. An unscoped return_to query deliberately resolves to the wrong one.
 document={activeElement:trigger,createElement:()=>new Element(),
  querySelector(selector){
   if(selector==='#security-reauth')return dialog;
   if(selector==='[data-reauth-page]')return modal?null:reauth;
   if(selector==='[name="return_to"]')return language.target;
   if(selector==='input[name="csrf"]')return {value:'unit-fixture-csrf'};
   return null;
  },
  querySelectorAll(selector){
   if(selector==='[data-reauth-form], [data-reauth-page]')return [reauth];
   if(selector==='[data-open-reauth]')return modal?[trigger]:[];
   if(selector==='[data-security-form]')return protectedForm?[original]:[];
   if(selector==='[data-passkey-reauth]')return [passkey];
   if(selector==='.security-freshness p')return [freshness];
   return [];
  }
 };
 const calls=[],destinations=[];let reloads=0,freshReads=0,credentialGets=0;
 const fetch=async(url,options)=>{
  calls.push({url,method:options.method||'GET',body:options.body,headers:options.headers});
  let data={},ok=true;
  if(url==='/api/v1/security/freshness')data={recent:++freshReads>1};
  else if(url==='/api/v1/security/passkeys/reauth/begin')data={challenge_id:'fixture-challenge',options:{challenge:'fixture-options'}};
  else if(!['/settings/security/reauth','/api/v1/security/passkeys/reauth/finish'].includes(url))throw Error('Unexpected request: '+url);
  if(rejected&&url==='/settings/security/reauth'){ok=false;data={error:'Fixture confirmation refused'};}
  return {ok,headers:{get:()=> 'application/json'},json:async()=>data};
 };
 class FormData {constructor(form){this.entries=[['csrf','unit-fixture-csrf'],['password',form.password.value]];if(form.target)this.entries.push(['return_to',form.target.value]);}[Symbol.iterator](){return this.entries[Symbol.iterator]();}}
 class HTMLFormElement {submit(){this.nativeSubmits++;}}
 const PublicKeyCredential={parseRequestOptionsFromJSON:options=>options};
 const sandbox={document,fetch,FormData,HTMLFormElement,URLSearchParams,AbortController,setTimeout,clearTimeout,Uint8Array,PublicKeyCredential,
  navigator:{credentials:{async get(){credentialGets++;return {toJSON:()=>({id:'fixture-credential'})};}}},
  location:{assign:url=>destinations.push(url),reload:()=>reloads++}};
 sandbox.window={isSecureContext:true,PublicKeyCredential};
 vm.runInNewContext(source,sandbox);
 const event=()=>({prevented:false,preventDefault(){this.prevented=true;}});
 return {language,reauth,original,dialog,trigger,freshness,calls,destinations,document,get reloads(){return reloads;},get credentialGets(){return credentialGets;},
  async confirm(){const e=event();await reauth.listeners.get('submit')(e);assert.equal(e.prevented,true);},
  startModal(){const e=event();trigger.listeners.get('click')(e);assert.equal(e.prevented,true);},
  startProtected(){const e=event();original.listeners.get('submit')(e);assert.equal(e.prevented,true);},
  startPasskey(){passkey.listeners.get('click')();}};
}
async function settled(predicate){for(let turn=0;turn<20&&!predicate();turn++)await new Promise(resolve=>setImmediate(resolve));assert.equal(Boolean(predicate()),true,'controller work settled within the bounded unit loop');}

test('standalone password confirmation uses its own return target despite the preceding language form',async()=>{
 for(const target of ['/settings/security','/settings/users']){
  const f=fixture({target});await f.confirm();
  assert.equal(f.language.target.value,'/settings/security/reauth');assert.deepEqual(f.destinations,[target]);
  assert.equal(f.calls.length,1);assert.equal(f.calls[0].url,'/settings/security/reauth');assert.equal(f.calls[0].method,'POST');
  assert.equal(f.calls[0].body.get('return_to'),target);assert.equal(f.calls[0].headers['X-CSRF-Token'],'unit-fixture-csrf');
  assert.equal(f.reauth.resets,1);assert.equal(f.reauth.password.value,'');assert.equal(f.reauth.getAttribute('aria-busy'),'false');assert.equal(f.reauth.submitButton.disabled,false);assert.equal(f.reloads,0);
 }
});

test('missing or empty standalone return target falls back to Security, never the language return target',async()=>{
 for(const target of [null,'']){const f=fixture({target});await f.confirm();assert.deepEqual(f.destinations,['/settings/security']);}
});

test('refused standalone confirmation preserves the draft and does not navigate',async()=>{
 const f=fixture({target:'/settings/users',rejected:true});await f.confirm();
 assert.deepEqual(f.destinations,[]);assert.equal(f.reauth.resets,0);assert.equal(f.reauth.password.value,'unit-fixture-password');
 assert.equal(f.reauth.output.textContent,'Fixture confirmation refused');assert.equal(f.reauth.output.attributes.get('class:security-error'),true);assert.equal(f.reauth.submitButton.disabled,false);
});

test('standalone passkey confirmation shares the scoped destination and fallback behavior',async()=>{
 for(const target of ['/settings/security','/settings/users',null,'']){
  const f=fixture({target});f.startPasskey();await settled(()=>f.destinations.length===1);
  assert.deepEqual(f.destinations,[target||'/settings/security']);assert.equal(f.credentialGets,1);
  assert.deepEqual(f.calls.map(call=>[call.url,call.method]),[['/api/v1/security/passkeys/reauth/begin','POST'],['/api/v1/security/passkeys/reauth/finish','POST']]);
 }
});

test('modal confirmation closes and restores focus without following the unrelated language target',async()=>{
 const f=fixture({modal:true});f.startModal();assert.equal(f.dialog.open,true);assert.equal(f.document.activeElement,f.reauth.password);
 await f.confirm();assert.equal(f.dialog.open,false);assert.equal(f.document.activeElement,f.trigger);assert.deepEqual(f.destinations,[]);assert.equal(f.reloads,0);
 assert.match(f.freshness.textContent,/bestätigt/);
});

test('modal confirmation resumes the original protected form only after checking fresh authority',async()=>{
 const f=fixture({modal:true,protectedForm:true});f.document.activeElement=f.original.submitButton;f.startProtected();await settled(()=>f.dialog.open);
 assert.equal(f.original.nativeSubmits,0);await f.confirm();
 assert.equal(f.original.nativeSubmits,1);assert.equal(f.original.draft,'Pending new user draft');assert.equal(f.dialog.open,false);assert.equal(f.document.activeElement,f.original.submitButton);
 assert.deepEqual(f.calls.map(call=>[call.url,call.method]),[['/api/v1/security/freshness','GET'],['/settings/security/reauth','POST'],['/api/v1/security/freshness','GET']]);
 assert.deepEqual(f.destinations,[]);assert.equal(f.reloads,0);assert.equal(f.original.getAttribute('aria-busy'),'false');
});
