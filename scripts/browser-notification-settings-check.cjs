'use strict';

// Actual authenticated settings use only the explicitly owned localhost:8101
// fixture. Device display cases are intercepted API projections, never evidence
// of a real subscription, provider request, OS notification or device receipt.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const {fixtureState:readFixtureState}=require('./notification-browser-fixture.cjs');

const workspace=path.resolve(__dirname,'..'),origin=new URL(process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101');
if(origin.origin!=='http://localhost:8101'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password)throw Error('Notification browser checks require the owned localhost:8101 fixture');
const output=path.resolve(process.env.HOUSEHOLD_NOTIFICATIONS_BROWSER_OUTPUT||path.join(workspace,'outputs/notifications-device-channel/browser'));
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Notification evidence must remain inside workspace outputs');
const report={checks:[],accessibility:[],screenshots:[],errors:[],externalRequests:[],controlledFixtures:[],passed:false,physicalDeliveryTested:false};
const secrets=[];let browser,page,context,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(condition,label)=>{if(!condition)throw Error(label);report.checks.push(label);};
const clone=value=>JSON.parse(JSON.stringify(value));
const row=()=>page.locator('.notification-preference').filter({has:page.locator('#notification-category-test')});
const form=()=>row().locator('form');
const savedState=()=>page.evaluate(async()=>{const r=await fetch('/api/v1/core/notification-preferences',{cache:'no-store'});if(!r.ok)throw Error('Cannot read fixture notification preferences');return r.json();});

async function fixtureState(){return readFixtureState(workspace,secrets,{pinned:false});}
async function ready(){
 await page.locator('[data-notification-settings]').waitFor();
 await page.waitForFunction(()=>/Aktueller Stand geladen|Current settings loaded/.test(document.querySelector('[data-notification-status]')?.textContent||''));
 await page.waitForFunction(()=>document.querySelector('[data-notification-refresh]')?.disabled===false);
}
async function refresh(){await page.locator('[data-notification-refresh]').click();await ready();}
async function post(route,body){
 return page.evaluate(async({route,body})=>{
  const r=await fetch(route,{method:'POST',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},body:JSON.stringify(body)});
  return {status:r.status,body:await r.json()};
 },{route,body});
}
async function saveCategory(expected=200){
 const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST');
 await form().locator('button[type=submit]').focus();await page.keyboard.press('Enter');
 must((await response).status()===expected,'Real notification preference save returns '+expected);
 await page.waitForFunction(()=>document.querySelector('#notification-category-test')?.closest('article')?.querySelector('form')?.getAttribute('aria-busy')==='false');
}
async function locale(value){
 if(await page.locator('html').getAttribute('data-locale')!==value){await page.locator('.language-control button[value='+value+']').click();await page.waitForFunction(value=>document.documentElement.dataset.locale===value,value);}
 await ready();
}
async function capture(name){
 await page.evaluate(()=>{document.activeElement?.blur();window.scrollTo(0,0);});
 await page.screenshot({path:path.join(output,name+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(name+'.png');
}
async function accessibility(name){
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().filter(animation=>Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation=>animation.finished.catch(()=>{})));});
 const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
 report.accessibility.push({name,violations:result.violations.map(v=>({id:v.id,impact:v.impact,targets:v.nodes.map(n=>n.target),details:v.nodes.map(n=>n.failureSummary)}))});
 must(result.violations.length===0,'No Axe accessibility violations: '+name);
 must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'No horizontal overflow: '+name);
}

async function actualPreferences(){
 await page.goto(origin.origin+'/settings/notifications');await ready();await locale('de');
 const choreState=await page.evaluate(async()=>(await fetch('/api/v1/core/bootstrap',{cache:'no-store'})).json().then(boot=>boot.states.chores.state));
 if(choreState!=='enabled'){
  must(await page.getByRole('link',{name:'Aufgabeneinstellungen öffnen',exact:true}).count()===0,'Disabled task module exposes no unusable settings link');
  await page.evaluate(async()=>{
   const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   const response=await fetch('/settings/modules/chores',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.chores.version)})});
   if(!response.ok)throw Error('Actual isolated task-module enable failed');
   const current=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   if(current.states.chores.state!=='enabled')throw Error('Task-module enable was not committed');
  });
  await page.reload();await ready();
 }
 const configuration=await page.evaluate(async()=>(await fetch('/api/v1/core/push/context')).json());
 must(configuration.state==='disabled','Owned browser fixture has server push disabled');
 must(await page.locator('[data-push-enable]').isDisabled(),'Disabled server cannot request a device connection');
 must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'Opening settings does not request Notification permission');
 await page.locator('[data-push-refresh]').click();
 await page.waitForFunction(()=>document.querySelector('[data-notification-push]')?.getAttribute('aria-busy')==='false');
 must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'Refreshing registration status does not request permission');
 await page.locator('.navigation-trigger').click();
 const tray=page.locator('#more-dialog');
 await tray.evaluate(async dialog=>{await Promise.all(dialog.getAnimations().map(a=>a.finished));});
 must(await tray.locator('a[href="/settings/notifications"]').getAttribute('aria-current')==='page','Settings tray contains the current notifications page');
 await tray.locator('a[href="/settings/notifications"]').focus();await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),page.keyboard.press('Enter')]);await ready();
 must(await page.locator('main h1').innerText()==='Benachrichtigungen','Keyboard tray navigation opens the actual notification page');
 must(await page.locator('[data-notification-preferences] article').count()===13,'All thirteen categories are represented once');
 must(await page.locator('[data-notification-preferences] > article h3').first().getAttribute('id')==='notification-category-chores_due','Available categories retain their established order');
 const upcoming=page.locator('.notification-upcoming');
 must(await upcoming.count()===0,'All implemented categories are directly available without an empty future-category disclosure');
 const securityRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-security')});
 must(await securityRow.locator('form').count()===1,'Account security exposes an explicit available account-consent form');
 must(await securityRow.locator('form input[type=checkbox]').isEnabled(),'Security account consent is an operable explicit choice');
 must(!(await securityRow.locator('form input[type=checkbox]').isChecked()),'New security category does not implicitly opt the account in');
 must(await page.locator('.notification-preference').filter({has:page.locator('#notification-category-mealplan')}).locator('form').count()===1,'Mealplan exposes an explicit available account-consent form');
 const securityDetails=securityRow.locator('details').first();
 await securityDetails.locator('summary').focus();await page.keyboard.press('Enter');
 must(await securityDetails.getAttribute('open')!==null,'Security guidance can be expanded using the keyboard');
 await page.keyboard.press('Enter');
 must(await securityDetails.getAttribute('open')===null,'Security guidance can be collapsed using the keyboard');
 must(await page.locator('.notification-unavailable form').count()===0,'Unavailable adapters expose no misleading active permission form');
 const original=await savedState(),originalTest=original.preferences.find(p=>p.category==='test');
 must(original.categories.filter(c=>c.available).map(c=>c.category).join(',')==='chores_due,chores_digest,chores_escalation,cooking_timer,shopping,pantry,fuel,expenses,meters,documents,mealplan,security,test','Exactly thirteen implemented adapters, including personal security, are available');
 const allowed=form().locator('input[type=checkbox]');
 await allowed.check();await form().locator('summary').click();
 const times=form().locator('input[type=time]');await times.nth(0).fill('23:00');await times.nth(1).fill('');
 let writes=0;const countWrites=request=>{if(new URL(request.url()).pathname==='/api/v1/core/notification-preferences'&&request.method()==='POST')writes++;};page.on('request',countWrites);
 const before=writes;await form().locator('button[type=submit]').click();
 await page.waitForFunction(()=>/Fülle beide Ruhezeiten/.test(document.querySelector('#notification-category-test')?.closest('article')?.querySelector('[role=status]')?.textContent||''));
 must(writes===before,'Incomplete quiet hours are rejected before a server mutation');
 await times.nth(1).fill('07:00');await form().locator('input[type=text]').fill('Europe/Vienna');await saveCategory();
 let current=(await savedState()).preferences.find(p=>p.category==='test');
 must(current.enabled&&current.quiet_start===1380&&current.quiet_end===420,'Real database retains enabled consent and overnight quiet hours');
 must(await form().locator('button[type=submit]').isEnabled(),'Successful preference save leaves a keyboard-operable action');
 const untouched=original.preferences.filter(p=>p.category!=='test');
 must(JSON.stringify((await savedState()).preferences.filter(p=>p.category!=='test'))===JSON.stringify(untouched),'Saving test category preserves every other category');
 await page.reload();await ready();
 must(await form().locator('input[type=checkbox]').isChecked(),'Real consent survives a full reload');
 await form().locator('summary').click();
 must(await form().locator('input[type=time]').nth(0).inputValue()==='23:00'&&await form().locator('input[type=time]').nth(1).inputValue()==='07:00','Quiet hours survive reload without a midnight conversion');
 const competing=await post('/api/v1/core/notification-preferences',{category:'test',enabled:false,timezone:'UTC',quiet_start:null,quiet_end:null,version:current.version});
 must(competing.status===200,'Independent real update advances the same category version (HTTP '+competing.status+', '+String(competing.body.error||'ok')+')');
 await saveCategory(409);
 await page.waitForFunction(()=>/inzwischen geändert/.test(document.querySelector('#notification-category-test')?.closest('article')?.querySelector('[role=status]')?.textContent||''));
 must(!(await form().locator('input[type=checkbox]').isChecked())&&await form().locator('input[type=text]').inputValue()==='UTC','Conflict reloads the changed row instead of claiming the draft was saved');
 current=(await savedState()).preferences.find(p=>p.category==='test');
 must(current.version===competing.body.version&&!current.enabled,'Rejected stale write leaves the committed competing preference unchanged');
 await saveCategory();
 const restore=await post('/api/v1/core/notification-preferences',{category:'test',enabled:originalTest.enabled,timezone:originalTest.timezone,quiet_start:originalTest.quiet_start,quiet_end:originalTest.quiet_end,version:(await savedState()).preferences.find(p=>p.category==='test').version});
 must(restore.status===200,'Synthetic category settings restored after real mutation checks');page.off('request',countWrites);await refresh();
 for(const category of ['chores_due','chores_digest','chores_escalation']){
  const before=(await savedState()).preferences.find(p=>p.category===category);
  const article=page.locator('article').filter({has:page.locator('#notification-category-'+category)}),choreForm=article.locator('form');
  must(await choreForm.locator('input[type=time],input[type=text]').count()===0,'Task-owned quiet hours have no competing core editor: '+category);
  await choreForm.locator('input[type=checkbox]').check();
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST'),choreForm.locator('button[type=submit]').click()]);
  must(response.status()===200,'Actual opt-in saves independently for '+category);
  const saved=(await savedState()).preferences.find(p=>p.category===category);
  must(saved.enabled&&saved.quiet_start===null&&saved.quiet_end===null,'Chore consent preserves module-owned quiet policy: '+category);
  const reset=await post('/api/v1/core/notification-preferences',{category,enabled:before.enabled,timezone:before.timezone,quiet_start:null,quiet_end:null,version:saved.version});
  must(reset.status===200,'Synthetic chore opt-in restored after check: '+category);
 }
 await refresh();
 const shoppingRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-shopping')}),shoppingForm=shoppingRow.locator('form');
 const shoppingBefore=(await savedState()).preferences.find(p=>p.category==='shopping');
 report.shoppingFirstCentralSaveTested=shoppingBefore.version===0;
 if(shoppingBefore.version===0){
  must(await shoppingRow.locator('[data-shopping-control=legacy]').isVisible(),'Before the first Shopping save, the permanent replacement of list device delivery is visible');
  must((await shoppingRow.locator('[data-shopping-control]').innerText()).includes('auch, wenn du die Kategorie ausgeschaltet speicherst'),'First-save explanation explicitly includes saving Shopping as off');
 }else must(await shoppingRow.locator('[data-shopping-control=central]').isVisible(),'An existing Shopping preference is shown as central even when disabled');
 must((await shoppingRow.innerText()).includes('nicht automatisch für Einkauf freigegeben'),'Account consent does not claim automatic device-category migration');
 const shoppingModule=await page.evaluate(async()=>(await fetch('/api/v1/core/bootstrap',{cache:'no-store'})).json().then(boot=>boot.states.shopping.state));
 const listLink=shoppingRow.getByRole('link',{name:'Listenfreigaben und Stummschaltungen prüfen',exact:true});
 must(shoppingModule==='enabled'?await listLink.getAttribute('href')==='/settings#settings-notifications':await listLink.count()===0,'Shopping cross-link follows current module availability');
 async function saveShopping(enabled){
  await shoppingForm.locator('input[type=checkbox]').setChecked(enabled);
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST'),shoppingForm.locator('button[type=submit]').click()]);
  must(response.status()===200,'Actual Shopping category save accepted with enabled='+enabled);
  await page.waitForFunction(()=>document.querySelector('#notification-category-shopping')?.closest('article')?.querySelector('form')?.getAttribute('aria-busy')==='false');
  const saved=(await savedState()).preferences.find(p=>p.category==='shopping');
  must(saved.enabled===enabled&&saved.version>0,'Shopping consent persists with a real central-control version');
  must(await shoppingRow.locator('[data-shopping-control=central]').isVisible(),'A committed save immediately shows central Shopping control');
  return saved;
 }
 const disabled=await saveShopping(false);
 await page.reload();await ready();
 let shoppingSaved=(await savedState()).preferences.find(p=>p.category==='shopping');
 must(shoppingSaved.version===disabled.version&&!shoppingSaved.enabled&&await shoppingRow.locator('[data-shopping-control=central]').isVisible(),'Saving Shopping as off retains explicit central control after reload');
 await saveShopping(true);shoppingSaved=await saveShopping(false);
 await page.reload();await ready();
 const shoppingFinal=(await savedState()).preferences.find(p=>p.category==='shopping');
 must(!shoppingFinal.enabled&&shoppingFinal.version===shoppingSaved.version&&shoppingFinal.version>disabled.version,'Opt-in then disable persists without restoring the legacy default');
 must((await shoppingRow.locator('[data-shopping-control]').innerText()).includes('frühere Listenversand aus'),'Disabled central Shopping clearly keeps the previous list delivery off');
 if(shoppingModule==='enabled'){
  await shoppingRow.getByRole('link',{name:'Listenfreigaben und Stummschaltungen prüfen',exact:true}).click();
  await page.locator('[data-shopping-watch-overview]').waitFor();
  must(new URL(page.url()).pathname==='/settings'&&new URL(page.url()).hash==='#settings-notifications','Shopping guidance opens the actual personal list-permissions section');
  must(await page.locator('[data-watch-overview-load]').isVisible(),'Personal list-permissions action is available at the linked destination');
  await page.goto(origin.origin+'/settings/notifications');await ready();
 }
 const pantryRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-pantry')}),pantryForm=pantryRow.locator('form');
 must((await pantryRow.innerText()).includes('Bestehende Geräte werden nicht automatisch freigegeben.'),'Pantry account opt-in explicitly preserves independent device consent');
 const pantryDetails=pantryRow.locator('details').first();
 must(await pantryDetails.getAttribute('open')===null,'Detailed stock and expiry rules stay in a compact native disclosure');
 await pantryDetails.locator('summary').focus();await page.keyboard.press('Enter');
 must(await pantryDetails.getAttribute('open')!==null&&(await pantryDetails.innerText()).includes('zwischen zwei Prüfungen'),'Keyboard disclosure explains the limits of stock observations');
 const otherBeforePantry=(await savedState()).preferences.filter(p=>p.category!=='pantry');
 for(const enabled of [false,true,false]){
  const before=(await savedState()).preferences.find(p=>p.category==='pantry');
  await pantryForm.locator('input[type=checkbox]').setChecked(enabled);
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST'),pantryForm.locator('button[type=submit]').click()]);
  must(response.status()===200,'Actual Pantry category save accepted with enabled='+enabled);
  await page.waitForFunction(()=>document.querySelector('#notification-category-pantry')?.closest('article')?.querySelector('form')?.getAttribute('aria-busy')==='false');
  await page.reload();await ready();
  const after=(await savedState()).preferences.find(p=>p.category==='pantry');
  must(after.enabled===enabled&&after.version===before.version+1&&await pantryForm.locator('input[type=checkbox]').isChecked()===enabled,'Pantry consent and version survive reload with enabled='+enabled);
 }
 must(JSON.stringify((await savedState()).preferences.filter(p=>p.category!=='pantry'))===JSON.stringify(otherBeforePantry),'Pantry category saves do not change other account categories');
 const pantryModule=await page.evaluate(async()=>(await fetch('/api/v1/core/bootstrap',{cache:'no-store'})).json().then(boot=>boot.states.pantry.state));
 if(pantryModule!=='enabled'){
  must(await pantryRow.getByRole('link',{name:'Vorrat und Ablaufdaten prüfen',exact:true}).count()===0,'Disabled Pantry module exposes no unusable stock link');
  await page.evaluate(async()=>{
   const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   const response=await fetch('/settings/modules/pantry',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.pantry.version)})});
   if(!response.ok)throw Error('Actual isolated Pantry-module enable failed');
   const current=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   if(current.states.pantry.state!=='enabled')throw Error('Pantry-module enable was not committed');
  });
  await page.reload();await ready();
 }
 const pantryLink=pantryRow.getByRole('link',{name:'Vorrat und Ablaufdaten prüfen',exact:true});
 must(await pantryLink.getAttribute('href')==='/m/pantry/stock','Pantry guidance links to the canonical stock overview');
 await pantryLink.focus();await page.keyboard.press('Enter');await page.locator('#pantry-board').waitFor();
 must(new URL(page.url()).pathname==='/m/pantry/stock'&&await page.locator('#pantry-filter').isVisible(),'Pantry guidance opens the actual stock overview through keyboard navigation');
 await page.goto(origin.origin+'/settings/notifications');await ready();
 const fuelRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-fuel')}),fuelForm=fuelRow.locator('form');
 const otherBeforeFuel=(await savedState()).preferences.filter(p=>p.category!=='fuel');
 for(const enabled of [false,true,false]){
  const before=(await savedState()).preferences.find(p=>p.category==='fuel');
  await fuelForm.locator('input[type=checkbox]').setChecked(enabled);
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST'),fuelForm.locator('button[type=submit]').click()]);
  must(response.status()===200,'Actual Fuel category save accepted with enabled='+enabled);
  await page.waitForFunction(()=>document.querySelector('#notification-category-fuel')?.closest('article')?.querySelector('form')?.getAttribute('aria-busy')==='false');
  await page.reload();await ready();const after=(await savedState()).preferences.find(p=>p.category==='fuel');
  must(after.enabled===enabled&&after.version===before.version+1&&await fuelForm.locator('input[type=checkbox]').isChecked()===enabled,'Fuel consent and version survive reload with enabled='+enabled);
 }
 must(JSON.stringify((await savedState()).preferences.filter(p=>p.category!=='fuel'))===JSON.stringify(otherBeforeFuel),'Fuel saves preserve all other account categories');
 const fuelModule=await page.evaluate(async()=>(await fetch('/api/v1/core/bootstrap',{cache:'no-store'})).json().then(boot=>boot.states.fuel.state));
 if(fuelModule!=='enabled'){
  must(await fuelRow.getByRole('link').count()===0,'Disabled Vehicles module exposes no unusable workflow links');
  await page.evaluate(async()=>{
   const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());
   const response=await fetch('/settings/modules/fuel',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.fuel.version)})});
   if(!response.ok)throw Error('Actual isolated Fuel-module enable failed');
   const current=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());if(current.states.fuel.state!=='enabled')throw Error('Fuel-module enable was not committed');
  });await page.reload();await ready();
 }
 for(const [name,route]of [['Fahrzeugtermine und Zuständigkeit prüfen','overview'],['Wartungspläne prüfen','maintenance']]){
  const link=fuelRow.getByRole('link',{name,exact:true});must(await link.getAttribute('href')==='/m/fuel/workspace/'+route,'Fuel guidance points to '+route);
  await link.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#fm-summary')?.getAttribute('aria-busy')==='false');
  must(await page.locator('#fm-error').isHidden(),'Actual Fuel '+route+' workflow loads without error');
  await page.getByRole('link',{name:'Gerätehinweise für Fahrzeuge verwalten',exact:true}).click();await ready();
  must(new URL(page.url()).pathname==='/settings/notifications','Fuel '+route+' returns through its explicit notification settings link');
 }
 const financeRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-expenses')}),financeForm=financeRow.locator('form');
 const otherBeforeFinance=(await savedState()).preferences.filter(p=>p.category!=='expenses');
 for(const enabled of [false,true,false]){
  const before=(await savedState()).preferences.find(p=>p.category==='expenses');await financeForm.locator('input[type=checkbox]').setChecked(enabled);
  const [response]=await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/core/notification-preferences'&&r.request().method()==='POST'),financeForm.locator('button[type=submit]').click()]);
  must(response.status()===200,'Explicit Finance consent save succeeds');await page.waitForFunction(()=>document.querySelector('#notification-category-expenses')?.closest('article')?.querySelector('form')?.getAttribute('aria-busy')==='false');
  await page.reload();await ready();const after=(await savedState()).preferences.find(p=>p.category==='expenses');
  must(after.enabled===enabled&&after.version===before.version+1&&await financeForm.locator('input[type=checkbox]').isChecked()===enabled,'Finance consent and version survive reload: '+enabled);
 }
 must(JSON.stringify((await savedState()).preferences.filter(p=>p.category!=='expenses'))===JSON.stringify(otherBeforeFinance),'Finance saves preserve all other categories');
 const financeModule=await page.evaluate(async()=>(await fetch('/api/v1/core/bootstrap',{cache:'no-store'})).json().then(boot=>boot.states.expenses.state));
 if(financeModule!=='enabled'){
  must(await financeRow.getByRole('link').count()===0,'Disabled Finance module exposes no unusable links');
  await page.evaluate(async()=>{const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());const r=await fetch('/settings/modules/expenses',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.expenses.version)})});if(!r.ok)throw Error('Actual isolated Finance enable failed');});await page.reload();await ready();
 }
 for(const [name,route]of [['Buchungen und Zahlungserinnerungen prüfen','entries'],['Aktuelle Budgets prüfen','planning?section=budgets']]){
  const link=financeRow.getByRole('link',{name,exact:true});must(await link.getAttribute('href')==='/m/expenses/workspace/'+route,'Finance guidance points to '+route);
  await link.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('#finance-workspace')?.getAttribute('aria-busy')==='false');
  must(await page.locator('#finance-error').isHidden(),'Actual Finance '+route+' loads without error');
  await page.getByRole('link',{name:'Finanzhinweise auf Geräten verwalten',exact:true}).click();await ready();
 }
 await page.locator('#notification-category-chores_due').locator('..').getByRole('link',{name:'Aufgabeneinstellungen öffnen',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#chores-workspace')?.getAttribute('aria-busy')==='false');
 must(await page.locator('#chores-error').isHidden(),'Actual chore notification settings load without error');
 must((await page.locator('#chores-workspace').innerText()).includes('Im Modus Aus werden keine Aufgabenhinweise gesendet.'),'Task settings explain the authoritative off mode and local-time policy');
 await page.getByRole('link',{name:'Gerätehinweise für Aufgaben verwalten',exact:true}).click();await ready();
 must(new URL(page.url()).pathname==='/settings/notifications','Task preferences link back to separate device consent');
 must(await page.locator('[data-notification-devices] .notification-empty').isVisible(),'Actual unregistered account shows a useful empty-device state');
 await accessibility('actual-disabled-desktop');await capture('actual-disabled-desktop');
 await page.setViewportSize({width:320,height:900});await accessibility('actual-disabled-mobile');await capture('actual-disabled-mobile');
 await page.setViewportSize({width:1440,height:1000});
}

async function controlledDevices(){
 report.controlledFixtures.push('Device settings and delivery APIs below are intercepted synthetic projections; no real device or provider transport was used.');
 const deviceID='11111111-1111-4111-8111-111111111111',deliveryID='22222222-2222-4222-8222-222222222222',now=new Date().toISOString();
 let testState=null,deviceState='registered',devicePreference={category:'test',enabled:false,paused:false,name:'Fixture kitchen',version:0,enabled_since:null};
 let testRequests=0,seenRequests=0,deliveryReads=0,deviceSaveMode='success';
 const projection=()=>({id:deviceID,device_id:'33333333-3333-4333-8333-333333333333',state:deviceState,registered_at:now,expires_at:new Date(Date.now()+86400000).toISOString(),name:devicePreference.name,categories:[clone(devicePreference)],last_test:testState?clone(testState):null});
 const json=(route,body,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
 await page.route('**/api/v1/core/push/device-preferences',route=>json(route,{devices:[projection()]}));
 await page.route('**/api/v1/core/push/subscriptions/'+deviceID+'/preferences',route=>{
  const body=route.request().postDataJSON();
  if(deviceSaveMode==='failure')return json(route,{error:'Synthetic rejected device preference'},503);
  if(deviceSaveMode==='conflict'){deviceSaveMode='success';devicePreference={...devicePreference,name:'Concurrent fixture name',version:devicePreference.version+1};return json(route,{error:'Synthetic version conflict'},409);}
  devicePreference={...body,version:body.version+1,enabled_since:body.enabled?now:null};return json(route,devicePreference);
 });
 await page.route('**/api/v1/core/push/subscriptions/'+deviceID+'/test',route=>{
  testRequests++;testState={id:deliveryID,state:'accepted',safe_code:'gateway_accepted',accepted_at:now,updated_at:now};
  return json(route,{id:deliveryID,state:'pending'});
 });
 await page.route('**/api/v1/core/push/deliveries/'+deliveryID,route=>{deliveryReads++;return json(route,testState);});
 await page.route('**/api/v1/core/push/deliveries/'+deliveryID+'/seen',route=>{seenRequests++;testState={...testState,safe_code:'user_confirmed',updated_at:new Date().toISOString()};return json(route,testState);});
 await refresh();
 const device=()=>page.locator('.notification-device'),testButton=()=>device().locator('.notification-device-header button');
 must(await device().locator('h3').innerText()==='Fixture kitchen','Controlled device label is shown without a subscription identifier');
 must(!(await device().innerText()).includes(deviceID)&&!(await device().innerText()).includes('33333333'),'Device UI never displays subscription or device UUIDs');
 await testButton().focus();await page.keyboard.press('Enter');
 must(await testButton().isDisabled(),'Pending test prevents a duplicate click');
 await testButton().evaluate(button=>button.click());
 await page.waitForFunction(()=>/Vom Push-Dienst angenommen/.test(document.querySelector('.notification-test-result')?.textContent||''),undefined,{timeout:20000});
 must(testRequests===1,'One explicit test action sends exactly one intercepted request');
 must((await device().locator('.notification-test-result').innerText()).includes('noch nicht bestätigt'),'Gateway acceptance explicitly does not claim device display');
 must(seenRequests===0,'Gateway acceptance never automatically confirms that the user saw a notification');
 testState.safe_code='display_requested';await refresh();
 must((await device().locator('.notification-test-result').innerText()).includes('Sichtbarkeit und Lesen sind damit noch nicht bestätigt'),'Browser display acknowledgement remains weaker than a seen confirmation');
 await device().getByRole('button',{name:'Ich habe diese Testnachricht gesehen',exact:true}).click();
 await page.waitForFunction(()=>/Von dir als gesehen bestätigt/.test(document.querySelector('.notification-test-result')?.textContent||''));
 must(seenRequests===1,'Explicit user confirmation alone records the controlled seen action');
 must(await device().getByRole('button',{name:'Ich habe diese Testnachricht gesehen',exact:true}).isHidden(),'Seen confirmation is not offered repeatedly after confirmation');
 await device().locator('summary').click();
 must(!(await device().locator('form[data-notification-category=shopping] input[type=checkbox]').first().isChecked()),'Existing controlled device without Shopping consent is not automatically opted in');
 must(!(await device().locator('form[data-notification-category=pantry] input[type=checkbox]').first().isChecked()),'Existing controlled device without Pantry consent is not automatically opted in');
 must(!(await device().locator('form[data-notification-category=fuel] input[type=checkbox]').first().isChecked()),'Existing controlled device without Fuel consent is not automatically opted in');
 must(!(await device().locator('form[data-notification-category=mealplan] input[type=checkbox]').first().isChecked()),'Existing controlled device without Mealplan consent is not automatically opted in');
 must(!(await device().locator('form[data-notification-category=security] input[type=checkbox]').first().isChecked()),'Existing controlled device without Security consent is not automatically opted in');
 const deviceForm=()=>device().locator('form[data-notification-category=test]'),name=()=>deviceForm().locator('input[type=text]');
 await name().fill('Preserved fixture draft');deviceSaveMode='failure';await deviceForm().locator('button[type=submit]').click();
 await page.waitForFunction(()=>/Synthetic rejected/.test(document.querySelector('.notification-device-category[data-notification-category=test] [role=status]')?.textContent||''));
 must(await name().inputValue()==='Preserved fixture draft'&&await deviceForm().locator('button').isEnabled(),'Failed controlled device save preserves the draft and enables retry');
 deviceSaveMode='conflict';await deviceForm().locator('button').click();
 await page.waitForFunction(()=>/inzwischen geändert/.test(document.querySelector('.notification-device-category[data-notification-category=test] [role=status]')?.textContent||''));
 must(await name().inputValue()==='Concurrent fixture name','Controlled device conflict loads the newer row');
 await deviceForm().locator('input[type=checkbox]').first().check();await deviceForm().locator('button').focus();await page.keyboard.press('Enter');
 await page.waitForFunction(()=>/Gerätefreigabe gespeichert/.test(document.querySelector('.notification-device-category[data-notification-category=test] [role=status]')?.textContent||''));
 must(devicePreference.enabled&&devicePreference.version===2,'Controlled device save carries the reviewed version and explicit category consent');
 must(!(await device().locator('form[data-notification-category=shopping] input[type=checkbox]').first().isChecked()),'Enabling another device category leaves Shopping permission off');
 must(!(await device().locator('form[data-notification-category=pantry] input[type=checkbox]').first().isChecked()),'Enabling another device category leaves Pantry permission off');
 must(!(await device().locator('form[data-notification-category=fuel] input[type=checkbox]').first().isChecked()),'Enabling another device category leaves Fuel permission off');
 must(!(await device().locator('form[data-notification-category=mealplan] input[type=checkbox]').first().isChecked()),'Enabling another device category leaves Mealplan permission off');
 must(!(await device().locator('form[data-notification-category=security] input[type=checkbox]').first().isChecked()),'Enabling another device category leaves Security permission off');
 for(const state of ['expired','cancelled','failed']){
  testState={...testState,state,safe_code:'',accepted_at:null};await refresh();
  must(await device().getByRole('button',{name:'Ich habe diese Testnachricht gesehen',exact:true}).isHidden(),'Terminal '+state+' state does not pretend a confirmed display');
  must(await device().locator('.notification-test-result [role=status]').innerText(),'Terminal '+state+' state supplies a recovery message');
 }
 testState=null;deviceState='expired';await refresh();must(await testButton().isDisabled(),'Expired controlled device cannot start a test');
 deviceState='renewal_required';await refresh();must((await device().innerText()).includes('Erneut verbinden erforderlich'),'Changed device key requests explicit reconnection');
 deviceState='registered';devicePreference.name='<img src=x onerror=alert(1)>';await refresh();
 must(await device().locator('h3').innerText()===devicePreference.name&&await device().locator('img').count()===0,'Device names are rendered as text rather than executable markup');
 devicePreference.name='Fixture kitchen';testState={id:deliveryID,state:'accepted',safe_code:'user_confirmed',accepted_at:now,updated_at:now};await refresh();
 const colors=new Map();
 for(const language of ['de','en']){
  await locale(language);
  for(const width of [1440,320]){
   await page.setViewportSize({width,height:width===320?900:1000});
   for(const mode of ['light','dark','pink-light','pink-dark']){
    await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);
    await page.waitForFunction(mode=>window.HouseholdTheme.get()===mode,mode);
    const summary=device().locator('summary');if(await device().locator('details').getAttribute('open')===null)await summary.click();
    const ground=await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--ground').trim());colors.set(mode,ground);
    must((await page.locator('main h1').innerText())===(language==='en'?'Notifications':'Benachrichtigungen'),'Notification heading matches '+language+'/'+mode+'/'+width);
    const pantryRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-pantry')});
    must(await pantryRow.locator('h3').innerText()===(language==='en'?'Pantry':'Vorrat'),'Pantry category name matches '+language+'/'+mode+'/'+width);
    const pantryDetails=pantryRow.locator('details').first(),pantrySummary=pantryDetails.locator('summary');
    must(await pantrySummary.innerText()===(language==='en'?'When you receive pantry alerts':'Wann du Vorratshinweise erhältst'),'Pantry guidance summary matches '+language+'/'+mode+'/'+width);
    if(await pantryDetails.getAttribute('open')===null){await pantrySummary.focus();await page.keyboard.press('Enter');}
    must((await pantryDetails.innerText()).includes(language==='en'?'A brief shortage between checks':'zwischen zwei Prüfungen'),'Expanded Pantry guidance is localized: '+language+'/'+mode+'/'+width);
    must(await pantryRow.getByRole('link',{name:language==='en'?'Review pantry stock and expiry dates':'Vorrat und Ablaufdaten prüfen',exact:true}).count()===1,'Pantry stock link is localized: '+language+'/'+mode+'/'+width);
    const fuelRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-fuel')}),fuelDetails=fuelRow.locator('details').first();
    must(await fuelRow.locator('h3').innerText()===(language==='en'?'Vehicles':'Fahrzeuge'),'Fuel category name matches '+language+'/'+mode+'/'+width);
    must(await fuelDetails.locator('summary').innerText()===(language==='en'?'Rules for deadlines and mileage targets':'Regeln für Termine und Kilometerziele'),'Fuel rule summary is localized: '+language+'/'+mode+'/'+width);
    if(await fuelDetails.getAttribute('open')===null){await fuelDetails.locator('summary').focus();await page.keyboard.press('Enter');}
    must((await fuelDetails.innerText()).includes(language==='en'?'30 calendar days':'30 Kalendertage'),'Fuel freshness guidance is localized: '+language+'/'+mode+'/'+width);
    must(await fuelRow.getByRole('link',{name:language==='en'?'Review maintenance plans':'Wartungspläne prüfen',exact:true}).count()===1,'Fuel maintenance link is localized: '+language+'/'+mode+'/'+width);
    const financeRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-expenses')}),financeDetails=financeRow.locator('details').first();
    must(await financeRow.locator('h3').innerText()===(language==='en'?'Finances':'Finanzen'),'Finance category matches '+language+'/'+mode+'/'+width);
    must(await financeDetails.locator('summary').innerText()===(language==='en'?'Payment deadlines and budget thresholds':'Zahlungsfristen und Budgetschwellen'),'Finance rules are localized');
    if(await financeDetails.getAttribute('open')===null){await financeDetails.locator('summary').focus();await page.keyboard.press('Enter');}
    must((await financeDetails.innerText()).includes(language==='en'?'75% or 95%':'75 % beziehungsweise 95 %'),'Finance hysteresis guidance matches '+language+'/'+mode+'/'+width);
    must(await financeRow.getByRole('link',{name:language==='en'?'Review current budgets':'Aktuelle Budgets prüfen',exact:true}).count()===1,'Finance budget link is localized');

    const securityRow=page.locator('.notification-preference').filter({has:page.locator('#notification-category-security')}),securityDetails=securityRow.locator('details').first();
    must(await securityRow.locator('h3').innerText()===(language==='en'?'Account security':'Kontosicherheit'),'Security category matches '+language+'/'+mode+'/'+width);
    must(await securityDetails.locator('summary').innerText()===(language==='en'?'Personal changes to your account':'Persönliche Änderungen an deinem Konto'),'Security guidance summary is localized: '+language+'/'+mode+'/'+width);
    if(await securityDetails.getAttribute('open')===null){await securityDetails.locator('summary').focus();await page.keyboard.press('Enter');}
    const securityCopy=await securityDetails.innerText();
    must(securityCopy.includes(language==='en'?'Other household members are not notified.':'Andere Haushaltsmitglieder werden nicht benachrichtigt.'),'Security recipient scope is localized: '+language+'/'+mode+'/'+width);
    must(securityCopy.includes(language==='en'?'at most one hour':'höchstens eine Stunde')&&securityCopy.includes(language==='en'?'later permission does not replay past events':'spätere Freigaben holen keine Ereignisse nach'),'Security freshness and no-backfill guidance is localized: '+language+'/'+mode+'/'+width);
    must(securityCopy.includes(language==='en'?'Names, passwords and device details never appear on the lock screen.':'Auf dem Sperrbildschirm erscheinen keine Namen, Kennwörter oder Gerätedetails.'),'Security lock-screen privacy guidance is localized: '+language+'/'+mode+'/'+width);
    must(await securityRow.getByRole('link',{name:language==='en'?'Review account security':'Kontosicherheit prüfen',exact:true}).getAttribute('href')==='/settings/security','Security guidance links to the fixed account security page: '+language+'/'+mode+'/'+width);

    must((await page.locator('.notification-test-result').innerText()).includes(language==='en'?'You confirmed':'Von dir'),'Controlled seen state is localized: '+language+'/'+mode+'/'+width);
    if(width===1440){const buttonBox=await deviceForm().locator('button[type=submit]').boundingBox();must(buttonBox&&buttonBox.height>=44&&buttonBox.height<=60,'Desktop device save keeps a compact accessible height: '+language+'/'+mode);}
    await accessibility('controlled-'+language+'-'+mode+'-'+width);await capture('controlled-'+language+'-'+mode+'-'+width);
   }
  }
 }
 must(new Set(colors.values()).size===4,'Ocean and Pink each render distinct light and dark palettes');
 for(const mode of ['system','pink-system']){
  await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);
  await page.emulateMedia({colorScheme:'dark'});
  await page.waitForFunction(mode=>window.HouseholdTheme.get()===mode&&(mode!=='pink-system'||document.documentElement.dataset.theme==='pink-dark'),mode);
  must(await page.evaluate(()=>getComputedStyle(document.documentElement).colorScheme.includes('dark')),'Automatic '+mode+' responds to dark system preference');
  must(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--ground').trim())===colors.get(mode==='system'?'dark':'pink-dark'),'Automatic '+mode+' uses the actual dark family palette');
  await page.emulateMedia({colorScheme:'light'});
  await page.waitForFunction(mode=>window.HouseholdTheme.get()===mode&&(mode!=='pink-system'||document.documentElement.dataset.theme==='pink-light'),mode);
  must(await page.evaluate(()=>getComputedStyle(document.documentElement).colorScheme.includes('light')),'Automatic '+mode+' responds to light system preference');
  must(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--ground').trim())===colors.get(mode==='system'?'light':'pink-light'),'Automatic '+mode+' uses the actual light family palette');
 }
 must(await page.evaluate(()=>window.__notificationPermissionCalls===0),'All controlled device operations and appearance changes avoid a native permission prompt');
 // Explicitly injected page-transition events exercise the controller's
 // lifecycle contract; this is not a claim about actual browser BFCache use.
 testState={...testState,state:'pending',safe_code:'',accepted_at:null};await refresh();
 const readsBeforeHide=deliveryReads;
 await page.evaluate(()=>dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true})));
 must(await page.locator('[data-notification-settings]').isHidden(),'Controlled pagehide conceals private settings before cached-page reuse');
 await page.waitForTimeout(5500);
 must(deliveryReads===readsBeforeHide,'Controlled pagehide stops the scheduled delivery polling');
 testState={...testState,state:'accepted',safe_code:'user_confirmed',accepted_at:now};
 await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),page.evaluate(()=>dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})))]);
 await ready();must(await page.locator('[data-notification-settings]').isVisible(),'Controlled cached-page restoration loads fresh authenticated settings');
}

(async()=>{
 const state=await fixtureState();await fs.mkdir(output,{recursive:true});
 browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:process.platform==='win32'?{channel:'msedge'}:{})});
 watchdog=setTimeout(()=>{report.failure='Notification browser campaign exceeded eight minutes';void browser.close();},8*60*1000);
 context=await browser.newContext({storageState:state,viewport:{width:1440,height:1000},serviceWorkers:'block',colorScheme:'light'});
 context.setDefaultTimeout(15000);context.setDefaultNavigationTimeout(20000);
 await context.addInitScript(()=>{window.__notificationPermissionCalls=0;if(window.Notification)Notification.requestPermission=()=>{window.__notificationPermissionCalls++;return Promise.resolve('denied');};});
 context.on('request',request=>{const u=new URL(request.url());if(/^https?:$/.test(u.protocol)&&u.origin!==origin.origin)report.externalRequests.push(u.origin+u.pathname);});
 await context.route('**/*',route=>{const u=new URL(route.request().url());return /^https?:$/.test(u.protocol)&&u.origin!==origin.origin?route.abort():route.continue();});
 page=await context.newPage();page.on('pageerror',error=>report.errors.push(redact(error.message)));
 page.on('console',message=>{if(message.type()==='error'&&/Content Security Policy|Refused to (execute|apply|load)/i.test(message.text()))report.errors.push(redact(message.text()));});
 await actualPreferences();await controlledDevices();
 // The last read is deliberately denied only in this browser route fixture.
 // It exercises clearing the UI without revoking the shared synthetic session.
 await page.route('**/api/v1/core/notification-preferences',route=>route.fulfill({status:401,contentType:'application/json',body:JSON.stringify({error:'Synthetic expired session'})}));
 await page.locator('[data-notification-refresh]').click();
 await page.waitForFunction(()=>/sign-in changed|Anmeldung wurde geändert/.test(document.querySelector('[data-notification-status]')?.textContent||''));
 must(await page.locator('[data-notification-devices]').innerText()===''&&await page.locator('[data-notification-preferences]').innerText()==='','Expired-session projection clears account/device settings');
 must(await page.locator('[data-notification-refresh]').isDisabled(),'Expired-session projection requires reopening after sign-in');
 must(report.errors.length===0,'No JavaScript or CSP errors');must(report.externalRequests.length===0,'No external frontend or vendor push requests');report.passed=true;
})().catch(async error=>{
 report.failure=redact(error.message||error);process.exitCode=1;
 if(page&&!page.isClosed()){
  report.failurePath=new URL(page.url()).pathname;
  report.visibleStatus=(await page.locator('[data-notification-status],.notification-form [role=status],.notification-device-category [role=status]').allTextContents()).map(redact);
  await fs.mkdir(output,{recursive:true});await capture('failure-notification-settings');
 }
}).finally(async()=>{
 clearTimeout(watchdog);if(browser)await browser.close();await fs.mkdir(output,{recursive:true});
 await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));
 console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length,physicalDeliveryTested:false,...(report.failure?{failure:report.failure}:{})}));
});
