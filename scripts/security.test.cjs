'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require.resolve('../internal/web/static/security.js'),'utf8');

// Executes the shipped controller. These DOM/protocol adapters are unit inputs,
// not evidence of real authentication, WebAuthn hardware or browser focus.
function fixture({target='/settings/security',modal=false,protectedForm=false,rejected=false,jobResponse=null}={}){
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
  matches(selector){return this.attributes.has(selector.slice(1,-1));}
  submit(){this.nativeSubmits++;}
  reset(){this.resets++;this.password.value='';}
  contains(element){return element===this.submitButton||element===this.password;}
 }
 const language=new Form('/settings/language','/settings/security/reauth');
 const reauth=new Form('/settings/security/reauth',target);
 const original=new Form('/settings/users',null);original.attributes.set('data-security-document','');original.draft='Pending new user draft';
 if(protectedForm)original.attributes.set('data-security-form','');
 const job=new Form('/settings/backup/jobs',null);job.attributes.set('data-security-form','');
 const jobEvents=[];
 job.dispatchEvent=event=>{jobEvents.push(event);return event.type!=='household:security-saved';};
 const documentEvents=new Map();
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
 document={activeElement:trigger,createElement:()=>new Element(),addEventListener:(name,callback)=>documentEvents.set(name,callback),
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
  else if(url==='/settings/backup/jobs' && jobResponse){data=jobResponse;ok=!jobResponse.error;}
  else if(!['/settings/security/reauth','/api/v1/security/passkeys/reauth/finish'].includes(url))throw Error('Unexpected request: '+url);
  if(rejected&&url==='/settings/security/reauth'){ok=false;data={error:'Fixture confirmation refused'};}
  return {ok,headers:{get:()=> 'application/json'},json:async()=>data};
 };
 class FormData {constructor(form){this.entries=[['csrf','unit-fixture-csrf'],['password',form.password.value]];if(form.target)this.entries.push(['return_to',form.target.value]);}[Symbol.iterator](){return this.entries[Symbol.iterator]();}}
 const PublicKeyCredential={parseRequestOptionsFromJSON:options=>options};
 const sandbox={document,fetch,FormData,HTMLFormElement:Form,URLSearchParams,AbortController,setTimeout,clearTimeout,Uint8Array,PublicKeyCredential,
  CustomEvent,
  navigator:{credentials:{async get(){credentialGets++;return {toJSON:()=>({id:'fixture-credential'})};}}},
  location:{assign:url=>destinations.push(url),reload:()=>reloads++}};
 sandbox.window={isSecureContext:true,PublicKeyCredential};
 vm.runInNewContext(source,sandbox);
 const event=()=>({prevented:false,preventDefault(){this.prevented=true;}});
 return {language,reauth,original,job,jobEvents,dialog,trigger,freshness,calls,destinations,document,get reloads(){return reloads;},get credentialGets(){return credentialGets;},
  async confirm(){const e=event();await reauth.listeners.get('submit')(e);assert.equal(e.prevented,true);},
  startModal(){const e=event();trigger.listeners.get('click')(e);assert.equal(e.prevented,true);},
  startProtected(){const e=event();e.target=original;documentEvents.get('submit')(e);assert.equal(e.prevented,true);},
  startJob(){const e=event();e.target=job;documentEvents.get('submit')(e);assert.equal(e.prevented,true);},
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

test('successful background form clears the saving status while a rejected request keeps its error', async()=>{
 const success=fixture({jobResponse:{id:'queued-job'}});
 success.startJob();
 assert.equal(success.job.output.textContent,'Wird gespeichert …');
 await settled(()=>success.job.getAttribute('aria-busy')==='false');
 assert.equal(success.jobEvents[0].type,'household:security-saved');
 assert.equal(success.jobEvents[0].detail.id,'queued-job');
 assert.equal(success.reloads,0);
 assert.equal(success.job.output.hidden,true);
 assert.equal(success.job.output.textContent,'');

 const failure=fixture({jobResponse:{error:'Job was refused'}});
 failure.startJob();
 await settled(()=>failure.job.getAttribute('aria-busy')==='false');
 assert.equal(failure.jobEvents[0].type,'household:security-failed');
 assert.equal(failure.job.output.hidden,false);
 assert.equal(failure.job.output.textContent,'Job was refused');
 assert.equal(failure.job.output.attributes.get('class:security-error'),true);
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

function backupFixture({htmlFailure = 0, detailStatuses = [], readDetail} = {}) {
  const historySelector = '[data-backup-history]';
  const activeSelector = '.backup-running, .backup-waiting, [data-backup-active]';
  const feedback = {dataset: {}};
  const retainedAction = {closest: () => null};
  let historyFocused = false;
  const heading = {focus() { historyFocused = true; }};
  const node = (name, text, hidden = false) => ({
    innerHTML: text,
    hidden,
    childNodes: [text],
    contains: child => name === 'retained' && child === retainedAction,
    hasAttribute: attribute => name === 'history' && attribute === 'data-backup-history',
    querySelector: () => null,
    replaceChildren(text) { this.innerHTML = text; },
  });
  const targets = {
    [historySelector]: node('history', 'running'),
    '[data-backup-snapshots="local"]': node('local', 'empty'),
    '[data-backup-snapshots="offsite"]': node('offsite', '', true),
    '[data-backup-retained]': node('retained', 'old copy'),
  };
  const updated = {
    [historySelector]: node('history', 'complete'),
    '[data-backup-snapshots="local"]': node('local', 'local point'),
    '[data-backup-snapshots="offsite"]': node('offsite', 'external point'),
    '[data-backup-retained]': node('retained', '', true),
  };
  let reauth = false;
  let htmlRequests = 0;
  let detailRequests = 0;
  let scheduled = null;
  let jobs = [{id: 'fixture-job', state: 'complete'}];
  const events = new Map();
  const document = {
    activeElement: null,
    addEventListener(name, handler) { events.set(name, handler); },
    querySelector(selector) {
      if (selector === '[data-backup-feedback]') return feedback;
      if (selector === '#security-reauth[open]') return reauth ? {} : null;
      if (selector === activeSelector) return {};
      if (selector === `${historySelector} h2`) return heading;
      return targets[selector] || null;
    },
  };
  class DOMParser {
    parseFromString() {
      return {querySelector: selector => updated[selector] || null};
    }
  }
  const fetch = async url => {
    if (url === '/api/v1/core/backup') {
      return {ok: true, json: async () => ({jobs})};
    }
    if (url.startsWith('/api/v1/core/backup/jobs/')) {
      const response = detailStatuses[detailRequests++];
      const status = typeof response === 'function' ? await response() : response;
      const id = url.split('/').at(-1);
      return {status, ok: status === 200, json: async () => {
        await readDetail?.(id);
        return {id, state: 'complete'};
      }};
    }
    assert.equal(url, '/settings/backup');
    htmlRequests++;
    return {ok: htmlRequests !== htmlFailure, status: htmlRequests === htmlFailure ? 502 : 200, text: async () => '<html></html>'};
  };
  const source = fs.readFileSync(require.resolve('../internal/web/static/backup.js'), 'utf8');
  vm.runInNewContext(source, {
    document, window: {}, fetch, DOMParser, AbortSignal,
    setTimeout(callback) { scheduled = callback; },
    clearTimeout() { scheduled = null; },
  });
  return {
    feedback, targets,
    setReauth(value) { reauth = value; },
    focusRetainedAction() { document.activeElement = retainedAction; },
    get historyFocused() { return historyFocused; },
    get htmlRequests() { return htmlRequests; },
    get detailRequests() { return detailRequests; },
    get scheduled() { return scheduled !== null; },
    saveJob(id = 'fixture-job') {
      jobs = [];
      events.get('household:security-saved')({
        target: {matches: () => true}, detail: {id}, preventDefault() {},
      });
    },
    async poll() {
      assert.ok(scheduled, 'the status poll must be scheduled');
      const callback = scheduled;
      scheduled = null;
      await callback();
    },
  };
}

test('backup poll retries a deferred history refresh after its first HTML request fails', async () => {
  const f = backupFixture({htmlFailure: 2});
  const history = f.targets['[data-backup-history]'];
  f.setReauth(true);
  await f.poll();
  assert.equal(history.innerHTML, 'running', 'reauthentication preserves the current DOM');
  f.setReauth(false);
  await f.poll();
  assert.equal(history.innerHTML, 'running', 'the HTML request failed');
  await f.poll();
  assert.equal(f.htmlRequests, 3);
  assert.equal(history.innerHTML, 'complete');
  assert.equal(f.scheduled, false, 'finished jobs no longer need polling');
});

test('backup refresh adds the offsite list and removes a discarded copy while restoring keyboard focus', async () => {
  const f = backupFixture();
  f.focusRetainedAction();
  await f.poll();
  assert.equal(f.targets['[data-backup-snapshots="local"]'].innerHTML, 'local point');
  assert.equal(f.targets['[data-backup-snapshots="offsite"]'].hidden, false);
  assert.equal(f.targets['[data-backup-snapshots="offsite"]'].innerHTML, 'external point');
  assert.equal(f.targets['[data-backup-retained]'].hidden, true);
  assert.equal(f.historyFocused, true);
  assert.equal(f.scheduled, false);
});

test('backup poll retries unavailable job details and stops tracking a permanently missing job', async () => {
  const transient = backupFixture({detailStatuses: [503, 200]});
  transient.saveJob();
  await transient.poll();
  assert.equal(transient.scheduled, true);
  await transient.poll();
  assert.equal(transient.detailRequests, 2);
  assert.equal(transient.feedback.textContent, 'Sicherungsauftrag abgeschlossen.');
  assert.equal(transient.scheduled, false);

  const missing = backupFixture({detailStatuses: [404]});
  missing.saveJob();
  await missing.poll();
  assert.equal(missing.detailRequests, 1);
  assert.equal(missing.feedback.textContent, 'Sicherungsauftrag nicht gefunden. Prüfe den Verlauf.');
  assert.equal(missing.scheduled, false);
});

test('a stale missing-job response cannot clear a more recently submitted backup job', async () => {
  let finishOldRequest;
  const oldRequest = new Promise(resolve => { finishOldRequest = resolve; });
  const f = backupFixture({detailStatuses: [() => oldRequest, 200]});
  f.saveJob();
  const polling = f.poll();
  await settled(() => f.detailRequests === 1);
  f.saveJob('newer-job');
  finishOldRequest(404);
  await polling;
  assert.equal(f.feedback.textContent, 'Sicherungsauftrag aktualisiert.');
  await f.poll();
  assert.equal(f.detailRequests, 2);
  assert.equal(f.feedback.textContent, 'Sicherungsauftrag abgeschlossen.');
});

test('backup follows a newly submitted job when the previous detail body is still being read', async () => {
  let reading = false;
  let finishOldBody;
  const oldBody = new Promise(resolve => { finishOldBody = resolve; });
  const f = backupFixture({detailStatuses: [200, 200], readDetail(id) {
    if (id !== 'fixture-job') return;
    reading = true;
    return oldBody;
  }});
  f.saveJob();
  const polling = f.poll();
  await settled(() => reading);
  f.saveJob('newer-job');
  if (f.scheduled) await f.poll();
  finishOldBody();
  await polling;
  assert.equal(f.feedback.textContent, 'Sicherungsauftrag aktualisiert.');
  await f.poll();
  assert.equal(f.detailRequests, 2);
  assert.equal(f.feedback.textContent, 'Sicherungsauftrag abgeschlossen.');
  assert.equal(f.scheduled, false);
});
