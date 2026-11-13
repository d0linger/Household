'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState}=require('./notification-browser-fixture.cjs');
const root=path.resolve(__dirname,'..'),origin=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
if(origin!=='http://localhost:8101')throw Error('Documents navigation requires the owned localhost:8101 fixture');
const output=path.resolve(root,process.env.HOUSEHOLD_DOCUMENTS_NAVIGATION_OUTPUT||('outputs/ux-workspace/documents-context-'+Date.now()));
if(!output.startsWith(path.join(root,'outputs')+path.sep))throw Error('Evidence must remain inside project outputs');
const report={passed:false,checks:[],accessibility:[],screenshots:[],errors:[],externalRequests:[],scope:'Actual fixture document, explicit source-list return, record tools, keyboard draft protection and closed query authority. No production or physical-device acceptance.'};
const secrets=[];let browser,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(condition,label)=>{if(!condition)throw Error(label);report.checks.push(label);};
(async()=>{
 await fs.mkdir(output,{recursive:true});const state=fixtureState(root,secrets);
 browser=await chromium.launch({headless:true,...(process.platform==='win32'?{channel:'msedge'}:{}),args:['--disable-background-networking','--disable-component-update']});
 watchdog=setTimeout(()=>{report.failure='Document navigation browser deadline exceeded';process.exitCode=1;void browser.close();},360000);
 const context=await browser.newContext({storageState:state,serviceWorkers:'block',reducedMotion:'reduce',viewport:{width:1440,height:1000}});
 await context.route('**/*',route=>{const url=new URL(route.request().url());if(url.origin!==origin){report.externalRequests.push(url.origin+url.pathname);return route.abort();}return route.continue();});
 const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error.message)));
 let dialogs=0;page.on('dialog',dialog=>{dialogs++;void dialog.dismiss();});
 async function ready(){await page.waitForFunction(()=>document.querySelector('#documents-workspace')?.getAttribute('aria-busy')==='false');must(await page.locator('#doc-error').isHidden(),'Actual authorized document view loaded');}
 await page.goto(origin+'/m/documents/workspace/inbox');await ready();
 if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value="de"]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
 must(await page.locator('.workspace-primary a').count()===3,'Documents show three primary destinations');must(await page.locator('.doc-nav').count()===0,'Old seven-item document navigation removed');
 await page.locator('#doc-new').click();await ready();must(new URL(page.url()).searchParams.get('from')==='inbox','Creating a document retains its actual Inbox origin');
 const title='UX document '+Date.now();await page.locator('#doc-content [name=title]').fill(title);
 await Promise.all([page.waitForURL(url=>url.searchParams.has('id')),page.getByRole('button',{name:'Dokument anlegen',exact:true}).click()]);await ready();
 const id=new URL(page.url()).searchParams.get('id');must(/^[0-9a-f-]{36}$/.test(id),'Actual document saved through the existing form');
 must(await page.locator('#doc-selection-title').textContent()===title,'Authorized selected document title is explicit');
 must(await page.locator('.workspace-primary [aria-current=page]').getAttribute('data-doc-pane')==='library','Document review retains the Library primary group');
 must(await page.locator('#doc-selection-tools a').count()===7,'All seven actual document tools are attached to selected record');
 const apiQueries=[];page.on('request',request=>{const url=new URL(request.url());if(url.pathname.startsWith('/api/v1/documents/'))apiQueries.push(url.searchParams);});
 for(const pane of ['inbox','library','deadlines','processing','duplicates']){
  await page.goto(origin+'/m/documents/workspace/review?id='+id+'&from='+pane);await ready();
  must(await page.locator('#doc-selection-back').getAttribute('href')==='/m/documents/workspace/'+pane,'Explicit return link preserves '+pane+' origin');
 }
 must(apiQueries.every(query=>!query.has('from')),'Page return context is never forwarded to business APIs');
 for(const suffix of ['from=rules','from=https%3A%2F%2Fother.invalid','from=inbox&from=library']){
  const response=await context.request.get(origin+'/m/documents/workspace/review?id='+id+'&'+suffix);must(response.status()===422,'Invalid page-only return context rejected: '+suffix);
 }
 const apiRejected=await context.request.get(origin+'/api/v1/documents/detail?id='+id+'&from=inbox');must(apiRejected.status()===422,'Business detail API retains its closed query contract');
 let release;const held=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/v1/documents/detail?*',async route=>{await held;await route.continue();});
 await page.goto(origin+'/m/documents/workspace/review?id='+id+'&from=library');
 must(await page.locator('#doc-selection-context').isHidden(),'Selected title stays hidden until authorized detail response completes');release();await ready();await page.unroute('**/api/v1/documents/detail?*');
 await page.locator('#doc-content [name=notes]').fill('Unsaved document context draft');
 const dialogsBefore=dialogs;await page.locator('#doc-selection-tools a[href="#doc-tool-deadlines"]').focus();await page.keyboard.press('Enter');
 must(await page.locator('#doc-tool-deadlines').evaluate(el=>el===document.activeElement),'Keyboard tool activation focuses its actual record section');
 must(dialogs===dialogsBefore&&await page.locator('#doc-content [name=notes]').inputValue()==='Unsaved document context draft','In-document tool keeps unsaved draft without discard prompt');
 const before=page.url();await page.locator('[data-workspace-manage]').click();must(dialogs===dialogsBefore+1&&page.url()===before,'Cancelled Manage navigation remains on the same document');
 await page.locator('#doc-selection-back').click();must(dialogs===dialogsBefore+2&&page.url()===before,'Cancelled return navigation protects the same draft');
 must(await page.locator('#doc-content [name=notes]').inputValue()==='Unsaved document context draft','Both cancelled navigations preserve actual form values');
 await Promise.all([page.waitForNavigation(),page.getByRole('button',{name:'Prüfung speichern',exact:true}).click()]);await ready();
 must(new URL(page.url()).searchParams.get('from')==='library','Saving retains the explicit return origin');
 for(const [width,theme]of[[1440,'dark'],[320,'pink-light']]){
  await page.setViewportSize({width,height:1000});await page.evaluate(theme=>window.HouseholdTheme.apply(theme,{persist:false}),theme);await page.locator('.workspace-primary a').first().focus();await page.evaluate(()=>window.scrollTo(0,0));
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Record context fits '+width+'px');const label=theme+'-'+width;
  await page.screenshot({path:path.join(output,label+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(label+'.png');
  const axe=await new AxeBuilder({page}).include('#documents-workspace').analyze();report.accessibility.push({label,violations:axe.violations.map(v=>({id:v.id,impact:v.impact,nodes:v.nodes.length}))});must(axe.violations.length===0,'Record context Axe '+label);
 }
 await page.locator('#doc-selection-back').click();await ready();must(new URL(page.url()).pathname==='/m/documents/workspace/library','Saved document returns through the real context link');
 must(report.errors.length===0,'No browser runtime errors');must(report.externalRequests.length===0,'No external frontend requests');report.passed=true;
 await context.close();
})().catch(error=>{report.failure=redact(error.stack||error);process.exitCode=1;}).finally(async()=>{
 clearTimeout(watchdog);await browser?.close();await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,axe:report.accessibility.length,report:path.join(output,'browser-report.json'),failure:report.failure}));
});
