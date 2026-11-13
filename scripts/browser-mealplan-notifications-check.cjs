'use strict';

// Actual isolated preparation configuration. Provider transport and physical device
// display are deliberately outside this browser driver's acceptance scope.
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');
const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Mealplan notification browser requires owned localhost:8101');
const output=path.resolve(workspace,process.env.HOUSEHOLD_MEALPLAN_NOTIFICATIONS_BROWSER_OUTPUT||'outputs/notifications-mealplan/browser-notifications');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Mealplan evidence must stay inside workspace outputs');
const report={passed:false,checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],captureDiagnostics:[],physicalDeliveryTested:false};
const secrets=[];let browser,page,watchdog,permissionRequests=0;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(value,message)=>{if(!value)throw Error(message);report.checks.push(message);};

async function fixtureState(){return readFixtureState(workspace,secrets);}
async function read(route){
 return page.evaluate(async route=>{const r=await fetch(route,{cache:'no-store'});if(!r.ok)throw Error('Actual mealplan read failed: '+r.status);return r.json();},route);
}
let fixture;
const editor=()=>page.locator('#planning-notifications');
const endpoint=()=>'/api/v1/planning/'+fixture.plan+'/meals/'+fixture.meal+'/notifications';
const settings=()=>read(endpoint());
async function ready(){await page.locator('#planning-days > section').nth(6).waitFor();}
async function go(){await page.goto(origin.origin+'/m/mealplan/workspace/'+fixture.plan+'?week='+fixture.day+'&meal='+fixture.meal);await ready();await editor().waitFor();}
async function edit(){await editor().getByRole('button',{name:/^(Zubereitungshinweise einrichten|Set up preparation notifications)$/}).click();await editor().locator('form').waitFor();await page.waitForFunction(()=>document.querySelector('#planning-notifications').getAttribute('aria-busy')==='false');}
async function save(expected=200){const response=page.waitForResponse(r=>new URL(r.url()).pathname===endpoint()&&r.request().method()==='POST');await editor().locator('button.primary').click();const r=await response;must(r.status()===expected,'Actual preparation save returns '+expected);await page.waitForFunction(()=>document.querySelector('#planning-notifications').getAttribute('aria-busy')==='false');return r.json();}
async function discard(){await editor().getByRole('button',{name:/^(Eingaben verwerfen|Discard entries)$/}).click();if(await page.locator('#planning-confirm').isVisible())await page.locator('#planning-confirm-yes').click();await editor().locator('form').waitFor({state:'hidden'});must(await editor().getByRole('button',{name:/^(Zubereitungshinweise einrichten|Set up preparation notifications)$/}).evaluate(el=>el===document.activeElement),'Discard restores the exact preparation action focus');}
async function consent(){return page.evaluate(async()=>{const a=await fetch('/api/v1/core/notification-preferences',{cache:'no-store'}),d=await fetch('/api/v1/core/push/device-preferences',{cache:'no-store'});if(!a.ok||!d.ok)throw Error('Consent reads failed');return {account:await a.json(),devices:await d.json()};});}
async function direct(data,changes={}){return page.evaluate(async({url,data,changes})=>{const r=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name=csrf-token]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation:data.generation,plan_version:data.plan_version,meal_version:data.meal_version,settings_version:data.settings.version,timezone:data.timezone,responsible_member_id:data.settings.responsible_member_id||'',enabled:data.settings.enabled,confirmed:true,steps:data.settings.steps.map(({id,kind,label,local_date,local_time})=>({id,kind,label,local_date,local_time})),...changes})});return {status:r.status,body:await r.json()};},{url:endpoint(),data,changes});}
async function changeMeal(changes){return page.evaluate(async({fixture,changes})=>{const boot=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),week=await fetch('/api/v1/planning/'+fixture.plan+'?week='+fixture.day).then(r=>r.json()),meal=week.meals.find(m=>m.id===fixture.meal);const r=await fetch('/api/v1/planning/'+fixture.plan,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation:boot.states.mealplan.generation,plan_version:week.plan.version,action:'save',meal_id:meal.id,version:meal.version,meal:{title:meal.title,planned_on:meal.planned_on,slot:meal.meal_slot,kind:meal.kind,status:meal.status,servings:meal.servings,notes:meal.notes,...changes},components:[]})});return r.status;},{fixture,changes});}
async function capture(name){
 const diagnostic={name,phase:'settling',settling:null};report.captureDiagnostics.push(diagnostic);
 const settling=await page.evaluate(async()=>{
  const result={fonts:{status:'waiting'},motion:{status:'not-started'}};
  const safeToken=value=>typeof value==='string'&&/^[A-Za-z][A-Za-z0-9_-]{0,79}$/.test(value)&&!/[a-f0-9]{24,}/i.test(value)&&!/[0-9]{6,}/.test(value)?value:null;
  const metadata=animation=>{
   const timing=animation.effect?.getComputedTiming(),target=animation.effect?.target;
   const numeric=value=>typeof value==='number'&&Number.isFinite(value)?value:null;
   return {playState:animation.playState,pending:animation.pending,playbackRate:numeric(animation.playbackRate),currentTime:numeric(animation.currentTime),endTime:numeric(timing?.endTime),duration:numeric(timing?.duration),iterations:numeric(timing?.iterations),target:target instanceof Element?{tag:target.tagName.toLowerCase(),id:safeToken(target.id),classes:[...target.classList].slice(0,8).map(safeToken).filter(Boolean)}:null};
  };
  const bounded=async promise=>{let timer;try{return await Promise.race([promise.then(()=>true),new Promise(resolve=>{timer=setTimeout(()=>resolve(false),5000);})]);}finally{clearTimeout(timer);}};
  const fontStart=performance.now(),fontsReady=await bounded(document.fonts.ready);
  result.fonts={status:fontsReady?'settled':'timeout',documentStatus:document.fonts.status,waitMs:Math.round(performance.now()-fontStart)};
  if(!fontsReady){result.failure='Font readiness exceeded five seconds';return result;}
  const isActive=animation=>animation.playState==='running'&&animation.playbackRate>0&&Number.isFinite(animation.effect?.getComputedTiming().endTime);
  const animations=document.getAnimations(),active=animations.filter(isActive);
  result.motion={status:'waiting',method:'observed-state-poll',pollIntervalMs:50,polls:0,observed:animations.length,active:active.length,ignored:animations.length-active.length,before:animations.slice(0,32).map(metadata),metadataOmitted:Math.max(0,animations.length-32),finished:0,cancelled:0};
  const motionStart=performance.now(),motionDeadline=motionStart+5000;
  let current=animations,remaining=active;
  // Some browser transitions expose finished state without resolving .finished.
  // Re-read actual state, including animations started during settling, instead.
  while(remaining.length&&performance.now()<motionDeadline){
   await new Promise(resolve=>setTimeout(resolve,Math.min(50,Math.max(0,motionDeadline-performance.now()))));
   current=document.getAnimations();remaining=current.filter(isActive);result.motion.polls++;
  }
  const settled=remaining.length===0;
  result.motion.status=settled?'settled':'timeout';result.motion.waitMs=Math.round(performance.now()-motionStart);result.motion.after=active.slice(0,32).map(metadata);
  result.motion.finished=active.filter(animation=>animation.playState==='finished').length;result.motion.cancelled=active.filter(animation=>animation.playState==='idle').length;
  result.motion.currentObserved=current.length;result.motion.remainingActive=remaining.length;result.motion.remaining=remaining.slice(0,32).map(metadata);
  if(!settled)result.failure='Running finite forward animations did not settle within five seconds';
  return result;
 });
 diagnostic.settling=settling;
 if(settling.failure){diagnostic.phase='settling-failed';throw Error(name+': '+settling.failure);}
 diagnostic.phase='accessibility';
 const axe=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:axe.violations.map(v=>({id:v.id,impact:v.impact,targets:v.nodes.map(n=>n.target),details:v.nodes.map(n=>n.failureSummary)}))});must(!axe.violations.length,'Clean Axe '+name);
 diagnostic.phase='layout-and-keyboard';
 must(await page.evaluate(()=>{const e=document.querySelector('#planning-notifications');return document.documentElement.scrollWidth<=innerWidth+1&&e.scrollWidth<=e.clientWidth+1;}),'Preparation controls fit '+name);
 const person=editor().locator('[name=responsible_member_id]');await person.focus();must(await person.evaluate(n=>n===document.activeElement),'Recipient supports keyboard focus '+name);await page.keyboard.press('Tab');must(await editor().locator('[name=enabled]').evaluate(n=>n===document.activeElement),'Keyboard advances to explicit enablement '+name);
 await editor().scrollIntoViewIfNeeded();for(const fullPage of [true,false]){diagnostic.phase=fullPage?'full-page-capture':'viewport-capture';const file=name+(fullPage?'':'-viewport')+'.png';await page.screenshot({path:path.join(output,file),fullPage,animations:'disabled'});report.screenshots.push(file);}
 diagnostic.phase='complete';
}
(async()=>{
 const state=await fixtureState();await fs.mkdir(output,{recursive:true});browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:process.platform==='win32'?{channel:'msedge'}:{})});
 watchdog=setTimeout(()=>{report.errors.push('Eight-minute preparation browser deadline exceeded');void browser.close();},480000);watchdog.unref();
 try{
  const context=await browser.newContext({storageState:state,viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(new URL(route.request().url()).origin);return route.abort();}return route.continue();});
  await context.exposeBinding('__recordPreparationPermission',()=>{permissionRequests++;});await context.addInitScript(()=>{if(window.Notification)Notification.requestPermission=async()=>{await window.__recordPreparationPermission();return 'default';};});
  page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',e=>report.errors.push(redact(e)));page.on('dialog',d=>d.type()==='beforeunload'?void d.accept():void d.dismiss());await page.goto(origin.origin+'/settings');
  fixture=await page.evaluate(async()=>{
   let boot=await fetch('/api/v1/core/bootstrap').then(r=>r.json());if(boot.user.role!=='admin')throw Error('Actual fixture administrator required');
   if(boot.states.mealplan.state!=='enabled'){const r=await fetch('/settings/modules/mealplan',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.mealplan.version)})});if(!r.ok)throw Error('Mealplan activation failed');boot=await fetch('/api/v1/core/bootstrap').then(r=>r.json());}
   async function post(url,data){const r=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(data)});if(!r.ok)throw Error('Fixture save '+r.status);return r.json();}
   const day=new Date(Date.now()+7*86400000).toISOString().slice(0,10),plan=await post('/api/v1/m/mealplan/plans',{generation:boot.states.mealplan.generation,data:{name:'Synthetic explicit preparation '+Date.now(),starts_on:day,ends_on:day}});
   const meal=await post('/api/v1/planning/'+plan.id,{generation:boot.states.mealplan.generation,plan_version:plan.version,action:'save',meal:{title:'Synthetic confirmed preparation',planned_on:day,slot:'dinner',kind:'free',status:'planned',servings:'2',notes:''},components:[]});return {plan:plan.id,meal:meal.meal_id,day,member:boot.user.membership_id};
  });
  await go();if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await ready();await editor().waitFor();}
  const initial=await settings(),before=await consent();must(initial.settings.version===0&&!initial.settings.enabled&&initial.settings.responsible_member_id===null&&initial.settings.steps.length===0,'New saved meal has no inferred recipient, permission or times');
  await edit();must(await editor().locator('[name=responsible_member_id]').inputValue()===''&&!await editor().locator('[name=enabled]').isChecked(),'Editor starts unassigned and disabled');must(await editor().locator('[data-reminder-step]').count()===0,'No recipe or meal-slot time was guessed');
  await editor().locator('[name=enabled]').check();await save(422);must((await settings()).settings.version===0,'Missing explicit steps and recipient create no sidecar');
  await editor().getByRole('button',{name:'Termin ergänzen',exact:true}).click();const row=editor().locator('[data-reminder-step]').first();must(await row.locator('[name=local_date]').inputValue()===''&&await row.locator('[name=local_time]').inputValue()==='','New step date and time are explicitly empty');
  await row.locator('[name=kind]').selectOption('start');await row.locator('[name=label]').fill('Synthetic start only');await row.locator('[name=local_date]').fill(fixture.day);await row.locator('[name=local_time]').fill('17:30');await save(422);must(await row.locator('[name=label]').inputValue()==='Synthetic start only','Missing-recipient rejection preserves step draft');
  await editor().locator('[name=responsible_member_id]').selectOption(fixture.member);await page.locator('#planning-meal-form [name=notes]').fill('Independent meal draft');await save();
  let current=await settings();must(current.settings.enabled&&current.settings.steps.length===1&&current.settings.responsible_member_id===fixture.member,'Real explicit assignment and confirmed start persist');must(current.plan_version===initial.plan_version&&current.meal_version===initial.meal_version,'Sidecar save preserves plan and meal revisions');must(await page.locator('#planning-meal-form [name=notes]').inputValue()==='Independent meal draft','Notification save preserves independent meal draft');must(JSON.stringify(await consent())===JSON.stringify(before),'Assignment leaves account and device consent untouched');
  await go();await edit();must(await row.locator('[name=local_time]').inputValue()==='17:30','Reload restores explicitly saved local time');must(await row.locator('[name=label]').inputValue()==='Synthetic start only','Reload restores explicit step description');
  const competing=await direct(current,{enabled:false});must(competing.status===200,'Actual competing settings update accepted');await save(409);must(await editor().locator('[name=enabled]').isChecked(),'409 preserves original enabled draft');must(!(await settings()).settings.enabled,'Stale draft does not overwrite competing settings');await discard();await edit();must(!await editor().locator('[name=enabled]').isChecked(),'Explicit discard and reopen use current revision');
  await editor().locator('[name=enabled]').check();let originalKey,originalBody;const pattern='**'+endpoint();await page.route(pattern,async route=>{if(route.request().method()!=='POST')return route.continue();originalKey=route.request().headers()['idempotency-key'];originalBody=route.request().postData();const actual=await route.fetch();if(!actual.ok())throw Error('Real underlying save failed');await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Synthetic lost response after actual save'})});});
  await save(503);must(await editor().locator('[name=responsible_member_id]').isDisabled(),'Uncertain response locks edited authority');must(await editor().getByRole('button',{name:'Eingaben verwerfen',exact:true}).isDisabled(),'Uncertain accepted write cannot be discarded');await page.unroute(pattern);const repeated=page.waitForRequest(r=>new URL(r.url()).pathname===endpoint()&&r.method()==='POST');await editor().getByRole('button',{name:'Unklare Speicherung erneut prüfen',exact:true}).click();const repeat=await repeated;await editor().locator('form').waitFor({state:'hidden'});must(repeat.headers()['idempotency-key']===originalKey&&repeat.postData()===originalBody,'Response-loss recovery retains exact step IDs, body and idempotency key');current=await settings();must(current.settings.enabled&&current.settings.version===3,'Retry commits exactly once');
  // Actual meal edits invalidate confirmed reminders without sending or shifting them.
  await page.locator('#planning-meal-form [name=notes]').fill('Actual meal source changed');const changed=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/planning/'+fixture.plan&&r.request().method()==='POST');await page.locator('#planning-save').click();must((await changed).status()===200,'Actual meal edit accepted');await page.locator('#planning-editor').waitFor({state:'hidden'});await page.locator('[data-edit="'+fixture.meal+'"]').click();await edit();must((await settings()).needs_confirmation,'Actual meal revision requires explicit confirmation');must((await editor().innerText()).includes('erneut prüfen'),'UI explains source reconfirmation');await save();must(!(await settings()).needs_confirmation,'Explicit save confirms current source version');
  await page.locator('#planning-meal-form [name=notes]').fill('Preserve draft across stale notification read');must(await changeMeal({notes:'Newer actual server source'})===200,'Actual concurrent meal source update accepted');
  await editor().getByRole('button',{name:'Zubereitungshinweise einrichten',exact:true}).click();await editor().getByRole('alert').waitFor();must(await editor().locator('form').isHidden(),'Fresh GET cannot silently confirm a source newer than displayed meal');must(await page.locator('#planning-meal-form [name=notes]').inputValue()==='Preserve draft across stale notification read','Stale source refusal preserves independent unsaved meal draft');must(await page.locator('#planning-refresh').isVisible(),'Stale source refusal exposes the existing saved-plan review action');
  await go();await edit();must((await settings()).needs_confirmation&&!await editor().locator('button.primary').isDisabled(),'Current rendered meal can explicitly reconfirm older settings');await save();
  must(await changeMeal({status:'cancelled'})===200,'Actual meal cancellation accepted');await go();await edit();must(await editor().locator('button.primary').isDisabled()&&await editor().locator('[name=enabled]').isDisabled()&&await editor().locator('[name=responsible_member_id]').isDisabled(),'Ineligible cancelled meal displays settings with meaningful writes disabled');must(!(await settings()).eligible,'Actual cancelled meal is ineligible');await discard();must(await changeMeal({status:'planned'})===200,'Explicit meal reopening accepted');await go();await edit();must((await settings()).needs_confirmation,'Reopening does not silently restore confirmed notification authority');await save();
  for(const language of ['de','en']){
   if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value='+language+']').click();await ready();await editor().waitFor();}
   await edit();must(await editor().getByRole('combobox',{name:language==='de'?'Zuständig für diese Hinweise':'Responsible for these notifications',exact:true}).count()===1,'Recipient control translated '+language);must(await editor().getByRole('link').getAttribute('href')==='/settings/notifications','Separate account/device consent link '+language);
   for(const width of [1440,320])for(const mode of ['light','dark','pink-light','pink-dark']){await page.setViewportSize({width,height:width===320?900:1000});await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);await capture(language+'-'+mode+'-'+width);}
   await discard();
  }
  must(JSON.stringify(await consent())===JSON.stringify(before),'All editor, retry, conflict, locale and theme flows preserve account/device consent');must(permissionRequests===0,'No implicit Notification permission prompt');must(!report.errors.length,'No browser runtime errors');must(!report.externalRequests.length,'No external requests');
  report.boundaries={delivery:'No provider transport, worker scheduling, physical device display or audio claim.',authority:'Reader, foreign-household, lifecycle and server-clock edge cases require actual-session native integration acceptance.'};report.passed=true;
 }catch(e){report.failure=redact(e);throw e;}finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(e=>{console.error(redact(e));process.exitCode=1;});
