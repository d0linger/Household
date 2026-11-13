'use strict';
// Real reviewed assignments on the explicitly owned fixture; no provider or
// device delivery is simulated or claimed by this browser acceptance driver.
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');
const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Finance notification browser requires the owned localhost:8101 fixture');
const output=path.resolve(workspace,process.env.HOUSEHOLD_FINANCE_NOTIFICATIONS_BROWSER_OUTPUT||'outputs/notifications-device-channel/finance/browser-reminders');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Finance notification evidence must remain inside workspace outputs');
const report={passed:false,checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],physicalDeliveryTested:false};
const secrets=[];let browser,page,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(value,label)=>{if(!value)throw Error(label);report.checks.push(label);};
const api='/api/v1/expenses/finance';
async function fixtureState(){return readFixtureState(workspace,secrets,{pinned:false});}
async function read(route){return page.evaluate(async route=>{const r=await fetch(route,{cache:'no-store'});if(!r.ok)throw Error('Actual Finance read failed: '+r.status);return r.json();},route);}
async function ready(){await page.waitForFunction(()=>document.querySelector('#finance-workspace')?.getAttribute('aria-busy')==='false');must(await page.locator('#finance-error').isHidden(),'Current authorized Finance workspace loads');}
async function go(id=''){await page.goto(origin.origin+'/m/expenses/workspace/entries'+(id?'?id='+id:''));await ready();if(id)await page.locator('#finance-entry-editor:not([hidden])').waitFor();}
async function apply(trigger){
 const preview=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/preview'&&r.request().method()==='POST');await trigger();must((await preview).status()===200,'Real Finance preview accepts reviewed source');
 await page.locator('#finance-review:not([hidden])').waitFor();const accepted=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/apply'&&r.request().method()==='POST');await page.locator('#finance-apply').click();const response=await accepted;must(response.status()===200,'Actual reviewed Finance command commits');await page.locator('#finance-operation:not([hidden])').waitFor();
 const key=response.request().headers()['idempotency-key'];const result=await read(api+'/operations/'+key);must(result.request_key===key,'Reviewed source retains durable exact command receipt');return result.resource_id;
}
async function choose(name,id,label){const select=page.locator('[name='+name+']:visible'),field=select.locator('xpath=ancestor::div[contains(@class,"reference-field")][1]');await field.getByRole('searchbox').fill(label);await field.getByRole('button',{name:'Suchen',exact:true}).click();await select.locator('option[value="'+id+'"]').waitFor({state:'attached'});await select.selectOption(id);}
async function openReminder(){await page.locator('#finance-entry-current-actions').getByRole('button',{name:/^(Zahlungserinnerung|Payment reminder)$/,exact:true}).click();await page.locator('#finance-reminder-dialog[open]').waitFor();await page.waitForFunction(()=>document.querySelector('#finance-reminder-dialog')?.getAttribute('aria-busy')==='false');}
async function saveReminder(action){
 const response=page.waitForResponse(r=>/\/payment-reminder$/.test(new URL(r.url()).pathname)&&r.request().method()==='POST').then(async result=>{
  must(result.status()===200,'Actual reminder '+action+' succeeds');
  return result.json();
 });
 await page.locator('[data-reminder-action='+action+']').click();const state=await response;
 await page.waitForFunction(()=>document.querySelector('#finance-reminder-dialog')?.getAttribute('aria-busy')==='false');return state;
}
async function closeReminder(){await page.locator('#finance-reminder-dialog').getByRole('button',{name:/^(Schließen|Close)$/,exact:true}).click();await page.locator('#finance-reminder-dialog').waitFor({state:'hidden'});}
async function capture(name){
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().filter(animation=>Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation=>animation.finished.catch(()=>{})));});
 const scan=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:scan.violations});must(scan.violations.length===0,'Axe '+name);
 must(await page.evaluate(()=>{const d=document.querySelector('#finance-reminder-dialog'),r=d.getBoundingClientRect();return document.documentElement.scrollWidth<=innerWidth+1&&r.left>=0&&r.right<=innerWidth+1&&d.scrollWidth<=d.clientWidth+1;}),'No page or reminder dialog horizontal overflow '+name);
 await page.screenshot({path:path.join(output,name+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(name+'.png');
}
(async()=>{
 const storageState=await fixtureState();await fs.mkdir(output,{recursive:true});
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 watchdog=setTimeout(()=>{report.errors.push('Eight-minute Finance browser deadline exceeded');void browser.close();},480000);watchdog.unref();
 try{
  const context=await browser.newContext({storageState,viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(new URL(route.request().url()).origin);return route.abort();}return route.continue();});
  await context.addInitScript(()=>{window.__notificationPermissionCalls=0;if(window.Notification)Notification.requestPermission=()=>{window.__notificationPermissionCalls++;return Promise.resolve('default');};});
  page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error)));await go();
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
  const fixture=await page.evaluate(async()=>{const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json()),name='Finance reminder synthetic '+Date.now();
   const r=await fetch('/api/v1/m/expenses/categories',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':boot.csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation:boot.states.expenses.generation,data:{name}})});if(!r.ok)throw Error('Actual category fixture failed');
   const category=await r.json(),today=new Date(),due=new Date(today);due.setDate(due.getDate()+14);return{category,name,today:today.toISOString().slice(0,10),due:due.toISOString().slice(0,10),member:boot.user.membership_id};});
  await page.locator('#finance-new-entry').click();await page.locator('[name=entry_description]').fill(fixture.name);await page.locator('[name=entry_date]').fill(fixture.today);await page.locator('[name=entry_original]').fill('12,00');
  await page.locator('#finance-add-line').click();const line=page.locator('#finance-category-lines>.finance-line').last();await choose(await line.locator('select').first().getAttribute('name'),fixture.category.id,fixture.category.name);await line.locator('input[name$=_amount]').fill('12,00');
  const entry=await apply(()=>page.locator('#finance-entry-save').click());await go(entry);await openReminder();
  const modal=page.locator('#finance-reminder-dialog');must(await modal.locator('[data-reminder-action=confirm]').isDisabled(),'No confirmation exists before saving a real draft');
  await modal.locator('[name=due_on]').fill(fixture.due);await modal.locator('[name=responsible_member_id]').selectOption(fixture.member);
  let state=await saveReminder('save');must(state.reminder.state==='draft'&&!state.verified_paid,'Saved reminder is a draft, not an accounting payment');state=await saveReminder('confirm');must(state.reminder.state==='confirmed'&&!state.needs_reconfirmation,'Explicit confirmation binds the current source');
  await closeReminder();await page.reload();await ready();await page.locator('#finance-entry-editor:not([hidden])').waitFor();await openReminder();must(await modal.locator('[name=due_on]').inputValue()===fixture.due,'Due date survives real page reload');
  const firstPage=page,secondPage=await context.newPage();page=secondPage;
  try{await go(entry);await page.locator('#finance-entry-current-actions').getByRole('button',{name:'Bearbeiten',exact:true}).click();await page.locator('[name=entry_description]').fill(fixture.name+' revised');await apply(()=>page.locator('#finance-entry-save').click());}finally{await secondPage.close();page=firstPage;}
  await modal.getByRole('button',{name:'Aktuellen Stand laden',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#finance-reminder-dialog')?.getAttribute('aria-busy')==='false');
  must((await modal.innerText()).includes('Die Buchung wurde geändert'),'Actual source edit requires fresh confirmation');
  must(await modal.locator('[data-reminder-action=confirm]').isDisabled(),'Fresh reminder API data cannot confirm unseen changed entry contents');
  const sourceReview=modal.getByRole('link',{name:'Aktuellen Buchungsstand prüfen',exact:true});must(await sourceReview.getAttribute('href')==='/m/expenses/workspace/entries?id='+entry,'Source review uses the exact authorized entry deep link');
  await sourceReview.click();await ready();await page.locator('#finance-entry-editor:not([hidden])').waitFor();must(await page.locator('[name=entry_description]').inputValue()===fixture.name+' revised','Deep link displays the actual revised source');await openReminder();
  state=await saveReminder('confirm');must(!state.needs_reconfirmation,'Explicit reconfirmation follows review of current source');
  // A second real writer creates a conflict; no network response is fabricated.
  const stale=await read(api+'/entries/'+entry+'/payment-reminder');
  const competing=await page.evaluate(async({api,entry,stale})=>{const b=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());const r=await fetch(api+'/entries/'+entry+'/payment-reminder',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':b.csrf},body:JSON.stringify({action:'save',version:stale.reminder.version,entry_version:stale.entry_version,generation:stale.generation,access_version:stale.access_version,due_on:stale.reminder.due_on,responsible_member_id:stale.reminder.responsible_member_id,request_id:crypto.randomUUID()})});return r.status;},{api,entry,stale});must(competing===200,'Independent actual reminder update creates a newer version');
  const conflict=page.waitForResponse(r=>/\/payment-reminder$/.test(new URL(r.url()).pathname)&&r.request().method()==='POST');await modal.locator('[data-reminder-action=complete]').click();must((await conflict).status()===409,'Stale current modal cannot complete a newer reminder');await page.waitForFunction(()=>document.querySelector('#finance-reminder-dialog')?.getAttribute('aria-busy')==='false');
  must(await modal.locator('[data-reminder-action=save]').isDisabled(),'Conflict requires explicit reload before new writes');await modal.getByRole('button',{name:'Aktuellen Stand laden',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#finance-reminder-dialog')?.getAttribute('aria-busy')==='false');
  state=await saveReminder('confirm');state=await saveReminder('complete');must(state.reminder.state==='completed'&&!state.verified_paid,'Reminder completion never invents actual payment');await saveReminder('save');await saveReminder('cancel');
  state=await read(api+'/entries/'+entry+'/payment-reminder');must(state.reminder.state==='cancelled','Explicit cancellation persists');
  await saveReminder('save');await saveReminder('confirm');await closeReminder();
  for(const language of ['de','en']){
   if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value='+language+']').click();await page.waitForFunction(lang=>document.documentElement.dataset.locale===lang,language);await ready();}
   await go(entry);await openReminder();
   const chooser=modal.getByRole('combobox',{name:language==='en'?'Responsible household member':'Zuständiges Haushaltsmitglied',exact:true});must(await chooser.count()===1,'Responsibility has the exact localized accessible name '+language);
   must(await modal.getByRole('link',{name:language==='en'?'Manage finance device notifications':'Finanzhinweise auf Geräten verwalten',exact:true}).count()===1,'Device consent link is localized '+language);
   for(const width of [1440,320])for(const mode of ['light','dark','pink-light','pink-dark']){
    await page.setViewportSize({width,height:width===320?900:1000});await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);await chooser.focus();must(await chooser.evaluate(el=>el===document.activeElement),'Responsible selection remains keyboard reachable '+language+'/'+mode+'/'+width);
    await page.keyboard.press('Tab');must(await page.evaluate(()=>document.querySelector('#finance-reminder-dialog').contains(document.activeElement)),'Native modal retains keyboard focus');await capture(language+'-'+mode+'-'+width);
   }
   await page.keyboard.press('Escape');await modal.waitFor({state:'hidden'});must(await page.evaluate(()=>document.querySelector('#finance-entry-current-actions').contains(document.activeElement)),'Escape returns focus to entry action '+language);
  }
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
  await page.goto(origin.origin+'/m/expenses/workspace/accounts');await ready();await page.getByRole('button',{name:'Konto anlegen',exact:true}).click();await page.locator('#finance-editor:not([hidden])').waitFor();
  const accountName=fixture.name+' payment account';await page.locator('[name=name]:visible').fill(accountName);await page.locator('[name=account_type]:visible').selectOption('bank');await page.locator('[name=currency]:visible').selectOption('EUR');
  const account=await apply(()=>page.getByRole('button',{name:'Eingaben serverseitig prüfen',exact:true}).click());
  await go(entry);await page.locator('#finance-entry-current-actions').getByRole('button',{name:'Bearbeiten',exact:true}).click();await page.locator('#finance-payment-fields>summary').click();await choose('entry_payment_account',account,accountName);await page.locator('[name=entry_payment_native]:visible').fill('12,00');
  await apply(()=>page.locator('#finance-entry-post').click());await go(entry);await openReminder();state=await read(api+'/entries/'+entry+'/payment-reminder');
  must(state.verified_paid===true,'Real reviewed posting with matching account movement is verified payment');must((await modal.innerText()).includes('Passende tatsächliche Zahlung belegt'),'UI identifies actual payment separately from reminder completion');
  must(await modal.locator('[data-reminder-action=confirm]').isDisabled()&&await modal.locator('[data-reminder-action=save]').isDisabled(),'Actual verified payment cannot schedule another due alert');await closeReminder();
  must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'Reminder flow never implicitly requests device permission');must(!report.errors.length,'No JavaScript errors');must(!report.externalRequests.length,'No external browser requests');report.passed=true;
 }catch(error){report.failure=redact(error);throw error;}finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(redact(error));process.exitCode=1;});
