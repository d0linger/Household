'use strict';
const fs=require('node:fs/promises');
const path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
const target=new URL(base);
if(!['localhost','127.0.0.1'].includes(target.hostname)||target.port!=='8101'||target.pathname!=='/')throw Error('Requires isolated review server8101');
const output=path.resolve('outputs/security-review');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(condition,message)=>{if(!condition)throw Error(message);report.checks.push(message);};
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  await fs.mkdir(output,{recursive:true});
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light'});
  await context.route('**/*',route=>{const url=new URL(route.request().url());if(url.origin!==new URL(base).origin){report.externalRequests.push(url.href);return route.abort();}return route.continue();});
  const page=await context.newPage();page.on('pageerror',error=>report.errors.push(String(error)));
  await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);
  const fixture=await page.evaluate(async()=>{
   const csrf=document.querySelector('meta[name="csrf-token"]').content;
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
   async function write(url,method,data,version){const response=await fetch(url,{method,headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,version,generation:bootstrap.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();}
   const record=await write('/api/v1/m/shopping/lists','POST',{name:'Wocheneinkauf',description:'private-audit-browser-sentinel'});
   await write('/api/v1/m/shopping/lists/'+record.id,'PATCH',{name:'Wocheneinkauf & Gäste',description:'changed-private-audit-browser-sentinel'},record.version);
   return record;
  });
  await page.goto(base+'/settings/audit?module=shopping&action=UPDATE&resource='+fixture.id);
  await page.getByRole('heading',{name:'Änderungsprotokoll',exact:true}).waitFor();
  must(await page.locator('.audit-entry').count()===1,'Resource/module/action filters isolate the actual update');
  const summary=page.locator('.audit-diff summary').first();await summary.focus();await page.keyboard.press('Enter');
  must(await page.locator('.audit-diff').first().getAttribute('open')!==null,'Keyboard opens before/after field comparison');
  const text=await page.locator('.audit-entry').innerText();must(text.includes('Wocheneinkauf')&&text.includes('Wocheneinkauf & Gäste')&&text.includes('Geschützt'),'Before/after names and protected-field marker visible');
  must(!text.includes('private-audit-browser-sentinel'),'Private note absent from rendered history');
  const api=await context.request.get(base+'/api/v1/core/audit?resource='+fixture.id);const raw=await api.text();
  must(api.status()===200&&api.headers()['cache-control']==='no-store','Audit API private and uncached');must(!raw.includes('private-audit-browser-sentinel')&&raw.includes('***REDACTED***'),'Secrets redacted before API serialization');
  async function shot(name,viewportOnly=false){const file=path.join(output,name+'.png');if(viewportOnly)await page.locator('.audit-entry').scrollIntoViewIfNeeded();else await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:file,fullPage:!viewportOnly,animations:'disabled'});report.screenshots.push(file);}
  async function axe(name){const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)}))});must(result.violations.length===0,'Audit accessibility: '+name);}
  await shot('after-audit-desktop');await axe('desktop expanded diff');
  await page.setViewportSize({width:390,height:844});await page.evaluate(()=>{localStorage.setItem('household-theme','dark');document.documentElement.dataset.theme='dark';});
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Expanded mobile audit has no horizontal page overflow');
  await shot('after-audit-mobile-dark',true);await axe('dark mobile expanded diff');
  await page.goto(base+'/settings');await shot('after-settings-mobile-dark');
  must(await page.getByRole('link',{name:'Protokoll',exact:true}).count()===1,'Settings exposes administrator history');
  await page.getByRole('link',{name:'Protokoll',exact:true}).click();await page.locator('[name=module]').selectOption('shopping');await page.locator('[name=action]').selectOption('DELETE');
  await Promise.all([page.waitForURL(/action=DELETE/),page.getByRole('button',{name:'Filtern',exact:true}).click()]);
  must(new URL(page.url()).searchParams.get('module')==='shopping','Native filter form preserves chosen module');
  await page.goto(base+'/settings/audit?request=00000000-0000-4000-8000-000000000000');must(await page.getByRole('heading',{name:'Keine passenden Einträge'}).count()===1,'Empty filter result offers recovery');
  await page.route('**/api/v1/m/fuel/reports/summary',route=>route.fulfill({status:422,contentType:'application/json',body:JSON.stringify({error:'Testgrenze: Bericht zu groß.'})}));
  await page.goto(base+'/m/fuel/reports');await page.getByText('Testgrenze: Bericht zu groß.',{exact:true}).waitFor();must(true,'Report overflow explanation reaches the interface as text');
  must(report.errors.length===0,'No JavaScript errors');must(report.externalRequests.length===0,'No external requests');
  report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-audit-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,screenshots:report.screenshots.length,accessibility:report.accessibility.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
