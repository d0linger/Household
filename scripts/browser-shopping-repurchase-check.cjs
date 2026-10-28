'use strict';
// Explicit isolated review-server test. It creates real reviewed purchases;
// never run against live8100 or a user's production installation.
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const fs=require('node:fs/promises'),path=require('node:path');
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://127.0.0.1:8101',origin=new URL(base);
const initialPassword=process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234',changedPassword='Repurchase browser changed1234';
if(!['127.0.0.1','localhost'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household8101');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 const output=path.resolve('outputs/repurchase-review');await fs.mkdir(output,{recursive:true});
 try{
  const context=await browser.newContext({viewport:{width:390,height:844},serviceWorkers:'block',...(process.env.HOUSEHOLD_BROWSER_HANDOFF?{storageState:process.env.HOUSEHOLD_BROWSER_HANDOFF}:{})});
  await context.route('**/*',route=>new URL(route.request().url()).origin===origin.origin?route.continue():route.abort());
  const page=await context.newPage();page.setDefaultTimeout(20000);
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill(process.env.HOUSEHOLD_BROWSER_USER||'bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:'Anmelden',exact:true}).click()]);}
  if(process.env.HOUSEHOLD_BROWSER_HANDOFF){
   await page.goto(base+'/');if(new URL(page.url()).pathname==='/login')throw Error('Shared synthetic session expired');
  }else{
   await login(initialPassword);
   if(new URL(page.url()).pathname==='/change-password'){
    await page.locator('[name=current]').fill(initialPassword);await page.locator('[name=next]').fill(changedPassword);
    await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern',exact:true}).click()]);await login(changedPassword);
   }
  }
  const fixture=await page.evaluate(async()=>{
   const csrf=document.querySelector('meta[name="csrf-token"]').content;let bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
   if(bootstrap.states.shopping.state!=='enabled'){
    const enabled=await fetch('/settings/modules/shopping',{method:'POST',body:new URLSearchParams({csrf,enabled:'true',version:String(bootstrap.states.shopping.version)})});if(!enabled.ok)throw Error('Enable Shopping fixture module');
    bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
   }
   const generation=bootstrap.states.shopping.generation;
   const api=async(url,body)=>{const r=await fetch(url,{method:body?'POST':'GET',headers:{Accept:'application/json','Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:body?JSON.stringify(body):undefined});if(!r.ok)throw Error(await r.text());return r.json();};
   const create=(entity,data)=>api('/api/v1/m/shopping/'+entity,{generation,data});
   const product=await create('products',{name:'Geprüfte Nachkauf-Haferflocken '+Date.now(),pack_quantity:'500',pack_unit:'g'});
   for(const offset of[-21,-14,-7]){
    const list=await create('lists',{name:'Abgeschlossener Browser-Test '+offset+' '+Date.now()});
    const item=await create('items',{list_id:list.id,name:product.name,product_id:product.id,quantity:'1',unit_code:'piece'});
    await api('/api/v1/m/shopping/items/'+item.id+'/actions/check',{generation,version:item.version,data:{checked:true}});
    const preview=await api('/api/v1/shopping/lists/'+list.id+'/checkout');
    const day=new Date(preview.today+'T00:00:00Z');day.setUTCDate(day.getUTCDate()+offset);
    await api('/api/v1/shopping/lists/'+list.id+'/checkout',{generation,fingerprint:preview.fingerprint,received_on:day.toISOString().slice(0,10),lines:[{item_id:item.id,decision:'no_stock',quantity:'1',unit:'piece'}]});
   }
   return{product,list:await create('lists',{name:'Nachkauf manuell prüfen '+Date.now()})};
  });
  await page.goto(base+'/m/shopping/lists/'+fixture.list.id);
  const panel=page.locator('#shopping-repurchase');await panel.locator('summary').click();await panel.getByRole('button',{name:'Kaufabstände prüfen',exact:true}).click();
  await panel.getByRole('heading',{name:fixture.product.name}).waitFor();
  if(await page.locator('[data-shopping-item]').count()!==0)throw Error('Viewing suggestion changed shopping list');
  const card=panel.locator('article').filter({has:page.getByRole('heading',{name:fixture.product.name})});
  await card.getByText(/3 unterschiedliche Kaufdaten/).waitFor();
  await card.getByText('500 g pro Packung',{exact:true}).waitFor();
  const quantity=card.locator('[name=quantity]');if(await quantity.inputValue()!=='')throw Error('Quantity was guessed automatically');
  await quantity.fill('2');await card.getByRole('button',{name:'Geprüft zur Liste hinzufügen',exact:true}).click();
  await card.getByText('Zur Liste hinzugefügt. Damit wurde noch kein Einkauf bestätigt.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Erledigen: '+fixture.product.name,exact:true}).waitFor();
  await page.screenshot({path:path.join(output,'repurchase-mobile.png'),fullPage:true});
  await panel.scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,'repurchase-mobile-viewport.png')});
  const accessibility=await new AxeBuilder({page}).analyze();if(accessibility.violations.length)throw Error(JSON.stringify(accessibility.violations));
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'repurchase-desktop.png'),fullPage:true});
  await page.emulateMedia({colorScheme:'dark'});await page.evaluate(()=>{document.documentElement.dataset.theme='dark';});await page.screenshot({path:path.join(output,'repurchase-dark.png'),fullPage:true});
  await fs.writeFile(path.join(output,'report.json'),JSON.stringify({checks:['actual committed history','no viewing mutation','dates/gaps explained','quantity explicitly reviewed','actual item added once','mobile/desktop/dark','axe'],violations:accessibility.violations},null,2));
 }finally{await browser.close();}
})().catch(error=>{let message=String(error);for(const secret of[initialPassword,changedPassword])message=message.split(secret).join('[redacted]');console.error(message);process.exitCode=1;});
