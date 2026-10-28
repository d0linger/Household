'use strict';
// Pure guards plus a closed real-Go evidence recorder. Importing this module
// never opens a database, starts Docker, launches a browser or runs Go.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const {spawn}=require('node:child_process');
const SHA=/^[a-f0-9]{64}$/,UUID=/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/,RUN=/^[a-f0-9]{32}$/;
const MAX_LOG=128<<20,MAX_LINE=2<<20,MAX_FILES=5000;
const isSHA=value=>typeof value==='string'&&SHA.test(value),isUUID=value=>typeof value==='string'&&UUID.test(value),isRun=value=>typeof value==='string'&&RUN.test(value);
const hash=value=>crypto.createHash('sha256').update(value).digest('hex');
function requireThat(ok,message){if(!ok)throw Error(message);}
function inside(root,target,paths=path){const relative=paths.relative(root,target);return relative!==''&&!relative.startsWith('..'+paths.sep)&&relative!=='..'&&!paths.isAbsolute(relative);}
function guardPath(root,target){root=path.resolve(root);target=path.resolve(target);requireThat(inside(root,target),'Evidence path leaves its exact root');let current=root;for(const part of ['',...path.relative(root,target).split(path.sep)]){if(part)current=path.join(current,part);if(!fs.existsSync(current))break;requireThat(!fs.lstatSync(current).isSymbolicLink(),'Evidence path refuses symbolic links');}return target;}
function readBounded(file,max){const stat=fs.lstatSync(file);requireThat(stat.isFile()&&!stat.isSymbolicLink()&&stat.size>0&&stat.size<=max,'Invalid/bounded regular evidence file');return fs.readFileSync(file);}
function jsonFile(file,max){return JSON.parse(readBounded(file,max).toString('utf8').replace(/^\uFEFF/,''));}
function sourceSnapshot(root){const files=[];const visit=relative=>{const full=path.join(root,relative),stat=fs.lstatSync(full);requireThat(!stat.isSymbolicLink(),'Native source refuses symlinks');if(stat.isDirectory()){for(const name of fs.readdirSync(full).sort())visit(relative+'/'+name);}else{requireThat(stat.isFile()&&stat.size<=32<<20&&files.length<MAX_FILES,'Native source exceeds bounds');files.push({path:relative,sha256:hash(fs.readFileSync(full))});}};for(const name of ['cmd','internal','go.mod','go.sum'])visit(name);return files.sort((a,b)=>a.path.localeCompare(b.path,'en'));}
function browserSnapshot(root){const files=[];const visit=relative=>{const stat=fs.lstatSync(path.join(root,relative));requireThat(!stat.isSymbolicLink(),'Browser source refuses symbolic links');if(stat.isDirectory()){for(const name of fs.readdirSync(path.join(root,relative)).sort())visit(relative+'/'+name);}else{requireThat(stat.isFile()&&stat.size<=32<<20&&files.length<MAX_FILES,'Browser source exceeds bounds');files.push({path:relative,sha256:hash(fs.readFileSync(path.join(root,relative)))});}};for(const name of ['scripts','package.json','package-lock.json'])visit(name);return files.sort((a,b)=>a.path.localeCompare(b.path,'en'));}
function validateManifest(manifest){requireThat(Array.isArray(manifest)&&manifest.length>0&&manifest.length<=MAX_FILES,'Native manifest is empty/excessive');const seen=new Set();for(const item of manifest){requireThat(item&&typeof item.path==='string'&&/^(?:cmd\/|internal\/|go\.mod$|go\.sum$)/.test(item.path)&&!item.path.includes('\\')&&!item.path.split('/').some(part=>!part||part==='.'||part==='..')&&!seen.has(item.path)&&isSHA(item.sha256),'Native manifest contains an invalid/duplicate path/hash');seen.add(item.path);}}
function assertSources(root,manifest){validateManifest(manifest);const current=sourceSnapshot(root);requireThat(current.length===manifest.length,'Native source membership changed');const expected=new Map(manifest.map(item=>[item.path,item.sha256]));for(const item of current)requireThat(expected.get(item.path)===item.sha256,'Native source hash/membership changed: '+item.path);return current.length;}
function requiredFuelMains(root,manifest){const names=new Set();for(const file of manifest.filter(item=>/^internal\/web\/fuel_module.*_integration_test\.go$/.test(item.path))){const text=readBounded(path.join(root,file.path),32<<20).toString('utf8');for(const match of text.matchAll(/^func (TestFuelModule[A-Za-z0-9_]+)\(/gm))names.add(match[1]);}requireThat(names.size>=20,'Current source must define every Fuel main contract');return [...names].sort();}
function parseEvents(text){requireThat(Buffer.byteLength(text)<=MAX_LOG,'Native JSONL exceeds limit');return text.split(/\r?\n/).filter(Boolean).map(line=>{requireThat(Buffer.byteLength(line)<=MAX_LINE,'Native JSON line exceeds limit');const event=JSON.parse(line);requireThat(event&&typeof event==='object'&&typeof event.Action==='string','Malformed native event');return event;});}
function validateNativeEvents(events,required,summary,moduleName='household'){
 const run=new Set(),terminal=new Map(),allMains=new Map();
 for(const event of events){
  requireThat(event.Action!=='fail'&&event.Action!=='build-fail','Actual native event failed');
  if(!event.Test)continue;
  requireThat(typeof event.Test==='string'&&typeof event.Package==='string','Native test identity is malformed');
  requireThat(event.Action!=='skip','Actual native case was skipped');
  const identity=event.Package+'/'+event.Test;
  if(event.Action==='run'){
   requireThat(!run.has(identity)&&!terminal.has(identity),'Native test repeated or started after completion');
   if(event.Test.includes('/')){
    const main=event.Package+'/'+event.Test.split('/')[0];
    requireThat(run.has(main)&&!terminal.has(main),'Native subtest main is not currently running');
    // test2json has no parent ID. A slash may be part of one flat t.Run name,
    // even if the same textual prefix ran earlier or is a parallel sibling.
    // Only the top-level main is unambiguously identified by this name.
   }
   run.add(identity);
  }
  if(event.Action==='pass'){
   requireThat(run.has(identity)&&!terminal.has(identity),'Native terminal result preceded its run or repeated');
   if(!event.Test.includes('/'))for(const child of run)requireThat(!child.startsWith(identity+'/')||terminal.has(child),'Native main completed before its subtest');
   terminal.set(identity,'pass');
   if(!event.Test.includes('/'))allMains.set(identity,'pass');
  }
 }
 for(const identity of run)requireThat(terminal.get(identity)==='pass','Native test or subtest was unfinished: '+identity);
 for(const name of required){const identity=moduleName+'/internal/web/'+name;requireThat(run.has(identity)&&terminal.get(identity)==='pass','Required Fuel main did not run and pass: '+name);}
 const counts={main_pass:[...allMains.values()].filter(action=>action==='pass').length,main_fail:[...allMains.values()].filter(action=>action==='fail').length,main_skip:[...allMains.values()].filter(action=>action==='skip').length};
 requireThat(counts.main_pass>=20&&counts.main_fail===0&&counts.main_skip===0,'Native campaign is incomplete/failed/skipped');
 if(summary)for(const key of Object.keys(counts))requireThat(summary[key]===counts[key],'Claimed native counts differ from actual JSONL');
 return counts;
}
function nativePreflight(root,summaryPath,manifestPath,manifestSHA){
 root=path.resolve(root);summaryPath=path.resolve(summaryPath);manifestPath=path.resolve(manifestPath);const output=path.join(root,'outputs');
 guardPath(output,summaryPath);guardPath(output,manifestPath);requireThat(summaryPath.endsWith('-summary.json')&&manifestPath===summaryPath.slice(0,-'-summary.json'.length)+'-source-manifest.json','Native campaign paths/prefix differ');
 requireThat(isSHA(manifestSHA)&&hash(readBounded(manifestPath,4<<20))===manifestSHA,'Native manifest pin differs');
 const summary=jsonFile(summaryPath,1<<20),manifest=jsonFile(manifestPath,4<<20);
 requireThat(summary.exit_code===0&&summary.source_unchanged===true&&summary.database==='household_test'&&summary.port===55434&&summary.main_pass>=20&&summary.main_fail===0&&summary.main_skip===0&&summary.source_files===manifest.length,'Native summary is not full unchanged isolated acceptance');
 assertSources(root,manifest);const required=requiredFuelMains(root,manifest),moduleName=/^module (\S+)$/m.exec(readBounded(path.join(root,'go.mod'),1<<20).toString('utf8'))?.[1];requireThat(moduleName,'Go module identity missing');
 const events=parseEvents(readBounded(summaryPath.slice(0,-'-summary.json'.length)+'.jsonl',MAX_LOG).toString('utf8'));
 validateNativeEvents(events,required,summary,moduleName);
 return {required_mains:required.length,source_files:manifest.length,manifest_sha256:manifestSHA,summary_sha256:hash(readBounded(summaryPath,1<<20))};
}
function validateImage(actual,expectedId,manifestSHA){requireThat(/^sha256:[a-f0-9]{64}$/.test(expectedId)&&isSHA(manifestSHA)&&actual?.id===expectedId&&actual.user==='10001:10001'&&actual.source_manifest_sha256===manifestSHA,'Exact non-root current-source image proof required');return true;}
function validateDatabase(actual){requireThat(actual?.container==='household-tests-db-1'&&actual.project==='household-tests'&&actual.service==='db'&&actual.port==='127.0.0.1:55434'&&Array.isArray(actual.networks)&&actual.networks.includes('household-tests_default'),'Source is not the exact isolated test container/network/port');return true;}
function ownedResource(name,label,run,kind){if(!isRun(run)||label!==run)return false;const suffixes=kind==='container'?['app','toolbox','restore']:kind==='volume'?['uploads','work','repository']:[];return suffixes.some(suffix=>name==='household-fuel-browser-'+run+'-'+suffix);}
function validateRequest(value){const keys=['actor_id','household_id','key','namespace','nonce','old_generation'];requireThat(value&&Object.keys(value).sort().join(',')===keys.sort().join(','),'Unexpected maintenance request shape');for(const name of ['actor_id','household_id','key','nonce'])requireThat(isUUID(value[name]),'Invalid maintenance UUID');requireThat(Number.isSafeInteger(value.old_generation)&&value.old_generation>0&&value.old_generation<Number.MAX_SAFE_INTEGER&&value.namespace==='household.fuel.pending.v1.'+value.household_id+'.'+value.actor_id,'Invalid pending locator scope');return value;}
function validateAuthority(scope,requested){requireThat(scope?.user?.id===requested.actor_id&&scope.user.household_id===requested.household_id&&scope.user.role==='admin'&&scope.user.must_change_password===false&&isUUID(scope.user.session_id)&&typeof scope.csrf==='string'&&scope.csrf.length>0&&scope.csrf.length<=4096,'Current control authority differs from requested scope');return true;}
function validateOwnerScope(scope,requested){requireThat(scope?.database==='household_test'&&scope.role==='household_owner'&&scope.households===1&&scope.actor_allowed===true&&scope.session_recent===true&&scope.generation===requested.old_generation&&scope.state==='enabled','Actual owner projection refused source/current authority/recent session');return true;}
function validateOCR(evidence,rows){requireThat(Array.isArray(evidence)&&evidence.length===6&&new Set(evidence.map(item=>item.job_id)).size===6&&new Set(evidence.map(item=>item.attachment_id)).size===6&&new Set(evidence.map(item=>item.kind+'/'+item.format)).size===6&&Array.isArray(rows)&&rows.length===6,'Six distinct completed OCR sources are required');for(const source of evidence){const row=rows.find(item=>item.job_id===source.job_id),mime={png:'image/png',jpeg:'image/jpeg',pdf:'application/pdf'}[source.format];requireThat(isUUID(source.job_id)&&isUUID(source.attachment_id)&&['receipt','odometer'].includes(source.kind)&&typeof source.format==='string'&&mime&&isSHA(source.sha256)&&Number.isSafeInteger(source.bytes)&&source.bytes>0&&source.bytes<=10*1024*1024&&row&&row.attachment_id===source.attachment_id&&row.kind===source.kind&&row.state==='complete'&&row.job_sha256===source.sha256&&row.actual_sha256===source.sha256&&row.media_type===mime&&source.media_type===mime&&row.size_bytes===source.bytes,'Actual canonical attachment/job hash differs from downloaded source');}return rows.length;}
function validatePurge(proof,oldGeneration){requireThat(Number.isSafeInteger(oldGeneration)&&oldGeneration>0&&oldGeneration<Number.MAX_SAFE_INTEGER&&proof?.request_complete===true&&proof.generation===oldGeneration+1&&proof.state==='disabled'&&proof.cleared_payloads===true&&Number.isSafeInteger(proof.retained_fences)&&proof.retained_fences>0,'Actual purge/minimal-fence proof failed');return true;}
function validateCurrentWorkspace(view,generation){requireThat(Number.isSafeInteger(generation)&&generation>0&&view?.context?.generation===generation&&view.context.can_write===true&&typeof view.context.auth_scope==='string'&&view.context.auth_scope.length>0,'Fresh authorized workspace does not prove current generation');return true;}
function validateMaintenanceStages(stages,snapshot){requireThat(isSHA(snapshot)&&Array.isArray(stages),'Actual maintenance evidence missing');let previous=-1;for(const name of ['current-http-authority','owner-read-scope','owner-read-capture_sources','http-disable-fuel','real-encrypted-backup','real-restore-check','owner-read-backup','http-request-purge','owner-read-purge_request','actual-owner-purge','owner-read-purged','http-enable-fuel','current-unfiltered-workspace']){const matches=stages.map((stage,index)=>({stage,index})).filter(item=>item.stage.name===name);requireThat(matches.length===1&&matches[0].stage.state==='passed'&&matches[0].index>previous,'Missing/reordered/failed real maintenance stage: '+name);previous=matches[0].index;}return true;}
function nativeArguments(){return ['test','-tags=integration','-p','1','-json','./internal/web','-run','^TestFuelModule','-count=1','-timeout=15m'];}
function validateTestURLs(env){for(const[name,user]of [['HOUSEHOLD_TEST_OWNER_URL','household_owner'],['HOUSEHOLD_TEST_APP_URL','household_app']]){const url=new URL(env[name]||'');requireThat(['postgres:','postgresql:'].includes(url.protocol)&&['127.0.0.1','localhost'].includes(url.hostname)&&url.port==='55434'&&url.pathname==='/household_test'&&decodeURIComponent(url.username)===user&&url.password&&!url.hash&&[...url.searchParams.keys()].join(',')==='sslmode'&&url.searchParams.get('sslmode')==='disable','Native recording requires exact isolated test URLs');}requireThat(env.HOUSEHOLD_REQUIRE_INTEGRATION==='1','Native recording cannot silently skip integration');}
function redactedLines(write,redact,maxBytes=65536){
 const decoder=new(require('node:string_decoder').StringDecoder)('utf8');
 let pending='',received=0,ended=false;
 const flush=()=>{let end;while((end=pending.indexOf('\n'))>=0){write(redact(pending.slice(0,end+1)));pending=pending.slice(end+1);}};
 return {
  push(chunk){requireThat(!ended,'Stderr stream already ended');received+=chunk.length;requireThat(received<=maxBytes,'Native stderr exceeds bound');pending+=decoder.write(chunk);flush();},
  end(){requireThat(!ended,'Stderr stream ended twice');ended=true;pending+=decoder.end();flush();if(pending)write(redact(pending));pending='';}
 };
}
function nativeSanitizer(passwords){
 requireThat(Array.isArray(passwords)&&passwords.every(value=>typeof value==='string'&&value.length>0&&!/[\r\n]/.test(value)),'Native redaction requires single-line exact credentials');
 return text=>passwords.reduce((value,password)=>value.split(password).join('[private value]'),text)
  .replace(/postgres(?:ql)?:\/\/[^\s"']+/gi,'[private database URI]')
  // Only Output/stderr text is sanitized. JSONL test identity fields remain
  // intact. Quoted or escaped JSON keys and headers may precede private data.
  .replace(/((?:\\?["'])?(?:cookie|csrf|token|secret|password|authorization)[a-z_-]*(?:\\?["'])?\s*[:=]).*/gi,'$1[private value]');
}
function operatorFailureReason(stdout,stderr,passwords=[]){
 requireThat(typeof stdout==='string'&&typeof stderr==='string'&&Buffer.byteLength(stdout)<=1048576&&Buffer.byteLength(stderr)<=65536,'Operator failure streams exceed bounds');
 const redact=nativeSanitizer(passwords),bounded=text=>redact(text).split(/\r?\n/)[0].replace(/[\u0000-\u001f\u007f]/g,' ').trim().slice(0,4000);
 let reason='';
 for(const line of stdout.split(/\r?\n/)){
  if(!line||Buffer.byteLength(line)>65536)continue;
  let event;try{event=JSON.parse(line);}catch{continue;}
  if(event&&event.level==='ERROR'&&event.msg==='household stopped'&&typeof event.error==='string'&&event.error.trim())reason=bounded(event.error);
 }
 if(reason)return reason;
 // Sanitize the complete bounded stderr before selecting/truncating a line;
 // slicing raw output first could expose a suffix of an exact private value.
 const lines=redact(stderr).split(/\r?\n/).filter(line=>line.trim());
 return lines.length?bounded(lines[lines.length-1]):'no safe operator error available';
}
async function recordNative(root,prefix){
 root=path.resolve(root);prefix=guardPath(path.join(root,'outputs'),path.resolve(prefix));requireThat(/^[a-z0-9-]+$/.test(path.basename(prefix)),'Native output prefix is invalid');validateTestURLs(process.env);const manifest=sourceSnapshot(root),required=requiredFuelMains(root,manifest);const passwords=['HOUSEHOLD_TEST_OWNER_URL','HOUSEHOLD_TEST_APP_URL'].map(name=>decodeURIComponent(new URL(process.env[name]).password));const redact=nativeSanitizer(passwords);fs.mkdirSync(path.dirname(prefix),{recursive:true});const manifestFile=prefix+'-source-manifest.json';fs.writeFileSync(manifestFile,JSON.stringify(manifest,null,2)+'\n',{flag:'wx'});const log=fs.openSync(prefix+'.jsonl','wx'),err=fs.openSync(prefix+'-stderr.log','wx');
 let buffer='',total=0;const events=[];let streamError;
 const result=await new Promise(resolve=>{
  const child=spawn('go',nativeArguments(),{cwd:root,env:process.env,shell:false,windowsHide:true,stdio:['ignore','pipe','pipe']});
  const decoder=new(require('node:string_decoder').StringDecoder)('utf8');
  const stderr=redactedLines(text=>fs.writeSync(err,text),redact);
  const fail=error=>{streamError=error;child.kill();};
  const timer=setTimeout(()=>fail(Error('Native recorder exceeded16minutes')),16*60*1000);
  const line=text=>{
   if(!text.trim()||streamError)return;
   try{
    requireThat(Buffer.byteLength(text)<=MAX_LINE,'Native line exceeds bound');
    const event=JSON.parse(text);
    if(typeof event.Output==='string')event.Output=redact(event.Output);
    const encoded=JSON.stringify(event)+'\n';
    total+=Buffer.byteLength(encoded);
    requireThat(total<=MAX_LOG,'Native evidence exceeds bound');
    events.push(event);fs.writeSync(log,encoded);
   }catch(error){fail(error);}
  };
  child.stdout.on('data',chunk=>{
   if(streamError)return;
   try{
    buffer+=decoder.write(chunk);
    requireThat(Buffer.byteLength(buffer)<=MAX_LINE*2,'Native pending line exceeds bound');
    let end;
    while((end=buffer.indexOf('\n'))>=0){line(buffer.slice(0,end));buffer=buffer.slice(end+1);}
   }catch(error){fail(error);}
  });
  child.stderr.on('data',chunk=>{if(!streamError)try{stderr.push(chunk);}catch(error){fail(error);}});
  child.on('error',error=>{streamError=error;});
  child.on('close',(code,signal)=>{
   clearTimeout(timer);
   if(!streamError)try{buffer+=decoder.end();if(buffer)line(buffer);stderr.end();}catch(error){streamError=error;}
   resolve({code:code??1,signal});
  });
 });fs.closeSync(log);fs.closeSync(err);
 const mains=events.filter(event=>event.Test&&!event.Test.includes('/')&&['pass','fail','skip'].includes(event.Action));let stable=true;try{assertSources(root,manifest);}catch{stable=false;}
 const summary={exit_code:result.code,pattern:'^TestFuelModule',source_files:manifest.length,source_unchanged:stable,main_pass:mains.filter(e=>e.Action==='pass').length,main_fail:mains.filter(e=>e.Action==='fail').length,main_skip:mains.filter(e=>e.Action==='skip').length,database:'household_test',port:55434,source_path_changes:stable?0:1,output_redaction:'URL/password and token-like Output text only; terminal identities preserved'};
 if(streamError)summary.recorder_failure=String(streamError.message);fs.writeFileSync(prefix+'-summary.json',JSON.stringify(summary,null,2)+'\n',{flag:'wx'});requireThat(!streamError&&result.code===0,'Actual native recording failed');return nativePreflight(root,prefix+'-summary.json',manifestFile,hash(fs.readFileSync(manifestFile)));
}
module.exports={hash,inside,guardPath,readBounded,jsonFile,sourceSnapshot,browserSnapshot,validateManifest,assertSources,requiredFuelMains,parseEvents,validateNativeEvents,nativePreflight,validateImage,validateDatabase,ownedResource,validateRequest,validateAuthority,validateOwnerScope,validateOCR,validatePurge,validateCurrentWorkspace,validateMaintenanceStages,nativeArguments,validateTestURLs,redactedLines,nativeSanitizer,operatorFailureReason,recordNative};
function stdinJSON(){const chunks=[];let size=0;while(true){const buffer=Buffer.alloc(4096),read=fs.readSync(0,buffer,0,buffer.length,null);if(!read)break;size+=read;requireThat(size<=16384,'Guard metadata exceeds bound');chunks.push(buffer.subarray(0,read));}return JSON.parse(Buffer.concat(chunks).toString('utf8'));}
if(require.main===module){(async()=>{const[action,...args]=process.argv.slice(2);let result;if(action==='native-preflight'&&args.length===4)result=nativePreflight(...args);else if(action==='source'&&args.length===3){const[root,file,pin]=args;guardPath(path.join(path.resolve(root),'outputs'),path.resolve(file));requireThat(isSHA(pin)&&hash(readBounded(file,4<<20))===pin,'Source manifest pin differs');result={source_files:assertSources(root,jsonFile(file,4<<20))};}else if(action==='browser-source'&&args.length===1)result=browserSnapshot(args[0]);else if(action==='cleanup'&&args.length===4)result={owned:ownedResource(...args)};else if(action==='image'&&args.length===2)result={valid:validateImage(stdinJSON(),...args)};else if(action==='database'&&args.length===0)result={valid:validateDatabase(stdinJSON())};else if(action==='record-native'&&args.length===2)result=await recordNative(...args);else throw Error('Closed Fuel guard command/arguments required');console.log(JSON.stringify(result));})().catch(error=>{console.error(String(error.message).split(/\r?\n/)[0]);process.exitCode=1;});}
