'use strict';

// Presentation acceptance on the owned synthetic fixture; only locale is saved.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const {fixtureState} = require('./notification-browser-fixture.cjs');
const {setBrowserLocale} = require('./browser-locale-helper.cjs');
const root = path.resolve(__dirname, '..'), origin = 'http://localhost:8101';
const output = path.join(root, 'outputs/warm-midnight/acceptance');
const report = {passed:false, checks:[], accessibility:[], screenshots:[], errors:[], failures:[], physicalDevicesTested:false,
  zoomScope:'200% desktop reflow equivalent: 720×450 CSS viewport at DPR 2 (1440×900 physical pixels); OS/browser-menu zoom is not automated.'};
let browser;
function must(value, label) {if (!value) throw Error(label); report.checks.push(label);}
async function observedPage(context){
  await context.route('**/*',route=>{
    const url=new URL(route.request().url());
    if(url.origin===origin)return route.continue();
    report.errors.push('External request '+url.origin);
    return route.abort();
  });
  const page=await context.newPage();
  page.on('pageerror',error=>report.errors.push(error.message));
  return page;
}
async function capture(page, name) {await page.screenshot({path:path.join(output,name+'.png'),animations:'disabled'});report.screenshots.push(name+'.png');}
async function check(label, fn) {try {await fn();} catch(error) {report.failures.push({label,message:error.message});}}
async function visit(page, route) {
  const response = await page.goto(origin+route);
  must(response.status()===200 && new URL(page.url()).pathname===route.split('?')[0], 'Authorized route '+route);
  await page.waitForFunction(()=>!document.querySelector('main [aria-busy=true]'),{},{timeout:15000});
}
async function reflow(page, label) {
  const state=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth>innerWidth+1,right:document.querySelector('main').getBoundingClientRect().right,width:innerWidth}));
  must(!state.overflow && state.right<=state.width+1,'Contained page '+label);
}
(async()=>{
  await fs.mkdir(output,{recursive:true});
  const secrets=[],state=fixtureState(root,secrets,{pinned:false});
  browser=await chromium.launch({headless:true,...(process.platform==='win32'?{channel:'msedge'}:{})});
  const context=await browser.newContext({storageState:state,viewport:{width:1440,height:900},serviceWorkers:'block',reducedMotion:'reduce'});
  const page=await observedPage(context);
  await visit(page,'/');const initialLocale=await page.locator('html').getAttribute('data-locale');await setBrowserLocale(page,'de');
  const modules=await page.locator('.sidebar a[data-module-id]').evaluateAll(links=>links.map(link=>({name:link.dataset.moduleId,route:link.getAttribute('href')})));
  must(modules.length===9,'All nine fixture modules available');
  const settings = await page.locator('#more-dialog .settings-nav a').evaluateAll(links => links.map(link => link.getAttribute('href')));
  must(settings.length === 9 && new Set(settings).size === 9, 'All nine settings destinations available');
  const routes = [{name:'today',route:'/'}, ...modules, ...settings.map(route => ({
    name: 'admin-' + (route === '/settings' ? 'general' : route.split('/').pop()), route
  }))];
  const records=await page.evaluate(async()=>{const read=async module=>(await fetch('/api/v1/m/'+module)).json();return {shopping:await read('shopping/lists'),recipes:await read('recipes/recipes')};});
  for(const [module,data] of Object.entries(records)){const id=data.records?.[0]?.id;must(/^[a-f0-9-]{36}$/i.test(id),'Actual '+module+' record available');routes.push({name:module+'-record',route:module==='shopping'?'/m/shopping/lists/'+id:'/m/recipes/recipes/'+id+'/workspace'});}
  report.routes=routes;
  for(const locale of ['de','en']){
    await setBrowserLocale(page,locale);
    for(const item of routes) await check(locale+' '+item.name,async()=>{
      await visit(page,item.route);
      must(await page.locator('html').getAttribute('data-locale')===locale,'Actual locale '+locale+' '+item.name);
      const widths=locale==='de'?[1440,1024,760,390,320]:[390];
      for(const width of widths){
        await page.setViewportSize({width,height:width===1024?768:width===390?844:width===320?720:900});
        await page.evaluate(()=>HouseholdTheme.apply('light'));
        await reflow(page,locale+'/'+item.name+'/'+width);
        if(item.name==='admin-modules' && [1440,390].includes(width)){
          const columns=await page.locator('#settings-modules').evaluate(grid=>getComputedStyle(grid).gridTemplateColumns.trim().split(/\s+/).length);
          must(columns===(width===1440?3:1),'Admin modules occupy the expected columns at '+width);
        }
        if(item.name==='today' && width===390){
          const aligned=await page.evaluate(()=>Math.abs(document.querySelector('.mobile-brand').getBoundingClientRect().top+document.querySelector('.mobile-brand').offsetHeight/2-document.querySelector('.navigation-trigger').getBoundingClientRect().top-document.querySelector('.navigation-trigger').offsetHeight/2)<6);
          must(aligned,'Mobile wordmark and account control share a row');
        }
        if(locale==='de' && [1440,390].includes(width)){
          const scan=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
          report.accessibility.push({name:item.name+'/'+width,violations:scan.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))});
          must(!scan.violations.length,'Axe '+item.name+'/'+width);await capture(page,item.name+'-'+width);
        }
      }
      for(const mode of ['light','dark','pink-light','pink-dark']){
        await page.evaluate(mode=>HouseholdTheme.apply(mode),mode);await reflow(page,locale+'/'+item.name+'/'+mode);
        if(item.name.startsWith('admin'))must(await page.evaluate(()=>getComputedStyle(document.body).backgroundColor==='rgb(7, 19, 31)' && getComputedStyle(document.body).colorScheme==='dark'),'Admin C retained '+mode+' '+item.name);
      }
    });
  }
  for(const width of [1440,390])await check('Right tray '+width,async()=>{
    await page.setViewportSize({width,height:900});await visit(page,'/settings');await page.locator('.navigation-trigger').click();
    must(await page.locator('#more-dialog').evaluate(d=>{const r=d.getBoundingClientRect();return d.open && Math.abs(r.right-innerWidth)<1 && r.left>0;}),'Tray anchored right '+width);
    await capture(page,'right-tray-'+width);await page.keyboard.press('Escape');
    must(await page.locator('.navigation-trigger').evaluate(b=>b===document.activeElement),'Tray restores focus '+width);
  });
  await check('Forced colors and reduced motion',async()=>{
    await page.emulateMedia({forcedColors:'active',reducedMotion:'reduce'});await page.locator('.navigation-trigger').click();
    must(await page.locator('#more-dialog').evaluate(d=>d.getAnimations().length===0),'Reduced motion has no tray animation');
    await page.keyboard.press('Escape');await reflow(page,'forced colors');await capture(page,'forced-colors');
  });
  const zoom=await browser.newContext({storageState:await context.storageState(),viewport:{width:720,height:450},deviceScaleFactor:2,serviceWorkers:'block',reducedMotion:'reduce'});
  const zoomPage=await observedPage(zoom);
  for(const item of routes)await check('200% reflow '+item.name,async()=>{await visit(zoomPage,item.route);await reflow(zoomPage,'200% '+item.name);});
  await capture(zoomPage,'zoom-reflow-200');await zoom.close();await setBrowserLocale(page,initialLocale);await context.close();
  must(!report.errors.length,'No page errors or external requests');report.passed=!report.failures.length;
})().catch(error=>report.failures.push({label:'Harness',message:error.message})).finally(async()=>{
  await browser?.close();await fs.mkdir(output,{recursive:true});await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));
  console.log(JSON.stringify({passed:report.passed,checks:report.checks.length,axe:report.accessibility.length,captures:report.screenshots.length,failures:report.failures}));if(!report.passed)process.exitCode=1;
});
