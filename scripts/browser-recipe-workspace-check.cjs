'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server8101');
const output=path.resolve('outputs/recipe-review'),report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(String(error)));page.on('dialog',dialog=>dialog.accept());
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);}
  await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);await login('Browser review only 1234');}
  await require('./browser-locale-helper.cjs').setBrowserLocale(page);
  const fixture=await page.evaluate(async()=>{
   const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),stamp=Date.now();
   const names=['Gemeinsam kochen '+stamp,('Familienrezept'+stamp+'M'.repeat(80)).slice(0,80),('Gemeinsam '+stamp+' vorbereiten und mit der ganzen Familie am Wochenende entspannt kochen').slice(0,80)],tags=[];
   for(const name of names){
    const response=await fetch('/api/v1/m/recipes/tags',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation:bootstrap.states.recipes.generation,data:{name}})});
    if(!response.ok)throw Error(await response.text());tags.push(await response.json());
   }
   return{tag:tags[0],layoutTags:tags.slice(1),generation:bootstrap.states.recipes.generation};
  });
  async function capture(name){await page.evaluate(async()=>{scrollTo(0,0);await new Promise(requestAnimationFrame);});const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  async function guardedSwitch(trigger,name,shot){const title='Neuer ungespeicherter Entwurf · '+name,amount='130.222222';await page.locator('[name=title]').fill(title);await page.locator('#recipe-ingredients > li').last().locator('[data-path$=".quantity"]').fill(amount);const preserved=async()=>await page.locator('[name=title]').inputValue()===title&&await page.locator('#recipe-ingredients > li').last().locator('[data-path$=".quantity"]').inputValue()===amount;await page.locator(trigger).click();await page.locator('#recipe-switch-dialog[open]').waitFor();must(await preserved(),name+' preview preserves unsaved metadata and ingredient');await page.locator('#recipe-switch-cancel').click();must(await preserved(),name+' cancellation preserves both unsaved fields');await page.locator(trigger).click();await page.locator('#recipe-switch-dialog[open]').waitFor();if(shot)await capture(shot);await page.locator('#recipe-switch-confirm').click();must(await page.locator('[name=title]').inputValue()!==title&&await page.locator('#recipe-ingredients > li').last().locator('[data-path$=".quantity"]').inputValue()!==amount,name+' replaces fields only after explicit confirmation');}
  await page.goto(base+'/m/recipes/recipes');await page.getByRole('link',{name:'Neues Rezept',exact:true}).click();await page.locator('#recipe-form:not([hidden])').waitFor();
  must(!await page.locator('.recipe-contents').evaluate(el=>el.open),'Recipe contents start collapsed');
  await page.locator('.recipe-contents summary').focus();await page.keyboard.press('Enter');
  must(await page.locator('.recipe-contents').evaluate(el=>el.open),'Recipe contents open with keyboard');
  for(const target of ['recipe-basics-heading','recipe-ingredients-heading','recipe-steps-heading','recipe-tags-heading','recipe-save-heading']){const link=page.locator('.recipe-section-nav a[href="#'+target+'"]');await link.focus();await page.keyboard.press('Enter');must(await page.locator('#'+target).evaluate(el=>el===document.activeElement),'Recipe section jump focuses '+target);}
  await page.locator('#recipe-save').click();await page.locator('[name=title][aria-invalid=true]').waitFor();
  must(await page.locator('#recipe-errors').evaluate(el=>el===document.activeElement),'Named recipe validation error receives focus');
  await page.locator('[name=title]').fill('Haferpfannkuchen für ein gemeinsames Frühstück');await page.locator('[name=base_servings]').fill('2.500001');await page.locator('[name=summary]').fill('Zutaten in einer Schüssel verrühren, kurz ruhen lassen und portionsweise ausbacken.');await page.locator('[name=prep_minutes]').fill('10');await page.locator('[name=cook_minutes]').fill('15');
  const addIngredient=async(name,amount,unit)=>{await page.locator('#recipe-add-ingredient').click();const row=page.locator('#recipe-ingredients > li').last();await row.locator('[data-path$=".name"]').fill(name);if(amount)await row.locator('[data-path$=".quantity"]').fill(amount);if(unit)await row.locator('[data-path$=".unit_code"]').selectOption(unit);return row;};
  await addIngredient('Haferflocken','125.000001','g');const salt=await addIngredient('Salz','','');await salt.locator('[data-path$=".quantity_note"]').fill('Eine Prise, nach Geschmack');
  await page.locator('#recipe-add-step').click();await page.locator('#recipe-steps > li').last().locator('textarea').fill('Haferflocken mit den weiteren Zutaten verrühren.');await page.locator('#recipe-steps [data-path$=".timer_seconds"]').fill('0');
  await page.locator('#recipe-add-step').click();await page.locator('#recipe-steps > li').last().locator('textarea').fill('Teig ruhen lassen und anschließend in der Pfanne ausbacken.');await page.locator('#recipe-steps > li').last().locator('[data-path$=".timer_seconds"]').fill('90');
  await page.locator('#recipe-tag-choice[data-reference-state=ready]').selectOption(fixture.tag.id);await page.locator('#recipe-add-tag').click();
  for(const tag of fixture.layoutTags){await page.locator('#recipe-tag-choice').selectOption(tag.id);await page.locator('#recipe-add-tag').click();}
  const initialTheme=await page.evaluate(()=>window.HouseholdTheme.get()),initialViewport=page.viewportSize();
  for(const theme of ['light','dark','pink-light','pink-dark']){
   await page.evaluate(theme=>window.HouseholdTheme.apply(theme),theme);
   for(const width of [320,390,768,1440]){
    await page.setViewportSize({width,height:1000});await page.evaluate(()=>new Promise(requestAnimationFrame));
    const layout=await page.locator('#recipe-tags > li').evaluateAll(rows=>rows.map(row=>{
     const label=row.querySelector('span'),button=row.querySelector('button'),bounds=row.getBoundingClientRect(),labelBounds=label.getBoundingClientRect(),buttonBounds=button.getBoundingClientRect(),range=document.createRange();range.selectNodeContents(button);
     return{name:label.textContent,labelFits:label.scrollWidth<=label.clientWidth+1&&labelBounds.right<=bounds.right+1,buttonFits:buttonBounds.left>=bounds.left-1&&buttonBounds.right<=bounds.right+1,singleLine:range.getClientRects().length===1,width:buttonBounds.width,height:buttonBounds.height};
    }));
    must(layout.length===3&&[fixture.tag,...fixture.layoutTags].every(tag=>layout.some(row=>row.name===tag.name&&row.labelFits)),theme+'/'+width+' preserves complete short, spaced and unbroken 80-character tags within their rows');
    must(layout.every(row=>row.singleLine&&row.buttonFits&&row.width>=44&&row.height>=44),theme+'/'+width+' keeps every remove label on one readable line with a 44px target');
    must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),theme+'/'+width+' long tags cause no horizontal page overflow');
    if(width===320||width===1440){const file=path.join(output,'recipe-tags-'+theme+'-'+width+'.png');await page.locator('section[aria-labelledby="recipe-tags-heading"]').screenshot({path:file,animations:'disabled'});report.screenshots.push(file);}
   }
   await page.setViewportSize({width:320,height:1000});
   const row=page.locator('#recipe-tags > li').filter({has:page.getByText(fixture.layoutTags[0].name,{exact:true})}),button=row.getByRole('button',{name:'Entfernen',exact:true});
   const index=await row.evaluate(row=>Array.from(row.parentElement.children).indexOf(row));
   await page.locator('#recipe-add-tag').focus();for(let tab=0;tab<=index;tab++)await page.keyboard.press('Tab');
   must(await button.evaluate(button=>button===document.activeElement&&button.matches(':focus-visible')),theme+' keyboard navigation visibly focuses the long-tag remove button');
   await page.keyboard.press(theme.includes('dark')?'Space':'Enter');
   must(await row.count()===0&&await page.locator('#recipe-tags > li').count()===2&&await page.locator('#recipe-tag-choice').evaluate(select=>select===document.activeElement),theme+' keyboard removal removes only the chosen tag and returns focus to its selector');
   await page.locator('#recipe-tag-choice').selectOption(fixture.layoutTags[0].id);await page.locator('#recipe-add-tag').click();
   const name='recipe-tags-'+theme,scan=await new AxeBuilder({page}).include('section[aria-labelledby="recipe-tags-heading"]').withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
   report.accessibility.push({name,violations:scan.violations});must(!scan.violations.length,'Accessibility '+name);
  }
  await page.evaluate(theme=>window.HouseholdTheme.apply(theme),initialTheme);await page.setViewportSize(initialViewport);
  const up=page.getByRole('button',{name:'Zutat 2 nach oben',exact:true});await up.focus();await page.keyboard.press('Enter');
  must(await page.locator('#recipe-ingredients > li').first().locator('[data-path$=".name"]').inputValue()==='Salz','Keyboard reorders ingredients without losing their quantities');
  must(await page.locator('#recipe-ingredients > li').first().locator('button:not(:disabled)').first().evaluate(el=>el===document.activeElement),'Reordered row retains usable keyboard focus');
  await page.locator('#recipe-save-draft').click();await page.locator('#recipe-status').getByText(/24 Stunden/).waitFor();await page.reload();await page.locator('#recipe-local-draft:not([hidden])').waitFor();await page.locator('#recipe-load-draft').click();
  must(await page.locator('[name=base_servings]').inputValue()==='2.500001'&&await page.locator('#recipe-ingredients > li').count()===2,'Explicit account-bound device draft survives reload');
  await guardedSwitch('#recipe-load-draft','Device draft');
  await capture('recipe-editor-desktop');
  await page.route('**/api/v1/recipes/workspace',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Speichern vorübergehend nicht erreichbar'})}));await page.locator('#recipe-save').click();await page.locator('#recipe-errors').getByText('Speichern vorübergehend nicht erreichbar',{exact:true}).waitFor();
  must(await page.locator('#recipe-ingredients > li').last().locator('[data-path$=".quantity"]').inputValue()==='125.000001','Failed save preserves the complete exact draft');await page.unroute('**/api/v1/recipes/workspace');
  const creation=page.waitForResponse(response=>response.url()===base+'/api/v1/recipes/workspace'&&response.request().method()==='POST');await page.locator('#recipe-save').click();const created=await creation;if(!created.ok())throw Error('Recipe creation failed: '+await created.text());await page.waitForURL('**/workspace');await page.locator('#recipe-form:not([hidden])').waitFor();
  const originalURL=page.url(),rid=await page.locator('#recipe-workspace').getAttribute('data-id');
  let bundle=await page.evaluate(async rid=>fetch('/api/v1/recipes/'+rid+'/workspace').then(r=>r.json()),rid);
  must(bundle.recipe.base_servings==='2.500001'&&bundle.ingredients[1].quantity==='125.000001'&&bundle.ingredients[0].quantity===null,'Real save keeps exact and unknown quantities');
  must(bundle.tags.length===3&&fixture.layoutTags.every(tag=>bundle.tags.some(saved=>saved.id===tag.id&&saved.name===tag.name)),'Real save and reload preserve complete 80-character tag names after keyboard removal and reassignment');
  must(bundle.steps[0].timer_seconds===null&&bundle.steps[1].timer_seconds===90,'Zero timer stays absent while positive timer is retained');
  await page.locator('#recipe-version-list li').first().waitFor();must(await page.locator('#recipe-version-list li').count()===1,'Whole editor save produces one full recipe version');
  must(!await page.evaluate(()=>Object.keys(localStorage).some(key=>key.startsWith('household-recipe-draft:'))),'Successful save removes the device draft');
  await page.locator('[name=notes]').fill('Mein noch ungespeicherter Hinweis');
  await page.evaluate(async({rid,generation,bundle})=>{const response=await fetch('/api/v1/m/recipes/ingredients/'+bundle.ingredients[1].id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation,version:bundle.ingredients[1].version,data:{quantity:'150.000001'}})});if(!response.ok)throw Error(await response.text());},{rid,generation:fixture.generation,bundle});
  await page.locator('#recipe-save').click();await page.locator('#recipe-conflict:not([hidden])').waitFor();await page.locator('#recipe-compare').click();await page.locator('#recipe-comparison:not([hidden])').waitFor();
  must(await page.locator('#recipe-differences').innerText().then(text=>text.includes('Haferflocken')&&text.includes('125.000001 → 150.000001')),'Conflict names changed ingredient and exact old/current values');
  must(await page.locator('[name=notes]').inputValue()==='Mein noch ungespeicherter Hinweis','Concurrent generic edit preserves my full draft');
  await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'dark'});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('recipe-conflict-mobile-dark');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile editor has no horizontal page overflow');
  await page.locator('#recipe-keep-draft').click();await Promise.all([page.waitForEvent('domcontentloaded'),page.locator('#recipe-save').click()]);await page.locator('#recipe-form:not([hidden])').waitFor();await page.locator('#recipe-version-list li').nth(2).waitFor();
  must(await page.locator('#recipe-ingredients > li').last().locator('[data-path$=".quantity"]').inputValue()==='125.000001','Explicit reviewed overwrite retains my chosen quantity');
  await page.locator('#recipe-version-list li').last().getByRole('button').click();await page.locator('#recipe-history-preview:not([hidden])').waitFor();
  must(await page.locator('#recipe-history-content').innerText().then(text=>text.includes('125.000001')&&!text.includes('Mein noch ungespeicherter Hinweis')),'History preview is the immutable original content');
  await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('recipe-history-mobile-dark');
  await guardedSwitch('#recipe-restore-version','History restoration','recipe-draft-replace-mobile-dark');must(await page.locator('[name=notes]').inputValue()==='','Reviewed history becomes an editable draft without immediate mutation');
  await Promise.all([page.waitForEvent('domcontentloaded'),page.locator('#recipe-save').click()]);await page.locator('#recipe-form:not([hidden])').waitFor();await page.locator('#recipe-version-list li').nth(3).waitFor();must(await page.locator('#recipe-version-list li').count()===4,'Restoring history appends a new version');
  await page.locator('#recipe-version-list li').last().getByRole('button').click();await page.locator('#recipe-history-preview:not([hidden])').waitFor();await guardedSwitch('#recipe-variant','Variant preparation');await page.locator('[name=title]').fill('Haferpfannkuchen · Wochenendvariante');await Promise.all([page.waitForURL(url=>url.href!==originalURL&&url.pathname.endsWith('/workspace')),page.locator('#recipe-save').click()]);await page.locator('#recipe-form:not([hidden])').waitFor();
  const variantID=await page.locator('#recipe-workspace').getAttribute('data-id');const variant=await page.evaluate(async id=>fetch('/api/v1/recipes/'+id+'/workspace').then(r=>r.json()),variantID);
  must(variantID!==rid&&variant.recipe.source_version_id&&variant.ingredients[0].id!==bundle.ingredients[0].id,'Variant has independent recipe/child identities and reviewed provenance');
  await page.locator('#recipe-ingredients > li').first().getByRole('button',{name:'Entfernen',exact:true}).click();await Promise.all([page.waitForEvent('domcontentloaded'),page.locator('#recipe-save').click()]);await page.locator('#recipe-form:not([hidden])').waitFor();
  bundle=await page.evaluate(async id=>fetch('/api/v1/recipes/'+id+'/workspace').then(r=>r.json()),rid);must(bundle.ingredients.length===2,'Variant edits do not change the source recipe');
  const litreID=await page.evaluate(async({id,generation})=>{const current=await fetch('/api/v1/recipes/'+id+'/workspace').then(r=>r.json()),ingredient=current.ingredients[0];const response=await fetch('/api/v1/m/recipes/ingredients/'+ingredient.id,{method:'PATCH',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation,version:ingredient.version,data:{name:'Wasser',quantity:'1.250001',unit_code:'l'}})});if(!response.ok)throw Error(await response.text());return ingredient.id;},{id:variantID,generation:fixture.generation});
  await page.reload();await page.locator('#recipe-form:not([hidden])').waitFor();must(await page.locator('#recipe-ingredients [data-path$=".unit_code"]').first().inputValue()==='l','Existing litre ingredient is selected when the editor loads');
  await page.locator('[name=notes]').fill('Nur die Notiz geändert; Liter bleiben erhalten.');await Promise.all([page.waitForEvent('domcontentloaded'),page.locator('#recipe-save').click()]);await page.locator('#recipe-form:not([hidden])').waitFor();
  const litreAfter=await page.evaluate(async({id,ingredient})=>{const saved=await fetch('/api/v1/recipes/'+id+'/workspace').then(r=>r.json());return saved.ingredients.find(row=>row.id===ingredient);},{id:variantID,ingredient:litreID});must(litreAfter?.unit_code==='l'&&litreAfter.quantity==='1.250001','Saving an unrelated edit preserves the existing litre unit and exact quantity');
  await page.locator('[name=notes]').fill('Privater Geräteentwurf');await page.locator('#recipe-save-draft').click();must(await page.evaluate(()=>Object.keys(localStorage).some(key=>key.startsWith('household-recipe-draft:'))),'Private draft is stored only after explicit save');
  await page.goto(base+'/offline');must(await page.evaluate(()=>Object.keys(localStorage).some(key=>key.startsWith('household-recipe-draft:'))),'Anonymous offline fallback does not erase an explicitly saved private draft');
  await page.evaluate(()=>{localStorage.setItem('household-recipe-draft:foreign',JSON.stringify({owner:'another-user',household:'another-household',generation:1,expires:Date.now()+10000}));localStorage.setItem('household-recipe-draft:expired',JSON.stringify({owner:'expired',household:'expired',generation:1,expires:1}));});
  await page.goto(base+'/settings');must(await page.evaluate(()=>!localStorage.getItem('household-recipe-draft:foreign')&&!localStorage.getItem('household-recipe-draft:expired')&&Object.keys(localStorage).some(key=>key.startsWith('household-recipe-draft:'))),'Authenticated navigation removes foreign and expired drafts while keeping my current draft');
  await page.goto(base+'/settings/security');await page.locator('.navigation-trigger').click();await page.locator('#more-dialog form[action="/logout"] button').click();await page.waitForURL('**/login');must(!await page.evaluate(()=>Object.keys(localStorage).some(key=>key.startsWith('household-recipe-draft:'))),'Logout clears every private recipe device draft');
  must(!report.errors.length,'No recipe JavaScript errors');must(!report.externalRequests.length,'No external recipe requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
