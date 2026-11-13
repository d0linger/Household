'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright');const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101',origin=new URL(base);
if(!['localhost','127.0.0.1'].includes(origin.hostname)||origin.port!=='8101'||origin.pathname!=='/')throw Error('Requires isolated Household review server 8101');
const output=path.resolve(process.env.HOUSEHOLD_COOKING_BROWSER_OUTPUT||'outputs/cooking-review'),report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[],physicalAudioTested:false,physicalPushTested:false,controlledFixtures:['Wake Lock denial; lost response after a real committed start; rejected save/network responses; explicit hidden/visible lifecycle signal. Audio instrumentation wraps the real browser API and does not prove audible hardware output.']};
if(!output.startsWith(path.resolve('outputs')+path.sep))throw Error('Cooking evidence must stay inside project outputs');
const must=(value,name)=>{if(!value)throw Error(name);report.checks.push(name);};
(async()=>{
 await fs.mkdir(output,{recursive:true});const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 const watchdog=setTimeout(()=>{report.timedOut=true;void browser.close();},8*60*1000);
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block',...(process.env.HOUSEHOLD_BROWSER_HANDOFF?{storageState:process.env.HOUSEHOLD_BROWSER_HANDOFF}:{})});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin.origin){report.externalRequests.push(route.request().url());return route.abort();}return route.continue();});
  await context.addInitScript(()=>Object.defineProperty(navigator,'wakeLock',{value:{request:async()=>{throw Error('Für diesen Test abgelehnt');}},configurable:true}));
  await context.addInitScript(()=>{
   window.__cookingPermissionRequests=0;if(window.Notification)Notification.requestPermission=()=>{window.__cookingPermissionRequests++;return Promise.resolve('denied');};
   window.__cookingAudioEvidence={contexts:[],starts:0};
   const Audio=window.AudioContext||window.webkitAudioContext;if(!Audio)return;
   const resume=Audio.prototype.resume,oscillator=Audio.prototype.createOscillator;
   Audio.prototype.resume=function(...args){if(!window.__cookingAudioEvidence.contexts.includes(this))window.__cookingAudioEvidence.contexts.push(this);return resume.apply(this,args);};
   Audio.prototype.createOscillator=function(...args){const value=oscillator.apply(this,args),start=value.start;value.start=function(...times){window.__cookingAudioEvidence.starts++;return start.apply(this,times);};return value;};
  });
  const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(String(error)));page.on('dialog',dialog=>dialog.accept());
  async function login(password){await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);}
  if(process.env.HOUSEHOLD_BROWSER_HANDOFF){await page.goto(base+'/');must(new URL(page.url()).pathname!=='/login','Prepared actual authenticated session remains authorized');}else await login(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');
  if(new URL(page.url()).pathname==='/change-password'){await page.locator('[name=current]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234');await page.locator('[name=next]').fill('Browser review only 1234');await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);await login('Browser review only 1234');}
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');}
  const fixture=await page.evaluate(async()=>{const bootstrap=await fetch('/api/v1/core/bootstrap').then(r=>r.json()),generation=bootstrap.states.recipes.generation;const response=await fetch('/api/v1/recipes/workspace',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation,data:{title:'Kürbisrisotto für einen ruhigen Herbstabend',base_servings:'3',summary:'Reis cremig garen und nach Geschmack abschmecken.'},ingredients:[{key:crypto.randomUUID(),data:{name:'Risottoreis',quantity:'100.000001',unit_code:'g',section_name:'Risotto'}},{key:crypto.randomUUID(),data:{name:'Muskatnuss',quantity_note:'Nach Geschmack'}}],steps:[{key:crypto.randomUUID(),data:{instruction:'Kürbis schneiden. Reis mit etwas Öl glasig anschwitzen.',timer_seconds:'0'}},{key:crypto.randomUUID(),data:{instruction:'Brühe portionsweise zugeben und unter Rühren cremig garen.',timer_seconds:'120'}}]})});if(!response.ok)throw Error(await response.text());return{...await response.json(),generation};});
  async function capture(name){await page.evaluate(async()=>{scrollTo(0,0);await new Promise(requestAnimationFrame);});const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true,animations:'disabled'});report.screenshots.push(file);const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations});must(!result.violations.length,'Accessibility '+name);}
  async function settled(){await page.waitForFunction(()=>!document.querySelector('#cooking-controls').disabled);}
  async function state(){return page.evaluate(async()=>fetch('/api/v1/cooking/'+document.querySelector('#cooking').dataset.id).then(r=>r.json()));}
  async function change(action){const response=page.waitForResponse(r=>r.url().startsWith(base+'/api/v1/cooking/')&&r.request().method()==='POST');await action();const r=await response;if(!r.ok())throw Error(await r.text());await settled();}
  await page.goto(base+'/m/recipes/recipes');must(await page.getByRole('link',{name:'Kochen und Verlauf',exact:true}).count()===1,'Cooking overview discoverable in recipe navigation');
  await page.goto(base+'/m/recipes/cook/'+fixture.id);await page.locator('#cooking-source:not([hidden])').waitFor();await page.locator('#cooking-start-form input').fill('1');
  let startKey='',startedID='';await page.route('**/api/v1/cooking',async route=>{if(route.request().method()!=='POST')return route.continue();startKey=route.request().headers()['idempotency-key'];const actual=await route.fetch();if(!actual.ok())throw Error(await actual.text());startedID=(await actual.json()).id;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Antwort nach gespeichertem Start verloren'})});});
  await page.getByRole('button',{name:'Kochsitzung starten',exact:true}).click();await page.locator('#cooking-start-recovery:not([hidden])').waitFor();must(await page.locator('#cooking-start-form fieldset').evaluate(el=>el.disabled),'Uncertain start offers explicit recovery while its draft is frozen');await page.unroute('**/api/v1/cooking');
  const retried=page.waitForRequest(r=>r.url()===base+'/api/v1/cooking'&&r.method()==='POST');await Promise.all([page.waitForURL(/\/cooking\/[a-f0-9-]+$/),page.locator('#cooking-retry-start').click()]);must((await retried).headers()['idempotency-key']===startKey&&page.url().endsWith(startedID),'Start recovery repeats the same key and opens the already saved session');await page.locator('#cooking-session:not([hidden])').waitFor();
  const sessionURL=page.url();let saved=await state();must(saved.servings==='1.000000'&&saved.quantities[0].approximate&&saved.quantities[1]===null,'Portions use exact rational source and preserve unknown amount');
  must(await page.evaluate(()=>window.__cookingPermissionRequests===0&&window.__cookingAudioEvidence.contexts.length===0),'Opening a cooking session requests neither notification permission nor audio');
  must(await page.getByRole('link',{name:'Timer-Geräte auswählen',exact:true}).getAttribute('href')==='/settings/notifications','Explicit timer device selection uses the shared account/device consent page');
  must(await page.locator('#cooking-sound').getAttribute('aria-pressed')==='false','Foreground sound defaults off');
  await page.bringToFront();await page.locator('#cooking-sound').focus();await page.keyboard.press('Enter');
  await page.waitForFunction(()=>document.querySelector('#cooking-sound').getAttribute('aria-pressed')==='true');
  must(await page.evaluate(()=>window.__cookingAudioEvidence.starts===1&&window.__cookingAudioEvidence.contexts[0].state==='running'),'Explicit keyboard gesture starts a real AudioContext and test oscillator; physical audibility is not claimed');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));});
  await page.waitForFunction(()=>window.__cookingAudioEvidence.contexts.every(value=>value.state==='closed'));
  must(await page.locator('#cooking-sound').getAttribute('aria-pressed')==='false','Hidden lifecycle signal closes audio and resets opt-in');
  await page.evaluate(()=>{delete document.hidden;document.dispatchEvent(new Event('visibilitychange'));});await settled();
  must(await page.locator('#cooking-sound').getAttribute('aria-pressed')==='false'&&await page.evaluate(()=>window.__cookingAudioEvidence.contexts.every(value=>value.state==='closed')),'Returning visible does not silently reopen sound');
  await page.locator('#cooking-sound').click();await page.waitForFunction(()=>document.querySelector('#cooking-sound').getAttribute('aria-pressed')==='true');
  await page.locator('#cooking-sound').click();await page.waitForFunction(()=>window.__cookingAudioEvidence.contexts.every(value=>value.state==='closed'));
  must(await page.locator('#cooking-sound').getAttribute('aria-pressed')==='false','Explicit sound opt-out closes its actual browser context');
  must((await page.locator('#cooking-ingredients').innerText()).includes('≈ 33.333334')&&(await page.locator('#cooking-ingredients').innerText()).includes('Menge offen'),'Rounded and unknown quantities are labelled');
  await page.locator('#cooking-wake').click();await page.locator('#cooking-wake-status').getByText(/Wachhalten nicht verfügbar/).waitFor();must(true,'Denied Wake Lock remains a usable cooking screen');
  const ingredient=page.locator('[data-ingredient]').first();await ingredient.focus();await change(()=>page.keyboard.press('Space'));must(await page.locator('[data-ingredient]').first().isChecked(),'Keyboard ingredient checklist persists');
  must(await page.locator('[data-ingredient]').first().evaluate(el=>el===document.activeElement),'Ingredient retains focus after persistence');
  await change(()=>page.locator('#cooking-next').click());must(await page.locator('#cooking-step-title').innerText()==='Schritt 2 von 2','Next step persists and shows position');
  await page.locator('#cooking-portions-form input').fill('1.333333');
  await change(()=>page.locator('#cooking-step-timer').click());
  must(await page.locator('#cooking-portions-form input').inputValue()==='1.333333'&&(await state()).servings==='1.000000','Unsubmitted portion draft survives an independent timer action');
  await change(()=>page.locator('#cooking-portions-form button').click());
  await page.locator('#cooking-timer-form [name=name]').fill('Tisch vorbereiten');await page.locator('#cooking-timer-form [name=seconds]').fill('180');await change(()=>page.getByRole('button',{name:'Weiteren Timer starten',exact:true}).click());
  saved=await state();const firstID=saved.timers[0].id,secondID=saved.timers[1].id,deadline=saved.timers[1].deadline;
  await context.setOffline(true);await page.locator('#cooking-connection').getByText(/^Offline:/).waitFor();
  must((await page.locator('#cooking-connection').innerText()).includes('erst nach Serverbestätigung'),'Offline copy distinguishes existing confirmed timers from unsaved changes');
  await page.locator('#cooking-timer-form [name=name]').fill('Nicht bestätigter Offline-Timer');await page.locator('#cooking-timer-form [name=seconds]').fill('60');
  await page.getByRole('button',{name:'Weiteren Timer starten',exact:true}).click();await page.locator('#cooking-error:not([hidden])').waitFor();
  must(await page.locator('#cooking-controls').evaluate(el=>el.disabled),'An actually offline timer start is uncertain and cannot be treated as saved');
  await context.setOffline(false);await page.locator('#cooking-connection').getByText(/Verbindung wieder verfügbar/).waitFor();
  const afterOffline=await state();must(afterOffline.timers.length===2&&afterOffline.timers.some(value=>value.id===secondID&&value.deadline===deadline),'Offline start created no timer and preserved the existing server deadline');
  await page.locator('#cooking-refresh').click();await settled();
  must(await page.locator('#cooking-timers output[aria-live=off]').count()===2,'Ticking countdowns are silent while named expiry has a separate announcement');
  const timer=id=>page.locator('[data-timer="'+id+'"]');await change(()=>timer(firstID).getByRole('button',{name:'Pausieren',exact:true}).click());const paused=(await state()).timers[0].remaining_millis;
  await capture('cooking-session-desktop');await page.reload();await page.locator('#cooking-session:not([hidden])').waitFor();saved=await state();
  must(saved.current_step===1&&saved.checked_ids.length===1&&saved.timers.length===2,'Progress, checklist and multiple timers survive reload');
  must(saved.timers[0].remaining_millis===paused&&saved.timers[1].deadline===deadline,'Paused remaining time and running deadline survive reload unchanged');
  await change(()=>timer(firstID).getByRole('button',{name:'Fortsetzen',exact:true}).click());must((await state()).timers[0].state==='running','Paused timer resumes');
  await timer(firstID).locator('input').fill('1');await timer(firstID).getByRole('button',{name:'Mit dieser Dauer neu starten'}).click();await page.locator('#cooking-confirm[open]').waitFor();await page.locator('#cooking-confirm-cancel').click();must((await state()).timers[0].duration_seconds===120,'Cancelled timer adjustment preserves original duration');
  await timer(firstID).getByRole('button',{name:'Mit dieser Dauer neu starten'}).click();await change(()=>page.locator('#cooking-confirm-yes').click());await timer(firstID).getByText('Abgelaufen',{exact:true}).waitFor();
  await page.reload();await timer(firstID).getByText('Abgelaufen',{exact:true}).waitFor();must(true,'Elapsed timer remains elapsed after reload');await change(()=>timer(firstID).getByRole('button',{name:'Ablauf bestätigen',exact:true}).click());must((await state()).timers[0].state==='done','Elapsed acknowledgement is persisted');
  await timer(firstID).getByRole('button',{name:'Timer entfernen',exact:true}).click();await change(()=>page.locator('#cooking-confirm-yes').click());must((await state()).timers.length===1,'Reviewed timer removal preserves the other timer');
  const sidBefore=(await state()).id;await page.route('**/api/v1/cooking/'+sidBefore,route=>route.request().method()==='POST'?route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'Vorübergehend nicht erreichbar'})}):route.continue());await page.locator('[data-ingredient]').last().check();await page.locator('#cooking-error:not([hidden])').waitFor();must(await page.locator('#cooking-controls').evaluate(el=>el.disabled)&&await page.locator('[data-ingredient]').last().isChecked(),'Failed checklist save is retained and cannot be silently replaced by a timer action');await page.unroute('**/api/v1/cooking/'+sidBefore);await page.locator('#cooking-refresh').click();await settled();must(!await page.locator('[data-ingredient]').last().isChecked(),'Explicit saved-state review resolves a failed checklist draft');
  await page.locator('#cooking-finish-form [name=notes]').fill('Etwas mehr Brühe und Muskat passen beim nächsten Mal.');
  await page.evaluate(async({generation})=>{const sid=document.querySelector('#cooking').dataset.id,s=await fetch('/api/v1/cooking/'+sid).then(r=>r.json());const response=await fetch('/api/v1/cooking/'+sid,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({generation,version:s.version,action:'portions',servings:'2'})});if(!response.ok)throw Error(await response.text());},fixture);
  await page.locator('#cooking-previous').click();await page.locator('#cooking-error:not([hidden])').waitFor();must(await page.locator('#cooking-controls').evaluate(el=>el.disabled),'Concurrent edit requires review before another mutation');must((await page.locator('#cooking-finish-form [name=notes]').inputValue()).includes('Muskat'),'Conflict preserves unsaved completion notes');
  await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'dark'});await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('cooking-conflict-mobile-dark');
  await page.locator('#cooking-refresh').click();await settled();must((await page.locator('#cooking-finish-form [name=notes]').inputValue()).includes('Muskat'),'Explicit refresh preserves completion notes');
  const sid=(await state()).id;await page.route('**/api/v1/cooking/'+sid,route=>route.request().method()==='POST'?route.abort('failed'):route.continue());await page.locator('#cooking-previous').click();await page.locator('#cooking-error:not([hidden])').waitFor();must(await page.locator('#cooking-controls').evaluate(el=>el.disabled),'Uncertain network failure blocks accidental repeat');await page.unroute('**/api/v1/cooking/'+sid);await page.locator('#cooking-refresh').click();await settled();
  await page.locator('#cooking-finish-form [name=actual_servings]').fill('1.500001');await page.locator('#cooking-finish-form [name=rating]').selectOption('4');await page.getByRole('button',{name:'Abschluss prüfen',exact:true}).click();await page.locator('#cooking-confirm[open]').waitFor();
  must(await page.locator('#cooking-confirm-cancel').evaluate(el=>el===document.activeElement),'Completion review starts at safe cancellation control');await capture('cooking-finish-mobile-dark');await page.locator('#cooking-confirm-cancel').click();must((await state()).state==='active','Cancelled finish does not create history');
  await page.getByRole('button',{name:'Abschluss prüfen',exact:true}).click();await page.locator('#cooking-confirm-yes').click();await page.locator('#cooking-terminal:not([hidden])').waitFor();saved=await state();must(saved.state==='completed'&&saved.result_id&&saved.timers.every(t=>t.state==='done'),'Confirmed finish creates history and terminates timers');
  await page.getByRole('link',{name:'Bestätigten Verlauf ansehen'}).click();await page.locator('#cooking-record:not([hidden])').waitFor();must((await page.locator('#cooking-record-meta').innerText()).includes('1.500001 Portionen')&&(await page.locator('#cooking-record-notes').innerText()).includes('Muskat'),'Completed history retains exact servings, rating and notes');
  must((await page.locator('#cooking-record-source').innerText()).includes('Zutatenmengen für das Originalrezept mit 3.000000 Portionen.'),'History distinguishes original ingredient basis from actual prepared servings');await page.evaluate(()=>document.documentElement.dataset.theme='dark');await capture('cooking-history-mobile-dark');must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile cooking has no horizontal overflow');
  await page.goto(sessionURL);await page.locator('#cooking-terminal:not([hidden])').waitFor();must(await page.locator('#cooking-controls').evaluate(el=>el.disabled),'Terminal session cannot accidentally be edited');
  await page.goto(base+'/m/recipes/cook/'+fixture.id);await page.locator('#cooking-source:not([hidden])').waitFor();await Promise.all([page.waitForURL(/\/cooking\/[a-f0-9-]+$/),page.getByRole('button',{name:'Kochsitzung starten',exact:true}).click()]);await page.locator('#cooking-session:not([hidden])').waitFor();
  await page.locator('#cooking-timer-form [name=name]').fill('Browser timer matrix');await page.locator('#cooking-timer-form [name=seconds]').fill('3600');await change(()=>page.getByRole('button',{name:'Weiteren Timer starten',exact:true}).click());
  const matrixSession=await state(),matrixTimer=matrixSession.timers[0],paletteColors=new Map();
  must(matrixSession.state==='active'&&matrixTimer.duration_seconds===3600,'Palette matrix uses a separately saved one-hour timer, preserving earlier short timer workflows');
  async function matrixLocale(language){
   if(await page.locator('html').getAttribute('data-locale')!==language){await page.locator('.language-control button[value='+language+']').click();await page.waitForFunction(value=>document.documentElement.dataset.locale===value,language);}
   await page.locator('#cooking-session:not([hidden])').waitFor();await settled();
  }
  for(const language of ['de','en']){
   await matrixLocale(language);
   for(const width of [1440,320]){
    await page.setViewportSize({width,height:width===320?900:1000});
    for(const mode of ['light','dark','pink-light','pink-dark']){
     const name='cooking-timer-'+language+'-'+mode+'-'+width;
     await page.evaluate(mode=>window.HouseholdTheme.apply(mode,{persist:false}),mode);await page.waitForFunction(mode=>window.HouseholdTheme.get()===mode,mode);
     paletteColors.set(mode,await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--ground').trim()));
     const sound=page.getByRole('button',{name:language==='de'?'Ton aktivieren und testen':'Enable and test sound',exact:true}),devices=page.getByRole('link',{name:language==='de'?'Timer-Geräte auswählen':'Choose timer devices',exact:true});
     must(await sound.isVisible()&&await sound.getAttribute('aria-pressed')==='false','Sound opt-in is localized and remains off: '+name);
     must(await devices.getAttribute('href')==='/settings/notifications','Localized device choice preserves its authorized settings destination: '+name);
     await sound.focus();must(await sound.evaluate(element=>element===document.activeElement),'Sound action is keyboard reachable: '+name);await page.keyboard.press('Tab');must(await devices.evaluate(element=>element===document.activeElement),'Device choice follows sound in keyboard order: '+name);
     must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Timer controls have no horizontal overflow: '+name);
     await capture(name);
    }
   }
  }
  must(new Set(paletteColors.values()).size===4,'Timer controls retain four distinct Ocean/Pink palettes');
  const afterMatrix=await state();must(afterMatrix.id===matrixSession.id&&afterMatrix.timers.length===1&&afterMatrix.timers[0].id===matrixTimer.id&&afterMatrix.timers[0].deadline===matrixTimer.deadline,'Language and appearance choices preserve the same real running timer and deadline');
  await matrixLocale('de');
  await page.locator('#cooking-abandon').click();await page.locator('#cooking-confirm-yes').click();await page.locator('#cooking-terminal:not([hidden])').waitFor();saved=await state();must(saved.state==='abandoned'&&!saved.result_id,'Explicit abandonment produces no completed cooking event');
  must(await page.evaluate(()=>window.__cookingPermissionRequests===0),'All cooking operations leave browser notification permission untouched');
  must(!report.errors.length,'No cooking JavaScript errors');must(!report.externalRequests.length,'No external cooking requests');report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{clearTimeout(watchdog);await fs.writeFile(path.join(output,'browser-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,accessibility:report.accessibility.length,screenshots:report.screenshots.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
