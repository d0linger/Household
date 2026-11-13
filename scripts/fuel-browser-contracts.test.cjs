'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {spawnSync}=require('node:child_process');
const C=require('./fuel-browser-contracts.cjs');
const id=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0'),run='a'.repeat(32),pin='b'.repeat(64),image='sha256:'+'c'.repeat(64);
const clone=value=>structuredClone(value);
const request=()=>({actor_id:id(1),household_id:id(2),key:id(3),nonce:id(4),old_generation:1,namespace:'household.fuel.pending.v1.'+id(2)+'.'+id(1)});

test('portable containment distinguishes descendants, siblings and Windows casing',()=>{
 assert.equal(C.inside('/work/household','.local'),false);
 for(const [target,expected]of[['/work/household/.local/auth',true],['/work/household',false],['/work/household-other/auth',false],['/work/household/../foreign',false]])assert.equal(C.inside('/work/household',target),expected);
 assert.equal(C.inside('C:\\Household\\.local','c:\\household\\.local\\auth',path.win32),true);
 assert.equal(C.inside('C:\\Household\\.local','C:\\Household\\.local-other\\auth',path.win32),false);
});
test('native recording is a closed real integration selection, never a list-only or skip-capable run',()=>{
 const args=C.nativeArguments();assert.ok(args.includes('-tags=integration'));assert.ok(args.includes('^TestFuelModule'));assert.ok(args.includes('-count=1'));assert.equal(args.includes('-list'),false);
 const env={HOUSEHOLD_REQUIRE_INTEGRATION:'1',HOUSEHOLD_TEST_OWNER_URL:'postgres://household_owner:unit-only@127.0.0.1:55434/household_test?sslmode=disable',HOUSEHOLD_TEST_APP_URL:'postgres://household_app:unit-only@127.0.0.1:55434/household_test?sslmode=disable'};
 C.validateTestURLs(env);
 for(const url of [env.HOUSEHOLD_TEST_OWNER_URL.replace('55434','5432'),env.HOUSEHOLD_TEST_OWNER_URL.replace('household_test','household'),env.HOUSEHOLD_TEST_OWNER_URL+'&host=foreign',env.HOUSEHOLD_TEST_OWNER_URL+'&sslmode=disable',env.HOUSEHOLD_TEST_OWNER_URL.replace('household_owner','postgres')])assert.throws(()=>C.validateTestURLs({...env,HOUSEHOLD_TEST_OWNER_URL:url}));
 assert.throws(()=>C.validateTestURLs({...env,HOUSEHOLD_REQUIRE_INTEGRATION:'0'}));
});
test('native Go, recorder and CI deadlines preserve cleanup headroom within the existing job budget',()=>{
 const workflow=require('yaml').parse(fs.readFileSync(path.join(__dirname,'../.github/workflows/fuel-ocr-recovery.yml'),'utf8'));
 const job=workflow.jobs['fuel-ocr-recovery'],step=job.steps.find(item=>item.id==='native');
 const goMinutes=Number(C.nativeArguments().find(arg=>/^-timeout=\d+m$/.test(arg)).match(/\d+/)[0]);
 assert.ok(Number.isInteger(goMinutes)&&goMinutes>=17&&goMinutes<=20);
 assert.ok(Object.isFrozen(C.NATIVE_BUDGET));
 assert.ok(C.NATIVE_BUDGET.recorderMinutes>=goMinutes+2&&C.NATIVE_BUDGET.recorderMinutes<=22);
 assert.ok(step['timeout-minutes']>=C.NATIVE_BUDGET.recorderMinutes+1&&step['timeout-minutes']<=23);
 assert.equal(job['timeout-minutes'],75);
 // The browser controller already owns 25 minutes. Leave at least 20 minutes
 // for installation, image builds and owned-resource cleanup, without moving it.
 assert.ok(job['timeout-minutes']-step['timeout-minutes']-25>=20);
});

test('local recorder has an explicit bounded profile with the identical closed Go selection',()=>{
 assert.deepEqual(C.NATIVE_BUDGET,{testMinutes:20,recorderMinutes:22});
 assert.deepEqual(C.nativeProfile('default',{CI:'true'}),C.NATIVE_BUDGET);
 assert.deepEqual(C.nativeProfile('local',{}),{testMinutes:40,recorderMinutes:42});
 assert.ok(Object.isFrozen(C.NATIVE_LOCAL_BUDGET));
 const ordinary=C.nativeArguments('default',{}),local=C.nativeArguments('local',{});
 assert.deepEqual(local.map(arg=>arg==='-timeout=40m'?'-timeout=20m':arg),ordinary);
 assert.deepEqual(ordinary,['test','-tags=integration','-p','1','-json','./internal/web','-run','^TestFuelModule','-count=1','-timeout=20m']);
 assert.equal(local.filter(arg=>arg.startsWith('-timeout=')).length,1);
 for(const invalid of [null,false,'LOCAL','40','local-60',{testMinutes:40,recorderMinutes:42}])assert.throws(()=>C.nativeProfile(invalid,{}),/Unknown native recorder profile/);
});

test('local profile refuses any CI or GitHub Actions marker including false and empty strings',()=>{
 for(const key of ['CI','GITHUB_ACTIONS'])for(const value of ['true','false','1','0','']){
  const env={[key]:value};assert.throws(()=>C.nativeProfile('local',env),/forbidden/);assert.throws(()=>C.nativeArguments('local',env),/forbidden/);
  assert.doesNotThrow(()=>C.nativeArguments('default',env),'ordinary CI recording remains supported');
 }
});

test('budget evidence records selected profile, exact limits and an absolute recorder deadline',()=>{
 const started=new Date('2026-11-03T18:00:00.000Z');
 for(const [profile,testMinutes,recorderMinutes,deadline]of [['default',20,22,'2026-11-03T18:22:00.000Z'],['local',40,42,'2026-11-03T18:42:00.000Z']]){
  const evidence=C.nativeBudgetEvidence(profile,started,{});
  assert.deepEqual(evidence,{recorder_profile:profile,test_timeout_minutes:testMinutes,recorder_timeout_minutes:recorderMinutes,recording_started_at:started.toISOString(),recorder_deadline_at:deadline});
  C.validateNativeBudgetEvidence(evidence,{});
  for(const change of [{test_timeout_minutes:0},{test_timeout_minutes:41},{recorder_timeout_minutes:90},{recorder_profile:'custom'},{recorder_deadline_at:'2026-11-03T19:00:00.000Z'},{recording_started_at:'not-a-date'}])assert.throws(()=>C.validateNativeBudgetEvidence({...evidence,...change},{}));
  for(const key of Object.keys(evidence)){const incomplete={...evidence};delete incomplete[key];assert.throws(()=>C.validateNativeBudgetEvidence(incomplete,{}));}
 }
 assert.throws(()=>C.nativeBudgetEvidence('local',new Date('invalid'),{}));
 assert.throws(()=>C.nativeBudgetEvidence('local',started,{GITHUB_ACTIONS:'true'}),/forbidden/);
 const local=C.nativeBudgetEvidence('local',started,{});assert.throws(()=>C.validateNativeBudgetEvidence(local,{CI:'true'}),/forbidden/);
 C.validateNativeBudgetEvidence({},{}); // Historical evidence retains full separate source/event validation.
});

test('explicit local CLI refuses CI before creating artifacts or attempting to launch Go',t=>{
 const f=campaign(t),prefix=path.join(f.root,'outputs/native/local-refused'),command=path.join(__dirname,'fuel-browser-contracts.cjs');
 const before=fs.readdirSync(path.dirname(prefix)).sort();
 for(const marker of ['CI','GITHUB_ACTIONS']){
  const env={...process.env,[marker]:'true',HOUSEHOLD_REQUIRE_INTEGRATION:'1',HOUSEHOLD_TEST_OWNER_URL:'postgres://household_owner:unit-only@127.0.0.1:55434/household_test?sslmode=disable',HOUSEHOLD_TEST_APP_URL:'postgres://household_app:unit-only@127.0.0.1:55434/household_test?sslmode=disable'};
  const result=spawnSync(process.execPath,[command,'record-native-local',f.root,prefix],{env,encoding:'utf8',timeout:10000,windowsHide:true});
  assert.equal(result.status,1);assert.match(result.stderr,/Local native recorder profile is forbidden/);assert.equal(result.stdout,'');assert.deepEqual(fs.readdirSync(path.dirname(prefix)).sort(),before);
 }
 for(const args of [['record-native-local',f.root,prefix,'--timeout=60m'],['record-native',f.root,prefix,'local']]){
  const result=spawnSync(process.execPath,[command,...args],{encoding:'utf8',timeout:10000,windowsHide:true});assert.equal(result.status,1);assert.match(result.stderr,/Closed Fuel guard command/);assert.deepEqual(fs.readdirSync(path.dirname(prefix)).sort(),before);
 }
});
test('image evidence requires exact immutable id, non-root identity and native-source label',()=>{
 const actual={id:image,user:'10001:10001',source_manifest_sha256:pin};assert.equal(C.validateImage(actual,image,pin),true);
 for(const change of [{id:'household:latest'},{id:'sha256:'+'d'.repeat(64)},{user:'root'},{user:'0:0'},{source_manifest_sha256:'e'.repeat(64)},{source_manifest_sha256:undefined}])assert.throws(()=>C.validateImage({...actual,...change},image,pin));
});
test('database identity cannot alias the production database, network, service or port',()=>{
 const actual={container:'household-tests-db-1',project:'household-tests',service:'db',port:'127.0.0.1:55434',networks:['household-tests_default']};C.validateDatabase(actual);
 for(const change of [{container:'household-db-1'},{project:'household'},{service:'app'},{port:'0.0.0.0:55434'},{port:'127.0.0.1:5432'},{networks:['household_database']}])assert.throws(()=>C.validateDatabase({...actual,...change}));
});
test('cleanup ownership needs both exact closed name and exact run label',()=>{
 for(const suffix of ['app','restore','toolbox'])assert.equal(C.ownedResource('household-fuel-browser-'+run+'-'+suffix,run,run,'container'),true);
 assert.equal(C.ownedResource('household-fuel-browser-'+run+'-repository',run,run,'volume'),true);
 for(const [name,label,kind]of [['household-tests-db-1',run,'container'],['household-fuel-browser-'+run+'-app-extra',run,'container'],['household-fuel-browser-'+run+'-app','f'.repeat(32),'container'],['household-fuel-browser-'+run+'-app',run,'volume']])assert.equal(C.ownedResource(name,label,run,kind),false);
 assert.equal(C.ownedResource('household-fuel-browser-'+run+'-app',run,[run],'container'),false);
});
test('pending locator requires a closed actor/household namespace and exact scalar identities',()=>{
 C.validateRequest(request());
 for(const change of [{old_generation:0},{old_generation:'1'},{old_generation:Number.MAX_SAFE_INTEGER},{key:[id(3)]},{actor_id:id(8)},{namespace:'household.fuel.pending.v1.foreign'},{review:'must-not-be-in-request'}])assert.throws(()=>C.validateRequest({...request(),...change}));
});
test('current control authentication cannot be replaced by a prior or read-only actor',()=>{
 const actual={user:{id:id(1),household_id:id(2),role:'admin',must_change_password:false,session_id:id(5)},csrf:'x'.repeat(43)};C.validateAuthority(actual,request());
 for(const change of [{id:id(8)},{household_id:id(9)},{role:'reader'},{role:'editor'},{must_change_password:true},{must_change_password:undefined},{session_id:[id(5)]}])assert.throws(()=>C.validateAuthority({...actual,user:{...actual.user,...change}},request()));
 assert.throws(()=>C.validateAuthority({...actual,csrf:''},request()));
});
test('owner projection fails closed on wrong role, tenant count, stale session or generation',()=>{
 const scope={database:'household_test',role:'household_owner',households:1,actor_allowed:true,session_recent:true,generation:1,state:'enabled'};C.validateOwnerScope(scope,request());
 for(const change of [{database:'household'},{role:'postgres'},{households:2},{actor_allowed:false},{session_recent:false},{generation:2},{generation:'1'},{state:'disabled'}])assert.throws(()=>C.validateOwnerScope({...scope,...change},request()));
});
test('maintenance session SQL uses actual step-up freshness independently of creation time',()=>{
 const controller=fs.readFileSync(path.join(__dirname,'fuel-module-browser-controller.cjs'),'utf8');
 const session=controller.match(/'session_recent',EXISTS\(([\s\S]*?)\),\s*'generation'/)?.[1];
 assert.ok(session,'Closed controller session projection exists');
 const auth=fs.readFileSync(path.join(__dirname,'../internal/auth/security.go'),'utf8');
 const applicationFreshness=auth.match(/SELECT (reauthenticated_at>clock_timestamp\(\)[^"\n]+) FROM core.sessions WHERE id=\$1/)?.[1];
 assert.ok(applicationFreshness,'Read the actual LockRecentSession rule');
 assert.ok(session.replaceAll('s.reauthenticated_at','reauthenticated_at').includes(applicationFreshness),'Controller must retain the exact application freshness bounds');
 assert.doesNotMatch(session,/created_at/,'An old session with genuine recent confirmation remains eligible; a fresh creation cannot replace stale confirmation');
 for(const guard of ['s.revoked_at IS NULL','s.idle_expires_at>clock_timestamp()','s.absolute_expires_at>clock_timestamp()','s.auth_version=u.auth_version','u.disabled_at IS NULL','NOT u.must_change_password'])assert.ok(session.includes(guard),'Session authority requires '+guard);
 assert.match(session,/s\.id=:'session'::uuid AND s\.user_id=:'actor'::uuid/,'Projection binds the exact current actor and session');
});
test('browser maintenance handoff follows protected confirmation and saves its current session',()=>{
 const browser=fs.readFileSync(path.join(__dirname,'browser-fuel-module-check.cjs'),'utf8');
 const branch=browser.slice(browser.indexOf('if(generationHandshake){'));
 const positions=["await confirmMaintenanceAuthority('maintenance handoff')","await saveControlState()","await fs.writeFile(handshake+'.request.json'"] .map(fragment=>branch.indexOf(fragment));
 assert.ok(positions.every((value,index)=>value>=0&&(index===0||value>positions[index-1])),'Password confirmation and server freshness must precede saving cookies and announcing maintenance');
 assert.ok(browser.includes('activePassword=changedPassword'),'Use the actual new password after the bootstrap password change');
 assert.equal((browser.match(/await confirmMaintenanceAuthority\(/g)||[]).length,2,'Only initial smoke and pre-maintenance confirmation consume the authentication budget');
 assert.ok(browser.indexOf("await confirmMaintenanceAuthority('initial setup smoke')")<browser.indexOf("await workflow('FU01'"),'Verify the real confirmation flow before expensive workflows');
});
test('protected UI confirmation captures genuine bytes before navigation and rejects upstream refusal',async()=>{
 const browser=fs.readFileSync(path.join(__dirname,'browser-fuel-module-check.cjs'),'utf8');
 const source=browser.slice(browser.indexOf('async function confirmMaintenanceAuthority('),browser.indexOf("  await confirmMaintenanceAuthority('initial setup smoke')"));
 for(const [status,payload,allowed]of [[200,{confirmed:true},true],[200,{confirmed:false},false],[403,{error:'denied'},false]]){
  let handler,navigated=false,closed=false,freshnessReads=0;
  const bytes=Buffer.from(JSON.stringify(payload));
  const upstream={status:()=>status,body:async()=>{assert.equal(navigated,false,'Read upstream bytes before UI navigation');return bytes;}};
  const route={request:()=>({method:()=> 'POST'}),fetch:async options=>{assert.deepEqual(options,{maxRedirects:0,timeout:15000});return upstream;},fulfill:async options=>{assert.equal(options.response,upstream);assert.equal(options.body,bytes,'Forward original bytes without invented confirmation');navigated=true;},abort:async()=>{}};
  const tab={route:async(match,callback)=>{assert.equal(match(new URL('http://127.0.0.1:8101/settings/security/reauth')),true);assert.equal(match(new URL('http://127.0.0.1:8101/other')),false);handler=callback;},goto:async()=>{},locator:()=>({locator:selector=>({fill:async secret=>assert.equal(secret,'actual-current-secret'),click:async()=>{assert.equal(selector,'button.primary');await handler(route);}})}),waitForURL:async predicate=>assert.equal(predicate(new URL('http://127.0.0.1:8101/settings/security')),true),close:async()=>{closed=true;}};
  const helper=new Function('context','base','activePassword','must','json','return ('+source+');')({newPage:async()=>tab},'http://127.0.0.1:8101','actual-current-secret',(ok,message)=>assert.ok(ok,message),async url=>{assert.equal(url,'/api/v1/security/freshness');freshnessReads++;return{recent:true};});
  if(allowed)await helper('pure smoke');else await assert.rejects(helper('pure refusal'));
  assert.equal(closed,true);assert.equal(freshnessReads,allowed?1:0);
 }
});
test('protected confirmation never propagates upstream authentication headers or error causes',async()=>{
 const browser=fs.readFileSync(path.join(__dirname,'browser-fuel-module-check.cjs'),'utf8');
 const source=browser.slice(browser.indexOf('async function confirmMaintenanceAuthority('),browser.indexOf("  await confirmMaintenanceAuthority('initial setup smoke')"));
 const sentinels=['sentinel-cookie-private','sentinel-csrf-private','sentinel-authorization-private'];
 const privateError=Error('request call log\nCookie: '+sentinels[0]+'\nX-CSRF-Token: '+sentinels[1]+'\nAuthorization: Bearer '+sentinels[2]);
 let handler,closed=false,aborted=false;
 const route={request:()=>({method:()=> 'POST'}),fetch:async()=>{throw privateError;},abort:async()=>{aborted=true;throw privateError;}};
 const tab={route:async(_match,callback)=>{handler=callback;},goto:async()=>{},locator:()=>({locator:()=>({fill:async()=>{},click:async()=>handler(route)})}),waitForURL:async()=>assert.fail('Refused upstream request must not advance'),close:async()=>{closed=true;}};
 const helper=new Function('context','base','activePassword','must','json','return ('+source+');')({newPage:async()=>tab},'http://127.0.0.1:8101','fixture-password',()=>assert.fail('No upstream success was received'),()=>assert.fail('No freshness check after upstream failure'));
 await assert.rejects(helper('privacy guard'),error=>{
  assert.equal(error.message,'Protected confirmation upstream request failed');
  assert.equal(error.cause,undefined);
  const output=String(error)+'\n'+error.stack+'\n'+JSON.stringify(error,Object.getOwnPropertyNames(error));
  for(const secret of sentinels)assert.equal(output.includes(secret),false,'Sensitive upstream call log must not reach failure output');
  return true;
 });
 assert.equal(aborted,true);assert.equal(closed,true);
});
function ocr(){const evidence=[],rows=[];let n=10;for(const kind of ['receipt','odometer'])for(const format of ['png','jpeg','pdf']){const item={job_id:id(n++),attachment_id:id(n++),kind,format,media_type:format==='pdf'?'application/pdf':'image/'+format,sha256:String(n%10).repeat(64),bytes:1200};evidence.push(item);rows.push({job_id:item.job_id,attachment_id:item.attachment_id,kind,state:'complete',job_sha256:item.sha256,actual_sha256:item.sha256,media_type:item.media_type,size_bytes:1200});}return{evidence,rows};}
test('OCR source proof compares canonical bytes, stored original and immutable capture job',()=>{
 const {evidence,rows}=ocr();assert.equal(C.validateOCR(evidence,rows),6);
 for(const change of [{actual_sha256:'f'.repeat(64)},{job_sha256:'e'.repeat(64)},{attachment_id:id(99)},{kind:'odometer'},{state:'review'},{media_type:'image/png'},{size_bytes:1201}]){const altered=clone(rows);Object.assign(altered[2],change);assert.throws(()=>C.validateOCR(evidence,altered));}
 assert.throws(()=>C.validateOCR(evidence.slice(0,5),rows));const duplicated=clone(evidence);duplicated[5]=duplicated[0];assert.throws(()=>C.validateOCR(duplicated,rows));
});
test('OCR guard rejects non-scalar hashes and impossible byte metadata',()=>{
 for(const change of [{sha256:['f'.repeat(64)]},{bytes:0},{bytes:10*1024*1024+1},{format:['pdf']},{job_id:[id(10)]}]){const {evidence,rows}=ocr();Object.assign(evidence[0],change);assert.throws(()=>C.validateOCR(evidence,rows));}
});
test('OCR proof requires six different actual attachments, including across formats',()=>{
 const {evidence,rows}=ocr();evidence[5].attachment_id=evidence[0].attachment_id;rows[5].attachment_id=rows[0].attachment_id;
 assert.throws(()=>C.validateOCR(evidence,rows));
});
test('purge proof needs exactly one new generation and retained minimal fences',()=>{
 const proof={request_complete:true,generation:2,state:'disabled',cleared_payloads:true,retained_fences:100};C.validatePurge(proof,1);
 for(const change of [{request_complete:false},{generation:1},{generation:3},{state:'enabled'},{cleared_payloads:false},{retained_fences:0},{retained_fences:'100'}])assert.throws(()=>C.validatePurge({...proof,...change},1));
 assert.throws(()=>C.validatePurge(proof,'1'));
});
test('fresh workspace cannot be inferred from a missing receipt or stale generation',()=>{
 C.validateCurrentWorkspace({context:{generation:2,can_write:true,auth_scope:'current'}},2);
 for(const context of [{generation:1,can_write:true,auth_scope:'current'},{generation:2,can_write:false,auth_scope:'current'},{generation:2,can_write:true,auth_scope:''}])assert.throws(()=>C.validateCurrentWorkspace({context},2));
});
const stageNames=['current-http-authority','owner-read-scope','owner-read-capture_sources','http-disable-fuel','real-encrypted-backup','real-restore-check','owner-read-backup','http-request-purge','owner-read-purge_request','actual-owner-purge','owner-read-purged','http-enable-fuel','current-unfiltered-workspace'];
test('maintenance acknowledgement requires every real stage in order',()=>{
 // These objects are unit-level guard inputs, never operational backup receipts.
 const stages=stageNames.map(name=>({name,state:'passed',elapsed_ms:1}));C.validateMaintenanceStages(stages,pin);
 for(const name of ['real-encrypted-backup','real-restore-check','owner-read-backup','actual-owner-purge','current-unfiltered-workspace'])assert.throws(()=>C.validateMaintenanceStages(stages.filter(stage=>stage.name!==name),pin));
 const reordered=clone(stages);[reordered[4],reordered[5]]=[reordered[5],reordered[4]];assert.throws(()=>C.validateMaintenanceStages(reordered,pin));
 const failed=clone(stages);failed[5].state='failed';assert.throws(()=>C.validateMaintenanceStages(failed,pin));assert.throws(()=>C.validateMaintenanceStages([...stages,stages[4]],pin));assert.throws(()=>C.validateMaintenanceStages(stages,'not-a-snapshot'));
});
function campaign(t){const root=fs.mkdtempSync(path.join(os.tmpdir(),'household-fuel-guard-'));t.after(()=>{assert.equal(path.dirname(root),os.tmpdir());assert.ok(path.basename(root).startsWith('household-fuel-guard-'));fs.rmSync(root,{recursive:true,force:true});});fs.mkdirSync(path.join(root,'internal/web'),{recursive:true});fs.mkdirSync(path.join(root,'cmd'),{recursive:true});fs.mkdirSync(path.join(root,'outputs/native'),{recursive:true});const names=Array.from({length:20},(_,i)=>'TestFuelModuleGuardFixture'+i);fs.writeFileSync(path.join(root,'go.mod'),'module household\ngo 1.27.3\n');fs.writeFileSync(path.join(root,'go.sum'),'guard fixture\n');fs.writeFileSync(path.join(root,'cmd/main.go'),'package main\n');fs.writeFileSync(path.join(root,'internal/web/fuel_module_guard_integration_test.go'),names.map(name=>'func '+name+'(t *testing.T) {}').join('\n'));const manifest=C.sourceSnapshot(root),prefix=path.join(root,'outputs/native/current'),manifestPath=prefix+'-source-manifest.json',summaryPath=prefix+'-summary.json',logPath=prefix+'.jsonl';const events=names.flatMap(Test=>[{Action:'run',Package:'household/internal/web',Test},{Action:'pass',Package:'household/internal/web',Test}]);events.push({Action:'pass',Package:'household/internal/web'});const summary={exit_code:0,source_unchanged:true,source_files:manifest.length,main_pass:20,main_fail:0,main_skip:0,database:'household_test',port:55434};fs.writeFileSync(manifestPath,JSON.stringify(manifest));fs.writeFileSync(summaryPath,JSON.stringify(summary));fs.writeFileSync(logPath,events.map(e=>JSON.stringify(e)).join('\n')+'\n');return{root,names,events,summary,manifest,manifestPath,summaryPath,logPath,pin:C.hash(fs.readFileSync(manifestPath))};}
test('native proof binds actual current sources, campaign prefix and every required main',t=>{const f=campaign(t);assert.equal(C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin).required_mains,20);assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,'f'.repeat(64)));const other=path.join(path.dirname(f.manifestPath),'foreign-source-manifest.json');fs.copyFileSync(f.manifestPath,other);assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,other,f.pin));});

test('native preflight validates recorded budgets without replacing exact source and event proof',t=>{
 const f=campaign(t),evidence=C.nativeBudgetEvidence('default',new Date('2026-11-03T18:00:00.000Z'),{});
 fs.writeFileSync(f.summaryPath,JSON.stringify({...f.summary,...evidence}));assert.equal(C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin).required_mains,20);
 fs.writeFileSync(f.summaryPath,JSON.stringify({...f.summary,...evidence,recorder_timeout_minutes:42}));assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin),/budget evidence differs/);
 const local=C.nativeBudgetEvidence('local',new Date('2026-11-03T18:00:00.000Z'),{});fs.writeFileSync(f.summaryPath,JSON.stringify({...f.summary,...local}));
 if(process.env.CI!==undefined||process.env.GITHUB_ACTIONS!==undefined)assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin),/forbidden/);
 else {
  assert.equal(C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin).required_mains,20);
  fs.writeFileSync(f.logPath,f.events.filter(event=>event.Test!==f.names[0]).map(event=>JSON.stringify(event)).join('\n')+'\n');
  assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin),/Required Fuel main did not run and pass/);
 }
});
test('native proof rejects changed and newly added source membership',t=>{const f=campaign(t);fs.appendFileSync(path.join(f.root,'cmd/main.go'),'// changed');assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin));const g=campaign(t);fs.writeFileSync(path.join(g.root,'internal/unaccepted.go'),'package internal');assert.throws(()=>C.nativePreflight(g.root,g.summaryPath,g.manifestPath,g.pin));});
test('native terminal passes cannot substitute for a run, skip, omission or duplicated main',t=>{const f=campaign(t);for(const events of [f.events.slice(1),f.events.filter(e=>e.Test!==f.names[0]),[...f.events,f.events[1]],f.events.map(e=>e.Test===f.names[0]&&e.Action==='pass'?{...e,Action:'skip'}:e)])assert.throws(()=>C.validateNativeEvents(events,f.names,f.summary));});
test('native claimed counts, package identities and any actual failure are checked',t=>{const f=campaign(t);assert.throws(()=>C.validateNativeEvents(f.events,f.names,{...f.summary,main_pass:21}));assert.throws(()=>C.validateNativeEvents(f.events.map(e=>({...e,Package:'foreign/internal/web'})),f.names,f.summary));assert.throws(()=>C.validateNativeEvents([...f.events,{Action:'fail',Package:'household/internal/other'}],f.names,f.summary));});
test('native subtests must run before passing and all started subtests must finish',t=>{
 const f=campaign(t),child={Package:'household/internal/web',Test:f.names[0]+'/real_boundary'};
 const prefix=f.events.slice(0,1),suffix=f.events.slice(1);
 C.validateNativeEvents([...prefix,{...child,Action:'run'},{...child,Action:'pass'},...suffix],f.names,f.summary);
 for(const middle of [
  [{...child,Action:'run'},{...child,Action:'skip'}],
  [{...child,Action:'run'}],
  [{...child,Action:'pass'},{...child,Action:'run'}],
  [{...child,Action:'run'},{...child,Action:'pass'},{...child,Action:'pass'}],
  [{...child,Action:'run'},{...child,Action:'run'},{...child,Action:'pass'}]
 ])assert.throws(()=>C.validateNativeEvents([...prefix,...middle,...suffix],f.names,f.summary));
 const reordered=[f.events[1],f.events[0],...f.events.slice(2)];assert.throws(()=>C.validateNativeEvents(reordered,f.names,f.summary));
});
test('native subtests cannot precede parent run or follow parent completion',t=>{
 const f=campaign(t),parent=f.events[0],child={Package:parent.Package,Test:parent.Test+'/child'};
 for(const events of [
  [{...child,Action:'run'},{...child,Action:'pass'},...f.events],
  [...f.events.slice(0,2),{...child,Action:'run'},{...child,Action:'pass'},...f.events.slice(2)],
  [parent,{...child,Action:'run'},f.events[1],{...child,Action:'pass'},...f.events.slice(2)]
 ])assert.throws(()=>C.validateNativeEvents(events,f.names,f.summary));
 const grandchild={...child,Test:child.Test+'/grandchild'};
 C.validateNativeEvents([parent,{...child,Action:'run'},{...grandchild,Action:'run'},{...grandchild,Action:'pass'},{...child,Action:'pass'},...f.events.slice(1)],f.names,f.summary);
});
test('native embedded slashes bind to the actual main without inventing textual-prefix parents',t=>{
 const f=campaign(t),parent=f.events[0],event=(Test,Action)=>({Package:parent.Package,Test,Action});
 const embedded=parent.Test+'/minor/0';
 C.validateNativeEvents([parent,event(embedded,'run'),event(embedded,'pass'),...f.events.slice(1)],f.names,f.summary);
 const registered=parent.Test+'/decimal',deep=registered+'/minor/0';
 C.validateNativeEvents([parent,event(registered,'run'),event(deep,'run'),event(deep,'pass'),event(registered,'pass'),...f.events.slice(1)],f.names,f.summary);
 // t.Run("decimal") may finish before a later flat t.Run("decimal/minor/0").
 // test2json cannot distinguish that sibling from nested textual prefixes.
 C.validateNativeEvents([parent,event(registered,'run'),event(registered,'pass'),event(deep,'run'),event(deep,'pass'),...f.events.slice(1)],f.names,f.summary);
 assert.throws(()=>C.validateNativeEvents([parent,event(embedded,'run'),f.events[1],event(embedded,'pass'),...f.events.slice(2)],f.names,f.summary));
 assert.throws(()=>C.validateNativeEvents([event(embedded,'run'),event(embedded,'pass'),...f.events],f.names,f.summary));
 assert.throws(()=>C.validateNativeEvents([...f.events.slice(0,2),event(embedded,'run'),event(embedded,'pass'),...f.events.slice(2)],f.names,f.summary));
 assert.throws(()=>C.validateNativeEvents([parent,{...event(embedded,'run'),Package:'household/internal/store'},...f.events.slice(1)],f.names,f.summary));
});
test('parallel flat slash-name siblings may finish before their longer textual sibling',t=>{
 const f=campaign(t),main=f.events[0],event=(Test,Action)=>({Package:main.Package,Test,Action});
 const a=main.Test+'/a',flat=main.Test+'/a/b';
 C.validateNativeEvents([main,event(a,'run'),event(a,'pause'),event(flat,'run'),event(flat,'pause'),event(a,'cont'),event(flat,'cont'),event(a,'pass'),event(flat,'pass'),...f.events.slice(1)],f.names,f.summary);
 // The main still cannot finish while either independently registered case
 // is open; neither a textual relationship nor parallel pause hides a case.
 assert.throws(()=>C.validateNativeEvents([main,event(a,'run'),event(flat,'run'),event(a,'pass'),f.events[1],event(flat,'pass'),...f.events.slice(2)],f.names,f.summary));
});
test('stderr redaction waits for complete UTF-8 lines across arbitrary chunks and flushes the final line',()=>{
 const parts=[],secret='private-password',redact=text=>text.replaceAll(secret,'[private]').replace(/postgres:\/\/[^\s]+/g,'[database]');
 const sink=C.redactedLines(text=>parts.push(text),redact);
 const bytes=Buffer.from('Fehler ü: postgres://owner:'+secret+'@db/test\nfinal '+secret);
 for(let i=0;i<bytes.length;i++)sink.push(bytes.subarray(i,i+1));
 assert.deepEqual(parts,['Fehler ü: [database]\n']);sink.end();assert.deepEqual(parts,['Fehler ü: [database]\n','final [private]']);
 assert.equal(parts.join('').includes(secret),false);assert.throws(()=>sink.push(Buffer.from('late')));
});
test('stderr buffering refuses over-limit pending output before publishing an incomplete secret',()=>{
 const parts=[],sink=C.redactedLines(text=>parts.push(text),text=>text,8);
 sink.push(Buffer.from('password'));assert.throws(()=>sink.push(Buffer.from('x')));assert.deepEqual(parts,[]);
});
test('actual recorder sanitizer covers JSON keys, escaped dumps, headers, URIs and known credentials',()=>{
 const redact=C.nativeSanitizer(['known-password']);
 for(const input of [
  '{"csrf":"opaque-value"}',
  '{"token":"opaque-value","other":"tail"}',
  String.raw`{\"secret_key\":\"opaque-value\"}`,
  'X-CSRF-Token: opaque-value',
  'Authorization: Bearer opaque-value',
  'Set-Cookie: opaque-value',
  'postgres://owner:opaque-value@127.0.0.1/db',
  'failure known-password'
 ]){
  const result=redact(input);assert.equal(result.includes('opaque-value'),false);assert.equal(result.includes('known-password'),false);
 }
 const output=[],sink=C.redactedLines(text=>output.push(text),redact);
 for(const byte of Buffer.from('ä {"csrf":"opaque-value"}\nfinal known-password'))sink.push(Buffer.from([byte]));
 sink.end();assert.equal(output.join('').includes('opaque-value'),false);assert.equal(output.join('').includes('known-password'),false);assert.ok(output.join('').startsWith('ä '));
 const event={Action:'pass',Package:'household/internal/web',Test:'TestFuelModuleSecurity',Output:'{"token":"opaque-value"}'};
 const sanitized={...event,Output:redact(event.Output)};assert.deepEqual([sanitized.Action,sanitized.Package,sanitized.Test],[event.Action,event.Package,event.Test]);
 assert.throws(()=>C.nativeSanitizer(['split\npassword']));
});
test('operator failure extracts only the structured terminal Slog error and prefers it over stderr',()=>{
 const stopped=error=>JSON.stringify({time:'2026-10-28T12:00:00Z',level:'ERROR',msg:'household stopped',error});
 const stdout=[JSON.stringify({level:'INFO',msg:'backup progress',private_payload:'never-publish-this'}),stopped('earlier failure'),stopped('incomplete shopping barcode contract')].join('\n');
 assert.equal(C.operatorFailureReason(stdout,'generic Docker failure\n'),'incomplete shopping barcode contract');
 assert.equal(C.operatorFailureReason(stopped('restore failed\nprivate unstructured tail'),''),'restore failed');
});

test('operator failure ignores arbitrary, malformed and nonterminal stdout without disclosure',()=>{
 for(const stdout of [
  'RAW PRIVATE STDOUT',
  '{malformed secret payload',
  JSON.stringify({level:'ERROR',msg:'different event',error:'PRIVATE EVENT'}),
  JSON.stringify({level:'INFO',msg:'household stopped',error:'PRIVATE INFO'}),
  JSON.stringify({level:'ERROR',msg:'household stopped',error:{payload:'PRIVATE OBJECT'}}),
  JSON.stringify({level:'ERROR',msg:'household stopped',error:'',payload:'PRIVATE EXTRA'})
 ]){
  assert.equal(C.operatorFailureReason(stdout,'first stderr\nlast actionable stderr\n'),'last actionable stderr');
  assert.equal(C.operatorFailureReason(stdout,''),'no safe operator error available');
 }
});

test('operator failure uses the actual credential policy for URLs, quoted JSON and known bootstrap',()=>{
 const known='known-bootstrap-password',stopped=error=>JSON.stringify({level:'ERROR',msg:'household stopped',error,private_payload:'never-publish-this'});
 for(const input of [
  'connect postgres://owner:opaque-value@db:5432/household_restore failed',
  'operator {"password":"opaque-value"}',
  'operator {\\\"csrf\\\":\\\"opaque-value\\\"}',
  'operator Authorization: Bearer opaque-value',
  'operator '+known
 ])for(const [stdout,stderr]of [[stopped(input),''],['unrelated stdout',input]]){
  const reason=C.operatorFailureReason(stdout,stderr,[known]);
  assert.ok(reason.startsWith('connect ')||reason.startsWith('operator '));
  for(const secret of ['opaque-value',known,'never-publish-this'])assert.equal(reason.includes(secret),false);
 }
});

test('operator failure bounds inputs and output while sanitizing before fallback truncation',()=>{
 const known='known-bootstrap-password',stderr='x'.repeat(3995)+known+'\n';
 const reason=C.operatorFailureReason('',stderr,[known]);
 assert.equal(reason.length,4000);assert.equal(reason.includes('known'),false);
 const stdout=JSON.stringify({level:'ERROR',msg:'household stopped',error:'x'.repeat(60000)});
 assert.equal(C.operatorFailureReason(stdout,'').length,4000);
 const oversizedEvent=JSON.stringify({level:'ERROR',msg:'household stopped',error:'x'.repeat(65536)});
 assert.equal(C.operatorFailureReason(oversizedEvent,'bounded fallback'),'bounded fallback');
 assert.throws(()=>C.operatorFailureReason('x'.repeat(1048577),''));
 assert.throws(()=>C.operatorFailureReason('','x'.repeat(65537)));
 assert.throws(()=>C.operatorFailureReason({},''));
});

test('native summary cannot cross role/database/port or hide incomplete sources',t=>{for(const change of [{database:'household'},{port:5432},{main_pass:19},{main_skip:1},{source_unchanged:false},{source_files:999},{exit_code:1}]){const f=campaign(t);fs.writeFileSync(f.summaryPath,JSON.stringify({...f.summary,...change}));assert.throws(()=>C.nativePreflight(f.root,f.summaryPath,f.manifestPath,f.pin));}});
test('native parser and manifest validation reject corruption, path escape and duplicate entries',()=>{assert.throws(()=>C.parseEvents('not-json'));assert.throws(()=>C.parseEvents(JSON.stringify({Action:1})));for(const manifest of [[{path:'../foreign',sha256:pin}],[{path:'internal/../foreign',sha256:pin}],[{path:'C:\\foreign',sha256:pin}],[{path:'internal/x.go',sha256:pin},{path:'internal/x.go',sha256:pin}],[{path:'internal/x.go',sha256:[pin]}]])assert.throws(()=>C.validateManifest(manifest));});
