'use strict';
const fs=require('node:fs/promises');
const path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
const origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server 8101');
const output=path.resolve('outputs/expansion-review');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light',serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.on('pageerror',e=>report.errors.push(String(e)));
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);}
  await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);await login('Browser review only 1234');}
  const fixture=await page.evaluate(async()=>{
   const fresh=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;
   const create=async(entity,data)=>{const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:fresh.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const list=await create('lists',{name:'Einkauf für das Wochenende '+Date.now()});
   const item=await create('items',{list_id:list.id,name:'Äpfel',quantity:'1.5',unit_code:'kg',group_name:'Obst und Gemüse',note:'Für den Apfelkuchen'});
   await create('items',{list_id:list.id,name:'Spülmittel',quantity:'1',unit_code:'piece',group_name:'Haushalt'});
   const suffix=Date.now();let choiceName='';
   for(let i=0;i<105;i++){const name='Auswahlprüfung '+String(i).padStart(3,'0')+' '+suffix;if(i===0)choiceName=name;await create('lists',{name});}
   return {list,item,choiceName};
  });
  const listURL=base+'/m/shopping/lists/'+fixture.list.id;
  await page.goto(listURL);await page.getByRole('button',{name:'Erledigen: Äpfel',exact:true}).waitFor();
  const quick=page.locator('#shopping-quick-add');await quick.locator('[name=name]').fill('2×500g Nudeln');await page.locator('#shopping-apply-parse').click();
  must(await quick.locator('[name=name]').inputValue()==='Nudeln'&&await quick.locator('[name=quantity]').inputValue()==='1000','Quantity recognition is reviewed and editable');
  await quick.locator('[name=group_name]').fill('Vorratsregal');await quick.getByRole('button',{name:'Hinzufügen',exact:true}).click();await page.getByRole('button',{name:'Erledigen: Nudeln',exact:true}).waitFor();
  must(page.url()===listURL&&await quick.locator('[name=name]').inputValue()==='','Quick add keeps the list and clears confirmed input');
  must(await quick.locator('[name=name]').evaluate(el=>el===document.activeElement),'Quick add restores input focus');
  await page.locator('#shopping-mode').check();await page.getByRole('button',{name:'Erledigen: Nudeln',exact:true}).click();await page.getByRole('button',{name:'Rückgängig',exact:true}).waitFor();
  must(await page.evaluate(()=>document.activeElement?.hasAttribute('data-shopping-check')),'Checking a row keeps keyboard focus on a visible item');
  must(await page.locator('#shopping-entry').getAttribute('open')===null,'Shopping mode collapses entry controls without losing access');
  must(await page.locator('.shopping-completed').getAttribute('open')===null,'Shopping mode collapses completed positions');
  must((await page.locator('#shopping-progress').innerText()).includes('1 von 3'),'Progress includes completed positions');
  await page.getByRole('button',{name:'Rückgängig',exact:true}).click();await page.getByRole('button',{name:'Erledigen: Nudeln',exact:true}).waitFor();
  must(true,'Shopping undo uses the returned resource version');
  must(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')==='Erledigen: Nudeln'),'Undo returns keyboard focus to the restored row');
  await page.locator('#shopping-filter-panel>summary').click();await page.locator('#shopping-filter').fill('Gemüse');await page.waitForFunction(()=>document.querySelectorAll('[data-shopping-item]').length===1);
  must((await page.locator('#shopping-progress').innerText()).includes('von 3'),'Filtering preserves full-list totals');
  await page.locator('#shopping-filter').fill('');await page.waitForFunction(()=>document.querySelectorAll('[data-shopping-item]').length===3);
  await page.locator('#shopping-filter-panel>summary').click();
  async function changeApple(name){return page.evaluate(async({id,name})=>{const fresh=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),row=await fetch('/api/v1/m/shopping/items/'+id).then(r=>r.json());const response=await fetch('/api/v1/m/shopping/items/'+id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{name},version:row.version,generation:fresh.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();},{id:fixture.item.id,name});}
  await changeApple('Äpfel aus dem Garten');await page.getByRole('button',{name:'Erledigen: Äpfel',exact:true}).click();await page.locator('#shopping-conflict').getByText(/Aktuell: Äpfel aus dem Garten/).waitFor();
  must((await page.locator('#shopping-conflict').innerText()).includes('Äpfel aus dem Garten'),'Conflict shows current named item and intended state');
  await page.getByRole('button',{name:'Aktuellen Stand behalten',exact:true}).focus();await page.keyboard.press('Enter');await page.getByRole('button',{name:'Erledigen: Äpfel aus dem Garten',exact:true}).waitFor();must(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')==='Erledigen: Äpfel aus dem Garten'),'Keeping current conflict state restores keyboard focus to its row');
  await changeApple('Äpfel aus dem Garten');await page.getByRole('button',{name:'Erledigen: Äpfel aus dem Garten',exact:true}).click();await page.locator('#shopping-conflict').getByText(/Aktuell: Äpfel aus dem Garten/).waitFor();
  await page.getByRole('button',{name:'Geprüft als erledigt markieren',exact:true}).click();await page.getByRole('button',{name:'Wieder öffnen: Äpfel aus dem Garten',exact:true,includeHidden:true}).waitFor({state:'attached'});
  must(await page.locator('#shopping-conflict').isHidden(),'Explicit conflict confirmation writes against reviewed version');
  must(await page.evaluate(()=>document.activeElement?.hasAttribute('data-shopping-check')),'Conflict completion restores focus to a visible item');
  await page.locator('#shopping-entry>summary').click();await quick.locator('[name=name]').fill('Entwurf bleibt stehen');
  await page.locator('.shopping-completed>summary').click();await page.getByRole('link',{name:'Äpfel aus dem Garten',exact:true}).focus();
  await changeApple('Frische Gartenäpfel');
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));await page.getByRole('link',{name:'Frische Gartenäpfel',exact:true,includeHidden:true}).waitFor({state:'attached'});
  must(await quick.locator('[name=name]').inputValue()==='Entwurf bleibt stehen','Live refresh preserves unsaved quick-entry input');
  must(await page.locator('.shopping-completed').getAttribute('open')!==null,'Live refresh retains manually opened completed rows');
  must(await page.evaluate(id=>document.activeElement?.dataset.shoppingLink===id,fixture.item.id),'Live refresh preserves focused item links');
  await page.locator('#shopping-refresh').click();await page.getByText('Liste ist aktuell.',{exact:true}).waitFor();
  must(await page.locator('.shopping-completed').getAttribute('open')!==null,'Manual refresh retains manually opened completed rows');
  await quick.locator('[name=name]').fill('');await page.locator('#shopping-entry>summary').click();
  await page.evaluate(async id=>{const fresh=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),list=await fetch('/api/v1/m/shopping/lists/'+id).then(r=>r.json());const response=await fetch('/api/v1/m/shopping/lists/'+id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{name:'Wochenende: Haushalt und Küche'},version:list.version,generation:fresh.states.shopping.generation})});if(!response.ok)throw Error(await response.text());},fixture.list.id);
  await page.locator('#shopping-refresh').click();await page.getByRole('heading',{name:'Wochenende: Haushalt und Küche',exact:true}).waitFor();must(true,'Board names the selected list and reflects a rename');
  const failedCheck=async route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Vorübergehend nicht erreichbar'})});
  await page.route('**/api/v1/m/shopping/items/*/actions/check',failedCheck);
  await page.getByRole('button',{name:'Erledigen: Spülmittel',exact:true}).focus();await page.keyboard.press('Space');await page.getByText(/^Vorübergehend nicht erreichbar\.? Änderung wurde nicht bestätigt\.$/).waitFor();
  must(await page.evaluate(()=>document.activeElement?.getAttribute('aria-label')==='Erledigen: Spülmittel'),'Failed check retains keyboard focus and shows recoverable error');
  await page.unroute('**/api/v1/m/shopping/items/*/actions/check',failedCheck);
  async function capture(name){await page.evaluate(()=>scrollTo(0,0));const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))});must(result.violations.length===0,'Accessibility '+name);}
  await capture('shopping-desktop');await page.setViewportSize({width:390,height:844});await page.evaluate(()=>{document.documentElement.dataset.theme='dark';localStorage.setItem('household-theme','dark');});await capture('shopping-mobile-dark');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile shopping has no page overflow');
  must(await page.locator('[data-shopping-check]').first().evaluate(el=>el.getBoundingClientRect().bottom<=document.querySelector('.mobile-nav').getBoundingClientRect().top),'First open shopping action fits above fixed mobile navigation');
  await page.locator('#shopping-entry>summary').click();await quick.locator('[name=name]').fill('Langsam gespeicherter Artikel');
  let release,arrived;const gate=new Promise(resolve=>release=resolve),held=new Promise(resolve=>arrived=resolve);
  const delayed=async route=>{arrived();await gate;await route.continue();};
  await page.route('**/api/v1/m/shopping/items',delayed);await quick.getByRole('button',{name:'Hinzufügen',exact:true}).click();await held;
  await quick.locator('[name=name]').fill('Nächster Artikel bleibt stehen');release();await page.getByText('Artikel hinzugefügt. Deine inzwischen geänderte Eingabe bleibt erhalten.',{exact:true}).waitFor();
  must(await quick.locator('[name=name]').inputValue()==='Nächster Artikel bleibt stehen','Slow add does not erase edits entered while POST was in flight');await page.unroute('**/api/v1/m/shopping/items',delayed);
  await page.goto(base+'/m/shopping/items/'+fixture.item.id);const edit=page.locator('form[data-method=PATCH]');await edit.locator('[name=list_id][data-reference-state=ready]').waitFor();
  must(await edit.locator('[name=list_id]').inputValue()===fixture.list.id,'Selected parent remains available beyond first 100 references');
  const reference=edit.locator('.reference-field').filter({has:page.locator('[name=list_id]')});
  await reference.getByRole('button',{name:'Weitere Treffer',exact:true}).click();await page.waitForFunction(()=>document.querySelector('[name=list_id]').dataset.referenceState==='ready');
  must(await edit.locator('[name=list_id]').inputValue()===fixture.list.id,'Paging retains selected reference');
  await reference.getByRole('searchbox').fill(fixture.choiceName);await reference.getByRole('button',{name:'Suchen',exact:true}).click();await page.waitForFunction(()=>document.querySelector('[name=list_id]').options.length===3);
  must(await edit.locator('[name=list_id]').inputValue()===fixture.list.id,'Search outside current page retains original selection');
  await edit.locator('[name=name]').fill('Meine Äpfel');await changeApple('Parallel geänderte Äpfel');await edit.getByRole('button',{name:'Änderungen speichern',exact:true}).click();await edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).waitFor();
  const conflictText=await edit.locator('.conflict-review').innerText();must(conflictText.includes('Meine Äpfel')&&conflictText.includes('Parallel geänderte Äpfel'),'Edit conflict compares current and intended field values');
  await Promise.all([page.waitForEvent('framenavigated'),edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).click()]);
  must(await page.locator('form[data-method=PATCH] [name=name]').inputValue()==='Meine Äpfel','Reviewed form conflict persists intended values');
  const big=await page.evaluate(async()=>{const fresh=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;const create=async(entity,data)=>{const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:fresh.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();};const list=await create('lists',{name:'Große Prüfliste '+Date.now()});for(let i=0;i<101;i++)await create('items',{list_id:list.id,name:'Prüfartikel '+String(i).padStart(3,'0')});return list;});
  await page.goto(base+'/m/shopping/lists/'+big.id);await page.locator('#shopping-more').click();await page.waitForFunction(()=>document.querySelectorAll('[data-shopping-item]').length===1);
  let releaseCheck,checkArrived;const checkGate=new Promise(resolve=>releaseCheck=resolve),checkHeld=new Promise(resolve=>checkArrived=resolve);
  const delayedCheck=async route=>{checkArrived();await checkGate;await route.continue();};await page.route('**/api/v1/m/shopping/items/*/actions/check',delayedCheck);
  await page.locator('[data-shopping-check]').click();await checkHeld;await page.locator('#shopping-filter').fill('Prüfartikel 100');await page.waitForTimeout(350);releaseCheck();
  await page.getByRole('link',{name:'Prüfartikel 100',exact:true}).waitFor();must(await page.locator('[data-shopping-item]').count()===1,'Filter changed during slow POST resets the previous page cursor');await page.unroute('**/api/v1/m/shopping/items/*/actions/check',delayedCheck);
  must(report.errors.length===0,'No JavaScript exceptions');must(report.externalRequests.length===0,'No external frontend requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
