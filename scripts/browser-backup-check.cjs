'use strict';
const fs=require('node:fs/promises');
const path=require('node:path');
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const base=process.env.HOUSEHOLD_BROWSER_URL||'http://localhost:8101';
const target=new URL(base);
if(!['localhost','127.0.0.1'].includes(target.hostname)||target.port!=='8101'||target.pathname!=='/')throw Error('Requires isolated review server8101');
const output=path.resolve('outputs/backup-review');
const report={checks:[],screenshots:[],accessibility:[],errors:[],externalRequests:[]};
const must=(condition,message)=>{if(!condition)throw Error(message);report.checks.push(message);};
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':undefined)});
 try{
  await fs.mkdir(output,{recursive:true});
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light'});
  await context.route('**/*',route=>{const url=new URL(route.request().url());if(url.origin!==target.origin){report.externalRequests.push(url.href);return route.abort();}return route.continue();});
  const page=await context.newPage();page.on('pageerror',error=>report.errors.push(String(error)));
  let password=process.env.HOUSEHOLD_BROWSER_PASSWORD||'Browser review only 1234';
  const login=async()=>{await page.goto(base+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(password);
   await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.getByRole('button',{name:/^Anmelden/}).click()]);};
  await login();
  // A freshly prepared fixture account must change its password first.
  if(new URL(page.url()).pathname==='/change-password'){const next='Browser backup review 1234';await page.locator('[name=current]').fill(password);await page.locator('[name=next]').fill(next);
   await Promise.all([page.waitForURL('**/login'),page.getByRole('button',{name:'Passwort speichern'}).click()]);password=next;await login();}
  async function shot(name){const file=path.join(output,name+'.png');await page.screenshot({path:file,fullPage:true});report.screenshots.push(file);}
  async function axe(name){const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();report.accessibility.push({name,violations:result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)}))});must(result.violations.length===0,'No axe violations: '+name);}

  await page.goto(base+'/settings');
  must(await page.locator('.settings-nav a[href="/settings/backup"]').count()===1,'Settings navigation offers backups to administrators');
  must(await page.locator('.settings-nav a[href="/settings/system"]').count()===1,'Settings navigation offers the system status');
  await page.goto(base+'/settings/backup');
  await page.getByRole('heading',{name:'Sicherung',exact:true}).waitFor();
  must(await page.locator('.settings-nav a[href="/settings/backup"][aria-current="page"]').count()===1,'Backup page marks its navigation entry');
  for(const heading of ['Zustand','Zeitplan und Aufbewahrung','Lokale Wiederherstellungspunkte','Verlauf'])must(await page.getByRole('heading',{name:heading,exact:true}).count()===1,'Backup page section: '+heading);
  must(await page.getByRole('button',{name:'Jetzt sichern',exact:true}).count()===1,'Back-up-now action is a real button');
  must(await page.locator('[name=local_time]').inputValue()!=='','Schedule form shows the stored start time');
  const offsiteHelp=await page.getByRole('heading',{name:'Externes Ziel einrichten'}).count();
  must(offsiteHelp===1,'Without an offsite target the page explains its .env setup');
  await shot('backup-desktop');await axe('backup page desktop');

  const restore=page.locator('[data-restore-open]').first();
  if(await restore.count()){
   await restore.click();
   const dialog=page.locator('#backup-restore');
   must(await dialog.evaluate(node=>node.open),'Restore dialog opens');
   const phrase=await dialog.locator('[data-restore-phrase]').innerText();
   must(/^WIEDERHERSTELLEN [0-9A-F]{8}$/.test(phrase),'Restore dialog shows the snapshot-bound confirmation');
   must(await page.evaluate(()=>document.activeElement?.name)==='confirmation','Focus moves to the confirmation field');
   await axe('restore dialog');
   await dialog.getByRole('button',{name:'Abbrechen',exact:true}).click();
   must(!(await dialog.evaluate(node=>node.open)),'Restore dialog closes without a request');
  }else report.checks.push('Fixture has no restore point; dialog checks skipped');

  await page.setViewportSize({width:390,height:844});
  await page.evaluate(()=>{localStorage.setItem('household-theme','dark');document.documentElement.dataset.theme='dark';});
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile backup page has no horizontal overflow');
  await shot('backup-mobile-dark');await axe('backup page mobile dark');

  await page.goto(base+'/settings/system');
  await page.getByRole('heading',{name:'Systemstatus',exact:true}).waitFor();
  must(await page.locator('.system-check').count()>=6,'System page lists its operational checks');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile system page has no horizontal overflow');
  await shot('system-mobile-dark');await axe('system page mobile dark');

  await page.goto(base+'/help');
  await page.getByRole('heading',{name:'Hilfe',exact:true}).waitFor();
  must(await page.locator('.help-terms section').count()>=14&&await page.locator('#probewiederherstellung').count()===1,'Help page explains every listed term');
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile help page has no horizontal overflow');
  await shot('help-mobile-dark');await axe('help page mobile dark');
  await page.goto(base+'/share');
  await page.getByRole('heading',{name:'Geteilte Dateien',exact:true}).waitFor();
  must(await page.locator('[data-share-empty]').isVisible()&&await page.locator('[data-share-missed]').isHidden(),'Share page shows its empty state');
  await page.goto(base+'/share?missed=1');
  await page.locator('[data-share-missed]').waitFor();
  must(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Mobile share page has no horizontal overflow');
  await shot('share-mobile-dark');await axe('share page mobile dark');
  // A parked shared photo becomes exactly one document through the real save flow.
  await page.evaluate(async()=>{const boot=await fetch('/api/v1/core/bootstrap',{cache:'no-store'}).then(r=>r.json());if(boot.states.documents.state!=='enabled'){const result=await fetch('/settings/modules/documents',{method:'POST',body:new URLSearchParams({csrf:boot.csrf,enabled:'true',version:String(boot.states.documents.version)})});if(!result.ok)throw Error('Module enable failed');}});
  const parked=await page.evaluate(async()=>{const canvas=document.createElement('canvas');canvas.width=9;canvas.height=5;const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
   const key='/share/parked/'+crypto.randomUUID()+'-0';await (await caches.open('household-share-v1')).put(key,new Response(blob,{headers:{'Content-Type':'image/png','X-Share-Name':encodeURIComponent('beleg.png')}}));return key;});
  await page.goto(base+'/share');
  await page.getByRole('button',{name:'Als Dokument speichern',exact:true}).click();
  await page.getByRole('link',{name:'Gespeichert. Dokument öffnen',exact:true}).waitFor({timeout:30000});
  must(await page.evaluate(async key=>!(await (await caches.open('household-share-v1')).match(key)),parked),'Saved shared file leaves the device cache');
  const documentsNamed=await page.evaluate(async()=>{const response=await fetch('/api/v1/m/documents/documents?q=beleg',{headers:{Accept:'application/json'}});const body=await response.json();return (body.records||body.items||body).filter?.(row=>(row.data?.title||row.title)==='beleg').length??-1;});
  must(documentsNamed===1,'Shared file became exactly one document');

  await page.setViewportSize({width:1440,height:1000});
  await page.evaluate(()=>{localStorage.setItem('household-theme','light');document.documentElement.dataset.theme='light';});
  await page.goto(base+'/settings/backup');
  await page.locator('form.language-control button[name=locale][value=en]').click();
  await page.waitForLoadState('load');
  await page.goto(base+'/settings/backup');
  await page.getByRole('heading',{name:'Backup',exact:true}).waitFor();
  must(await page.getByRole('button',{name:'Back up now',exact:true}).count()===1,'English backup page');
  await shot('backup-desktop-en');await axe('backup page English');
  await page.goto(base+'/settings/system');
  await page.getByRole('heading',{name:'System status',exact:true}).waitFor();
  must(true,'English system page');
  await page.goto(base+'/settings/backup');
  must(await page.getByRole('button',{name:'Export all data',exact:true}).count()===1,'English backup page offers the household export');
  await page.goto(base+'/help');
  await page.getByRole('heading',{name:'Help',exact:true}).waitFor();
  await axe('help page English');
  await page.goto(base+'/share');
  await page.getByRole('heading',{name:'Shared files',exact:true}).waitFor();
  must(await page.getByText('No shared files on this device.',{exact:true}).isVisible(),'English share page');
  await page.locator('form.language-control button[name=locale][value=de]').click();
  await page.waitForLoadState('load');

  must(report.errors.length===0,'No JavaScript errors');must(report.externalRequests.length===0,'No external requests');
  report.passed=true;
 }catch(error){report.passed=false;report.failure=String(error);throw error;}
 finally{await fs.writeFile(path.join(output,'browser-backup-report.json'),JSON.stringify(report,null,2));await browser.close();}
 console.log(JSON.stringify({checks:report.checks.length,screenshots:report.screenshots.length,accessibility:report.accessibility.length}));
})().catch(error=>{console.error(error);process.exitCode=1;});
