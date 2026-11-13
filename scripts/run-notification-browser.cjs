'use strict';
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto'),{spawn}=require('node:child_process');
const fixture=require('./notification-browser-fixture.cjs'),contracts=require('./fuel-browser-contracts.cjs');
const definitions=Object.freeze({
 settings:{script:'browser-notification-settings-check.cjs',env:'HOUSEHOLD_NOTIFICATIONS_BROWSER_OUTPUT',report:'report.json',axe:18,captures:18,min:500},
 fuel:{script:'browser-fuel-notifications-check.cjs',env:'HOUSEHOLD_FUEL_NOTIFICATIONS_BROWSER_OUTPUT',report:'browser-report.json',axe:32,captures:32,min:100},
 finance:{script:'browser-finance-notifications-check.cjs',env:'HOUSEHOLD_FINANCE_NOTIFICATIONS_BROWSER_OUTPUT',report:'browser-report.json',axe:16,captures:16,min:90},
 meters:{script:'browser-meters-notifications-check.cjs',env:'HOUSEHOLD_METERS_NOTIFICATIONS_BROWSER_OUTPUT',report:'browser-report.json',axe:16,captures:32,min:90},
 documents:{script:'browser-documents-notifications-check.cjs',env:'HOUSEHOLD_DOCUMENTS_NOTIFICATIONS_BROWSER_OUTPUT',report:'browser-report.json',axe:16,captures:32,min:90},
 mealplan:{script:'browser-mealplan-notifications-check.cjs',env:'HOUSEHOLD_MEALPLAN_NOTIFICATIONS_BROWSER_OUTPUT',report:'browser-report.json',axe:16,captures:32,min:90}
});
const hash=raw=>crypto.createHash('sha256').update(raw).digest('hex'),need=fixture.need;
function validateReport(report,definition){
 need(report&&report.passed===true&&!report.failure&&report.physicalDeliveryTested===false,'Browser report did not pass or claims physical delivery');
 for(const field of ['checks','accessibility','screenshots','errors','externalRequests'])need(Array.isArray(report[field]),'Browser report field missing');
 need(!report.errors.length&&!report.externalRequests.length&&report.checks.length>=definition.min,'Browser errors or incomplete functional coverage');
 need(report.accessibility.length===definition.axe&&report.accessibility.every(scan=>Array.isArray(scan.violations)&&scan.violations.length===0)&&new Set(report.accessibility.map(scan=>scan.name)).size===definition.axe,'Browser accessibility matrix incomplete');
 need(report.screenshots.length===definition.captures&&new Set(report.screenshots).size===definition.captures&&report.screenshots.every(name=>/^[A-Za-z0-9_-]+\.png$/.test(name)),'Browser capture matrix incomplete or unsafe');
}
function assertPins(root,pins){for(const pin of pins)need(hash(fs.readFileSync(path.join(root,pin.file)))===pin.sha256,'Browser driver/tool bytes changed');}
function runChild(script,env,seconds,{spawnProcess=spawn,killProcess=(child)=>{if(process.platform==='win32')child.kill('SIGKILL');else process.kill(-child.pid,'SIGKILL');}}={}){
 return new Promise((resolve,reject)=>{
  const child=spawnProcess(process.execPath,[script],{env,stdio:['ignore','pipe','pipe'],detached:process.platform!=='win32',windowsHide:true});
  const chunks=[];let bytes=0,failure,timer,killTimer;
  const stop=()=>{try{killProcess(child);}catch(error){if(error.code!=='ESRCH')failure||=Error('Owned browser process termination failed');}};
  timer=setTimeout(()=>{failure=Error('Notification browser exceeded its process deadline');stop();killTimer=setTimeout(()=>reject(failure),5000);},seconds*1000);
  const collect=data=>{bytes+=data.length;if(bytes>(32<<20)){failure=Error('Browser output exceeded its bound');stop();}else chunks.push(data);};
  child.stdout.on('data',collect);child.stderr.on('data',collect);
  child.on('error',error=>{failure=Error('Owned browser process could not run');});
  child.on('close',code=>{clearTimeout(timer);clearTimeout(killTimer);if(failure||code!==0)reject(Object.assign(failure||Error('Notification browser driver failed'),{captured:Buffer.concat(chunks).toString('utf8')}));else resolve(Buffer.concat(chunks).toString('utf8'));});
 });
}
async function authenticate(directory,secrets){
 const password=process.env.HOUSEHOLD_ADMIN_PASSWORD;need(typeof password==='string'&&password.length>=14,'Explicit synthetic bootstrap password required');secrets.push(password);
 const {chromium}=require('playwright'),browser=await chromium.launch({headless:true});
 const changed='CI notification '+crypto.randomBytes(24).toString('hex');secrets.push(changed);
 try{
  const context=await browser.newContext({serviceWorkers:'block'});context.setDefaultTimeout(20000);
  await context.route('**/*',route=>new URL(route.request().url()).origin===fixture.origin?route.continue():route.abort());
  const page=await context.newPage();
  async function login(value){await page.goto(fixture.origin+'/login');await page.locator('[name=username]').fill('bootstrap');await page.locator('[name=password]').fill(value);await Promise.all([page.waitForURL(url=>url.pathname!=='/login'),page.locator('form[action="/login"] button.primary').click()]);}
  await login(password);need(new URL(page.url()).pathname==='/change-password','Fresh CI bootstrap must require password change');
  await page.locator('[name=current]').fill(password);await page.locator('[name=next]').fill(changed);
  await Promise.all([page.waitForURL('**/login'),page.locator('form[action="/change-password"] button.primary').click()]);await login(changed);
  await page.goto(fixture.origin+'/settings/security/reauth?return_to=%2Fsettings%2Fusers');const form=page.locator('[data-reauth-page]');await form.locator('[name=password]').fill(changed);
  await Promise.all([page.waitForURL('**/settings/users'),form.locator('button.primary').click()]);
  if(await page.locator('html').getAttribute('data-locale')!=='de'){await page.locator('.language-control button[value=de]').click();await page.waitForFunction(()=>document.documentElement.dataset.locale==='de');}
  const identity=await page.evaluate(async()=>{const r=await fetch('/api/v1/core/bootstrap',{cache:'no-store'});if(!r.ok)throw Error('Fixture login rejected');return r.json();});
  need(identity.user.role==='admin'&&identity.user.username==='bootstrap','Current fixture administrator identity differs');
  const state=await context.storageState();state.cookies.forEach(cookie=>secrets.push(cookie.value));
  fs.writeFileSync(path.join(directory,'handoff.json'),JSON.stringify(state),{flag:'wx',mode:0o600});
 }finally{await browser.close();}
}
async function main(args){
 const [suite]=args;need(args.length===1&&Object.hasOwn(definitions,suite),'Choose exactly one registered notification browser suite');
 const root=fs.realpathSync(path.resolve(__dirname,'..')),definition=definitions[suite],env=process.env;
 need(env.HOUSEHOLD_NOTIFICATION_BROWSER_SUITE===suite,'Workflow suite identity differs');
 const manifest={schema:1,mode:'github-actions',run_id:env.GITHUB_RUN_ID,run_attempt:env.GITHUB_RUN_ATTEMPT,suite,origin:fixture.origin,container_id:env.HOUSEHOLD_NOTIFICATION_BROWSER_PG_ID};
 fixture.validateCI(manifest,fixture.inspect(manifest.container_id),env);
 const privateRoot=path.join(root,'.local');fs.mkdirSync(privateRoot,{recursive:true,mode:0o700});need(fs.realpathSync(privateRoot)===privateRoot,'Private root uses an alias');
 const output=path.join(root,'outputs/notification-browser',suite);need(!fs.existsSync(output),'Preserve previous notification browser evidence');fs.mkdirSync(output,{recursive:true});
 const report={suite,passed:false,source_unchanged:false,cleanup_passed:false,physical_delivery_tested:false,hosted_execution:env.GITHUB_ACTIONS==='true',run_id:env.GITHUB_RUN_ID,run_attempt:env.GITHUB_RUN_ATTEMPT};
 const secrets=[env.HOUSEHOLD_TEST_OWNER_URL,env.HOUSEHOLD_TEST_APP_URL,env.HOUSEHOLD_ADMIN_PASSWORD,env.HOUSEHOLD_CSRF_SECRET,env.HOUSEHOLD_RATE_SECRET,env.HOUSEHOLD_ENCRYPTION_KEY].filter(Boolean);
 const redact=value=>secrets.reduce((text,secret)=>text.split(secret).join('[redacted]'),String(value)).replace(/postgres(?:ql)?:\/\/[^\s"']+/g,'[private database URL]');
 const source=contracts.sourceSnapshot(root),pins=['scripts/run-notification-browser.cjs','scripts/notification-browser-fixture.cjs','scripts/browser-private-root.cjs','scripts/fuel-browser-contracts.cjs','scripts/'+definition.script,'.github/workflows/browser.yml'].map(file=>({file,sha256:hash(fs.readFileSync(path.join(root,file)))}));
 fs.writeFileSync(path.join(output,'source-before.json'),JSON.stringify(source,null,2),{flag:'wx'});report.tool_pins=pins;
 const binary=path.join(root,'bin/household');report.binary_sha256=hash(fs.readFileSync(binary));
 let directory;
 try{
  directory=fs.mkdtempSync(path.join(privateRoot,'notification-ci-'));fs.chmodSync(directory,0o700);
  manifest.directory=directory;manifest.handoff=path.join(directory,'handoff.json');const manifestFile=path.join(directory,'fixture.json');fs.writeFileSync(manifestFile,JSON.stringify(manifest),{flag:'wx',mode:0o600});
  await authenticate(directory,secrets);
  const childEnv={...env,HOUSEHOLD_BROWSER_URL:fixture.origin,HOUSEHOLD_BROWSER_HANDOFF:manifest.handoff,HOUSEHOLD_NOTIFICATION_BROWSER_CI_MANIFEST:manifestFile,[definition.env]:output};
  const log=await runChild(path.join(root,'scripts',definition.script),childEnv,540);fs.writeFileSync(path.join(output,'driver.log'),redact(log),{flag:'wx'});
  const resultFile=path.join(output,definition.report),result=fixture.boundedJSON(resultFile);validateReport(result,definition);
  report.report_sha256=hash(fs.readFileSync(resultFile));report.checks=result.checks.length;report.axe=result.accessibility.length;
  report.captures=result.screenshots.map(file=>{const full=path.join(output,file),stat=fs.lstatSync(full);need(stat.isFile()&&!stat.isSymbolicLink()&&stat.size>0&&stat.size<=(16<<20),'Screenshot evidence missing or unbounded');return {file,sha256:hash(fs.readFileSync(full))};});
  contracts.assertSources(root,source);assertPins(root,pins);need(hash(fs.readFileSync(binary))===report.binary_sha256,'Application binary changed during browser run');report.source_unchanged=true;report.passed=true;
 }catch(error){report.failure=redact(error.message);if(error.captured)fs.writeFileSync(path.join(output,'driver.log'),redact(error.captured),{flag:'wx'});}
 finally{
  try{contracts.assertSources(root,source);assertPins(root,pins);report.source_unchanged=true;fs.writeFileSync(path.join(output,'source-after.json'),JSON.stringify(contracts.sourceSnapshot(root),null,2),{flag:'wx'});}catch{report.source_unchanged=false;report.passed=false;}
  try{if(directory){need(path.dirname(directory)===privateRoot&&fs.realpathSync(directory)===directory&&!fs.lstatSync(directory).isSymbolicLink(),'Private cleanup path differs');fs.rmSync(directory,{recursive:true});}report.cleanup_passed=true;}catch{report.passed=false;report.cleanup_error='Exact owned private fixture cleanup failed';}
  report.passed=report.passed&&report.source_unchanged&&report.cleanup_passed;fs.writeFileSync(path.join(output,'acceptance.json'),JSON.stringify(report,null,2),{flag:'wx'});
 }
 need(report.passed,'Notification browser acceptance failed; inspect public evidence');console.log(JSON.stringify({suite,passed:true,checks:report.checks,axe:report.axe,captures:report.captures.length,physical_delivery_tested:false}));
}
module.exports={definitions,validateReport,assertPins,runChild};
if(require.main===module)main(process.argv.slice(2)).catch(error=>{console.error(error.message);process.exitCode=1;});
