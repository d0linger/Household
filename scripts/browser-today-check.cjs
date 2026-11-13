'use strict';

const fs=require('node:fs/promises');
const path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState}=require('./notification-browser-fixture.cjs');
const root=path.resolve(__dirname,'..'),origin=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
if(origin!=='http://localhost:8101')throw Error('Today browser checks require the isolated localhost:8101 fixture');
const output=path.resolve(root,process.env.HOUSEHOLD_TODAY_BROWSER_OUTPUT||('outputs/ux-workspace/today-'+Date.now()));
if(!output.startsWith(path.join(root,'outputs')+path.sep))throw Error('Evidence must stay inside project outputs');
const report={passed:false,checks:[],accessibility:[],screenshots:[],errors:[],externalRequests:[],controlledFixtures:[],scope:'Actual three-role Today page/API plus DE/EN, four themes, 320/1440px. Intercepted projections separately test UI states; domain ownership is tested by Go integration.'};
const secrets=[];
const layoutOnly=process.env.HOUSEHOLD_TODAY_LAYOUT_ONLY==='1';
if(layoutOnly)report.scope='Post-polish Today layout check: actual administrator API, DE/EN, four themes, 320/1440px. Full three-role and controlled UI functionality is recorded in the preceding full campaign.';
let browser,watchdog;
const redact=text=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(text));
const must=(value,label)=>{if(!value)throw Error(label);report.checks.push(label);};
async function settle(page){
 await page.evaluate(async()=>{
  await Promise.race([document.fonts.ready,new Promise((_,reject)=>setTimeout(()=>reject(Error('Fonts did not settle')),5000))]);
  const end=performance.now()+5000;
  while(document.getAnimations().some(a=>a.playState==='running'&&a.playbackRate>0&&Number.isFinite(a.effect?.getComputedTiming().endTime))){
   if(performance.now()>end)throw Error('Finite motion did not settle');
   await new Promise(resolve=>setTimeout(resolve,40));
  }
 });
}
async function ready(page){await page.locator('#today').waitFor();await page.waitForFunction(()=>document.querySelector('#today-refresh')?.disabled===false&&!document.querySelector('#today [aria-busy="true"]'));}

(async()=>{
 await fs.mkdir(output,{recursive:true});
 if(!process.env.HOUSEHOLD_BROWSER_HANDOFF)throw Error('Owned fixture handoff is required');
 browser=await chromium.launch({headless:true,...(process.platform==='win32'?{channel:'msedge'}:{}),args:['--disable-background-networking','--disable-component-update','--no-first-run']});
 watchdog=setTimeout(()=>{report.failure='Today browser deadline exceeded';process.exitCode=1;browser.close();},600000);
 for(const role of (layoutOnly?['admin']:['admin','editor','reader'])){
  const handoff=role==='admin'?process.env.HOUSEHOLD_BROWSER_HANDOFF:path.join(path.dirname(process.env.HOUSEHOLD_BROWSER_HANDOFF),'handoff-'+role+'.json');
  const state=fixtureState(root,secrets,{env:{...process.env,HOUSEHOLD_BROWSER_HANDOFF:handoff}});
  const context=await browser.newContext({storageState:state,serviceWorkers:'block',reducedMotion:'reduce',viewport:{width:1440,height:1000}});
  await context.route('**/*',route=>{const url=new URL(route.request().url());if(['http:','https:'].includes(url.protocol)&&url.origin!==origin){report.externalRequests.push(url.origin+url.pathname);return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error.message)));
  const response=await page.goto(origin+'/');must(response?.status()===200&&new URL(page.url()).pathname==='/',role+': real home renders Today without module redirect');await ready(page);
  const real=await page.evaluate(async()=>{const [identity,overview]=await Promise.all([fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json()),fetch('/api/v1/core/today',{cache:'no-store'})]);return {role:identity.user.role,status:overview.status,cache:overview.headers.get('cache-control'),data:await overview.json()};});
  must(real.role===role,role+': actual authenticated role');must(real.status===200&&real.cache.includes('no-store'),role+': actual private network-only Today API');
  must(/^\d{4}-\d{2}-\d{2}$/.test(real.data.day)&&Boolean(real.data.timezone),role+': actual household day and time zone');
  for(const [name,limit] of Object.entries({tasks:6,notifications:6,cooking:3,meals:6})){const section=real.data[name];must(section&&['ready','disabled'].includes(section.state)&&section.items.length<=limit,role+': actual bounded '+name+' projection');}
  must(await page.locator('#today-error').isHidden(),role+': actual overview loaded');
  await page.locator('#today-refresh').focus();must(await page.locator('#today-refresh').evaluate(el=>el===document.activeElement),role+': refresh keyboard reachable');
  await page.keyboard.press('Enter');await ready(page);must(await page.locator('#today-error').isHidden(),role+': keyboard refresh succeeds');
  must(await page.locator('#today-shortcuts-title').count()===1,role+': personal module shortcuts available');
  if(role==='admin'){
   for(const language of ['de','en']){
    if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value="'+language+'"]').click();await page.waitForFunction(lang=>document.documentElement.dataset.locale===lang,language);await ready(page);}
    must(await page.locator('#today-title').innerText()===(language==='de'?'Heute':'Today'),'Localized Today heading '+language);
    must(await page.locator('#today-tasks-title').innerText()===(language==='de'?'Deine Aufgaben':'Your tasks'),'Localized tasks heading '+language);
    for(const width of [320,1440])for(const theme of ['light','dark','pink-light','pink-dark']){
     await page.setViewportSize({width,height:1000});await page.evaluate(theme=>window.HouseholdTheme.apply(theme,{persist:false}),theme);await settle(page);
     const label=language+'-'+theme+'-'+width;must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),label+': no document overflow');
     await page.locator('#today-refresh').focus();must(await page.locator('#today-refresh').evaluate(el=>{const s=getComputedStyle(el);return s.outlineStyle!=='none'&&parseFloat(s.outlineWidth)>0;}),label+': visible keyboard focus');
     await page.screenshot({path:path.join(output,label+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(label+'.png');
     const axe=await new AxeBuilder({page}).include('#today').analyze();report.accessibility.push({label,violations:axe.violations.map(v=>({id:v.id,impact:v.impact,nodes:v.nodes.length}))});must(axe.violations.length===0,label+': Today Axe has no violations');
    }
   }
   if(layoutOnly){await context.close();continue;}
   const fixture={day:'2026-10-25',timezone:'Europe/Vienna',tasks:{state:'ready',items:[],more:false},notifications:{state:'ready',items:[{id:'fixture',title:'<img src=x onerror=alert(1)>',url:'javascript:alert(1)',read:false}],more:true},cooking:{state:'disabled',items:[],more:false},meals:{state:'error',items:[],more:false}};
   const projected=route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(fixture)});
   await page.route('**/api/v1/core/today',projected);await page.locator('#today-refresh').click();await ready(page);
   must(await page.locator('#today-notifications .today-item-title').textContent()==='Unread · <img src=x onerror=alert(1)>','Controlled title is literal text with unread label');
   must(await page.locator('#today-notifications img').count()===0,'Controlled title never creates markup');must(await page.locator('#today-notifications a').getAttribute('href')==='/notifications','Controlled unsafe link falls back to inbox');
   must(await page.locator('#today-meals .today-partial').count()===1,'Controlled partial meal error is explicit');must(await page.locator('#today-cooking').innerText()==='The cookbook is not enabled in this household.','Controlled disabled module is explicit');
   must(await page.locator('#today-tasks').innerText()==='You have no open tasks due today.','Controlled empty assigned task state is explicit');
   must((await page.locator('#today-status').innerText()).includes('Part of the overview'),'Controlled partial status retains healthy sections');report.controlledFixtures.push('Partial/empty/disabled projection with literal malicious title and unsafe URL');
   await page.unroute('**/api/v1/core/today',projected);
   const unavailable=route=>route.fulfill({status:503,body:'Unavailable'});await page.route('**/api/v1/core/today',unavailable);await page.locator('#today-refresh').click();await ready(page);
   must(await page.locator('#today-error').isVisible(),'Controlled HTTP error visible');must(await page.locator('#today .today-list').count()===0,'Failed refresh removes previous private values');report.controlledFixtures.push('HTTP503 retry clears prior data');
   await page.unroute('**/api/v1/core/today',unavailable);await page.locator('#today-refresh').click();await ready(page);must(await page.locator('#today-error').isHidden(),'Actual API retry recovers');
   await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pagehide')));must(await page.locator('#today .today-list').count()===0,'Pagehide clears private overview');
   await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));await ready(page);must(await page.locator('#today-error').isHidden(),'Persisted pageshow refreshes actual API');report.controlledFixtures.push('Synthetic lifecycle events exercise clear/reload hooks');
  }
  await context.close();
 }
 must(report.errors.length===0,'No browser runtime errors');must(report.externalRequests.length===0,'No external requests');report.passed=true;
})().catch(error=>{report.failure=redact(error.message);process.exitCode=1;}).finally(async()=>{clearTimeout(watchdog);if(browser)await browser.close();await fs.mkdir(output,{recursive:true});await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length,output,failure:report.failure}));});
