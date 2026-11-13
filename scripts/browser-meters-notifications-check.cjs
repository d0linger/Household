'use strict';
// Real reviewed assignments on the explicitly owned fixture; no provider or
// device delivery is simulated or claimed by this browser acceptance driver.
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');
const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Meter notification browser requires the owned localhost:8101 fixture');
const output=path.resolve(workspace,process.env.HOUSEHOLD_METERS_NOTIFICATIONS_BROWSER_OUTPUT||'outputs/notifications-meters/browser-notifications');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Meter notification evidence must remain inside workspace outputs');
const report={passed:false,checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],physicalDeliveryTested:false};
const secrets=[];let browser,page,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(value,label)=>{if(!value)throw Error(label);report.checks.push(label);};
const api='/api/v1/meters/';
async function fixtureState(){return readFixtureState(workspace,secrets,{pinned:false});}
async function read(route){return page.evaluate(async route=>{const response=await fetch(route,{cache:'no-store'});if(!response.ok)throw Error('Actual meter read failed: '+response.status);return response.json();},route);}
async function ready({error=false}={}){
 await page.waitForFunction(()=>document.querySelector('#meters-workspace')?.getAttribute('aria-busy')==='false');
 if(!error)must(await page.locator('#energy-error').isHidden(),'Current authorized meter workspace loads');
}
async function go(register){
 const response=await page.goto(origin.origin+'/m/meters/workspace/settings?register='+register);
 must(response.status()===200,'Actual selected-register settings route succeeds');await ready();await page.locator('#meter-notifications').waitFor();
}
async function edit(){
 await page.locator('#meter-notifications').getByRole('button',{name:/^(Zählerhinweise einrichten|Set up meter alerts)$/,exact:true}).click();
 await page.locator('#energy-editor [name=responsible_member_id]').waitFor();await ready();
}
async function submit(expected=200){
 const response=page.waitForResponse(r=>new URL(r.url()).pathname===api+'commands'&&r.request().method()==='POST');
 await page.locator('#energy-editor').getByRole('button',{name:/^(Benachrichtigungen speichern|Save notifications)$/,exact:true}).click();
 const result=await response;must(result.status()===expected,'Real notification command returns '+expected);await ready({error:expected!==200});
}
async function discard(){
 await page.locator('#energy-editor').getByRole('button',{name:/^(Eingaben verwerfen|Discard entries)$/,exact:true}).click();await ready();
 must(await page.locator('#energy-editor form').count()===0,'Explicit discard removes the notification draft');
}
async function saveDirect(register,settings,changes={}){
 return page.evaluate(async({api,register,settings,changes})=>{
  const bootstrap=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
  const data={register_id:register,register_version:String(settings.register_version),responsible_member_id:settings.responsible_member_id||'',
   reading_enabled:settings.reading_enabled,anomaly_enabled:settings.anomaly_enabled,stale_enabled:settings.stale_enabled,
   stale_after_seconds:String(settings.stale_after_seconds),...changes};
  const response=await fetch(api+'commands',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':bootstrap.csrf,'Idempotency-Key':crypto.randomUUID()},
   body:JSON.stringify({generation:bootstrap.states.meters.generation,version:settings.version,action:'notification.save',id:settings.version?register:'',data})});
  return {status:response.status,body:await response.json()};
 },{api,register,settings,changes});
}
async function consentSnapshot(){
 return page.evaluate(async()=>{
  const preference=await fetch('/api/v1/core/notification-preferences',{cache:'no-store'});
  const devices=await fetch('/api/v1/core/push/device-preferences',{cache:'no-store'});
  if(!preference.ok||!devices.ok)throw Error('Actual account/device permission snapshot unavailable');
  return {preferences:(await preference.json()).preferences,devices:await devices.json()};
 });
}
async function capture(name){
 // Theme ink changes immediately, while shared controls animate their surfaces.
 // Inspect the completed palette, as the central settings driver does.
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().filter(a=>Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a=>a.finished.catch(()=>{})));});
 const scan=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
 report.accessibility.push({name,violations:scan.violations.map(v=>({id:v.id,impact:v.impact,targets:v.nodes.map(n=>n.target),details:v.nodes.map(n=>n.failureSummary)}))});must(scan.violations.length===0,'Axe '+name);
 must(await page.evaluate(()=>{const form=document.querySelector('#energy-editor form');return document.documentElement.scrollWidth<=innerWidth+1&&form.scrollWidth<=form.clientWidth+1;}),'Page and editor fit viewport '+name);
 const field=page.locator('#energy-editor [name=responsible_member_id]');await field.focus();
 must(await field.evaluate(el=>el===document.activeElement),'Responsibility selection is keyboard reachable '+name);
 await page.keyboard.press('Tab');must(await page.locator('#energy-editor [name=reading_enabled]').evaluate(el=>el===document.activeElement),'Keyboard follows responsibility to reading consent '+name);
 await page.locator('#energy-editor').scrollIntoViewIfNeeded();
 await page.screenshot({path:path.join(output,name+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(name+'.png');
 await page.screenshot({path:path.join(output,name+'-viewport.png'),animations:'disabled'});report.screenshots.push(name+'-viewport.png');
}
(async()=>{
 const storageState=await fixtureState();await fs.mkdir(output,{recursive:true});
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 watchdog=setTimeout(()=>{report.errors.push('Eight-minute meter browser deadline exceeded');void browser.close();},480000);watchdog.unref();
 try{
  const context=await browser.newContext({storageState,viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(new URL(route.request().url()).origin);return route.abort();}return route.continue();});
  await context.addInitScript(()=>{window.__notificationPermissionCalls=0;if(window.Notification)Notification.requestPermission=()=>{window.__notificationPermissionCalls++;return Promise.resolve('default');};});
  page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error)));
  page.on('dialog',dialog=>{if(dialog.type()==='beforeunload')void dialog.accept();else void dialog.dismiss();});
  await page.goto(origin.origin+'/settings');
  const fixture=await page.evaluate(async()=>{
   let boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   if(boot.user.role!=='admin')throw Error('Meter acceptance requires actual fixture administrator');
   if(boot.states.meters.state!=='enabled'){
    const r=await fetch('/settings/modules/meters',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.meters.version)})});
    if(!r.ok)throw Error('Actual isolated meter enable failed');
    boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   }
   if(boot.states.meters.state!=='enabled')throw Error('Meter enable not committed');
   const name='Meter notification synthetic '+Date.now();
   const response=await fetch('/api/v1/meters/commands',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},
    body:JSON.stringify({generation:boot.states.meters.generation,version:0,action:'meter.save',id:'',data:{name,unit_code:'kWh',location_note:'Synthetic acceptance only'}})});
   if(!response.ok)throw Error('Actual synthetic meter command failed: '+response.status);
   const meter=await response.json(),overview=await fetch('/api/v1/meters/overview',{cache:'no-store'}).then(r=>r.json());
   const register=overview.registers.find(r=>r.meter_id===meter.id&&r.is_default);
   if(!register)throw Error('Actual new meter default register missing');
   return {register:register.id,member:boot.user.membership_id};
  });
  await go(fixture.register);
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
  const initial=(await read(api+'notification-settings?register='+fixture.register)).settings;
  must(initial.version===0&&initial.responsible_member_id===null&&!initial.reading_enabled&&!initial.anomaly_enabled&&!initial.stale_enabled,'New register notification configuration starts unassigned and entirely off');
  const consentBefore=await consentSnapshot();
  await edit();const form=page.locator('#energy-editor form');
  must(await form.locator('[name=responsible_member_id]').inputValue()==='','Initial UI does not infer the creator as responsible');
  for(const field of ['reading_enabled','anomaly_enabled','stale_enabled'])must(!await form.locator('[name='+field+']').isChecked(),'Initial UI leaves '+field+' off');
  await form.locator('[name=reading_enabled]').check();await submit(422);
  must((await page.locator('#energy-error-text').innerText()).includes('Zuständige Person'),'Unassigned enabled warning explains the missing person');
  must((await read(api+'notification-settings?register='+fixture.register)).settings.version===0,'Rejected unassigned configuration creates no sidecar');
  must(await form.locator('[name=reading_enabled]').isChecked(),'Rejected save retains the actual draft for correction');
  await form.locator('[name=responsible_member_id]').selectOption(fixture.member);
  await form.locator('[name=anomaly_enabled]').check();await form.locator('[name=stale_enabled]').check();
  await form.locator('[name=stale_after_seconds]').fill('900');await submit();
  let current=(await read(api+'notification-settings?register='+fixture.register)).settings;
  must(current.version===1&&current.responsible_member_id===fixture.member&&current.reading_enabled&&current.anomaly_enabled&&current.stale_enabled&&current.stale_after_seconds===900,'Explicit UI configuration persists each independent channel and responsibility');
  must(current.register_version===initial.register_version,'Notification sidecar does not rewrite the register version');
  must(JSON.stringify(await consentSnapshot())===JSON.stringify(consentBefore),'Register configuration changes no account or device consent');
  await page.reload();await ready();await edit();
  must(await form.locator('[name=responsible_member_id]').inputValue()===fixture.member&&await form.locator('[name=stale_after_seconds]').inputValue()==='900','Actual reload restores saved responsibility and staleness');
  const competing=await saveDirect(fixture.register,current,{stale_after_seconds:'1200'});must(competing.status===200,'Independent real update establishes a newer settings revision');
  await form.locator('[name=stale_after_seconds]').fill('1800');await submit(409);
  must(await page.locator('#energy-error').isVisible()&&await form.locator('[name=stale_after_seconds]').inputValue()==='1800','Stale editor receives a visible conflict and preserves the unsaved value');
  current=(await read(api+'notification-settings?register='+fixture.register)).settings;
  must(current.version===2&&current.stale_after_seconds===1200,'Stale write cannot overwrite the newer stored configuration');
  await discard();await edit();must(await form.locator('[name=stale_after_seconds]').inputValue()==='1200','Explicit discard and reopen use the current revision');
  // A draft may be abandoned without saving or requesting permission.
  await form.locator('[name=stale_enabled]').uncheck();await discard();
  must((await read(api+'notification-settings?register='+fixture.register)).settings.stale_enabled,'Discard does not persist an unchecked channel');
  for(const language of ['de','en']){
   if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value='+language+']').click();await page.waitForFunction(lang=>document.documentElement.dataset.locale===lang,language);await ready();}
   await edit();
   must(await form.getByRole('combobox',{name:language==='en'?'Responsible person':'Zuständige Person',exact:true}).count()===1,'Responsibility has a localized accessible label '+language);
   must(await page.locator('#meter-notifications').getByRole('link',{name:language==='en'?'Enable account and devices':'Konto und Geräte freigeben',exact:true}).getAttribute('href')==='/settings/notifications','Explicit central consent link is localized and compiled '+language);
   for(const width of [1440,320])for(const mode of ['light','dark','pink-light','pink-dark']){
    await page.setViewportSize({width,height:width===320?900:1000});await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);
    await capture(language+'-'+mode+'-'+width);
   }
   await discard();
  }
  must(JSON.stringify(await consentSnapshot())===JSON.stringify(consentBefore),'Language, theme, conflict and discard flows preserve actual notification permissions');
  must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'Meter controls never implicitly request notification permission');
  report.boundaries={readerAuthority:'Not exercised by this browser driver; requires separate real-session HTTP integration evidence.',
   delivery:'No provider delivery, worker scheduling, physical display or OS audio claim; service workers blocked during UI checks.'};
  must(!report.errors.length,'No JavaScript errors');must(!report.externalRequests.length,'No external browser requests');report.passed=true;
 }catch(error){report.failure=redact(error);throw error;}finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(redact(error));process.exitCode=1;});
