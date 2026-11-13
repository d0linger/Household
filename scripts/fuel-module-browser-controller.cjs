'use strict';
// Local parent-operated review controller. It uses the normal HTTP/maintenance
// interfaces; owner SQL is restricted to the literal SELECT statements below.
const fs=require('node:fs'),fsp=fs.promises,path=require('node:path');
const {spawn,spawnSync}=require('node:child_process');
const {request}=require('playwright');
const C=require('./fuel-browser-contracts.cjs');
const root=path.resolve(__dirname,'..'),uuid=/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const safeError=error=>String(error).split(/\r?\n/)[0].replace(/postgres(?:ql)?:\/\/[^\s"']+/gi,'[private database URI]').replace(/(?:password|token|cookie|csrf)[a-z-]*\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)/gi,'[private value]');
let driver,driverResult,driverEnded,api,deadline=Infinity,activeCommand;
const report={passed:false,stages:[],boundaries:{source_database:'household_test',restore_database:'household_restore',origin:'http://127.0.0.1:8101',direct_generation_writes:false,synthetic_verified_rows:false}};
const configPath=path.resolve(process.argv[2]||'');
let config,artifactsValidated=false;
function fail(message){throw Error(message);}
function remaining(headroom=0){const value=deadline-Date.now()-headroom;if(value<=0)fail('Real maintenance handshake deadline exhausted');return value;}
async function boundedJSON(file,max){const handle=await fsp.open(file,'r');try{const buffer=Buffer.alloc(max+1),{bytesRead}=await handle.read(buffer,0,max+1,0);if(bytesRead>max)fail('Bounded control artifact exceeded its limit');return JSON.parse(buffer.subarray(0,bytesRead).toString('utf8'));}finally{await handle.close();}}
async function waitDriver(ms,required=true){let timer;try{return await Promise.race([driverEnded,new Promise((resolve,reject)=>{timer=setTimeout(()=>required?reject(Error('Browser did not finish within its bounded deadline')):resolve(null),ms);})]);}finally{clearTimeout(timer);}}
async function stage(name,fn){const started=Date.now(),entry={name,state:'running'};report.stages.push(entry);try{const result=await fn();entry.state='passed';return result;}catch(error){entry.state='failed';entry.error=safeError(error);throw error;}finally{entry.elapsed_ms=Date.now()-started;}}
async function docker(name,args,{input='',maxMs=15000,headroom=5000}={}){
 if(driverResult)fail('Browser ended before '+name);
 const timeout=Math.max(1,Math.min(maxMs,remaining(headroom)));
 return stage(name,()=>new Promise((resolve,reject)=>{
  const child=spawn('docker',args,{cwd:root,windowsHide:true,shell:false,stdio:['pipe','pipe','pipe']});activeCommand=child;
  let out='',err='',done=false;
  const finish=(error,value)=>{if(done)return;done=true;clearTimeout(timer);if(activeCommand===child)activeCommand=null;error?reject(error):resolve(value);};
  const timer=setTimeout(()=>{child.kill();finish(Error(name+' timed out; no completion acknowledgement is allowed'));},timeout);
  child.stdout.on('data',chunk=>{out+=chunk;if(Buffer.byteLength(out)>1048576){child.kill();finish(Error(name+' exceeded output limit'));}});
  child.stderr.on('data',chunk=>{err+=chunk;if(Buffer.byteLength(err)>65536){child.kill();finish(Error(name+' exceeded error limit'));}});
  child.on('error',error=>finish(error));
  child.on('close',code=>{
   if(done)return;
   if(code!==0){const reason=C.operatorFailureReason(out,err,[process.env.HOUSEHOLD_BROWSER_PASSWORD].filter(Boolean));return finish(Error(name+' failed ('+code+'): '+reason));}
   finish(null,out.trim());
  });
  child.stdin.on('error',error=>{if(error.code!=='EPIPE')finish(error);});child.stdin.end(input);
 }));
}
const SQL={
 scope:`SELECT jsonb_build_object('database',current_database(),'role',session_user,
 'households',(SELECT count(*) FROM core.households),
 'actor_allowed',EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=:'household'::uuid AND m.user_id=:'actor'::uuid AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password),
 'session_recent',EXISTS(SELECT 1 FROM core.sessions s JOIN core.users u ON u.id=s.user_id
 WHERE s.id=:'session'::uuid AND s.user_id=:'actor'::uuid AND s.revoked_at IS NULL
 AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 AND s.auth_version=u.auth_version AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND s.reauthenticated_at>clock_timestamp()-interval '10 minutes' AND s.reauthenticated_at<=clock_timestamp()),
 'generation',(SELECT data_generation FROM core.module_states WHERE household_id=:'household'::uuid AND module_id='fuel'),
 'state',(SELECT state FROM core.module_states WHERE household_id=:'household'::uuid AND module_id='fuel'));
`,
 capture_sources:`SELECT coalesce(jsonb_agg(source_row),'[]'::jsonb) FROM(SELECT j.id AS job_id,j.attachment_id,j.kind,j.state,
 encode(j.attachment_sha256,'hex') AS job_sha256,encode(a.sha256,'hex') AS actual_sha256,a.media_type,a.size_bytes
 FROM fuel.capture_jobs j JOIN core.attachments a ON a.household_id=j.household_id AND a.id=j.attachment_id
 WHERE j.household_id=:'household'::uuid AND j.user_id=:'actor'::uuid AND j.generation=:'generation'::bigint
 AND j.state='complete' AND a.state='ready' AND a.module_id='fuel' AND a.resource_type='vehicles'
 AND a.module_generation=j.generation AND a.resource_id=j.vehicle_id ORDER BY j.created_at,j.id LIMIT 8) source_row;
`,
 backup:`SELECT jsonb_build_object('eligible',EXISTS(SELECT 1 FROM core.backup_runs b CROSS JOIN core.module_states s
 WHERE s.household_id=:'household'::uuid AND s.module_id='fuel' AND s.state='disabled' AND s.data_generation=:'generation'::bigint
 AND b.snapshot_reference=:'snapshot' AND b.state='complete' AND b.started_at>=s.changed_at
 AND b.started_at>clock_timestamp()-interval '7 days' AND b.verified_at>clock_timestamp()-interval '7 days'));
`,
 purge_request:`SELECT coalesce(jsonb_agg(id),'[]'::jsonb) FROM(SELECT id FROM core.module_purge_requests
 WHERE household_id=:'household'::uuid AND requested_by=:'actor'::uuid AND module_id='fuel'
 AND expected_generation=:'generation'::bigint AND backup_reference=:'snapshot' AND state='pending'
 AND expires_at>clock_timestamp() ORDER BY confirmed_at DESC,id LIMIT 2) matches;
`,
 purged:`SELECT jsonb_build_object('request_complete',EXISTS(SELECT 1 FROM core.module_purge_requests
 WHERE id=:'purge_request'::uuid AND household_id=:'household'::uuid AND state='completed'),
 'generation',(SELECT data_generation FROM core.module_states WHERE household_id=:'household'::uuid AND module_id='fuel'),
 'state',(SELECT state FROM core.module_states WHERE household_id=:'household'::uuid AND module_id='fuel'),
 'retained_fences',(SELECT count(*) FROM fuel.module_operations WHERE household_id=:'household'::uuid),
 'cleared_payloads',NOT EXISTS(SELECT 1 FROM fuel.module_operations WHERE household_id=:'household'::uuid AND(result IS NOT NULL OR result_sha256 IS NOT NULL)));
`
};
async function ownerRead(kind,vars){if(!Object.hasOwn(SQL,kind))fail('Unknown owner read');const args=['exec','-i',config.source_container,'psql','--no-psqlrc','-U','household_owner','-d','household_test','-qAt','-v','ON_ERROR_STOP=1'];for(const[key,value]of Object.entries(vars)){if(!['household','actor','session','generation','snapshot','purge_request'].includes(key))fail('Unknown owner parameter');if(key==='generation'){if(!/^[1-9]\d{0,15}$/.test(String(value)))fail('Invalid generation');}else if(key==='snapshot'){if(!/^[a-f0-9]{64}$/.test(value))fail('Invalid exact snapshot');}else if(!uuid.test(value))fail('Invalid owner UUID');args.push('-v',key+'='+value);}args.push('--file=-');const text=await docker('owner-read-'+kind,args,{input:SQL[kind],maxMs:10000});if(Buffer.byteLength(text)>16384)fail('Owner projection too large');return JSON.parse(text);}
async function bootstrap(){const response=await api.get('/api/v1/core/bootstrap',{maxRedirects:0,timeout:Math.min(10000,remaining(3000))});if(response.status()!==200||!response.headers()['content-type']?.includes('application/json'))fail('Current private control session is not authenticated');const raw=await response.body();if(raw.length>1048576)fail('Bootstrap projection exceeded limit');const current=JSON.parse(raw);C.validateAuthority(current,report.request);return current;}
async function postForm(name,url,form,scope,status){return stage(name,async()=>{const response=await api.post(url,{form,headers:{'X-CSRF-Token':scope.csrf},maxRedirects:0,timeout:Math.min(10000,remaining(3000))});if(response.status()!==status)fail(name+' was not accepted by the normal HTTP interface (status '+response.status()+')');});}
async function stopDriver(){
 if(!driver)return;
 report.driver_cleanup_verified=false;
 const refuse=message=>{report.passed=false;report.cleanup_failure=message;process.exitCode=1;};
 if(!Number.isSafeInteger(driver.pid)||driver.pid<=0){if(!driverResult)refuse('Owned driver PID was not confirmed');return;}
 const normal=driverResult?.code===0&&!driverResult.signal;
 try{
  if(process.platform==='win32'){
   if(normal){report.driver_cleanup_verified=true;return;}
   if(!driverResult)spawnSync('taskkill',['/PID',String(driver.pid),'/T','/F'],{windowsHide:true,stdio:'ignore',timeout:10000});
   if(!driverResult)await waitDriver(5000,false);
  }else{
   // detached:true creates a group owned by this exact spawned driver. Its root
   // exiting does not prove Chromium descendants left that group.
   const signalGroup=signal=>{try{process.kill(-driver.pid,signal);return true;}catch(error){if(error.code==='ESRCH')return false;throw error;}};
   const waitGroup=async ms=>{const until=Date.now()+ms;while(signalGroup(0)){if(Date.now()>=until)return false;await delay(50);}return true;};
   if(normal&&!signalGroup(0)){report.driver_cleanup_verified=true;return;}
   signalGroup('SIGTERM');
   if(!await waitGroup(5000)){signalGroup('SIGKILL');if(!await waitGroup(5000))refuse('Owned driver process group did not confirm exit');}
   if(!driverResult)await waitDriver(5000,false);
  }
  if(!driverResult)refuse('Owned driver root did not confirm exit');
  // Playwright launches Chromium in its own group. Only normal completion of
  // the driver (which awaits browser.close) proves its browser cleanup here.
  // Killing the root/group cannot establish that independent browser's exit.
  refuse('Forced or failed driver cleanup cannot prove separately detached Chromium exited; retain private recovery state');
 }catch(error){refuse('Owned driver tree cleanup was not confirmed: '+safeError(error));}
}


(async()=>{
 const privateParent=await fsp.realpath(path.dirname(configPath)),privateRoot=await fsp.realpath(path.join(root,'.local'));
 if(!privateParent.startsWith(privateRoot+path.sep)||!/^fuel-browser-auth-[a-f0-9]{32}$/.test(path.basename(privateParent)))fail('Configuration is not in the parent private Fuel directory');
 config=await boundedJSON(configPath,16384);
 if(!/^[a-f0-9]{32}$/.test(config.run_id)||path.basename(privateParent)!=='fuel-browser-auth-'+config.run_id||config.source_container!=='household-tests-db-1')fail('Invalid isolated run identity');
 const prefix='household-fuel-browser-'+config.run_id;
 for(const[name,suffix]of[['toolbox_container','toolbox'],['app_container','app'],['restore_container','restore']])if(config[name]!==prefix+'-'+suffix)fail('Container is outside exact run ownership');
 if(path.resolve(config.control_state)!==path.join(privateParent,'control-state.json'))fail('Control state leaves the private directory');
 const artifacts=await fsp.realpath(config.artifacts),output=await fsp.realpath(path.join(root,'outputs/fuel-module-review'));
 if(artifacts!==path.join(output,'standalone-'+config.run_id)||path.resolve(config.handshake)!==path.join(artifacts,'transition'))fail('Run artifacts leave the isolated output directory');
 artifactsValidated=true;
 report.run_id=config.run_id;report.source_manifest_sha256=config.source_manifest_sha256;report.images=config.images;
 const stdout=fs.openSync(path.join(artifacts,'driver-stdout.log'),'wx'),stderr=fs.openSync(path.join(artifacts,'driver-stderr.log'),'wx');
 const env={};for(const name of ['PATH','PATHEXT','SystemRoot','WINDIR','COMSPEC','USERPROFILE','LOCALAPPDATA','APPDATA','TEMP','TMP','HOME','PROGRAMFILES','ProgramFiles(x86)','PLAYWRIGHT_BROWSERS_PATH','CHROME_PATH'])if(process.env[name])env[name]=process.env[name];
 Object.assign(env,{HOUSEHOLD_BROWSER_URL:'http://127.0.0.1:8101',HOUSEHOLD_BROWSER_USER:'bootstrap',HOUSEHOLD_BROWSER_PASSWORD:process.env.HOUSEHOLD_BROWSER_PASSWORD||'',HOUSEHOLD_FUEL_BROWSER_CONTROL_STATE:config.control_state,HOUSEHOLD_FUEL_BROWSER_GENERATION_HANDSHAKE:config.handshake});
 if(!env.HOUSEHOLD_BROWSER_PASSWORD)fail('Private synthetic initial password is missing');
 driver=spawn(process.execPath,[path.join(root,'scripts/browser-fuel-module-check.cjs')],{cwd:root,env,windowsHide:true,detached:process.platform!=='win32',stdio:['ignore',stdout,stderr]});
 fs.closeSync(stdout);fs.closeSync(stderr);
 driverEnded=new Promise(resolve=>{driver.once('error',error=>{driverResult={code:1,error:safeError(error)};resolve(driverResult);});driver.once('exit',(code,signal)=>{driverResult={code,signal};resolve(driverResult);});});
 await fsp.writeFile(path.join(privateParent,'driver-pid.json'),JSON.stringify({pid:driver.pid,controller_pid:process.pid}));
 const waitUntil=Date.now()+20*60*1000;let received;
 while(Date.now()<waitUntil){if(driverResult)fail('Browser ended before the real maintenance handshake');try{received=await boundedJSON(config.handshake+'.request.json',2048);break;}catch(error){if(error.code!=='ENOENT')throw error;}await delay(250);}
 if(!received)fail('Browser did not reach maintenance within its bounded whole-run budget');
 C.validateRequest(received);
 report.request={key:received.key,actor_id:received.actor_id,household_id:received.household_id,old_generation:received.old_generation,nonce:received.nonce};
 deadline=Date.now()+110000; // Driver allows120s; retain time for validation/atomic acknowledgement.
 const controlStat=await fsp.stat(config.control_state);if(controlStat.size>262144)fail('Private browser control state exceeded limit');
 api=await request.newContext({baseURL:'http://127.0.0.1:8101',storageState:config.control_state,timeout:10000});
 let current=await stage('current-http-authority',bootstrap);
 const variables={household:received.household_id,actor:received.actor_id,session:current.user.session_id,generation:String(received.old_generation)};
 const scope=await ownerRead('scope',variables);
 C.validateOwnerScope(scope,received);
 const evidence=await boundedJSON(path.join(output,'ocr-source-evidence.json'),8192);
 const sourceRows=await ownerRead('capture_sources',{household:received.household_id,actor:received.actor_id,generation:String(received.old_generation)});
 report.ocr_canonical_hash_checks=C.validateOCR(evidence,sourceRows);
 await postForm('http-disable-fuel','/settings/modules/fuel',{version:String(current.states.fuel.version),enabled:'false'},current,303);
 current=await bootstrap();if(current.states.fuel.state!=='disabled'||current.states.fuel.generation!==received.old_generation)fail('Actual Fuel pause/generation was not confirmed');
 const backupOutput=await docker('real-encrypted-backup',['exec',config.toolbox_container,'household','backup'],{maxMs:45000,headroom:30000});
 let snapshot;for(const line of backupOutput.split(/\r?\n/)){try{const event=JSON.parse(line);if(typeof event.snapshot==='string')snapshot=event.snapshot;}catch{}}
 if(!/^[a-f0-9]{64}$/.test(snapshot||''))fail('Actual backup returned no exact snapshot identifier');report.snapshot=snapshot;
 await docker('real-restore-check',['exec',config.toolbox_container,'household','restore-check',snapshot],{maxMs:65000,headroom:25000});
 const backup=await ownerRead('backup',{household:received.household_id,generation:String(received.old_generation),snapshot});if(backup.eligible!==true)fail('Actual completed/verified backup does not follow the module pause');
 remaining(25000);current=await bootstrap();
 await postForm('http-request-purge','/settings/modules/fuel/purge',{confirm:'fuel',backup_reference:snapshot},current,200);
 const queued=await ownerRead('purge_request',{household:received.household_id,actor:received.actor_id,generation:String(received.old_generation),snapshot});
 if(!Array.isArray(queued)||queued.length!==1||!uuid.test(queued[0]))fail('Normal HTTP did not create exactly one scoped pending purge request');report.purge_request_id=queued[0];
 remaining(20000);
 await docker('actual-owner-purge',['exec',config.toolbox_container,'household','purge',queued[0]],{maxMs:15000,headroom:10000});
 const proof=await ownerRead('purged',{household:received.household_id,purge_request:queued[0]});
 C.validatePurge(proof,received.old_generation);report.purge_proof=proof;
 current=await bootstrap();if(current.states.fuel.generation!==proof.generation||current.states.fuel.state!=='disabled')fail('Application has not loaded the actual purged state');
 await postForm('http-enable-fuel','/settings/modules/fuel',{version:String(current.states.fuel.version),enabled:'true'},current,303);
 const fresh=await stage('current-unfiltered-workspace',async()=>{const response=await api.get('/api/v1/fuel/module?pane=trips',{maxRedirects:0,timeout:Math.min(10000,remaining(2000))});if(response.status()!==200)fail('Fresh current workspace unavailable');return response.json();});
 C.validateCurrentWorkspace(fresh,proof.generation);
 C.validateMaintenanceStages(report.stages,snapshot);
 const acknowledgement={key:received.key,nonce:received.nonce,actor_id:received.actor_id,household_id:received.household_id,generation:proof.generation},ack=JSON.stringify(acknowledgement);
 if(Buffer.byteLength(ack)>2048)fail('Acknowledgement exceeded bound');remaining(1000);
 const pendingAck=config.handshake+'.ack.pending';await fsp.writeFile(pendingAck,ack,{flag:'wx',mode:0o600});await fsp.rename(pendingAck,config.handshake+'.ack.json');
 report.acknowledged_generation=proof.generation;
 const result=await waitDriver(45000);report.driver=result;
 if(result.code!==0)fail('Browser driver did not accept the complete real workflow');report.passed=true;
})().catch(error=>{report.failure=safeError(error);process.exitCode=1;}).finally(async()=>{
 activeCommand?.kill();await stopDriver();await api?.dispose();
 if(artifactsValidated)await fsp.writeFile(path.join(config.artifacts,'controller-report.json'),JSON.stringify(report,null,2)+'\n');
 console.log(JSON.stringify({passed:report.passed,run_id:report.run_id,stages:report.stages.length,failure:report.failure}));
});
