'use strict';
// Creates synthetic local catalogue/list data on the isolated review server only.
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const fs=require('node:fs/promises'),path=require('node:path');
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://127.0.0.1:8101',origin=new URL(base);
const initialPassword=process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234',changedPassword='Barcode browser changed1234';
if(!['127.0.0.1','localhost'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household8101');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 const output=path.resolve('outputs/barcode-review');await fs.mkdir(output,{recursive:true});
 try{
  const context=await browser.newContext({viewport:{width:390,height:844},serviceWorkers:'block',...(process.env.HOUSEHOLD_BROWSER_HANDOFF?{storageState:process.env.HOUSEHOLD_BROWSER_HANDOFF}:{})});
  const external=[];await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){external.push(route.request().url());return route.abort();}return route.continue();});
  // Browser fallback evidence uses a deliberate denied camera, not a claim
  // that the physical scanner/device pipeline has been exercised.
  await context.addInitScript(()=>{window.__barcodeCameraCalls=0;Object.defineProperty(navigator,'mediaDevices',{configurable:true,value:{getUserMedia:async()=>{window.__barcodeCameraCalls++;throw new DOMException('Synthetic permission denial','NotAllowedError');}}});class DeniedDetector{static async getSupportedFormats(){return['ean_13'];}}Object.defineProperty(window,'BarcodeDetector',{configurable:true,value:DeniedDetector});});
  const page=await context.newPage();page.setDefaultTimeout(20000);const errors=[];page.on('pageerror',error=>errors.push(String(error)));
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill(process.env.HOUSEHOLD_BROWSER_USER||'bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:'Anmelden',exact:true}).click()]);}
  if(process.env.HOUSEHOLD_BROWSER_HANDOFF){await page.goto(base+'/');if(new URL(page.url()).pathname==='/login')throw Error('Shared synthetic session expired');}
  else{await login(initialPassword);if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(initialPassword);await page.locator('[name=next]').fill(changedPassword);await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern',exact:true}).click()]);await login(changedPassword);}}
  const fixture=await page.evaluate(async()=>{
   const csrf=document.querySelector('meta[name="csrf-token"]').content;let boot=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
   if(boot.states.shopping.state!=='enabled'){const enabled=await fetch('/settings/modules/shopping',{method:'POST',body:new URLSearchParams({csrf,enabled:'true',version:String(boot.states.shopping.version)})});if(!enabled.ok)throw Error('Enable Shopping fixture module');boot=await fetch('/api/v1/core/bootstrap').then(r=>r.json());}
   const generation=boot.states.shopping.generation,suffix=String(Date.now());
   async function create(entity,data){const response=await fetch('/api/v1/m/shopping/'+entity,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation,data})});if(!response.ok)throw Error(await response.text());return response.json();}
   return{product:await create('products',{name:'Lokal geprüfte Barcode-Packung '+suffix,brand:'Haushaltskatalog',pack_quantity:'500',pack_unit:'g'}),list:await create('lists',{name:'Barcode-Prüfung '+suffix}),code:'00073AbC-'+suffix};
  });
  const url=base+'/m/shopping/lists/'+fixture.list.id+'/barcode',response=await page.goto(url);
  if(!response.headers()['permissions-policy'].includes('camera=(self)'))throw Error('Dedicated camera view lacks self permission');
  if(!response.headers()['content-security-policy'].includes("media-src 'self' blob:"))throw Error('Dedicated camera view lacks local stream media policy');
  if(await page.evaluate(()=>window.__barcodeCameraCalls)!==0)throw Error('Camera requested before explicit user start');
  await page.locator('#barcode-camera>summary').click();await page.getByRole('button',{name:'Kamera starten',exact:true}).click();
  await page.locator('#barcode-camera-status').filter({hasText:'Kein Kamerazugriff'}).waitFor();if(await page.evaluate(()=>window.__barcodeCameraCalls)!==1)throw Error('Explicit camera denial flow did not run once');
  const input=page.locator('#barcode-lookup [name=code]');await input.fill(fixture.code);await input.press('Enter');
  await page.getByRole('heading',{name:'Noch keine lokale Zuordnung',exact:true}).waitFor();
  const noItems=await page.evaluate(async list=>{const r=await fetch('/api/v1/shopping/lists/'+list+'/items');if(!r.ok)throw Error(await r.text());return r.json();},fixture.list.id);
  if(!Array.isArray(noItems.records)||noItems.records.length!==0)throw Error('Lookup created a shopping item');
  const map=page.locator('#barcode-map'),selection=map.locator('[name=product_id]');await map.locator('[name=product_id][data-reference-state=ready]').waitFor();
  const search=selection.locator('xpath=ancestor::div[contains(@class,"reference-field")]').locator('input[type=search]');await search.fill(fixture.product.name);await search.locator('..').getByRole('button',{name:'Suchen',exact:true}).click();
  await map.locator('[name=product_id][data-reference-state=ready]').waitFor();await selection.selectOption(fixture.product.id);
  await page.locator('#barcode-map-product').filter({hasText:fixture.product.name}).waitFor();await map.getByRole('button',{name:'Geprüfte Zuordnung speichern',exact:true}).click();
  await page.getByRole('heading',{name:'Produkt gefunden',exact:true}).waitFor();await page.locator('#barcode-result').getByText(fixture.product.name+' · Haushaltskatalog · 500 g pro Packung',{exact:true}).waitFor();
  const quantity=page.locator('#barcode-result [name=quantity]');if(await quantity.inputValue()!=='')throw Error('Quantity guessed automatically');await quantity.fill('2');
  await page.getByRole('button',{name:'Geprüft zur Liste hinzufügen',exact:true}).click();await page.locator('#barcode-status').filter({hasText:'Kein Einkauf wurde bestätigt'}).waitFor();
  const actual=await page.evaluate(async list=>{const r=await fetch('/api/v1/shopping/lists/'+list+'/items');if(!r.ok)throw Error(await r.text());return r.json();},fixture.list.id);
  if(!Array.isArray(actual.records)||actual.records.length!==1||actual.records[0].product_id!==fixture.product.id||actual.records[0].checked_at!==null||Number(actual.records[0].quantity)!==2)throw Error('Explicit reviewed add differs from actual unchecked item');
  await page.screenshot({path:path.join(output,'barcode-mobile.png'),fullPage:true});const accessibility=await new AxeBuilder({page}).analyze();if(accessibility.violations.length)throw Error(JSON.stringify(accessibility.violations));
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'barcode-desktop.png'),fullPage:true});
  await page.emulateMedia({colorScheme:'dark'});await page.evaluate(()=>{document.documentElement.dataset.theme='dark';});await page.screenshot({path:path.join(output,'barcode-dark.png'),fullPage:true});
  const scanned=await browser.newContext({storageState:await context.storageState(),viewport:{width:390,height:844},serviceWorkers:'block'});
  await scanned.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){external.push(route.request().url());return route.abort();}return route.continue();});
  await scanned.addInitScript(({code})=>{
   window.__barcodeCameraCalls=0;window.__barcodeStopped=0;
   Object.defineProperty(navigator,'mediaDevices',{configurable:true,value:{getUserMedia:async()=>{
    window.__barcodeCameraCalls++;const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;canvas.getContext('2d').fillRect(0,0,640,360);
    const stream=canvas.captureStream(1);for(const track of stream.getTracks()){const stop=track.stop.bind(track);track.stop=()=>{window.__barcodeStopped++;stop();};}return stream;
   }}});
   class SyntheticDetector{static async getSupportedFormats(){return['code_128'];}async detect(video){return video.readyState>=2?[{rawValue:code}]:[];}}
   Object.defineProperty(window,'BarcodeDetector',{configurable:true,value:SyntheticDetector});
  },{code:fixture.code});
  const cameraPage=await scanned.newPage();cameraPage.on('pageerror',error=>errors.push(String(error)));await cameraPage.goto(url);
  if(await cameraPage.evaluate(()=>window.__barcodeCameraCalls)!==0)throw Error('Synthetic camera started automatically');
  await cameraPage.locator('#barcode-camera>summary').click();await cameraPage.getByRole('button',{name:'Kamera starten',exact:true}).click();
  await cameraPage.locator('#barcode-camera-status').filter({hasText:'Code übernommen, Kamera gestoppt'}).waitFor();
  if(await cameraPage.locator('#barcode-lookup [name=code]').inputValue()!==fixture.code)throw Error('Synthetic stream scan did not fill the exact inert draft');
  const media=await cameraPage.evaluate(()=>({requests:window.__barcodeCameraCalls,stopped:window.__barcodeStopped,source:document.querySelector('#barcode-video').srcObject}));
  if(media.requests!==1||media.stopped!==1||media.source!==null)throw Error('Synthetic scan retained camera tracks');
  const unchanged=await cameraPage.evaluate(async list=>fetch('/api/v1/shopping/lists/'+list+'/items').then(r=>r.json()),fixture.list.id);
  if(!Array.isArray(unchanged.records)||unchanged.records.length!==1||unchanged.records[0].id!==actual.records[0].id)throw Error('Scanning changed saved shopping items');
  await cameraPage.screenshot({path:path.join(output,'barcode-camera-draft.png'),fullPage:true});await scanned.close();
  const listResponse=await page.goto(base+'/m/shopping/lists/'+fixture.list.id);if(!listResponse.headers()['permissions-policy'].includes('camera=()'))throw Error('Camera permission leaked to ordinary list');
  if(external.length||errors.length)throw Error(JSON.stringify({external,errors}));
  await fs.writeFile(path.join(output,'report.json'),JSON.stringify({checks:['no automatic camera','explicit denied-camera manual fallback','unknown local code','searchable reviewed actual product mapping','blank quantity','explicit unchecked item','scoped camera and local stream policy','synthetic stream fills only draft and stops tracks','mobile/desktop/dark','axe'],cameraEvidence:'synthetic permission denial and canvas video stream with synthetic detector; physical device and native decoder recognition not exercised',externalRequests:external,pageErrors:errors,violations:accessibility.violations},null,2));
 }finally{await browser.close();}
})().catch(error=>{let message=String(error);for(const secret of[initialPassword,changedPassword])message=message.split(secret).join('[redacted]');console.error(message);process.exitCode=1;});
