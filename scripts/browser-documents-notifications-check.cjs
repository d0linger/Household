'use strict';

// Actual isolated deadline configuration. Provider transport and physical device
// display are deliberately outside this browser driver's acceptance scope.
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');
const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Document notification browser requires owned localhost:8101');
const output=path.resolve(workspace,process.env.HOUSEHOLD_DOCUMENTS_NOTIFICATIONS_BROWSER_OUTPUT||'outputs/notifications-documents/browser-notifications');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Document evidence must stay inside workspace outputs');
const report={passed:false,checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],physicalDeliveryTested:false};
const secrets=[];let browser,page,watchdog,permissionRequests=0;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(value,message)=>{if(!value)throw Error(message);report.checks.push(message);};

async function fixtureState(){return readFixtureState(workspace,secrets);}
async function read(route){
 return page.evaluate(async route=>{const r=await fetch(route,{cache:'no-store'});if(!r.ok)throw Error('Actual document read failed: '+r.status);return r.json();},route);
}
async function ready({error=false}={}){
 await page.waitForFunction(()=>document.querySelector('#documents-workspace')?.getAttribute('aria-busy')==='false');
 if(!error)must(await page.locator('#doc-error').isHidden(),'Current authorized document workspace loads');
}
async function locale(language){
 if(await page.locator('html').getAttribute('data-locale')===language)return;
 await page.locator('.language-control button[value='+language+']').click();
 await page.waitForFunction(value=>document.documentElement.dataset.locale===value,language);await ready();
}
async function go(id){
 const r=await page.goto(origin.origin+'/m/documents/workspace/review?id='+id);
 must(r.status()===200,'Actual document review route succeeds');await ready();
}
const row=id=>page.locator('[data-document-deadline="'+id+'"]');
const editor=()=>page.locator('[data-document-notification-editor]');
async function edit(id){
 await row(id).getByRole('button',{name:/^(Fristhinweise einrichten|Set up deadline notifications)$/,exact:true}).click();
 await editor().waitFor();await ready();
}
async function save(expected=200){
 const pending=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/documents/commands'&&r.request().method()==='POST'&&r.request().postDataJSON().action==='notification');
 await editor().getByRole('button',{name:/^(Fristhinweise speichern|Save deadline notifications)$/,exact:true}).click();
 const r=await pending;must(r.status()===expected,'Actual notification command returns '+expected);await ready({error:expected!==200});return r.json();
}
async function discard(id){
 await editor().getByRole('button',{name:/^(Eingaben verwerfen|Discard entries)$/,exact:true}).click();await ready();
 must(await editor().count()===0,'Explicit discard closes only the notification draft');
 must(await row(id).getByRole('button',{name:/^(Fristhinweise einrichten|Set up deadline notifications)$/,exact:true}).evaluate(el=>el===document.activeElement),'Discard returns focus to the exact deadline action');
}
async function settings(id){return (await read('/api/v1/documents/notification-settings?id='+id)).settings;}
async function consent(){return page.evaluate(async()=>{
 const account=await fetch('/api/v1/core/notification-preferences',{cache:'no-store'}),devices=await fetch('/api/v1/core/push/device-preferences',{cache:'no-store'});
 if(!account.ok||!devices.ok)throw Error('Actual consent state unavailable');
 return {preferences:(await account.json()).preferences,devices:await devices.json()};
});}
async function direct(settings,changes){return page.evaluate(async({settings,changes})=>{
 const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
 const r=await fetch('/api/v1/documents/commands',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},
  body:JSON.stringify({action:'notification',id:settings.document_id,version:settings.document_version,generation:boot.states.documents.generation,
   data:{deadline_id:settings.id,deadline_version:settings.deadline_version,settings_version:settings.version,responsible_member_id:settings.responsible_member_id||'',enabled:settings.enabled,...changes}})});
 return {status:r.status,body:await r.json()};
},{settings,changes});}
async function capture(name){
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().filter(a=>Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a=>a.finished.catch(()=>{})));});
 const axe=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
 report.accessibility.push({name,violations:axe.violations.map(v=>({id:v.id,impact:v.impact,targets:v.nodes.map(n=>n.target),details:v.nodes.map(n=>n.failureSummary)}))});
 must(axe.violations.length===0,'Clean Axe '+name);
 must(await page.evaluate(()=>{const form=document.querySelector('[data-document-notification-editor]');return document.documentElement.scrollWidth<=innerWidth+1&&form.scrollWidth<=form.clientWidth+1;}),'Page and deadline editor fit '+name);
 const person=editor().locator('[name=notification_responsible_member_id]');await person.focus();
 must(await person.evaluate(el=>el===document.activeElement),'Responsible person supports keyboard focus '+name);
 await page.keyboard.press('Tab');must(await editor().locator('[name=notification_enabled]').evaluate(el=>el===document.activeElement),'Keyboard proceeds to explicit permission '+name);
 await editor().scrollIntoViewIfNeeded();
 for(const fullPage of [true,false]){const file=name+(fullPage?'':'-viewport')+'.png';await page.screenshot({path:path.join(output,file),fullPage,animations:'disabled'});report.screenshots.push(file);}
}

(async()=>{
 const state=await fixtureState();await fs.mkdir(output,{recursive:true});
 browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:process.platform==='win32'?{channel:'msedge'}:{})});
 watchdog=setTimeout(()=>{report.errors.push('Eight-minute document browser deadline exceeded');void browser.close();},480000);watchdog.unref();
 try{
  const context=await browser.newContext({storageState:state,viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(new URL(route.request().url()).origin);return route.abort();}return route.continue();});
  await context.exposeBinding('__recordDocumentPermissionRequest',()=>{permissionRequests++;});
  await context.addInitScript(()=>{if(window.Notification)Notification.requestPermission=async()=>{await window.__recordDocumentPermissionRequest();return 'default';};});
  page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error)));
  page.on('dialog',dialog=>{if(dialog.type()==='beforeunload')void dialog.accept();else void dialog.dismiss();});
  await page.goto(origin.origin+'/settings');
  const fixture=await page.evaluate(async()=>{
   let boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   if(boot.user.role!=='admin')throw Error('Actual fixture administrator required');
   if(boot.states.documents.state!=='enabled'){
    const r=await fetch('/settings/modules/documents',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.documents.version)})});
    if(!r.ok)throw Error('Actual isolated document enable failed');
    boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   }
   if(boot.states.documents.state!=='enabled')throw Error('Document enable not committed');
   const r=await fetch('/api/v1/documents/commands',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},
    body:JSON.stringify({action:'save',id:'',version:0,generation:boot.states.documents.generation,data:{title:'Synthetic deadline acceptance '+Date.now(),issuer:'',category:'',notes:'',document_type:'other',fields:{},reviewed:false}})});
   if(!r.ok)throw Error('Actual synthetic document command failed: '+r.status);
   return {document:(await r.json()).id,member:boot.user.membership_id};
  });
  await go(fixture.document);await locale('de');
  await page.getByRole('button',{name:'Frist hinzufügen',exact:true}).click();
  await page.locator('[name=due_on]').fill(new Date(Date.now()+7*86400000).toISOString().slice(0,10));
  await page.locator('[name=source]').fill('Synthetic explicitly reviewed source');
  await page.locator('[name=confirmed]').check();
  const confirmed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/documents/commands'&&r.request().postDataJSON().action==='deadline');
  await page.getByRole('button',{name:'Frist speichern',exact:true}).click();
  const confirmedResponse=await confirmed;must(confirmedResponse.status()===200,'Actual UI confirmation creates the deadline');
  fixture.deadline=(await confirmedResponse.json()).deadline_id;await ready();
  const initial=await settings(fixture.deadline),consentBefore=await consent();
  must(initial.version===0&&initial.responsible_member_id===null&&!initial.enabled,'New deadline is unassigned and notifications are off');
  must((await row(fixture.deadline).locator('[data-deadline-notification-summary]').innerText()).includes('Keine Zuständigkeit'),'Deadline row exposes its unassigned state');
  await edit(fixture.deadline);
  must(await editor().locator('[name=notification_responsible_member_id]').inputValue()===''&&!await editor().locator('[name=notification_enabled]').isChecked(),'UI does not infer confirmation author or opt in automatically');
  await editor().locator('[name=notification_enabled]').check();await save(422);
  must((await settings(fixture.deadline)).version===0,'Missing-recipient rejection creates no notification sidecar');
  must(await editor().locator('[name=notification_enabled]').isChecked(),'Rejected save preserves entered permission');
  await editor().locator('[name=notification_responsible_member_id]').selectOption(fixture.member);await save();
  let current=await settings(fixture.deadline);
  must(current.version===1&&current.enabled&&current.responsible_member_id===fixture.member,'Actual assignment and permission are persisted');
  must(current.document_version===initial.document_version&&current.deadline_version===initial.deadline_version,'Notification save does not change document or deadline revisions');
  must(JSON.stringify(await consent())===JSON.stringify(consentBefore),'Deadline configuration leaves account and device consent unchanged');
  await go(fixture.document);await edit(fixture.deadline);
  must(await editor().locator('[name=notification_responsible_member_id]').inputValue()===fixture.member&&await editor().locator('[name=notification_enabled]').isChecked(),'Actual reload restores saved settings');
  const competing=await direct(current,{enabled:false});must(competing.status===200,'Independent actual update creates a competing revision');
  await save(409);must(await editor().locator('[name=notification_enabled]').isChecked(),'Conflict preserves unsaved configuration');
  current=await settings(fixture.deadline);must(current.version===2&&!current.enabled,'Stale save cannot overwrite newer settings');
  await discard(fixture.deadline);await edit(fixture.deadline);
  must(!await editor().locator('[name=notification_enabled]').isChecked(),'Reopen uses current settings revision');
  await editor().locator('[name=notification_enabled]').check();await discard(fixture.deadline);
  must(!(await settings(fixture.deadline)).enabled,'Discard never persists changed permission');
  // A separate document metadata draft must survive notification save/discard.
  await page.locator('#doc-content [name=notes]').fill('Unsaved metadata remains separate');
  await edit(fixture.deadline);await editor().locator('[name=notification_enabled]').check();await discard(fixture.deadline);
  must(await page.locator('#doc-content [name=notes]').inputValue()==='Unsaved metadata remains separate','Discard preserves unrelated unsaved metadata');
  await go(fixture.document);
  for(const language of ['de','en']){
   await locale(language);await edit(fixture.deadline);
   must(await editor().getByRole('combobox',{name:language==='en'?'Responsible person':'Zuständige Person',exact:true}).count()===1,'Localized responsible-person label '+language);
   must(await editor().getByRole('link',{name:language==='en'?'Enable account and devices':'Konto und Geräte freigeben',exact:true}).getAttribute('href')==='/settings/notifications','Explicit compiled consent link '+language);
   for(const width of [1440,320])for(const mode of ['light','dark','pink-light','pink-dark']){
    await page.setViewportSize({width,height:width===320?900:1000});await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);
    await capture(language+'-'+mode+'-'+width);
   }
   await discard(fixture.deadline);
  }
  must(JSON.stringify(await consent())===JSON.stringify(consentBefore),'Conflict, discard, language and theme flows preserve actual consent');
  must(permissionRequests===0,'No implicit Notification permission request in any visited document');
  report.boundaries={authority:'Reader and cross-household cases require separate actual-session integration evidence.',delivery:'No provider delivery, worker scheduling, physical display or OS audio claim; service workers blocked.'};
  must(!report.errors.length,'No browser runtime errors');must(!report.externalRequests.length,'No external browser requests');report.passed=true;
 }catch(error){report.failure=redact(error);throw error;}finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(redact(error));process.exitCode=1;});
