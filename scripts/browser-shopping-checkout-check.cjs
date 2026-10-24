'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server8101');
const output=path.resolve('outputs/checkout-review'),report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
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
   const headers={'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()};
   const create=async(mid,entity,data)=>{const response=await fetch('/api/v1/m/'+mid+'/'+entity,{method:'POST',headers:{...headers,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states[mid].generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const store=await create('shopping','stores',{name:'Markt am Samstag'}),p=await create('shopping','products',{name:'Haferflocken',brand:'Erntehof',pack_quantity:'750',pack_unit:'g'});
   const list=await create('shopping','lists',{name:'Einkauf mit Belegabgleich '+Date.now(),store_id:store.id});
   await create('shopping','price_observations',{product_id:p.id,store_id:store.id,observed_on:'2026-01-01',quantity:'1',unit_code:'piece',price_minor:'3.00'});
   const bulk=await create('shopping','items',{list_id:list.id,name:'Haferflocken für das gemeinsame Frühstück mit einer ausführlichen Produktbezeichnung',quantity:'2',unit_code:'piece',product_id:p.id});
   const service=await create('shopping','items',{list_id:list.id,name:'Geschenkverpackung',quantity:'1',unit_code:'piece'});
   await create('shopping','items',{list_id:list.id,name:'Erdbeeren nicht verfügbar',availability:'unavailable'});
   for(const item of [bulk,service]){const response=await fetch('/api/v1/m/shopping/items/'+item.id+'/actions/check',{method:'POST',headers:{...headers,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{checked:true},version:item.version,generation:bootstrap.states.shopping.generation})});if(!response.ok)throw Error(await response.text());}
   const product=await create('pantry','products',{name:'Haferflocken im Vorrat',unit_code:'g'}),location=await create('pantry','locations',{name:'Abschlusstest Regal '+Date.now()});
   const canvas=document.createElement('canvas');canvas.width=10;canvas.height=10;canvas.getContext('2d').fillRect(0,0,10,10);const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
   const upload=new FormData();upload.set('file',blob,'Kassenbeleg.png');upload.set('generation',String(bootstrap.states.shopping.generation));upload.set('csrf',csrf);upload.set('capture','false');
   const uploaded=await fetch('/api/v1/m/shopping/lists/'+list.id+'/attachments',{method:'POST',body:upload});if(!uploaded.ok)throw Error('Receipt upload failed');
   const receipts=await fetch('/api/v1/m/shopping/lists/'+list.id+'/attachments').then(r=>r.json());
   return{list,bulk,service,product,location,receipt:receipts[0],generation:bootstrap.states.shopping.generation};
  });
  const checkoutURL=base+'/m/shopping/lists/'+fixture.list.id+'/checkout';
  await page.goto(base+'/m/shopping/lists/'+fixture.list.id);
  await page.getByRole('link',{name:'Einkauf prüfen und Abschluss öffnen',exact:true}).click();await page.locator('#checkout-editor:not([hidden])').waitFor();
  must(page.url()===checkoutURL,'The list links to its authenticated checkout');
  async function capture(name){await page.evaluate(async()=>{scrollTo(0,0);await new Promise(requestAnimationFrame);});const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  let failureTested=false,captured=false;
  async function reviewAll(){
   for(let n=0;n<3;n++){
    const name=await page.locator('#checkout-name').innerText();
    must(await page.locator('#checkout-name').evaluate(el=>el===document.activeElement),'Position '+(n+1)+' receives keyboard focus');
    if(name===fixture.bulk.name){
     if(failureTested)must(await page.locator('[name=quantity]').inputValue()==='1.500007','Named conflict review retains the actual purchase quantity');
     await page.locator('[name=decision]').selectOption('store');await page.locator('[name=quantity]').fill('1.500007');await page.locator('[name=unit]').selectOption('kg');await page.locator('[name=price]').fill('6.00');
     await page.locator('[name=product_id][data-reference-state=ready]').selectOption(fixture.product.id);await page.locator('[name=location_id][data-reference-state=ready]').selectOption(fixture.location.id);await page.locator('[name=best_before]').fill('2027-01-02');
     if(!captured){await capture('checkout-position-desktop');captured=true;}
     if(!failureTested){await page.route('**/api/v1/m/pantry/products/'+fixture.product.id,route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Prüfverbindung unterbrochen'})}));await page.locator('#checkout-next').click();await page.locator('#checkout-line-error').getByText('Prüfverbindung unterbrochen',{exact:true}).waitFor();must(await page.locator('[name=quantity]').inputValue()==='1.500007','Failed product validation preserves exact quantity');must(await page.locator('#checkout-line-error').evaluate(el=>el===document.activeElement),'Validation failure receives focus');await page.unroute('**/api/v1/m/pantry/products/'+fixture.product.id);failureTested=true;}
    }else if(name===fixture.service.name){await page.locator('[name=decision]').selectOption('no_stock');await page.locator('[name=quantity]').fill('1');await page.locator('[name=unit]').selectOption('piece');await page.locator('[name=price]').fill('2.00');}
    else {must(await page.locator('[name=decision] option').allTextContents().then(options=>!options.some(x=>x==='In den Vorrat einlagern')),'Unavailable position cannot be stored');await page.locator('[name=decision]').selectOption('skip');}
    await page.locator('#checkout-next').click();
    if(n<2)await page.waitForFunction(old=>document.querySelector('#checkout-name').textContent!==old,name);else await page.locator('#checkout-review:not([hidden])').waitFor();
   }
  }
  await reviewAll();
  must(await page.locator('#checkout-lines').innerText().then(text=>text.includes('1500.007000 g')&&text.includes('Gekauft, ohne Einlagerung')&&text.includes('ausdrücklich ausgelassen')),'Review distinguishes exact stock, non-stock goods and skipped goods');
  await page.locator('[name=receipt_id]').selectOption(fixture.receipt.id);await page.locator('[name=receipt_total]').fill('8.50');
  must(await page.locator('#checkout-totals').innerText().then(text=>text.includes('0,50 €')&&text.includes('1 Positionen ohne Schätzung')),'Receipt difference stays explicit and unknown estimates remain unknown');
  await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('checkout-review-mobile-dark');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile checkout has no horizontal page overflow');
  await page.evaluate(async fixture=>{const response=await fetch('/api/v1/m/shopping/items/'+fixture.bulk.id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{quantity:'3'},version:2,generation:fixture.generation})});if(!response.ok)throw Error(await response.text());},fixture);
  await page.locator('[name=reviewed]').check();await page.getByRole('button',{name:'Geprüften Einkauf abschließen',exact:true}).click();await page.locator('#checkout-message').getByText(/Liste, Produkt oder Preis wurde geändert/).waitFor();
  must(await page.locator('[name=receipt_total]').inputValue()==='8.50'&&await page.locator('#checkout-review').isVisible(),'A stale source preserves the complete review and receipt draft');
  await page.locator('#checkout-refresh').click();await page.locator('#checkout-editor:not([hidden])').waitFor();
  must(await page.locator('#checkout-changes').innerText().then(text=>text.includes(fixture.bulk.name)&&text.includes('Geplante Menge: 2.000000 Stück → 3.000000 Stück')),'Refresh names the changed item and both reviewed quantities');
  await capture('checkout-conflict-mobile-dark');
  await reviewAll();
  must(await page.locator('[name=receipt_id]').inputValue()===fixture.receipt.id,'Refreshing the comparison preserves the selected authorized receipt');
  await page.locator('[name=reviewed]').check();await page.getByRole('button',{name:'Geprüften Einkauf abschließen',exact:true}).click();await page.locator('#checkout-completed:not([hidden])').waitFor();
  must(await page.locator('#checkout-completed').evaluate(el=>el===document.activeElement),'Completed checkout receives keyboard focus');
  must(await page.getByRole('link',{name:'Beleg öffnen: Kassenbeleg.png',exact:true}).count()===1,'Completed checkout links the uploaded receipt');
  const snapshot=await page.evaluate(async fixture=>{const c=await fetch('/api/v1/shopping/lists/'+fixture.list.id+'/checkout').then(r=>r.json());const lots=await fetch('/api/v1/pantry/products/'+fixture.product.id+'/lots').then(r=>r.json());return{c:c.completed,lots};},fixture);
  must(snapshot.lots.records.length===1&&snapshot.lots.records[0].stock==='1500.007000','A real checkout creates one exact inventory lot');
  must(snapshot.c.snapshot.unallocated_minor==='50'&&snapshot.c.snapshot.source.items.find(x=>x.id===fixture.bulk.id).quantity==='3.000000','Completion preserves the reviewed plan and unallocated receipt amount');
  await capture('checkout-completed-mobile-dark');await page.reload();await page.locator('#checkout-completed:not([hidden])').waitFor();
  must(!await page.locator('#checkout-review').isVisible(),'Reload opens immutable completion without another submission form');
  const emptyFixture=await page.evaluate(async()=>{
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),csrf=document.querySelector('meta[name="csrf-token"]').content;
   const create=async(entity,data)=>{const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states.shopping.generation})});if(!response.ok)throw Error(await response.text());return response.json();};
   const list=await create('lists',{name:'Parallel geleerte Abschlussliste'}),item=await create('items',{list_id:list.id,name:'Entfernter Entwurf'});return{list,item,generation:bootstrap.states.shopping.generation};
  });
  async function remove(item){await page.evaluate(async({item,generation})=>{const response=await fetch('/api/v1/m/shopping/items/'+item.id,{method:'DELETE',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},body:JSON.stringify({data:{},version:item.version,generation})});if(!response.ok)throw Error(await response.text());},{item,generation:emptyFixture.generation});}
  await page.goto(base+'/m/shopping/lists/'+emptyFixture.list.id+'/checkout');await page.locator('#checkout-editor:not([hidden])').waitFor();await remove(emptyFixture.item);await page.locator('#checkout-refresh').click();await page.locator('#checkout-message').getByText(/keine Positionen mehr/).waitFor();
  must(!await page.locator('#checkout-editor').isVisible()&&!await page.locator('#checkout-review').isVisible()&&!await page.locator('#checkout-next').isVisible(),'Empty refresh closes stale position editor and actions');
  const second=await page.evaluate(async fixture=>{const response=await fetch('/api/v1/m/shopping/items',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data:{list_id:fixture.list.id,name:'Vor Abschluss entfernt'},generation:fixture.generation})});if(!response.ok)throw Error(await response.text());return response.json();},emptyFixture);
  await page.locator('#checkout-refresh').click();await page.locator('#checkout-editor:not([hidden])').waitFor();await page.locator('[name=decision]').selectOption('skip');await page.locator('#checkout-next').click();await page.locator('#checkout-review:not([hidden])').waitFor();await remove(second);await page.locator('#checkout-refresh').click();await page.locator('#checkout-message').getByText(/keine Positionen mehr/).waitFor();
  must(!await page.locator('#checkout-review').isVisible()&&!await page.getByRole('button',{name:'Geprüften Einkauf abschließen',exact:true}).isVisible(),'Empty refresh closes stale final confirmation and leaves the list return link');
  must(await page.getByRole('link',{name:'Zur Liste',exact:true}).isVisible(),'Empty review keeps a usable route back to the list');
  must(!report.errors.length,'No checkout JavaScript errors');must(!report.externalRequests.length,'No external checkout requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
