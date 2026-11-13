'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server 8101');
const output=path.resolve('outputs/reuse-review');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.setDefaultNavigationTimeout(20000);page.on('pageerror',error=>report.errors.push(String(error)));
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);}
  await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);await login('Browser review only 1234');}
  await require('./browser-locale-helper.cjs').setBrowserLocale(page);
  const fixture=await page.evaluate(async()=>{
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;
   const create=async(entity,data)=>{const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const product=await create('products',{name:'Vollkornreis',brand:'Morgenhof',pack_quantity:'500',pack_unit:'g',favorite:true});
   const other=await create('products',{name:'Reis ohne Favorit'});
   const list=await create('lists',{name:'Meine wiederkehrende Grundausstattung '+Date.now(),is_template:true});
   return{product,other,list};
  });
  await page.goto(base+'/m/shopping/lists?q=is:template');
  await page.getByRole('link',{name:fixture.list.name,exact:false}).first().click();
  await page.getByText(/Vorlage · Artikel hier vorbereiten/).waitFor();
  must(await page.locator('#shopping-claim').count()===0,'Template exposes preparation without a live shopping claim');
  const quick=page.locator('#shopping-quick-add');
  await quick.locator('#shopping-favorites>summary').click();
  await quick.locator('#shopping-favorite[data-reference-state=ready]').waitFor();
  must(await quick.locator('#shopping-favorite option[value="'+fixture.other.id+'"]').count()===0,'Favorite picker excludes ordinary products');
  await quick.locator('#shopping-favorite').selectOption(fixture.product.id);
  let releaseFavorite;
  const favoriteGate=new Promise(resolve=>{releaseFavorite=resolve;});
  await page.route('**/api/v1/m/shopping/products/'+fixture.product.id,async route=>{await favoriteGate;await route.continue();});
  await page.getByRole('button',{name:'In Entwurf übernehmen',exact:true}).click();
  await quick.locator('[name=name]').fill('Mein neuer Entwurf');
  releaseFavorite();
  await page.getByText('Dein Entwurf wurde inzwischen geändert. Favorit bei Bedarf erneut übernehmen.',{exact:true}).waitFor();
  must(await quick.locator('[name=name]').inputValue()==='Mein neuer Entwurf','Delayed favorite response preserves a newer draft');
  await page.unroute('**/api/v1/m/shopping/products/'+fixture.product.id);
  await page.getByRole('button',{name:'In Entwurf übernehmen',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('#shopping-quick-add [name=name]').value==='Vollkornreis');
  must(await quick.locator('[name=quantity]').inputValue()==='1'&&await quick.locator('[name=unit_code]').inputValue()==='piece'&&await page.locator('[data-shopping-item]').count()===0,'Favorite creates a reviewed one-pack draft without writing an item');
  await capture('reuse-template-desktop');await page.setViewportSize({width:390,height:844});await capture('reuse-template-mobile');await page.setViewportSize({width:1440,height:1000});
  await quick.locator('[name=quantity]').fill('2');
  await quick.getByRole('button',{name:'Hinzufügen',exact:true}).click();
  await page.getByRole('link',{name:'Vollkornreis',exact:true}).waitFor();
  must(await page.getByRole('button',{name:'Erledigen: Vollkornreis',exact:true}).isDisabled(),'Template items cannot be checked as a live purchase');
  await page.getByRole('link',{name:'Neue Einkaufsliste erstellen',exact:true}).click();
  const copy=page.locator('#shopping-copy-form'),newName='Wochenende aus Vorlage '+Date.now();
  await copy.locator('[name=name]').fill(newName);
  await Promise.all([page.waitForURL(url=>url.pathname.startsWith('/m/shopping/lists/')&&!url.pathname.endsWith(fixture.list.id)),copy.getByRole('button',{name:'Neue Einkaufsliste erstellen',exact:true}).click()]);
  await page.getByRole('button',{name:'Erledigen: Vollkornreis',exact:true}).waitFor();
  must(await page.locator('#shopping-list-title').innerText()===newName&&!await page.getByRole('button',{name:'Erledigen: Vollkornreis',exact:true}).isDisabled(),'Copy redirects to an independent working list with unchecked articles');
  const child=await page.locator('[data-shopping-item]').getAttribute('data-shopping-item');
  must(await page.locator('[data-shopping-item]').innerText().then(text=>text.includes('2 Stück')&&text.includes('Morgenhof · 500 g')),'Copied amount and named pack survive the workflow');
  await page.getByRole('button',{name:'Erledigen: Vollkornreis',exact:true}).click();
  await page.getByRole('button',{name:'Wieder öffnen: Vollkornreis',exact:true,includeHidden:true}).waitFor({state:'attached'});
  const independent=await page.evaluate(async({id,child})=>{const data=await fetch('/api/v1/shopping/lists/'+id+'/items').then(r=>r.json());return data.records.length===1&&data.records[0].id!==child&&!data.records[0].checked_at;},{id:fixture.list.id,child});
  must(independent,'Checking the copied article does not change its template');
  await page.getByRole('button',{name:'Rückgängig',exact:true}).click();await page.getByRole('button',{name:'Erledigen: Vollkornreis',exact:true}).waitFor();await page.locator('#shopping-mode').check();
  async function capture(name){await page.evaluate(()=>scrollTo(0,0));const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  await capture('reuse-desktop');await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('reuse-mobile-dark');
  await page.getByRole('link',{name:'Vorlagen',exact:true}).click();
  must(new URL(page.url()).pathname==='/m/shopping/lists'&&new URL(page.url()).searchParams.get('q')==='is:template','Template tab leaves a list detail for the filtered overview');
  await page.goto(base+'/m/shopping/products/'+fixture.product.id);await page.getByRole('link',{name:'Favoriten',exact:true}).click();
  must(new URL(page.url()).pathname==='/m/shopping/products'&&new URL(page.url()).searchParams.get('q')==='is:favorite','Favorite tab leaves a product detail for the filtered overview');
  await page.waitForLoadState('networkidle');await page.evaluate(()=>Promise.all(document.getAnimations().map(animation=>animation.finished.catch(()=>{}))));
  const fits=await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1);
  if(!fits){report.overflow=await page.evaluate(()=>({document:document.documentElement.scrollWidth,viewport:innerWidth,elements:[...document.querySelectorAll('body *')].filter(el=>el.getBoundingClientRect().right>innerWidth+1).map(el=>({tag:el.tagName,id:el.id,class:el.className,width:el.getBoundingClientRect().width,right:el.getBoundingClientRect().right,text:el.textContent.slice(0,90)})).slice(0,25)}));await page.screenshot({path:path.join(output,'reuse-overflow.png'),fullPage:true});}
  must(fits,'Reuse mobile has no page overflow');must(!report.errors.length,'No reuse JavaScript errors');must(!report.externalRequests.length,'No external reuse requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
