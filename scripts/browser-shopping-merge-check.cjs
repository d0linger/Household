'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server8101');
const output=path.resolve('outputs/merge-review'),report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(String(error)));
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);}
  await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);await login('Browser review only 1234');}
  const fixture=await page.evaluate(async()=>{
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;
   const create=async(entity,data)=>{const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const p=await create('products',{name:'Mehl',brand:'Morgenhof',pack_quantity:'500',pack_unit:'g'}),other=await create('products',{name:'Mehl',brand:'Andere Mühle',pack_quantity:'500',pack_unit:'g'}),list=await create('lists',{name:'Geprüft zusammenführen '+Date.now()});
   const a=await create('items',{list_id:list.id,product_id:p.id,name:'Mehl für Brot',quantity:'2',unit_code:'piece',note:'Für Samstag'}),b=await create('items',{list_id:list.id,product_id:p.id,name:'Mehl zusätzlich',quantity:'1',unit_code:'kg',note:'Für Samstag'}),c=await create('items',{list_id:list.id,product_id:other.id,name:'Andere Mehlvariante',quantity:'1',unit_code:'piece',note:'Für Samstag'});
   return{a,b,c,list,generation:bootstrap.states.shopping.generation};
  });
  await page.goto(base+'/m/shopping/lists/'+fixture.list.id);await page.getByRole('button',{name:'Erledigen: Mehl für Brot',exact:true}).waitFor();await page.locator('#shopping-mode').check();await page.locator('#shopping-merge>summary').click();
  const keep=page.locator('#shopping-merge-keep'),remove=page.locator('#shopping-merge-remove'),comparison=page.locator('#shopping-merge-comparison'),review=page.locator('#shopping-merge-review');
  await keep.selectOption(fixture.a.id);await remove.selectOption(fixture.a.id);await review.click();
  await page.getByText('Zwei unterschiedliche offene Artikel auswählen.',{exact:true}).waitFor();must(await keep.evaluate(el=>el===document.activeElement),'Invalid self-selection returns focus to the first article');
  await remove.selectOption(fixture.c.id);await review.click();await comparison.getByText(/Unterschiede in den Artikeldetails prüfen/).waitFor();
  must(await comparison.getByRole('button',{name:'Geprüft zusammenführen'}).count()===0,'Different product variants cannot be confirmed');
  must(await comparison.innerText().then(text=>text.includes('Andere Mühle')&&text.includes('Morgenhof')),'Comparison names both conflicting brands');
  await remove.selectOption(fixture.b.id);await review.click();await comparison.getByText(/Ergebnis: 4 Stück/).waitFor();
  must(await comparison.evaluate(el=>el===document.activeElement),'Ready comparison receives keyboard focus');
  async function capture(name){await page.evaluate(async()=>{scrollTo(0,0);await new Promise(requestAnimationFrame);});const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  await capture('merge-comparison-desktop');await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('merge-comparison-mobile-dark');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Merge comparison has no mobile page overflow');
  await page.evaluate(async fixture=>{const response=await fetch('/api/v1/m/shopping/items/'+fixture.b.id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{quantity:'2'},version:fixture.b.version,generation:fixture.generation})});if(!response.ok)throw Error(await response.text());},fixture);
  await comparison.getByRole('button',{name:'Geprüft zusammenführen',exact:true}).click();await page.locator('#shopping-merge-status').getByText(/Ergebnis prüfen/).waitFor();
  const unchanged=await page.evaluate(async fixture=>{const result=await fetch('/api/v1/shopping/lists/'+fixture.list.id+'/items').then(r=>r.json());return result.records.length===3&&result.records.find(r=>r.id===fixture.a.id).quantity==='2.000000'&&result.records.find(r=>r.id===fixture.b.id).quantity==='2.000000';},fixture);
  must(unchanged,'An edit after preview rejects the merge without partial changes');
  await review.click();await comparison.getByText(/Ergebnis: 6 Stück/).waitFor();await comparison.getByRole('button',{name:'Geprüft zusammenführen',exact:true}).click();await page.getByText('Artikel zusammengeführt.',{exact:true}).waitFor();
  must(await page.locator('[data-shopping-item]').count()===2&&await page.locator('[data-shopping-item="'+fixture.a.id+'"]').innerText().then(text=>text.includes('6 Stück')),'Confirmed merge preserves the survivor and exact combined pack quantity');
  must(await page.locator('[data-shopping-item="'+fixture.b.id+'"]').count()===0,'Confirmed merge removes only the reviewed second position');
  must(await review.evaluate(el=>el===document.activeElement),'Successful merge restores focus to the review control');
  must(!report.errors.length,'No merge JavaScript errors');must(!report.externalRequests.length,'No external merge requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
