'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const base = process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101';
const origin = new URL(base);
if (!['localhost','127.0.0.1'].includes(origin.hostname) || origin.port !== '8101' || origin.pathname !== '/') throw Error('Requires isolated Household review server 8101');
const output = path.resolve('outputs/catalog-review');
const report = {checks:[], screenshots:[], accessibility:[], errors:[], externalRequests:[]};
const must = (value, name) => {if (!value) throw Error(name); report.checks.push(name);};
(async () => {
  await fs.mkdir(output,{recursive:true});
  const browser = await chromium.launch({headless:true, executablePath:process.env.CHROME_PATH || (process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
  try {
    const context = await browser.newContext({viewport:{width:1440,height:1000}, serviceWorkers:'block'});
    await context.route('**/*', route => {
      if (new URL(route.request().url()).origin !== origin.origin) {report.externalRequests.push(route.request().url()); return route.abort();}
      return route.continue();
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20000);page.setDefaultNavigationTimeout(20000);
    page.on('pageerror', error => report.errors.push(String(error)));
    await page.goto(base+'/login');
    await page.locator('[name=username]').fill('bootstrap');
    await page.locator('[name=password]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD || 'Browser review only 1234');
    await Promise.all([page.waitForURL(url=>url.pathname!=='/login'), page.getByRole('button',{name:/^Anmelden/}).click()]);
    if(new URL(page.url()).pathname==='/change-password'){
      await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD || 'Browser review only 1234');
      await page.locator('[name=next]').fill('Browser review only 1234');
      await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);
      await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill('Browser review only 1234');
      await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);
    }
    const fixture = await page.evaluate(async () => {
      const bootstrap = await fetch('/api/v1/core/bootstrap').then(r=>r.json());
      const csrf = document.querySelector('meta[name="csrf-token"]').content;
      const create = async (entity, data) => {
        const response = await fetch('/api/v1/m/shopping/'+entity,{method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({data,generation:bootstrap.states.shopping.generation})});
        if (!response.ok) throw Error(await response.text());
        return response.json();
      };
      const shop = await create('stores',{name:'Wochenmarkt '+Date.now()});
      const other = await create('stores',{name:'Supermarkt '+Date.now()});
      for (const [store,groups] of [[shop,['Vorrat','Kühlung']],[other,['Kühlung','Vorrat']]]) {
        for (const [index,name] of groups.entries()) await create('aisles',{store_id:store.id,name,sort_order:index});
      }
      const product = await create('products',{name:'Haferdrink',brand:'Morgenhof',pack_quantity:'500',pack_unit:'ml'});
      const replacement = await create('products',{name:'Haferdrink',brand:'Wiesenland',pack_quantity:'1',pack_unit:'l'});
      for (const [p,price] of [[product,'2.49'],[replacement,'3.00']]) await create('price_observations',{product_id:p.id,store_id:shop.id,observed_on:'2026-01-01',quantity:'1',unit_code:'piece',price_minor:price});
      const list = await create('lists',{name:'Wochenende · Markt und Vorrat',store_id:shop.id});
      const first = await create('items',{list_id:list.id,name:'Haferdrink für das Frühstück',product_id:product.id,quantity:'2',unit_code:'piece',group_name:'Vorrat'});
      const second = await create('items',{list_id:list.id,name:'Haferdrink zum Kochen',product_id:product.id,quantity:'0.5',unit_code:'l',group_name:'Kühlung'});
      await create('items',{list_id:list.id,name:'Spülbürste',quantity:'1'});
      const members = await fetch('/api/v1/core/reference-options/core/memberships').then(r=>r.json());
      return {shop,other,product,replacement,list,first,second,member:members.records[0]};
    });
    const listURL = base+'/m/shopping/lists/'+fixture.list.id;
    await page.goto(listURL);
    await page.getByRole('button',{name:'Erledigen: Haferdrink für das Frühstück',exact:true}).waitFor();
    must(await page.locator('#shopping-items>.shopping-group>h3').first().innerText()==='Vorrat','Store aisle order overrides alphabetic grouping');
    await page.locator('#shopping-context>summary').click();
    must((await page.locator('#shopping-estimate').innerText()).includes('7,47 €') && (await page.locator('#shopping-estimate').innerText()).includes('1 Positionen ohne'),'Estimate converts packs and liters exactly and names unknown prices');
    await page.locator('#shopping-claim').focus(); await page.keyboard.press('Enter');
    await page.getByRole('button',{name:'Einkaufsstatus um 30 Minuten verlängern',exact:true}).waitFor();
    must((await page.locator('#shopping-shopper').innerText()).includes('kauft ein bis'),'Shopping activity names a person and expiry');
    await page.locator('#shopping-release').focus(); await page.keyboard.press('Enter');
    await page.getByRole('button',{name:'Ich kaufe jetzt ein · 30 Minuten',exact:true}).waitFor();
    must(await page.locator('#shopping-claim').evaluate(el=>el===document.activeElement),'Releasing activity retains a usable keyboard focus target');
    const switchStore = async store => {
      await page.goto(listURL);
      const edit=page.locator('form[data-method=PATCH]');
      await edit.locator('[name=store_id][data-reference-state=ready]').waitFor();
      await edit.locator('[name=store_id]').selectOption(store.id);
      await Promise.all([page.waitForEvent('framenavigated'),edit.getByRole('button',{name:'Änderungen speichern',exact:true}).click()]);
      await page.locator('#shopping-items .shopping-group>h3').first().waitFor();
    };
    await switchStore(fixture.other);
    must(await page.locator('#shopping-items>.shopping-group>h3').first().innerText()==='Kühlung','Changing store applies its different aisle order');
    await page.locator('#shopping-context>summary').click();
    must((await page.locator('#shopping-estimate').innerText()).includes('3 Positionen ohne'),'A different store never silently inherits another store price');
    await switchStore(fixture.shop);
    await page.goto(base+'/m/shopping/items/'+fixture.second.id);
    const edit=page.locator('form[data-method=PATCH]');
    await edit.locator('[name=product_id][data-reference-state=ready]').waitFor();
    must((await edit.locator('[name=product_id] option:checked').innerText()).includes('Morgenhof') && (await edit.locator('[name=product_id] option:checked').innerText()).includes('500 ml'),'Product picker distinguishes brand and pack');
    async function slowSelectedReference(form, name, whileLoading) {
      const select=form.locator('[name='+name+']');
      const selected=await select.inputValue();
      const field=form.locator('.reference-field').filter({has:page.locator('[name='+name+']')});
      let release, arrived;
      const gate=new Promise(resolve=>release=resolve), held=new Promise(resolve=>arrived=resolve);
      const handler=async route=>{arrived();await gate;await route.continue();};
      const pattern='**/api/v1/core/reference-options/shopping/products?q=*';
      await page.route(pattern,handler);
      try {
        await field.getByRole('searchbox').fill('Haferdrink');
        await field.getByRole('button',{name:'Suchen',exact:true}).click();await held;
        must(await select.isDisabled() && await form.locator('button[type=submit],button:not([type])').isDisabled(),'Selected optional '+name+' blocks submit while reference search is loading');
        if(whileLoading)await whileLoading();
      } finally {release();}
      await form.locator('[name='+name+'][data-reference-state=ready]').waitFor();
      must(await select.inputValue()===selected,'Delayed optional '+name+' keeps its selected identity');
      await page.unroute(pattern,handler);
    }
    await slowSelectedReference(edit,'product_id');
    await page.evaluate(async id=>{
      const row=await fetch('/api/v1/m/shopping/items/'+id).then(r=>r.json());
      const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
      const response=await fetch('/api/v1/m/shopping/items/'+id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({version:row.version,generation:bootstrap.states.shopping.generation,data:{note:'Parallel geprüfte Variante'}})});
      if(!response.ok)throw Error(await response.text());
    },fixture.second.id);
    await edit.getByRole('button',{name:'Änderungen speichern',exact:true}).click();
    await edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).waitFor();
    let retryWrites=0;
    const countRetry=request=>{if(request.method()==='PATCH'&&new URL(request.url()).pathname==='/api/v1/m/shopping/items/'+fixture.second.id)retryWrites++;};
    page.on('request',countRetry);
    try {
      await slowSelectedReference(edit,'product_id',async()=>{
        await edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).click();
        await edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).click();
        must(retryWrites===0 && (await edit.locator('.conflict-review').innerText()).includes('Auswahl erneut laden'),'Conflict retry cannot omit a selected optional reference during delayed search');
      });
    } finally {page.off('request',countRetry);}
    await Promise.all([page.waitForEvent('framenavigated'),edit.getByRole('button',{name:'Geprüfte Eingaben erneut speichern',exact:true}).click()]);
    await edit.locator('[name=product_id][data-reference-state=ready]').waitFor();
    await edit.locator('[name=availability]').selectOption('unavailable');
    await Promise.all([page.waitForEvent('framenavigated'),edit.getByRole('button',{name:'Änderungen speichern',exact:true}).click()]);
    await page.goto(listURL);
    await page.getByText('Nicht verfügbar · Details öffnen, um Verfügbarkeit oder Ersatz zu ändern.',{exact:true}).waitFor();
    must(await page.getByRole('button',{name:'Erledigen: Haferdrink zum Kochen',exact:true}).isDisabled(),'Unavailable item remains visible with an explicit disabled check');
    await page.goto(base+'/m/shopping/items/'+fixture.second.id);
    await edit.locator('[name=replacement_product_id][data-reference-state=ready]').waitFor();
    await edit.locator('[name=assignee_id][data-reference-state=ready]').waitFor();
    await edit.locator('[name=availability]').selectOption('substituted');
    await edit.locator('[name=replacement_product_id]').selectOption(fixture.replacement.id);
    await edit.locator('[name=assignee_id]').selectOption(fixture.member.id);
    await Promise.all([page.waitForEvent('framenavigated'),edit.getByRole('button',{name:'Änderungen speichern',exact:true}).click()]);
    await page.goto(listURL);
    await page.getByText('Ersatz: Haferdrink · Wiesenland · 1 l',{exact:true}).waitFor();
    must(await page.getByText('Zuständig: '+fixture.member.label,{exact:true}).count()===1,'Assignment and explicit substitute appear on the working list');
    await page.locator('#shopping-context>summary').click();
    must((await page.locator('#shopping-estimate').innerText()).includes('6,48 €'),'Estimate uses the selected substitute pack and price');
    const quick=page.locator('#shopping-quick-add');
    if(await page.locator('#shopping-entry').getAttribute('open')===null)await page.locator('#shopping-entry>summary').click();
    await quick.locator('#shopping-details>summary').click();
    await quick.locator('[name=product_id][data-reference-state=ready]').waitFor();
    await quick.locator('[name=product_id]').selectOption(fixture.product.id);
    await quick.locator('[name=name]').fill('Auswahl bleibt erhalten');
    await slowSelectedReference(quick,'product_id');
    async function changeFirst(product) {
      await page.evaluate(async({id,product})=>{
        const row=await fetch('/api/v1/m/shopping/items/'+id).then(r=>r.json());
        const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json());
        const response=await fetch('/api/v1/m/shopping/items/'+id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({version:row.version,generation:bootstrap.states.shopping.generation,data:{product_id:product}})});
        if(!response.ok)throw Error(await response.text());
      },{id:fixture.first.id,product:product.id});
    }
    await changeFirst(fixture.replacement);
    await page.getByRole('button',{name:'Erledigen: Haferdrink für das Frühstück',exact:true}).click();
    await page.locator('#shopping-conflict').getByText(/Produkt:.*Morgenhof.*Wiesenland/).waitFor();
    must(await page.locator('#shopping-conflict').getByRole('link',{name:'Aktuelle Details öffnen',exact:true}).count()===1,'Product-only conflict displays old and current named variants before confirmation');
    await page.getByRole('button',{name:'Aktuellen Stand behalten',exact:true}).click();
    await page.locator('#shopping-conflict').waitFor({state:'hidden'});
    await changeFirst(fixture.product);
    await page.goto(base+'/m/shopping/products/'+fixture.product.id);
    await page.getByText('Dateien & unterstützte Erfassung',{exact:true}).click();
    const image=await page.evaluate(()=>{const canvas=document.createElement('canvas');canvas.width=160;canvas.height=160;const c=canvas.getContext('2d');c.fillStyle='#dcebe5';c.fillRect(0,0,160,160);c.fillStyle='#14201c';c.font='20px sans-serif';c.fillText('Haferdrink',20,65);c.fillText('500 ml',20,100);return canvas.toDataURL('image/png').split(',')[1];});
    await page.locator('input[type=file]').setInputFiles({name:'haferdrink.png',mimeType:'image/png',buffer:Buffer.from(image,'base64')});
    await Promise.all([page.waitForEvent('framenavigated'),page.getByRole('button',{name:'Datei speichern',exact:true}).click()]);
    await page.goto(listURL);
    await page.waitForFunction(()=>[...document.querySelectorAll('#shopping-items img')].some(image=>image.complete&&image.naturalWidth>0),null,{timeout:45000});
    must(true,'Sanitized private product preview loads through authenticated endpoint');
    await page.locator('#shopping-claim').click();
    await page.getByRole('button',{name:'Einkaufsstatus um 30 Minuten verlängern',exact:true}).waitFor();
    // Presentation edge case only: the claim itself is real; its response label
    // is expanded without changing the shared fixture account.
    const longShopper='Alexandra-Maria Musterfrau von Sonnenberg und Hohenwald';
    await page.route('**/api/v1/shopping/lists/'+fixture.list.id+'/items?*',async route=>{
      const response=await route.fetch(), body=await response.json();
      body.context.shopper_name=longShopper;
      await route.fulfill({response,json:body});
    });
    await page.locator('#shopping-refresh').click();
    await page.getByText(new RegExp('^'+longShopper+' kauft ein bis')).waitFor();
    await page.locator('#shopping-mode').check();
    async function capture(name){await page.evaluate(()=>scrollTo(0,0));const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))});must(!result.violations.length,'Accessibility '+name);}
    await capture('catalog-desktop');
    await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.dataset.theme='dark');
    await capture('catalog-mobile-dark');
    must(await page.locator('#shopping-shopper').evaluate(el=>!el.hidden&&!el.closest('details:not([open])')&&el.getBoundingClientRect().bottom<document.querySelector('[data-shopping-item]').getBoundingClientRect().top),'Active shopper with long name is visible before items without disclosure');
    must(await page.locator('#shopping-claim').evaluate(el=>el.getBoundingClientRect().bottom<document.querySelector('[data-shopping-item]').getBoundingClientRect().top),'Own shopping activity action is reachable before article checks');
    must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Catalog mobile view has no horizontal overflow');
    must(await page.locator('[data-shopping-check]').first().evaluate(el=>el.getBoundingClientRect().bottom<=document.querySelector('.mobile-nav').getBoundingClientRect().top),'Catalog first check remains above fixed mobile navigation');
    must(!report.errors.length,'No catalog JavaScript exceptions');
    must(!report.externalRequests.length,'No external catalog frontend requests');
    report.passed=true;
  } catch(error) {report.passed=false;report.failure=String(error);throw error;}
  finally {await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
  console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
