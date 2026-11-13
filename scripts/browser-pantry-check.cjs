'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server8101');
const workspace=path.resolve(__dirname,'..'),output=path.resolve(workspace,process.env.HOUSEHOLD_PANTRY_BROWSER_OUTPUT||'outputs/pantry-review');
if(!output.startsWith(path.join(workspace,'outputs')+path.sep))throw Error('Pantry browser evidence must remain inside workspace outputs');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(String(error)));
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.locator('form[action="/login"] button.primary').click()]);}
  await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.locator('form[action="/change-password"] button').click()]);await login('Browser review only 1234');}
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');}
  must(await page.locator('html').getAttribute('data-locale')==='de','German acceptance copy is explicitly selected after authentication');
  const fixture=await page.evaluate(async()=>{
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;
   if(bootstrap.states.pantry.state!=='enabled')throw Error('The isolated Pantry module must be enabled before this acceptance suite');
   const create=async(entity,data)=>{const response=await fetch('/api/v1/m/pantry/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states.pantry.generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const suffix=String(Date.now()),group='Vorratstest '+suffix;
   for(let n=0;n<51;n++)await create('products',{name:'Seitenprobe '+suffix+' '+n,unit_code:'g'});
   const shelf=await create('locations',{name:'Speisekammer '+suffix}),fridge=await create('locations',{name:'Kühlschrank '+suffix});
   const tea=await create('products',{name:group+' · Kräutertee aus dem Garten mit sorgfältig dokumentierter Herkunft und einer langen Produktbezeichnung',unit_code:'g',low_stock_threshold:'250'});
   await create('products',{name:group+' · Milch',unit_code:'l'});
   await create('products',{name:group+' · Linsen',unit_code:'kg',low_stock_threshold:'1'});
   const first=await create('lots',{product_id:tea.id,location_id:shelf.id,received_on:'2026-01-01',best_before_on:'2027-01-20',opened_on:'2026-01-02',notes:'Angebrochene Packung'});
   await create('stock_movements',{lot_id:first.id,quantity_delta:'9007199254.740993',reason:'receive'});
   const second=await create('lots',{product_id:tea.id,location_id:fridge.id,received_on:'2026-01-03',use_by_on:'2027-01-22',notes:'Zweite Charge'});
   await create('stock_movements',{lot_id:second.id,quantity_delta:'0.000007',reason:'receive'});
   return{group,suffix,tea,today:new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Vienna',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date())};
  });
  await page.goto(base+'/m/pantry/stock?q='+fixture.suffix);await page.locator('#pantry-status').getByText(/50 von/).waitFor();
  must(await page.locator('[data-pantry-product]').count()===50,'Product page is bounded to 50 records');
  await page.getByRole('button',{name:'Weitere Produkte',exact:true}).click();await page.locator('#pantry-status').getByText(/4 von 54/).waitFor();
  must(await page.locator('#pantry-products').evaluate(el=>el===document.activeElement),'Product paging moves keyboard focus to results');
  await page.getByRole('button',{name:'Vorherige Produkte',exact:true}).click();await page.locator('#pantry-status').getByText(/50 von/).waitFor();
  await page.getByLabel('Produkt suchen').fill(fixture.group);await page.getByRole('button',{name:'Anzeigen',exact:true}).click();await page.locator('#pantry-status').getByText(/3 von 3/).waitFor();
  must(await page.locator('#pantry-summary').innerText().then(text=>text.includes('9007199254,741 g')&&text.includes('0 kg')&&text.includes('0 l')),'Exact total strings and separate quantity units are displayed');
  const product=page.locator('[data-pantry-product="'+fixture.tea.id+'"]'),lotDetails=product.locator('details.pantry-lots'),lotToggle=product.locator('details.pantry-lots > summary');
  must(await lotDetails.count()===1&&await lotToggle.count()===1&&!(await lotDetails.evaluate(el=>el.open)),'Product has one initially collapsed lot disclosure');
  await lotToggle.focus();must(await lotToggle.evaluate(el=>el===document.activeElement),'Keyboard focus reaches the actual lot disclosure');
  await page.keyboard.press('Enter');await lotDetails.getByText('2 Chargen geladen.',{exact:true}).waitFor();must(await lotDetails.evaluate(el=>el.open),'Enter opens the focused lot disclosure');
  must(await product.getByRole('link',{name:'Angebrochene Packung',exact:true}).count()===1,'Keyboard disclosure loads named lot details');
  must(await product.innerText().then(text=>text.includes('Mindestens haltbar bis: 20.01.2027')&&text.includes('Zu verbrauchen bis: 22.01.2027')&&text.includes('Geöffnet: 02.01.2026')),'Best-before, use-by and opened dates keep distinct labels');
  const before=await page.locator('#pantry-products').innerText();await page.route('**/api/v1/pantry/stock?**',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Prüfverbindung unterbrochen'})}));
  await page.getByRole('button',{name:'Aktualisieren',exact:true}).click();await page.getByText(/Prüfverbindung unterbrochen/).waitFor();must(await page.locator('#pantry-products').innerText()===before,'Failed refresh preserves the previous stock display');
  must(await page.getByLabel('Produkt suchen').inputValue()===fixture.group,'Failed refresh preserves search input');await page.unroute('**/api/v1/pantry/stock?**');await page.getByRole('button',{name:'Aktualisieren',exact:true}).click();await page.locator('#pantry-status').getByText(/3 von 3/).waitFor();
  // A legacy SQL/import row can have no notes although the current form requires
  // a label. Exercise that API representation without changing production rules.
  await page.route('**/api/v1/pantry/products/*/lots?**',async route=>{const response=await route.fetch(),data=await response.json();if(data.records.length)data.records[0].name='';await route.fulfill({response,json:data});});
  must(!(await lotDetails.evaluate(el=>el.open)),'Refreshed lot disclosure starts collapsed');await lotToggle.click();await lotDetails.getByText('2 Chargen geladen.',{exact:true}).waitFor();
  must(await product.getByRole('link',{name:/^Charge vom 03.01.2026 · Kühlschrank/}).count()===1,'A legacy empty lot label has a named accessible detail link');
  await page.unroute('**/api/v1/pantry/products/*/lots?**');
  async function capture(name){await page.evaluate(async()=>{scrollTo(0,0);await new Promise(requestAnimationFrame);});const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  await page.evaluate(()=>window.HouseholdTheme.apply('light',{persist:false}));await capture('pantry-desktop');await page.setViewportSize({width:390,height:844});await page.evaluate(()=>window.HouseholdTheme.apply('dark',{persist:false}));await capture('pantry-mobile-dark');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Long product name and exact quantities fit the mobile layout');
  await page.getByLabel('Bestand am Tagesende').fill(fixture.today);await page.getByRole('button',{name:'Anzeigen',exact:true}).click();await page.locator('#pantry-history-note:not([hidden])').waitFor();
  must(await page.locator('#pantry-summary').innerText().then(text=>text.includes('Europe/Vienna')&&text.includes('9007199254,741 g')),'Historical date explicitly shows household timezone and exact stock');
  must(await page.locator('#pantry-history-note').innerText().then(text=>text.includes('heutigen beschreibenden Stand')&&text.includes('Buchungszeitpunkt')),'Historical metadata and booking-time limitations are visible');
  await capture('pantry-history-mobile-dark');
  await page.reload();await page.locator('#pantry-status').getByText(/3 von 3/).waitFor();must(await page.getByLabel('Produkt suchen').inputValue()===fixture.group&&await page.getByLabel('Bestand am Tagesende').inputValue()===fixture.today,'Reload preserves the chosen search and historical date');
  await page.getByLabel('Produkt suchen').fill('Kein Treffer '+fixture.group);await page.getByRole('button',{name:'Anzeigen',exact:true}).click();await page.getByText('Keine Produkte gefunden. Suchbegriff oder Stichtag anpassen.',{exact:true}).waitFor();must(await page.locator('[data-pantry-product]').count()===0,'Empty search provides a clear next step');
  must(!report.errors.length,'No pantry JavaScript errors');must(!report.externalRequests.length,'No external pantry requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
