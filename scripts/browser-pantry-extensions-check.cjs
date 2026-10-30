'use strict';
// Parent owns disposable 8101/browser execution; this runner is separate from the default suite.
const fs=require('node:fs/promises'),path=require('node:path'),crypto=require('node:crypto');
const assert=require('node:assert/strict');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://127.0.0.1:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/'||origin.username||origin.password)throw Error('Pantry browser requires isolated Household8101');
const output=path.resolve('outputs/pantry-extensions-review');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],blocked:[],evidence:{}};
const must=(value,label)=>{if(!value)throw Error(label);report.checks.push(label);};
const initial=process.env.HOUSEHOLD_BROWSER_PASSWORD,changed='Pantry review '+crypto.randomUUID();
function safe(error){let value=String(error);for(const secret of[initial,changed])if(secret)value=value.split(secret).join('[redacted]');return value;}
function foodFactsTarget(provider='off-staging'){
 const hosts={'off-staging':'world.openfoodfacts.net','off-production':'world.openfoodfacts.org'};
 if(typeof provider!=='string'||!Object.hasOwn(hosts,provider))throw Error('Food Facts acceptance provider must be off-staging or off-production');
 return {provider,host:hosts[provider]};
}
const expectedFoodFacts=foodFactsTarget(process.env.HOUSEHOLD_PANTRY_BROWSER_FOODFACTS_PROVIDER);
function parseCSV(text){
 if(Buffer.byteLength(text)>16*1024*1024)throw Error('CSV acceptance payload exceeded server export bound');
 const rows=[];let row=[],field='',quoted=false,closed=false;
 for(let i=0;i<text.length;i++){
  const ch=text[i];
  if(quoted){if(ch==='"'){if(text[i+1]==='"'){field+='"';i++;}else{quoted=false;closed=true;}}else field+=ch;continue;}
  if(ch==='"'){if(field||closed)throw Error('Malformed quoted CSV field');quoted=true;continue;}
  if(ch===','){row.push(field);field='';closed=false;continue;}
  if(ch==='\r'||ch==='\n'){if(ch==='\r'&&text[i+1]==='\n')i++;row.push(field);rows.push(row);row=[];field='';closed=false;continue;}
  if(closed)throw Error('Unexpected text after quoted CSV field');
  field+=ch;
 }
 if(quoted)throw Error('Unfinished quoted CSV field');
 if(field||row.length||closed){row.push(field);rows.push(row);}
 const header=rows.shift();
 if(!header?.length||new Set(header).size!==header.length)throw Error('Missing or duplicate CSV columns');
 return rows.map(values=>{if(values.length!==header.length)throw Error('CSV column count changed');return Object.fromEntries(header.map((key,i)=>[key,values[i]]));});
}
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'allow',...(process.env.HOUSEHOLD_BROWSER_HANDOFF?{storageState:process.env.HOUSEHOLD_BROWSER_HANDOFF}:{})});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(safe(error)));page.on('dialog',dialog=>dialog.accept());
  if(process.env.HOUSEHOLD_BROWSER_HANDOFF){await page.goto(base+'/');if(['/login','/change-password'].includes(new URL(page.url()).pathname))throw Error('Shared Pantry browser session unavailable');}
  else{
   if(!initial||!process.env.HOUSEHOLD_BROWSER_USER)throw Error('Explicit disposable credentials or shared session required');
   async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill(process.env.HOUSEHOLD_BROWSER_USER);await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(u=>u.pathname!=='/login'),page.getByRole('button',{name:'Anmelden',exact:true}).click()]);}
   await login(initial);
   if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(initial);await page.locator('[name=next]').fill(changed);await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern',exact:true}).click()]);await login(changed);}
  }
  const api=async(url,body,method='POST',key=crypto.randomUUID())=>page.evaluate(async({url,body,method,key})=>{const csrf=document.querySelector('meta[name="csrf-token"]').content;const response=await fetch(url,{method:body===undefined?'GET':method,headers:{Accept:'application/json',...(body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':key})},body:body===undefined?undefined:JSON.stringify(body)});const data=await response.json();if(!response.ok)throw Error(data.error||'Actual fixture request refused');return data;},{url,body,method,key});
  let bootstrap=await api('/api/v1/core/bootstrap');
  if(bootstrap.states.recipes.state!=='enabled'){
   await page.evaluate(async state=>{const response=await fetch('/settings/modules/recipes',{method:'POST',body:new URLSearchParams({csrf:document.querySelector('meta[name="csrf-token"]').content,enabled:'true',version:String(state.version)})});if(!response.ok)throw Error('Actual Recipes activation failed');},bootstrap.states.recipes);
   bootstrap=await api('/api/v1/core/bootstrap');
  }
  const suffix='Pantry browser '+Date.now(),today=(await api('/api/v1/pantry/leftovers/context')).today;
  const create=async(mid,entity,data)=>api('/api/v1/m/'+mid+'/'+entity,{generation:bootstrap.states[mid].generation,data});
  const location=await create('pantry','locations',{name:suffix+' fridge',sort_order:'1'});
  const product=await create('pantry','products',{name:suffix+' scan product',unit_code:'g',integer_quantities:false});
  const pack=await create('pantry','packs',{product_id:product.id,label:suffix+' 250g pack',quantity:'250',unit_code:'g',disabled:false});
  const code='LOCAL-'+Date.now();await create('pantry','barcodes',{product_id:product.id,pack_id:pack.id,code});
  const settled=async id=>page.waitForFunction(id=>document.querySelector('#'+id)?.getAttribute('aria-busy')==='false',id);
  async function choose(parent,id,label){const box=typeof parent==='string'?page.locator(parent):parent;await box.locator('input[type=search]').fill(label);await box.getByRole('button',{name:'Suchen',exact:true}).click();await box.locator('option[value="'+id+'"]').waitFor({state:'attached'});await box.locator('select').selectOption(id);}
  async function capture(name){const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const axe=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:axe.violations});must(!axe.violations.length,'Axe '+name);must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'No horizontal overflow '+name);}

  await page.goto(base+'/m/pantry/leftovers');await settled('pantry-leftovers');
  const containers=page.locator('#pl-containers>.pe-row');must(await containers.first().getByLabel('Tatsächlich übrige Portionen').inputValue()==='','Cooked portions never prefill leftover quantity');
  await page.locator('#pl-name').fill(suffix+' meal');await page.locator('#pl-prepared').fill(today);await page.locator('#pl-produced').fill('4');
  async function container(index,label,quantity){const row=containers.nth(index);await row.getByLabel('Eigene Behälterbezeichnung').fill(label);await row.getByLabel('Tatsächlich übrige Portionen').fill(quantity);await row.getByLabel('Eingelagert / erhalten am').fill(today);await choose(row.locator('.pe-picker'),location.id,suffix);}
  await container(0,suffix+' box A','1.25');await page.locator('#pl-add').click();await container(1,suffix+' box B','0.75');await page.locator('#pl-preview').click();await page.locator('#pl-review:not([hidden])').waitFor();await settled('pantry-leftovers');
  must((await page.locator('#pl-review-content').innerText()).includes('2.000000'),'Two containers have exact combined two portions');await capture('leftovers-review-desktop');
  let leftReceipt,leftRequest;
  await page.route('**/api/v1/pantry/leftovers/execute',async route=>{leftRequest={body:route.request().postData(),key:route.request().headers()['idempotency-key']};const response=await route.fetch();if(!response.ok())throw Error('Actual leftover execution failed');leftReceipt=await response.json();await route.abort('failed');});
  await page.locator('#pl-confirm').click();await page.locator('#pl-uncertain:not([hidden])').waitFor();await settled('pantry-leftovers');
  must(await page.locator('#pl-editor').isHidden(),'Unknown leftover outcome freezes edits');
  const locator=await page.evaluate(()=>sessionStorage.getItem('household-pantry-pending:leftovers'));must(locator&&!locator.includes('review')&&!locator.includes('meal'),'Only minimal recovery locator persists');
  await page.unroute('**/api/v1/pantry/leftovers/execute');await page.reload();await settled('pantry-leftovers');await page.locator('#pl-check').click();await page.locator('#pl-receipt:not([hidden])').waitFor();
  must(leftRequest.key&&leftReceipt.lines.length===2,'Actual response loss recovers the original two-line receipt');
  const firstLot=leftReceipt.lines[0].lot_id,prepared=await api('/api/v1/pantry/lots/'+firstLot+'/prepared');
  must(prepared.prepared.initial_portions==='1.250000'&&prepared.lot.stock==='1.250000','Prepared history and actual journal stock agree');
  await page.goto(base+'/m/pantry/leftovers/'+firstLot);await settled('pantry-leftovers');must(await page.locator('#pl-editor').isHidden(),'Prepared detail has no capture wizard');const preparedText=await page.locator('#pl-detail-content').innerText();must(preparedText.includes('Ursprünglicher Zugang dieser Charge: 1.250000')&&preparedText.includes(suffix+' meal'),'Normal stock navigation exposes the actual dish and historical initial portion quantity');
  await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'dark',reducedMotion:'reduce'});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('prepared-detail-mobile-dark');

  // Source fixture is created through actual Recipes and Cooking contracts.
  const recipe=await create('recipes','recipes',{title:suffix+' recipe',base_servings:'4',summary:'Disposable browser preparation'});
  await create('recipes','steps',{recipe_id:recipe.id,instruction:'Prepare the explicitly disposable fixture.',position:'1',timer_seconds:'0'});
  const cookingSource=await api('/api/v1/cooking/source/'+recipe.id),session=await api('/api/v1/cooking',{generation:bootstrap.states.recipes.generation,version:0,action:'start',source_version_id:cookingSource.source_version_id,servings:'4'});
  const currentSession=await api('/api/v1/cooking/'+session.id),cooked=await api('/api/v1/cooking/'+session.id,{generation:bootstrap.states.recipes.generation,version:Number(currentSession.version),action:'finish',actual_servings:'4',cooked_on:today,notes:'',rating:null});
  await page.goto(base+'/m/recipes/cooking/history/'+cooked.record_id);await page.locator('#cooking-record:not([hidden])').waitFor();await page.getByRole('link',{name:'Tatsächliche Reste separat einlagern',exact:true}).click();await settled('pantry-leftovers');
  must(await page.locator('#pl-produced').inputValue()==='4.000000'&&await containers.first().getByLabel('Tatsächlich übrige Portionen').inputValue()==='','Cooking link retains actual cap and blank leftover quantity');
  await page.locator('#pl-use-name').click();await container(0,suffix+' cooked box','4.000001');await page.locator('#pl-preview').click();await page.locator('#pl-error:not([hidden])').waitFor();must(await page.locator('#pl-review').isHidden(),'Cooking cap refuses excess leftovers before any posting');
  await containers.first().getByLabel('Tatsächlich übrige Portionen').fill('1');await page.locator('#pl-preview').click();await page.locator('#pl-review:not([hidden])').waitFor();await page.locator('#pl-confirm').click();await page.locator('#pl-receipt:not([hidden])').waitFor();
  await page.goto(base+'/m/pantry/leftovers?'+new URLSearchParams({source_kind:'cooking',source_id:cooked.record_id}));await settled('pantry-leftovers');must((await page.locator('#pl-error').innerText()).includes('bereits'),'Same cooking source cannot silently create another leftover batch');

  await page.goto(base+'/m/pantry/food-facts');await settled('pantry-food-facts');await page.locator('#pf-code').fill(code);await page.locator('#pf-local').click();await settled('pantry-food-facts');must(await page.locator('#pf-editor').isHidden()&&(await page.locator('#pf-local-result').innerText()).includes(product.name),'Existing Pantry alias wins without external adoption');must(await page.locator('#pf-external').isDisabled(),'Internal code is never sent to external catalogue');
  const offCode=process.env.HOUSEHOLD_PANTRY_BROWSER_FOODFACTS_CODE;
  if(!offCode){report.blocked.push('Required PA13 positive needs an explicit real '+expectedFoodFacts.provider+' product code');throw Error(report.blocked.at(-1));}
  await page.locator('#pf-code').fill(offCode);await page.locator('#pf-local').click();await settled('pantry-food-facts');
  const foodFactsBase='/api/v1/pantry/extensions/food-facts';
  const [actualLookup]=await Promise.all([page.waitForResponse(response=>{
   const request=response.request();if(request.method()!=='POST'||new URL(response.url()).pathname!==foodFactsBase+'/lookup')return false;
   const body=request.postDataJSON();return body?.allow_external===true&&body.code===offCode;
  }),page.locator('#pf-external').click()]);await settled('pantry-food-facts');
  const lookup=await actualLookup.json(),source=lookup.source;
  if(!actualLookup.ok()||lookup.state!=='found'||lookup.from_cache!==false||lookup.stale!==false||lookup.code!==offCode||!source){report.blocked.push('Required actual '+expectedFoodFacts.provider+' request not proven: HTTP '+actualLookup.status()+' state '+String(lookup.state)+' from_cache '+String(lookup.from_cache));throw Error(report.blocked.at(-1));}
  must(source.provider===expectedFoodFacts.provider&&source.code===offCode&&source.url==='https://'+expectedFoodFacts.host+'/product/'+offCode,'Explicit external click returns the exact real '+expectedFoodFacts.provider+' product; no cached acceptance');
  must(source.database_license==='ODbL-1.0'&&source.contents_license==='DbCL-1.0'&&/^[a-f0-9]{64}$/.test(source.payload_sha256)&&/^[a-f0-9]{64}$/.test(source.fingerprint),'Actual upstream bytes and cached source have hashes and both required licences');
  const fetched=Date.parse(source.fetched_at),expires=Date.parse(source.expires_at);
  must(typeof source.name==='string'&&source.name.trim().length>0&&Number.isFinite(fetched)&&Number.isFinite(expires)&&fetched<=Date.now()+1000&&expires>Date.now()&&expires>fetched&&expires-fetched<=7*24*60*60*1000,'Real '+expectedFoodFacts.provider+' source has a usable name and bounded fresh timestamps');
  must(!await page.locator('#pf-editor').isHidden(),'Actual fresh '+expectedFoodFacts.provider+' response opens explicit metadata review');
  await page.locator('#pf-selection input[value=name]').check();await page.locator('#pf-use-name').click();const ownName=(await page.locator('#pf-name').inputValue()).slice(0,120)+' '+suffix;await page.locator('#pf-name').fill(ownName);await page.locator('#pf-unit').selectOption('g');await page.locator('#pf-pack').check();await page.locator('#pf-pack-label').fill(suffix+' explicit pack');await page.locator('#pf-pack-quantity').fill('250');await page.locator('#pf-pack-unit').selectOption('g');await page.locator('#pf-preview').click();await page.locator('#pf-review:not([hidden])').waitFor();await settled('pantry-food-facts');
  must((await page.locator('#pf-review-content').innerText()).includes('ODbL-1.0'),'Source licence is visible in actual review');await capture('food-facts-review-mobile-dark');
  const [actualExecute]=await Promise.all([page.waitForResponse(response=>response.request().method()==='POST'&&new URL(response.url()).pathname===foodFactsBase+'/execute'),page.locator('#pf-confirm').click()]);
  const adoption=await actualExecute.json(),adoptionKey=actualExecute.request().headers()['idempotency-key'];await page.locator('#pf-receipt:not([hidden])').waitFor();await settled('pantry-food-facts');
  const uuid=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  must(actualExecute.ok()&&uuid.test(adoptionKey)&&['operation_id','product_id','pack_id','barcode_id','provenance_id'].every(field=>uuid.test(adoption[field]))&&adoption.generation===bootstrap.states.pantry.generation,'Actual execute returns the durable operation and all adopted resource identities');
  assert.deepEqual(adoption.source,source);assert.deepEqual(adoption.selected_fields,['name']);
  must(Number.isFinite(Date.parse(adoption.accepted_at))&&Date.parse(adoption.accepted_at)>=fetched&&Date.parse(adoption.accepted_at)<expires,'Stored adoption receipt binds the exact selected source within its validity');
  const accepted=await api(foodFactsBase+'/operations/'+adoptionKey+'?generation='+adoption.generation);assert.equal(accepted.state,'complete');assert.deepEqual(accepted.receipt,adoption);must(true,'Actual operation status returns the identical durable typed receipt');
  await page.locator('#pf-provenance-load').click();await settled('pantry-food-facts');must((await page.locator('#pf-provenance').innerText()).includes('Open Food Facts'),'Saved provenance is visible in the normal interface');
  await page.reload();await settled('pantry-food-facts');
  const recovered=await api(foodFactsBase+'/operations/'+adoptionKey+'?generation='+adoption.generation);assert.equal(recovered.state,'complete');assert.deepEqual(recovered.receipt,adoption);
  const origins=await api(foodFactsBase+'/provenance/'+adoption.product_id+'?generation='+adoption.generation);assert.deepEqual(origins,{sources:[source],more:false});must(true,'Reload preserves the same receipt and complete first provenance source');
  const savedProduct=await api('/api/v1/m/pantry/products/'+adoption.product_id),savedPack=await api('/api/v1/m/pantry/packs/'+adoption.pack_id),savedBarcode=await api('/api/v1/m/pantry/barcodes/'+adoption.barcode_id);
  must(savedProduct.name===ownName&&savedProduct.unit_code==='g'&&savedProduct.integer_quantities===false&&savedPack.product_id===adoption.product_id&&savedPack.label===suffix+' explicit pack'&&typeof savedPack.quantity==='number'&&Number.isSafeInteger(savedPack.quantity)&&savedPack.quantity===250&&savedPack.unit_code==='g'&&savedPack.disabled===false&&savedBarcode.product_id===adoption.product_id&&savedBarcode.pack_id===adoption.pack_id&&savedBarcode.code===offCode,'Actual product, explicit250g pack and exact GTIN alias match the accepted receipt');
  const lots=await api('/api/v1/pantry/products/'+adoption.product_id+'/lots');must(lots.records.length===0,'Product/pack/alias adoption creates no lot or stock');
  const expectedOrigin={source,adopted:{product_name:ownName,unit:'g',integer_quantities:false,pack_label:suffix+' explicit pack',pack_quantity:'250.000000',pack_unit:'g'},selected_fields:['name']},csvEvidence=[];
  for(const [entity,column,identity]of [['products','name',ownName],['packs','label',suffix+' explicit pack'],['barcodes','code',offCode]]){
   const csv=await page.evaluate(async url=>{const response=await fetch(url,{cache:'no-store'});if(!response.ok||!response.headers.get('Content-Type')?.startsWith('text/csv'))throw Error('Actual attributed CSV export refused');return response.text();},'/api/v1/m/pantry/'+entity+'/export.csv');
   const matches=parseCSV(csv).filter(row=>row[column]===identity);assert.equal(matches.length,1);assert.deepEqual(JSON.parse(matches[0].source_provenance),[expectedOrigin]);
   must(true,'Actual '+entity+' CSV retains exact adopted values, selected fields, source hashes and both licences');csvEvidence.push({entity,matchingRows:1,sha256:crypto.createHash('sha256').update(csv).digest('hex')});
  }
  report.evidence.foodFacts={expectedProvider:expectedFoodFacts.provider,expectedHost:expectedFoodFacts.host,explicitLookup:{state:lookup.state,from_cache:lookup.from_cache,stale:lookup.stale,source},receipt:adoption,operationKeySHA256:crypto.createHash('sha256').update(adoptionKey).digest('hex'),statusAfterReload:'identical complete receipt',csv:csvEvidence};

  await page.goto(base+'/settings');await page.locator('#preferences [name=offline]').check();await page.getByRole('button',{name:'Geräteeinstellungen speichern',exact:true}).click();await page.getByText('Geräteeinstellungen gespeichert.',{exact:true}).waitFor();
  await page.goto(base+'/m/pantry/scans');await page.locator('#ps-connect').click();await settled('pantry-scans');await page.locator('#ps-new').click();await settled('pantry-scans');
  await page.locator('#ps-scan-form [name=code]').fill(code);await page.locator('#ps-scan-form [name=quantity]').fill('2');await page.locator('#ps-scan-form [name=label]').fill(suffix+' scanned lot');await page.locator('#ps-scan-form button[type=submit]').click();await settled('pantry-scans');
  await page.evaluate(()=>navigator.serviceWorker.ready);await page.reload();await page.waitForFunction(()=>!!navigator.serviceWorker.controller);await context.setOffline(true);await page.goto(base+'/offline/scans');
  must(await page.locator('body').getAttribute('data-user')==='','Cached scan shell contains no server actor');await page.locator('#ps-open-local').click();await settled('pantry-scans');await page.locator('#ps-drafts button').first().click();await settled('pantry-scans');must((await page.locator('#ps-lines').innerText()).includes(code),'Explicit device draft survives actual offline restart');await capture('offline-scans-mobile-dark');
  await context.setOffline(false);await page.locator('#ps-resolve').click();await settled('pantry-scans');await choose('#ps-location',location.id,suffix);await page.locator('#ps-use-location').click();await settled('pantry-scans');const scanRow=page.locator('#ps-review-lines>.pe-row').first();await scanRow.getByLabel('Einheit oder Packung').selectOption('pack:'+pack.id);await scanRow.getByLabel('Erhalten am').fill(today);await page.locator('#ps-reviewed').check();
  const scanDraftPUTs=[];
  const scanProof=value=>crypto.createHash('sha256').update(JSON.stringify(value,(_key,item)=>item&&typeof item==='object'&&!Array.isArray(item)?Object.fromEntries(Object.keys(item).sort().map(key=>[key,item[key]])):item)).digest('hex');
  const sameScanEvidence=(left,right,label)=>must(scanProof(left)===scanProof(right),label);
  const countScanDraftPUT=request=>{if(request.method()==='PUT'&&new URL(request.url()).pathname.startsWith('/api/v1/pantry/batches/drafts/'))scanDraftPUTs.push({url:request.url(),key:request.headers()['idempotency-key'],body:request.postData()});};
  page.on('request',countScanDraftPUT);
  let savedDraftRequest;await page.route('**/api/v1/pantry/batches/drafts/*',async route=>{if(route.request().method()!=='PUT')return route.continue();savedDraftRequest={url:route.request().url(),key:route.request().headers()['idempotency-key'],body:route.request().postData()};const response=await route.fetch();if(!response.ok())throw Error('Actual scan server-draft save failed');await route.abort('failed');});
  await page.locator('#ps-transfer').click();await page.locator('#ps-uncertain:not([hidden])').waitFor();await settled('pantry-scans');await page.unroute('**/api/v1/pantry/batches/drafts/*');await page.reload();await page.locator('#ps-connect').click();await settled('pantry-scans');await page.locator('#ps-drafts button').first().click();await settled('pantry-scans');await page.locator('#ps-recover').click();await settled('pantry-scans');
  must(savedDraftRequest&&(await page.locator('#ps-existing').innerText()).includes('überein'),'Reload recovers exact owned server draft using preserved UUID/digest');
  const firstScanID=new URL(savedDraftRequest.url).pathname.split('/').at(-1),firstScanURL=await page.locator('#ps-existing a').getAttribute('href');
  must(uuid.test(firstScanID)&&uuid.test(savedDraftRequest.key)&&firstScanURL==='/m/pantry/batches?draft='+firstScanID,'Original reload recovery keeps the first exact server draft identity');
  const firstScanSnapshot=await api('/api/v1/pantry/batches/drafts/'+firstScanID);
  sameScanEvidence(scanDraftPUTs,[savedDraftRequest],'Original scan handoff has exactly one unchanged actual PUT');

  // A deliberately independent private draft retains its in-memory unknown
  // request while the real IndexedDB row expires. No production debug seam.
  await page.goto(base+'/m/pantry/scans');await page.locator('#ps-connect').click();await settled('pantry-scans');await page.locator('#ps-new').click();await settled('pantry-scans');
  await page.locator('#ps-scan-form [name=code]').fill(code);await page.locator('#ps-scan-form [name=quantity]').fill('2');await page.locator('#ps-scan-form [name=label]').fill(suffix+' expiry-only private draft');await page.locator('#ps-scan-form button[type=submit]').click();await settled('pantry-scans');
  await page.locator('#ps-resolve').click();await settled('pantry-scans');await choose('#ps-location',location.id,suffix);await page.locator('#ps-use-location').click();await settled('pantry-scans');
  const expiryRow=page.locator('#ps-review-lines>.pe-row').first();await expiryRow.getByLabel('Einheit oder Packung').selectOption('pack:'+pack.id);await expiryRow.getByLabel('Erhalten am').fill(today);await page.locator('#ps-reviewed').check();
  let expiryDraftRequest,expiryAccepted;
  await page.route('**/api/v1/pantry/batches/drafts/*',async route=>{
   if(route.request().method()!=='PUT')return route.continue();
   expiryDraftRequest={url:route.request().url(),key:route.request().headers()['idempotency-key'],body:route.request().postData()};
   const response=await route.fetch();if(!response.ok())throw Error('Actual expiry-only private draft save failed');expiryAccepted=await response.json();await route.abort('failed');
  });
  await page.locator('#ps-transfer').click();await page.locator('#ps-uncertain:not([hidden])').waitFor();await settled('pantry-scans');await page.unroute('**/api/v1/pantry/batches/drafts/*');
  const expiryID=new URL(expiryDraftRequest.url).pathname.split('/').at(-1),beforeExpiry=await page.evaluate(()=>window.HouseholdPantryDevice.inspect());
  const expiryLocal=beforeExpiry.drafts.find(d=>d.handoff?.draft_id===expiryID),beforeExpiryPUTs=scanDraftPUTs.length;
  must(uuid.test(expiryID)&&expiryID!==firstScanID&&uuid.test(expiryDraftRequest.key)&&expiryDraftRequest.key!==savedDraftRequest.key&&expiryAccepted.id===expiryID&&expiryAccepted.state==='active'&&expiryLocal?.handoff?.phase==='uncertain','Independent expiry fixture has an actually accepted server draft and original uncertain local handoff');
  sameScanEvidence(scanDraftPUTs,[savedDraftRequest,expiryDraftRequest],'Both independent scan fixtures retain their exact original request bodies and keys');
  try{
   // Date changes alone; real timers, server time and credentials are intact.
   await page.clock.setFixedTime(new Date(Date.now()+8*24*60*60*1000));
   await page.locator('#ps-connect').click();await settled('pantry-scans');
   const renewed=await page.evaluate(()=>window.HouseholdPantryDevice.inspect());
   must(renewed.drafts.length===0&&renewed.meta.epoch===beforeExpiry.meta.epoch&&renewed.meta.revision>beforeExpiry.meta.revision&&!renewed.expired,'Explicit reconnect renews the device lease and actually prunes the expired IndexedDB draft');
   sameScanEvidence(renewed.meta.owner,beforeExpiry.meta.owner,'Clock advance and lease renewal preserve the exact current device owner');
   must(await page.locator('#ps-retry').isVisible()&&!await page.locator('#ps-retry').isDisabled(),'Original pending request remains offered for the expiry guard regression');
   await page.locator('#ps-retry').click();await settled('pantry-scans');
   must(await page.locator('#ps-error').isVisible()&&(await page.locator('#ps-status').innerText()).includes('abgelaufen')&&scanDraftPUTs.length===beforeExpiryPUTs,'Expired pending retry is refused before any additional actual PUT after lease renewal');
   const [statusResponse]=await Promise.all([page.waitForResponse(response=>response.request().method()==='GET'&&new URL(response.url()).pathname===new URL(expiryDraftRequest.url).pathname),page.locator('#ps-recover').click()]);
   const statusDraft=await statusResponse.json();await settled('pantry-scans');
   must(statusResponse.ok()&&statusDraft.id===expiryID&&statusDraft.version===expiryAccepted.version&&statusDraft.state==='active'&&(await page.locator('#ps-existing').innerText()).includes('überein')&&await page.locator('#ps-existing a').getAttribute('href')==='/m/pantry/batches?draft='+expiryID,'Expired local intent still recovers the exact accepted server draft by the same-ID status GET');
   sameScanEvidence(statusDraft.lines,expiryAccepted.lines,'Same-ID status retains every accepted expiry-fixture line without rewriting');sameScanEvidence(scanDraftPUTs,[savedDraftRequest,expiryDraftRequest],'Expiry refusal and status GET leave both original PUT identities and bodies unchanged');
   must(await page.locator('#ps-retry').isHidden()&&await page.locator('#ps-recheck').isHidden(),'Recovered expired intent exposes no retry or recheck action');
   report.evidence.scanExpiry={clockAdvanceDays:8,actualIndexedDBPruned:true,leaseRenewed:true,putCountBeforeExpiry:beforeExpiryPUTs,additionalPUTs:scanDraftPUTs.length-beforeExpiryPUTs,stableOriginalRequest:true,sameIDStatusRecovered:true,secondDraftUnposted:true};
  }finally{await page.clock.setSystemTime(new Date());}
  const afterScanLots=await api('/api/v1/pantry/products/'+product.id+'/lots');must(afterScanLots.records.length===0,'Offline scan and online draft transfer have no stock authority');
  await page.goto(base+firstScanURL);await settled('pantry-batches');const firstBeforePost=await api('/api/v1/pantry/batches/drafts/'+firstScanID);sameScanEvidence(firstBeforePost,firstScanSnapshot,'Original first server draft remains exactly unchanged before its separate stock confirmation');must(firstBeforePost.id===firstScanID&&firstBeforePost.state==='active'&&firstBeforePost.lines[0].label===suffix+' scanned lot','Separate stock confirmation still uses the unchanged original first draft and version, never the expiry-only fixture');await page.locator('#pb-review').click();await page.locator('#pb-review-panel:not([hidden])').waitFor();await page.locator('#pb-confirm').click();await page.locator('#pb-receipt:not([hidden])').waitFor();await settled('pantry-batches');const posted=await api('/api/v1/pantry/products/'+product.id+'/lots');must(posted.records.length===1&&posted.records[0].stock==='500.000000','Only separate existing batch confirmation books exact two250g packs');
  const expiryAfterPost=await api('/api/v1/pantry/batches/drafts/'+expiryID);sameScanEvidence(expiryAfterPost,expiryAccepted,'Independent expiry-fixture server draft remains exactly unchanged after first-draft posting');must(expiryAfterPost.state==='active'&&expiryAfterPost.version===expiryAccepted.version,'Independent expired scan server draft stays unposted and unchanged');page.off('request',countScanDraftPUT);

  // A real canvas MediaStream proves video/CSP/track cleanup. Decoder is
  // explicitly synthetic; physical camera/native decoder acceptance is unclaimed.
  const cameraPage=await context.newPage();cameraPage.setDefaultTimeout(20000);cameraPage.on('pageerror',error=>report.errors.push(safe(error)));
  await cameraPage.addInitScript(code=>{
   Object.defineProperty(window,'BarcodeDetector',{configurable:true,value:class{static async getSupportedFormats(){return ['code_128'];}async detect(){return [{rawValue:code}];}}});
   Object.defineProperty(navigator.mediaDevices,'getUserMedia',{configurable:true,value:async()=>{const canvas=document.createElement('canvas');canvas.width=640;canvas.height=360;canvas.getContext('2d').fillRect(0,0,640,360);const stream=canvas.captureStream(5);window.__pantryCameraTracks=stream.getTracks();return stream;}});
  },code);
  await cameraPage.goto(base+'/m/pantry/scans');await cameraPage.locator('#ps-connect').click();await cameraPage.waitForFunction(()=>document.querySelector('#pantry-scans').getAttribute('aria-busy')==='false');await cameraPage.locator('#ps-new').click();await cameraPage.waitForFunction(()=>document.querySelector('#pantry-scans').getAttribute('aria-busy')==='false');await cameraPage.locator('#ps-camera summary').click();await cameraPage.locator('#ps-camera-start').click();await cameraPage.waitForFunction(code=>document.querySelector('#ps-scan-form [name=code]').value===code,code);
  must(await cameraPage.locator('#ps-lines>.pe-row').count()===0,'Camera hit only fills input; it creates no draft line or quantity');must(await cameraPage.evaluate(()=>window.__pantryCameraTracks.every(track=>track.readyState==='ended')),'Positive synthetic detection stops every actual video track');report.evidence.camera='Real canvas MediaStream + synthetic decoder; physical camera/native decoding unrun';await cameraPage.close();

  // Exercise actual IndexedDB transactions; these are local isolation tests,
  // not fabricated server authorization or stock proofs.
  await page.goto(base+'/m/pantry/scans');await page.locator('#ps-connect').click();await settled('pantry-scans');
  const localBoundary=await page.evaluate(async()=>{const D=window.HouseholdPantryDevice,view=await D.inspect(),lease={epoch:view.meta.epoch,revision:view.meta.revision,owner:view.meta.owner},existingLines=view.drafts.reduce((n,d)=>n+d.lines.length,0);if(!Number.isSafeInteger(existingLines)||existingLines<0||existingLines>99)throw Error('Actual local boundary fixture cannot start below the100-line cap');const created=await D.create(lease),target=created.drafts.at(-1);target.lines=Array.from({length:99-existingLines},(_,i)=>D.line({id:crypto.randomUUID(),code:'BOUNDARY-'+i}));const filled=await D.save(created.lease,target),beforeRace=filled.drafts.reduce((n,d)=>n+d.lines.length,0),current=filled.drafts.find(d=>d.id===target.id),a={...current,lines:[...current.lines,D.line({id:crypto.randomUUID(),code:'LAST-A'})]},b={...current,lines:[...current.lines,D.line({id:crypto.randomUUID(),code:'LAST-B'})]};const results=await Promise.allSettled([D.save(filled.lease,a),D.save(filled.lease,b)]);const state=await D.inspect(),oldLease={epoch:state.meta.epoch,revision:state.meta.revision,owner:state.meta.owner};await D.reset();let refused=false;try{await D.create(oldLease);}catch{refused=true;}return {beforeRace,accepted:results.filter(r=>r.status==='fulfilled').length,count:state.drafts.reduce((n,d)=>n+d.lines.length,0),resetRefused:refused,remaining:(await D.inspect()).drafts.length};});
  must(localBoundary.beforeRace===99&&localBoundary.accepted===1&&localBoundary.count===100,'Concurrent local writers cannot exceed total100 lines or overwrite revision');must(localBoundary.resetRefused&&localBoundary.remaining===0,'Epoch reset denies stale callback and removes local drafts');
  const stale=await context.newPage();stale.setDefaultTimeout(20000);await stale.goto(base+'/m/pantry/scans');await stale.locator('#ps-connect').click();await stale.waitForFunction(()=>document.querySelector('#pantry-scans').getAttribute('aria-busy')==='false');await stale.locator('#ps-new').click();await stale.waitForFunction(()=>document.querySelector('#pantry-scans').getAttribute('aria-busy')==='false');
  await page.goto(base+'/settings');await page.locator('#preferences [name=offline]').uncheck();await page.getByRole('button',{name:'Geräteeinstellungen speichern',exact:true}).click();await page.getByText('Geräteeinstellungen gespeichert.',{exact:true}).waitFor();await stale.waitForFunction(()=>document.querySelector('#ps-editor').hidden&&document.querySelector('#ps-lines').children.length===0);must((await stale.evaluate(()=>window.HouseholdPantryDevice.inspect())).drafts.length===0,'Actual device opt-out deletes drafts and locks another open tab');await stale.close();
  const cachePaths=await page.evaluate(async()=>{const paths=[];for(const key of await caches.keys())for(const request of await(await caches.open(key)).keys())paths.push(new URL(request.url).pathname);return paths;});must(['/offline','/offline/scans','/offline/shopping','/offline/recipes'].every(p=>cachePaths.includes(p))&&cachePaths.every(p=>['/offline','/offline/scans','/offline/shopping','/offline/recipes'].includes(p)||p.startsWith('/assets/')),'PWA caches only neutral shells and public assets');
  await page.goto(base+'/m/pantry/leftovers/'+firstLot);await settled('pantry-leftovers');const cookies=await context.cookies(base);await context.clearCookies();await page.evaluate(()=>window.dispatchEvent(new Event('focus')));await page.waitForFunction(()=>document.querySelector('#pl-detail').hidden&&document.querySelector('#pl-detail-content').children.length===0);must(true,'Actual authentication loss hides and clears prior prepared detail');await context.addCookies(cookies);await page.goto(base+'/');
  const recoveryHandshake=process.env.HOUSEHOLD_PANTRY_BROWSER_RECOVERY_HANDSHAKE,controlState=process.env.HOUSEHOLD_PANTRY_BROWSER_CONTROL_STATE;
  if(!!recoveryHandshake!==!!controlState)throw Error('Pantry recovery requires both parent-owned private paths');
  if(recoveryHandshake){
   if(process.env.HOUSEHOLD_BROWSER_HANDOFF)throw Error('Recovery requires the actual standalone session and final logout');
   const localRoot=await fs.realpath(path.resolve(__dirname,'../.local')),handshake=path.resolve(recoveryHandshake),statePath=path.resolve(controlState),privateDirectory=await fs.realpath(path.dirname(handshake));
   if(path.dirname(privateDirectory)!==localRoot||!/^pantry-browser-auth-[a-f0-9]{32}$/.test(path.basename(privateDirectory))||path.dirname(statePath)!==privateDirectory||path.basename(statePath)!=='control-state.json'||path.basename(handshake)!=='pantry-recovery'||(await fs.lstat(path.dirname(handshake))).isSymbolicLink())throw Error('Recovery paths must use the exact parent-owned private Pantry directory');
   const freezer=await create('pantry','locations',{name:suffix+' recovery freezer',sort_order:'2'}),travel=await create('pantry','locations',{name:suffix+' recovery travel',sort_order:'3'});
   async function transferPrepared(sourceID,destination,quantity){
    const current=await api('/api/v1/m/pantry/lots/'+sourceID),before=await api('/api/v1/pantry/products/'+current.product_id+'/lots');
    const known=new Set(before.records.map(row=>row.id));
    await api('/api/v1/m/pantry/lots/'+sourceID+'/actions/transfer',{generation:bootstrap.states.pantry.generation,version:Number(current.version),data:{location_id:destination.id,quantity}});
    const after=await api('/api/v1/pantry/products/'+current.product_id+'/lots'),added=after.records.filter(row=>!known.has(row.id));
    must(added.length===1&&uuid.test(added[0].id),'Actual normal transfer creates exactly one fresh lot identity');
    const target=await api('/api/v1/m/pantry/lots/'+added[0].id);
    must(target.id===added[0].id&&target.product_id===current.product_id&&target.location_id===destination.id,'Actual transferred lot detail retains the exact product and selected destination');
    return added[0].id;
   }
   const firstTransfer=await transferPrepared(firstLot,freezer,'0.5'),secondTransfer=await transferPrepared(firstTransfer,travel,'0.25');
   for(const [lotID,initial,stock,locationID]of [[firstLot,'1.250000','0.750000',location.id],[firstTransfer,'0.500000','0.250000',freezer.id],[secondTransfer,'0.250000','0.250000',travel.id]]){
    const detail=await api('/api/v1/pantry/lots/'+lotID+'/prepared');
    must(detail.prepared.root_lot_id===firstLot&&detail.prepared.dish_name===suffix+' meal'&&detail.prepared.root_initial_portions==='1.250000'&&detail.prepared.initial_portions===initial&&detail.lot.stock===stock&&detail.lot.location_id===locationID,'Actual preparation lineage and current stock survive both normal transfer hops');
   }
   const refusedLeft=crypto.randomUUID(),refusedFoodFacts=crypto.randomUUID(),generation=bootstrap.states.pantry.generation;
   must((await api('/api/v1/pantry/leftovers/operations/'+refusedLeft+'/abandon',{generation})).state==='refused'&&(await api(foodFactsBase+'/operations/'+refusedFoodFacts+'/abandon',{generation})).state==='refused','Explicit PA10 and PA13 refusals are stored through normal current-authority APIs');
   await page.goto(base+'/settings');await page.locator('#preferences [name=offline]').check();await page.getByRole('button',{name:'Geräteeinstellungen speichern',exact:true}).click();await page.getByText('Geräteeinstellungen gespeichert.',{exact:true}).waitFor();
   await page.goto(base+'/m/pantry/scans');await page.locator('#ps-connect').click();await settled('pantry-scans');await page.locator('#ps-new').click();await settled('pantry-scans');
   await page.locator('#ps-scan-form [name=code]').fill(code);await page.locator('#ps-scan-form [name=quantity]').fill('1');await page.locator('#ps-scan-form [name=label]').fill(suffix+' old-generation local intent');await page.locator('#ps-scan-form button[type=submit]').click();await settled('pantry-scans');
   const oldDevice=await page.evaluate(()=>window.HouseholdPantryDevice.inspect());must(oldDevice.drafts.length===1&&oldDevice.drafts[0].owner.generation===generation,'A real old-generation local scan intent exists before maintenance');
   const currentContext=(await api('/api/v1/pantry/extensions/context')).context,currentBootstrap=await api('/api/v1/core/bootstrap');
   must(currentContext.user_id===currentBootstrap.user.id&&currentContext.household_id===currentBootstrap.user.household_id&&currentContext.session_id===currentBootstrap.user.session_id&&currentContext.role==='admin'&&currentContext.generation===generation,'Recovery begins with the actual same current administrator session and generation');
   const requestData={version:1,namespace:'household-pantry-browser-recovery-v1',nonce:crypto.randomUUID(),actor_id:currentContext.user_id,household_id:currentContext.household_id,session_id:currentContext.session_id,old_generation:generation,
    leftover_key:leftRequest.key,foodfacts_key:adoptionKey,leftover_refused_key:refusedLeft,foodfacts_refused_key:refusedFoodFacts,leftover_operation_id:leftReceipt.operation_id,foodfacts_operation_id:adoption.operation_id,root_lot_id:firstLot,transfer_lot_ids:[firstTransfer,secondTransfer]};
   const requestJSON=JSON.stringify(requestData),requestSHA256=crypto.createHash('sha256').update(requestJSON).digest('hex'),stateJSON=JSON.stringify(await context.storageState());
   if(Buffer.byteLength(requestJSON)>2048||Buffer.byteLength(stateJSON)>256*1024)throw Error('Private recovery handoff exceeds its fixed bound');
   await fs.writeFile(statePath,stateJSON,{flag:'wx',mode:0o600});await fs.writeFile(handshake+'.request.json',requestJSON,{flag:'wx',mode:0o600});
   let acknowledgement;const deadline=Date.now()+120000;
   while(Date.now()<deadline){
    try{
     const info=await fs.lstat(handshake+'.ack.json');if(!info.isFile()||info.isSymbolicLink()||info.size>2048)throw Error('Recovery acknowledgement is not a bounded regular file');
     const handle=await fs.open(handshake+'.ack.json','r');
     try{const buffer=Buffer.alloc(2049),{bytesRead}=await handle.read(buffer,0,2049,0);if(bytesRead>2048)throw Error('Recovery acknowledgement exceeds2048bytes');acknowledgement=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(buffer.subarray(0,bytesRead)));}finally{await handle.close();}
     break;
    }catch(error){if(error.code!=='ENOENT')throw error;}
    await new Promise(resolve=>setTimeout(resolve,250));
   }
   if(!acknowledgement){report.blocked.push('Requested real encrypted restore/purge recovery received no committed acknowledgement');throw Error(report.blocked.at(-1));}
   assert.deepEqual(Object.keys(acknowledgement).sort(),['version','namespace','nonce','actor_id','household_id','session_id','old_generation','generation','request_sha256','snapshot','restore_verified','purge_applied'].sort());
   for(const field of ['version','namespace','nonce','actor_id','household_id','session_id','old_generation'])assert.equal(acknowledgement[field],requestData[field]);
   must(acknowledgement.request_sha256===requestSHA256&&acknowledgement.generation===generation+1&&/^[a-f0-9]{64}$/.test(acknowledgement.snapshot)&&acknowledgement.restore_verified===true&&acknowledgement.purge_applied===true,'Parent acknowledgement binds the exact original keys, actor, real restore and committed newer generation');
   await page.goto(base+'/m/pantry/scans');
   const live=(await api('/api/v1/pantry/extensions/context')).context,liveBootstrap=await api('/api/v1/core/bootstrap');
   must(live.generation===acknowledgement.generation&&liveBootstrap.states.pantry.generation===live.generation&&live.user_id===requestData.actor_id&&live.household_id===requestData.household_id&&live.session_id===requestData.session_id,'Current server confirms the acknowledged generation and original live actor/session');
   const readOutcome=url=>page.evaluate(async url=>{const response=await fetch(url,{cache:'no-store',headers:{Accept:'application/json'}});return {status:response.status,data:await response.json()};},url);
   for(const [operationBase,key]of [['/api/v1/pantry/leftovers',leftRequest.key],['/api/v1/pantry/leftovers',refusedLeft],[foodFactsBase,adoptionKey],[foodFactsBase,refusedFoodFacts]]){
    const stale=await readOutcome(operationBase+'/operations/'+key+'?generation='+generation);must(stale.status===409,'Old-generation receipt query cannot expose details or authorize another effect');
    const current=await api(operationBase+'/operations/'+key+'?generation='+live.generation);must(current.state==='unknown'&&!current.result&&!current.receipt,'Retained original key is not rebased into the new generation');
   }
   for(const id of [firstLot,firstTransfer,secondTransfer])must((await readOutcome('/api/v1/pantry/lots/'+id+'/prepared')).status===404,'Purged original and transferred preparation details are no longer exposed');
   for(const entity of ['products','packs','barcodes','lots','stock_movements']){const rows=await api('/api/v1/m/pantry/'+entity);must(Array.isArray(rows.records)&&rows.records.length===0,'Current '+entity+' remain empty after real purge without automatic intent replay');}
   await page.locator('#ps-connect').click();await settled('pantry-scans');must(await page.locator('#ps-generation').isVisible()&&await page.locator('#ps-editor').isHidden(),'Old-generation local intent stays locked until an explicit discard');
   must((await page.evaluate(()=>window.HouseholdPantryDevice.inspect())).drafts.length===1,'Generation change alone has not silently discarded or rebased the local intent');
   await page.locator('#ps-discard-old').click();await settled('pantry-scans');
   const newDevice=await page.evaluate(()=>window.HouseholdPantryDevice.inspect());must(newDevice.drafts.length===0&&newDevice.meta.owner.generation===live.generation,'Explicit confirmed discard clears only obsolete local drafts and opens current device generation');
   report.evidence.recovery={requested:true,requestSHA256,snapshot:acknowledgement.snapshot,old_generation:generation,generation:live.generation,restoreVerified:true,purgeApplied:true,transferHops:2,acceptedReceipts:2,refusedReceipts:2,oldLocalIntent:'explicitly discarded; never executed or rebased'};
  }else report.evidence.recovery={requested:false,executed:false};
  if(!process.env.HOUSEHOLD_BROWSER_HANDOFF){
   await page.goto(base+'/settings');await page.locator('#preferences [name=offline]').check();await page.getByRole('button',{name:'Geräteeinstellungen speichern',exact:true}).click();await page.getByText('Geräteeinstellungen gespeichert.',{exact:true}).waitFor();await page.goto(base+'/m/pantry/scans');await page.locator('#ps-connect').click();await settled('pantry-scans');await page.locator('#ps-new').click();await settled('pantry-scans');must((await page.evaluate(()=>window.HouseholdPantryDevice.inspect())).drafts.length===1,'Actual local draft exists before explicit logout');
   await page.goto(base+'/settings/security');const logoutForm=page.locator('#logout-form');
   must(await logoutForm.getAttribute('action')==='/logout'&&(await logoutForm.getAttribute('method'))?.toLowerCase()==='post','Actual security page exposes the normal same-origin POST logout form');
   const [logoutResponse]=await Promise.all([page.waitForResponse(response=>response.request().method()==='POST'&&new URL(response.url()).origin===origin.origin&&new URL(response.url()).pathname==='/logout'),page.waitForURL(url=>url.origin===origin.origin&&url.pathname==='/login'),logoutForm.locator('button').click()]);
   must(logoutResponse.status()===303&&logoutResponse.headers()['location']==='/login'&&new URL(page.url()).origin===origin.origin&&new URL(page.url()).pathname==='/login','Actual logout POST redirects to the same-origin login page');
   const loggedOut=await page.evaluate(async()=>{
    const status=(await fetch('/api/v1/core/bootstrap')).status;
    // Observe physical storage before the opt-in-aware helper can mask rows
    // or initialize its database again. All native requests have one deadline.
    const physical=await new Promise((resolve,reject)=>{
     let db,tx,done=false;
     const timer=setTimeout(()=>finish(Error('Physical local scan deletion inspection timed out')),5000);
     function finish(error,value){if(done)return;done=true;clearTimeout(timer);if(error&&tx)try{tx.abort();}catch{}db?.close();if(error)reject(error);else resolve(value);}
     Promise.resolve().then(()=>indexedDB.databases()).then(databases=>{
      if(done)return;
      if(!databases.some(item=>item.name==='household-pantry-scans')){finish(null,{database_absent:true,drafts:0,owner_cleared:true});return;}
      const request=indexedDB.open('household-pantry-scans');
      request.onupgradeneeded=()=>{request.transaction.abort();};
      request.onerror=()=>finish(Error('Physical local scan database could not be inspected'));
      request.onblocked=()=>finish(Error('Physical local scan inspection was blocked'));
      request.onsuccess=()=>{
       db=request.result;if(done){db.close();return;}
       if(db.version!==1||db.objectStoreNames.length!==2||!db.objectStoreNames.contains('meta')||!db.objectStoreNames.contains('drafts')){finish(Error('Physical local scan database has an unknown schema'));return;}
       try{
        tx=db.transaction(['meta','drafts'],'readonly');const count=tx.objectStore('drafts').count(),device=tx.objectStore('meta').get('device');let drafts,meta,metaRead=false;
        count.onsuccess=()=>{drafts=count.result;};
        device.onsuccess=()=>{meta=device.result;metaRead=true;};
        tx.oncomplete=()=>{if(!Number.isSafeInteger(drafts)||drafts<0||!metaRead)finish(Error('Physical local scan count or owner could not be inspected'));else finish(null,{database_absent:false,drafts,owner_cleared:meta===undefined||meta?.owner===null});};
        tx.onerror=tx.onabort=()=>finish(Error('Physical local scan count could not be completed'));
       }catch{finish(Error('Physical local scan inspection could not start'));}
      };
     }).catch(()=>finish(Error('Physical local scan databases could not be listed')));
    });
    return {status,physical,drafts:(await window.HouseholdPantryDevice.inspect()).drafts.length,owner:localStorage.getItem('household-offline-owner')};
   });
   must(loggedOut.status===401&&loggedOut.drafts===0&&loggedOut.owner===null,'Actual logout revokes session and clears local Scan namespace');
   must(loggedOut.physical.drafts===0&&loggedOut.physical.owner_cleared===true,'Native IndexedDB inspection proves physical scan drafts and prior device owner are cleared after logout');
   report.evidence.logout='Actual standalone logout';report.evidence.logoutStorage=loggedOut.physical;
  }else report.evidence.logout='Shared-session mode retains account; standalone logout acceptance required separately';
  must(report.errors.length===0,'No Pantry JavaScript errors');must(report.externalRequests.length===0,'No browser external requests');
  if(process.env.HOUSEHOLD_BROWSER_HANDOFF)await context.storageState({path:process.env.HOUSEHOLD_BROWSER_HANDOFF});report.passed=true;
 }catch(error){report.passed=false;report.failure=safe(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(safe(error));process.exitCode=1;});
