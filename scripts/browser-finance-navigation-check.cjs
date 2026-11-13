'use strict';
const fs=require('node:fs/promises'),path=require('node:path');
const {chromium}=require('playwright'),AxeBuilder=require('@axe-core/playwright').default;
const {fixtureState}=require('./notification-browser-fixture.cjs');
const root=path.resolve(__dirname,'..'),origin=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
if(origin!=='http://localhost:8101')throw Error('Finance navigation requires the owned localhost:8101 fixture');
const output=path.resolve(root,process.env.HOUSEHOLD_FINANCE_NAVIGATION_OUTPUT||('outputs/ux-workspace/finance-context-'+Date.now()));
if(!output.startsWith(path.join(root,'outputs')+path.sep))throw Error('Evidence must remain inside project outputs');
const report={passed:false,checks:[],accessibility:[],screenshots:[],errors:[],externalRequests:[],scope:'Actual reviewed fixture account, initial authorized filter resolution, contextual navigation, dirty draft protection and fail-closed unavailable account. No production or hosted-CI claim.'};
const secrets=[];let browser,watchdog;
const redact=value=>secrets.reduce((text,secret)=>secret?text.split(secret).join('[redacted]'):text,String(value));
const must=(condition,label)=>{if(!condition)throw Error(label);report.checks.push(label);};
const api='/api/v1/expenses/finance';
(async()=>{
 await fs.mkdir(output,{recursive:true});
 const state=fixtureState(root,secrets);
 browser=await chromium.launch({headless:true,...(process.platform==='win32'?{channel:'msedge'}:{}),args:['--disable-background-networking','--disable-component-update']});
 watchdog=setTimeout(()=>{report.failure='Finance context browser deadline exceeded';process.exitCode=1;browser.close();},480000);
 const context=await browser.newContext({storageState:state,serviceWorkers:'block',reducedMotion:'reduce',viewport:{width:1440,height:1000}});
 await context.route('**/*',route=>{const url=new URL(route.request().url());if(url.origin!==origin){report.externalRequests.push(url.origin+url.pathname);return route.abort();}return route.continue();});
 const page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',error=>report.errors.push(redact(error.message)));
 async function settled(){await page.waitForFunction(()=>document.querySelector('#finance-workspace')?.getAttribute('aria-busy')==='false');}
 async function ready(){await settled();must(await page.locator('#finance-error').isHidden(),'Actual authorized Finance view loaded');}
 async function go(pane,query=''){await page.goto(origin+'/m/expenses/workspace/'+pane+(query?'?'+query:''));await ready();}
 await go('accounts');
 if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value="de"]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');await ready();}
 must(await page.locator('#finance-pane').isHidden(),'Legacy seven-pane controller is not a second visible navigation');
 must(await page.locator('.workspace-primary [aria-current=page]').getAttribute('data-finance-pane')==='entries','Accounts retain the Entries primary group');
 const name='UX Finance account '+Date.now();
 await page.getByRole('button',{name:'Konto anlegen',exact:true}).click();await page.locator('#finance-editor:not([hidden])').waitFor();
 const editor=page.locator('#finance-editor-content');await editor.locator('[name=name]').fill(name);await editor.locator('[name=account_type]').selectOption('bank');await editor.locator('[name=currency]').selectOption('EUR');
 const preview=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/preview'&&r.request().method()==='POST');await page.getByRole('button',{name:'Eingaben serverseitig prüfen',exact:true}).click();must((await preview).status()===200,'Actual account preview accepted');
 await page.locator('#finance-review:not([hidden])').waitFor();const apply=page.waitForResponse(r=>new URL(r.url()).pathname===api+'/apply'&&r.request().method()==='POST');await page.locator('#finance-apply').click();const accepted=await apply;must(accepted.status()===200,'Actual account creation applied');await page.locator('#finance-operation:not([hidden])').waitFor();
 const key=accepted.request().headers()['idempotency-key'];const receipt=await page.evaluate(async target=>{const r=await fetch(target,{cache:'no-store'});if(!r.ok)throw Error('Actual operation receipt unavailable');return r.json();},api+'/operations/'+key);const account=receipt.resource_id;must(/^[0-9a-f-]{36}$/.test(account),'Actual durable account identity returned');
 await page.locator('#finance-operation-finish').click();await page.locator('#finance-operation').waitFor({state:'hidden'});
 const requests=[];page.on('request',request=>{const url=new URL(request.url());if(url.pathname.startsWith(api))requests.push({path:url.pathname,query:url.searchParams});});
 await go('entries','account='+account);
 const first=requests.find(r=>r.path===api+'/view'&&r.query.get('pane')==='entries');
 must(first?.query.get('account')===account,'First actual entries view is explicitly account-filtered');
 const referenceIndex=requests.findIndex(r=>r.path===api+'/references/accounts'&&r.query.get('selected')===account),viewIndex=requests.indexOf(first);must(referenceIndex>=0&&referenceIndex<viewIndex,'Selected account authority is resolved before the first view');
 must(await page.locator('#finance-account-context-name').textContent()===name,'Authorized account name appears in current selection context');must(await page.locator('#finance-filters [name=account]').inputValue()===account,'Loaded account remains selected in actual filter');
 for(const pane of ['entries','accounts','overview']){const href=await page.locator('.workspace-context [data-finance-pane='+pane+']').getAttribute('href');must(new URL(href,origin).searchParams.get('account')===account,'Context carries account into '+pane);}
 for(const selector of ['.workspace-primary [data-finance-pane=planning]','[data-workspace-manage]'])must(!new URL(await page.locator(selector).getAttribute('href'),origin).searchParams.has('account'),'Unsupported context does not receive account: '+selector);
 await page.locator('#finance-account-context-open').click();await page.locator('#finance-detail:not([hidden])').waitFor();must((await page.locator('#finance-detail-heading').innerText()).includes(name),'Complete authorized account detail has its name in the heading');must(await page.locator('#finance-detail-actions [data-finance-context-link]').count()===1,'Account detail offers its own entries context');
 await page.locator('#finance-detail-close').click();must(await page.locator('#finance-list-heading').evaluate(el=>el===document.activeElement),'Back to selection restores keyboard focus');must(await page.locator('#finance-filters [name=account]').inputValue()===account,'Returning from account detail preserves the filter');
 for(const [width,theme] of [[1440,'dark'],[320,'pink-light']]){
  await page.setViewportSize({width,height:1000});await page.evaluate(theme=>window.HouseholdTheme.apply(theme,{persist:false}),theme);
  await page.locator('.workspace-primary a').first().focus();await page.evaluate(()=>window.scrollTo(0,0));
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Selected account context fits '+width+'px');const label=theme+'-'+width;await page.screenshot({path:path.join(output,label+'.png'),fullPage:true,animations:'disabled'});report.screenshots.push(label+'.png');
  const axe=await new AxeBuilder({page}).include('#finance-workspace').analyze();report.accessibility.push({label,violations:axe.violations.map(v=>({id:v.id,impact:v.impact,nodes:v.nodes.length}))});must(axe.violations.length===0,'Selected account context Axe '+label);
 }
 await page.setViewportSize({width:1440,height:1000});await page.locator('#finance-new-entry').click();await page.locator('[name=entry_description]').fill('Unsaved contextual entry');
 const before=page.url();page.once('dialog',dialog=>dialog.dismiss());await page.locator('[data-workspace-manage]').click();must(page.url()===before&&await page.locator('[name=entry_description]').inputValue()==='Unsaved contextual entry','Cancelling Manage navigation preserves actual unsaved form');
 page.once('dialog',dialog=>dialog.accept());await page.locator('#finance-entry-close').click();await page.locator('#finance-entry-editor').waitFor({state:'hidden'});
 requests.length=0;const unavailable='00000000-0000-4000-8000-999999999999';await page.goto(origin+'/m/expenses/workspace/entries?account='+unavailable);await settled();
 must(await page.locator('#finance-error').isVisible(),'Unavailable account produces an explicit error');must(!requests.some(r=>r.path===api+'/view'&&r.query.get('pane')==='entries'),'Unavailable account never falls back to an unfiltered entries request');must(await page.locator('#finance-account-context').isHidden(),'Unavailable account leaves no stale account name context');
 await go('entries','account='+account);await page.locator('#finance-account-context-clear').click();await ready();must(await page.locator('#finance-account-context').isHidden()&&await page.locator('#finance-filters [name=account]').inputValue()==='','Explicit clear returns to unfiltered authorized selection');
 must(!new URL(page.url()).searchParams.has('account'),'Explicit clear also removes the old bookmarked account scope');
 // Select through the live UI without an account in the URL. After a real
 // logout, only a scope-required boolean may survive in this existing page.
 if(!await page.locator('#finance-extra-filters').evaluate(el=>el.open))await page.locator('#finance-extra-filters>summary').click();
 const select=page.locator('#finance-filters [name=account]'),field=select.locator('xpath=ancestor::div[contains(@class,"reference-field")][1]');
 await field.getByRole('searchbox').fill(name);await field.getByRole('button',{name:'Suchen',exact:true}).click();await select.locator('option[value="'+account+'"]').waitFor({state:'attached'});await select.selectOption(account);
 await page.locator('#finance-filters').getByRole('button',{name:'Auswahl anzeigen',exact:true}).click();await ready();must(await page.locator('#finance-account-context-name').textContent()===name&&!new URL(page.url()).searchParams.has('account'),'Actual manually selected account is loaded without a URL fallback');
 const boot=await page.evaluate(async()=>{const r=await fetch('/api/v1/core/bootstrap',{cache:'no-store'});if(!r.ok)throw Error('Actual current identity unavailable');return r.json();});
 const password=process.env.HOUSEHOLD_BROWSER_PASSWORD;if(!password)throw Error('Protected fixture password required for the real reauthentication check');secrets.push(password);
 const logout=await context.request.post(origin+'/logout',{form:{csrf:boot.csrf},maxRedirects:0});must(logout.status()===303,'Actual fixture session is logged out');
 await page.locator('#finance-filters').getByRole('button',{name:'Auswahl anzeigen',exact:true}).click();await settled();
 must(await page.locator('#finance-authority').isVisible(),'Actual expired session clears financial authority');must(await page.locator('#finance-account-context-name').textContent()===''&&!await page.locator('#finance-workspace').textContent().then(text=>text.includes(name)),'Revocation removes the account label from all financial DOM');
 const login=await context.newPage();await login.goto(origin+'/login');await login.locator('[name=username]').fill(boot.user.username);await login.locator('[name=password]').fill(password);await Promise.all([login.waitForURL(url=>url.pathname!=='/login'),login.locator('form[action="/login"] button.primary').click()]);must(new URL(login.url()).pathname!== '/change-password','Same fixture account reauthenticates without changing its password');await login.close();
 requests.length=0;await page.locator('#finance-reauth').click();await settled();must(await page.locator('#finance-error').isVisible()&&await page.locator('#finance-account-scope-reset').isVisible(),'Reauthenticated old page still requires explicit scope reset');must(!requests.some(r=>r.path===api+'/view'&&r.query.get('pane')==='entries'),'Reauthentication never widens a lost non-URL account scope');
 await page.locator('#finance-account-scope-reset').click();await ready();must(requests.some(r=>r.path===api+'/view'&&r.query.get('pane')==='entries'&&!r.query.has('account')),'Only explicit reset permits a new unfiltered authorized view');
 must(report.errors.length===0,'No browser runtime errors');must(report.externalRequests.length===0,'No external requests');report.passed=true;await context.close();
})().catch(error=>{report.failure=redact(error.message);process.exitCode=1;}).finally(async()=>{clearTimeout(watchdog);if(browser)await browser.close();await fs.mkdir(output,{recursive:true});await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,accessibility:report.accessibility.length,output,failure:report.failure}));});
