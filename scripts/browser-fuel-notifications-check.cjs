'use strict';
// Real reviewed assignments on the explicitly owned fixture; no provider or
// device delivery is simulated or claimed by this browser acceptance driver.
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');
const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Fuel notification browser requires the owned localhost:8101 fixture');
const output=path.resolve(workspace,process.env.HOUSEHOLD_FUEL_NOTIFICATIONS_BROWSER_OUTPUT||'outputs/notifications-device-channel/fuel/browser-assignments');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Fuel notification evidence must remain inside workspace outputs');
const report={passed:false,checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],physicalDeliveryTested:false};
const secrets=[];let browser,page,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(value,label)=>{if(!value)throw Error(label);report.checks.push(label);};
const api='/api/v1/fuel/module';
async function fixtureState(){return readFixtureState(workspace,secrets,{pinned:false});}
async function read(route){return page.evaluate(async route=>{const r=await fetch(route,{cache:'no-store'});if(!r.ok)throw Error('Fixture API read failed: '+r.status);return r.json();},route);}
async function ready(){await page.waitForFunction(()=>document.querySelector('#fm-summary')?.getAttribute('aria-busy')==='false'||document.querySelector('#fm-error')?.hidden===false);must(await page.locator('#fm-error').isHidden(),'Current Fuel workspace loads without error');}
async function go(pane,vehicle='',entity='',record=''){
 const query=new URLSearchParams();if(vehicle)query.set('vehicle',vehicle);if(entity)query.set('entity',entity);if(record)query.set('record',record);
 await page.goto(origin.origin+'/m/fuel/workspace/'+pane+(query.size?'?'+query:''));await ready();
}
async function open(action){const form=page.locator('form[data-action="'+action+'"]');await form.waitFor({state:'attached'});const details=form.locator('..');if(await details.getAttribute('open')===null){await details.locator(':scope > summary').focus();await page.keyboard.press('Enter');}return form;}
async function edit(pane,vehicle,entity,id,action){
 await go(pane,vehicle,entity,id);await page.locator('#fm-deep-record').waitFor({state:'visible'});
 await page.locator('#fm-deep-record [data-row-action="'+action+'"]').click();
 const form=await open(action);await page.waitForFunction(({action,id})=>document.querySelector('form[data-action="'+action+'"]')?._row?.id===id,{action,id});return form;
}
async function commit(action,field,expected){
 const form=await open(action),previewResponse=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/preview'&&r.request().method()==='POST');
 await form.locator('button[type=submit]').focus();await page.keyboard.press('Enter');const preview=await previewResponse;must(preview.status()===200,'Real reviewed '+action+' preview succeeds');
 const proposed=await preview.json();must(proposed.command.data[field]===expected||expected===null&&proposed.command.data[field]==='',action+' preview retains explicit '+field+' choice');
 await page.locator('#fm-review').waitFor({state:'visible'});must(await page.locator('#fm-review-ack').isHidden(),'Assignment review requires no unrelated unacknowledged warnings');
 const acceptedResponse=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/execute'&&r.request().method()==='POST');
 await page.locator('#fm-review-confirm').click();const accepted=await acceptedResponse;must(accepted.status()===200,'Reviewed '+action+' execution succeeds');
 const result=await accepted.json();must(Boolean(result.operation_id)&&Boolean(result.record_id),'Reviewed assignment has durable operation and record identity');
 await page.locator('#fm-receipt').waitFor({state:'visible'});await page.locator('#fm-new-operation').click();await page.locator('#fm-receipt').waitFor({state:'hidden'});await ready();return result.record_id;
}
async function record(entity,id){return(await read(api+'/records/'+entity+'/'+id)).record;}
async function capture(name){
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().filter(a=>Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a=>a.finished.catch(()=>{})));});
 const axe=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:axe.violations});must(!axe.violations.length,'Axe '+name);
 must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'No root overflow '+name);
 await page.screenshot({path:path.join(output,name+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(name+'.png');
}
(async()=>{
 const storageState=await fixtureState();await fs.mkdir(output,{recursive:true});
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 watchdog=setTimeout(()=>{report.errors.push('Eight-minute fixture browser deadline exceeded');void browser.close();},480000);watchdog.unref();
 try{
  const context=await browser.newContext({storageState,viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(new URL(route.request().url()).origin);return route.abort();}return route.continue();});
  await context.addInitScript(()=>{window.__notificationPermissionCalls=0;if(window.Notification)Notification.requestPermission=()=>{window.__notificationPermissionCalls++;return Promise.resolve('default');};});
  page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error)));
  await go('overview');if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
  const overview=await read(api+'?pane=overview');must(overview.context.can_write===true,'Fixture has actual write authority');
  const member=overview.options.assignees[0];must(Boolean(member?.id)&&Boolean(member?.name),'A current named household member is available');
  const name='Notification responsibility '+Date.now(),vehicleForm=await open('vehicle.save');
  must(await vehicleForm.locator('[name=responsible_member_id]').inputValue()==='','New vehicle responsibility starts explicitly unassigned');
  await vehicleForm.locator('[name=name]').fill(name);await vehicleForm.locator('[name=fuel_type]').selectOption('diesel');
  await vehicleForm.locator('[name=responsible_member_id]').selectOption(member.id);
  must((await vehicleForm.locator('[name=responsible_member_id] option:checked').innerText())===member.name,'Responsibility selector displays the actual member name');
  const vehicle=await commit('vehicle.save','responsible_member_id',member.id),assigned=await record('vehicles',vehicle);
  must(assigned.responsible_member_id===member.id&&Number.isFinite(Date.parse(assigned.responsible_since)),'Vehicle responsibility and server-owned assignment time persist');
  let form=await edit('overview',vehicle,'vehicles',vehicle,'vehicle.save');must(await form.locator('[name=responsible_member_id]').inputValue()===member.id,'Vehicle edit reloads saved responsibility');
  must(await form.locator('[name=responsible_since]').count()===0,'Assignment timestamp is not client-editable');
  await form.locator('[name=notes]').fill('Unrelated reviewed edit preserves responsibility time');await commit('vehicle.save','responsible_member_id',member.id);
  must((await record('vehicles',vehicle)).responsible_since===assigned.responsible_since,'Unchanged responsibility retains its consent-time boundary after unrelated edits');
  form=await edit('overview',vehicle,'vehicles',vehicle,'vehicle.save');await form.locator('[name=responsible_member_id]').selectOption('');await commit('vehicle.save','responsible_member_id',null);
  let saved=await record('vehicles',vehicle);must(saved.responsible_member_id===null&&saved.responsible_since===null,'Explicitly clearing vehicle responsibility clears recipient and timestamp');
  form=await edit('overview',vehicle,'vehicles',vehicle,'vehicle.save');must(await form.locator('[name=responsible_member_id]').inputValue()==='','Cleared responsibility remains empty after reload');
  await form.locator('[name=responsible_member_id]').selectOption(member.id);await commit('vehicle.save','responsible_member_id',member.id);
  await go('maintenance',vehicle);form=await open('maintenance.save');
  await form.locator('[name=title]').fill(name+' maintenance');const date=new Date(overview.context.today+'T12:00:00Z');date.setUTCDate(date.getUTCDate()+14);await form.locator('[name=due_on]').fill(date.toISOString().slice(0,10));
  must(await form.locator('[name=assignee_id]').inputValue()==='','Maintenance starts unassigned despite assigned vehicle');
  const plan=await commit('maintenance.save','assignee_id',null);saved=await record('maintenance_plans',plan);
  must(saved.assignee_id===null&&saved.assignee_since===null,'Unassigned maintenance does not inherit vehicle responsibility');
  form=await edit('maintenance',vehicle,'maintenance_plans',plan,'maintenance.save');await form.locator('[name=assignee_id]').selectOption(member.id);await commit('maintenance.save','assignee_id',member.id);
  const assignedPlan=await record('maintenance_plans',plan);must(assignedPlan.assignee_id===member.id&&Number.isFinite(Date.parse(assignedPlan.assignee_since)),'Maintenance explicitly stores its own member and assignment time');
  form=await edit('overview',vehicle,'vehicles',vehicle,'vehicle.save');await form.locator('[name=responsible_member_id]').selectOption('');await commit('vehicle.save','responsible_member_id',null);
  must((await record('maintenance_plans',plan)).assignee_id===member.id,'Clearing vehicle responsibility preserves independent maintenance assignment');
  form=await edit('maintenance',vehicle,'maintenance_plans',plan,'maintenance.save');await form.locator('[name=assignee_id]').selectOption('');await commit('maintenance.save','assignee_id',null);
  saved=await record('maintenance_plans',plan);must(saved.assignee_id===null&&saved.assignee_since===null,'Maintenance assignment can be explicitly cleared with its timestamp');
  for(const language of ['de','en']){
   if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value='+language+']').click();await page.waitForFunction(lang=>document.documentElement.dataset.locale===lang,language);await ready();}
   for(const [pane,entity,id,action,field]of [['overview','vehicles',vehicle,'vehicle.save','responsible_member_id'],['maintenance','maintenance_plans',plan,'maintenance.save','assignee_id']]){
    form=await edit(pane,vehicle,entity,id,action);
    const chooser=form.locator('[name='+field+']');must(await chooser.inputValue()==='','Cleared assignment is retained in '+language+'/'+pane);
    if(pane==='overview'){
     // The wrapping native label also contains select option text. Match the
     // computed accessible name, which correctly excludes those option names.
     const namedChooser=form.getByRole('combobox',{name:language==='en'?'Household member responsible for vehicle deadlines · optional':'Verantwortliches Haushaltsmitglied für Fahrzeugtermine · optional',exact:true});
     must(await namedChooser.count()===1&&await namedChooser.getAttribute('name')==='responsible_member_id','Vehicle responsibility combobox has the exact localized accessible name in '+language);
    }
    must(await page.getByRole('link',{name:language==='en'?'Manage vehicle device notifications':'Gerätehinweise für Fahrzeuge verwalten',exact:true}).count()===1,'Reciprocal settings link localized in '+language+'/'+pane);
    for(const width of [1440,320])for(const mode of ['light','dark','pink-light','pink-dark']){
     await page.setViewportSize({width,height:width===320?900:1000});await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);
     await chooser.focus();must(await chooser.evaluate(el=>el===document.activeElement),'Assignment control keyboard reachable '+language+'/'+pane+'/'+mode+'/'+width);
     await capture(language+'-'+pane+'-'+mode+'-'+width);
    }
   }
  }
  must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'Assignments and theme changes do not request device notification permission');
  must(!report.errors.length,'No browser JavaScript errors');must(!report.externalRequests.length,'No external browser requests');report.passed=true;
 }catch(error){report.failure=redact(error);throw error;}finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(redact(error));process.exitCode=1;});
