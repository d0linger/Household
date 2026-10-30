'use strict';
// A separate controlled-HTTP campaign; production builds have no test setter.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const {spawnSync}=require('node:child_process');
const G=require('./fuel-browser-contracts.cjs');
function controlledMains(text){
  if(!/^\/\/go:build integration && testtools$/m.test(text))throw Error('Controlled push source needs both explicit build tags');
  const names=[...text.matchAll(/^func (TestShoppingPushControlled[A-Za-z0-9_]+)\(/gm)].map(value=>value[1]).sort();
  if(!names.length||new Set(names).size!==names.length)throw Error('Controlled push campaign must define distinct actual mains');
  return names;
}
function terminalProof(events,required,moduleName='household'){
  if(!required.length)throw Error('Required controlled push mains are empty');
  const running=new Set(),done=new Set(),packages=new Set(),ended=new Set();
  let mains=0;
  for(const event of events){
    if(!event||typeof event.Action!=='string'||typeof event.Package!=='string'||!event.Package)throw Error('Malformed controlled push event');
    if(['fail','build-fail','skip'].includes(event.Action))throw Error('Controlled push test failed or skipped');
    if(!event.Test){
      if(event.Action==='start'){if(packages.has(event.Package))throw Error('Package repeated');packages.add(event.Package);}
      if(event.Action==='pass'){if(!packages.has(event.Package)||ended.has(event.Package))throw Error('Invalid package terminal');for(const id of running)if(id.startsWith(event.Package+'/')&&!done.has(id))throw Error('Package ended before its cases');ended.add(event.Package);}
      continue;
    }
    if(typeof event.Test!=='string'||!packages.has(event.Package)||ended.has(event.Package))throw Error('Case has no running package');
    const id=event.Package+'/'+event.Test;
    if(event.Action==='run'){
      if(running.has(id)||done.has(id))throw Error('Case repeated');
      if(event.Test.includes('/')){const parent=event.Package+'/'+event.Test.split('/')[0];if(!running.has(parent)||done.has(parent))throw Error('Subcase has no running main');}
      running.add(id);
    }
    if(event.Action==='pass'){
      if(!running.has(id)||done.has(id))throw Error('Case terminal is invalid');
      if(!event.Test.includes('/')){for(const child of running)if(child.startsWith(id+'/')&&!done.has(child))throw Error('Subcase unfinished');mains++;}
      done.add(id);
    }
  }
  if(!mains||running.size!==done.size||!packages.size||packages.size!==ended.size)throw Error('Controlled push campaign is incomplete');
  for(const name of required)if(!done.has(moduleName+'/internal/web/'+name))throw Error('Required controlled push main did not pass');
  return {main_pass:mains,main_fail:0,main_skip:0,cases:done.size};
}
function authority(env){
  if(env.HOUSEHOLD_REQUIRE_INTEGRATION!=='1')throw Error('Explicit isolated integration authority is required');
  const ports=env.GITHUB_ACTIONS==='true'?['5432','55434']:['55434'];
  let port;
  for(const [key,role] of [['HOUSEHOLD_TEST_OWNER_URL','household_owner'],['HOUSEHOLD_TEST_APP_URL','household_app']]){
    let value;try{value=new URL(env[key]);}catch{throw Error('Isolated database URI is missing');}
    const query=[...value.searchParams];
    if(!['postgres:','postgresql:'].includes(value.protocol)||!['localhost','127.0.0.1','[::1]'].includes(value.hostname)||!ports.includes(value.port)||value.pathname!=='/household_test'||value.username!==role||!value.password||value.hash||port&&port!==value.port||query.length!==1||query[0][0]!=='sslmode'||query[0][1]!=='disable')throw Error('Controlled push authority leaves the isolated test database');
    port=value.port;
  }
}
function redact(text,env){
  for(const name of ['HOUSEHOLD_TEST_OWNER_URL','HOUSEHOLD_TEST_APP_URL'])if(env[name])text=text.split(env[name]).join('[private database URI]');
  return text.replace(/postgres(?:ql)?:\/\/[^\s"']+/g,'[private database URI]');
}
function run(){
  const root=fs.realpathSync(path.resolve(__dirname,'..'));
  authority(process.env);
  const testFile=path.join(root,'internal/web/shopping_push_controlled_integration_test.go');
  const names=controlledMains(G.readBounded(testFile,1<<20).toString('utf8'));
  const manifest=G.sourceSnapshot(root);
  const dir=G.guardPath(path.join(root,'outputs'),path.join(root,'outputs/qa/shopping-push-testtools',crypto.randomUUID()));
  fs.mkdirSync(dir,{recursive:true});
  fs.writeFileSync(path.join(dir,'source-manifest.json'),JSON.stringify(manifest,null,2)+'\n',{flag:'wx'});
  const pattern='^(?:'+names.join('|')+')$';
  const result=spawnSync('go',['test','-race','-tags=integration,testtools','-p','1','-json','./internal/web','-run',pattern,'-count=1','-timeout=15m'],{cwd:root,env:process.env,encoding:'utf8',maxBuffer:64<<20,timeout:16*60*1000});
  const stdout=redact(result.stdout||'',process.env),stderr=redact(result.stderr||'',process.env);
  fs.writeFileSync(path.join(dir,'events.jsonl'),stdout,{flag:'wx'});
  fs.writeFileSync(path.join(dir,'stderr.txt'),stderr,{flag:'wx'});
  if(result.error||result.status!==0)throw Error('Controlled push execution failed; inspect redacted campaign evidence');
  const proof=terminalProof(G.parseEvents(stdout),names);
  G.assertSources(root,manifest);
  fs.writeFileSync(path.join(dir,'summary.json'),JSON.stringify({...proof,source_unchanged:true,required:names,physical_vendor_delivery:false},null,2)+'\n',{flag:'wx'});
  process.stdout.write(JSON.stringify({campaign:path.relative(root,dir).replaceAll('\\','/'),...proof,source_unchanged:true,physical_vendor_delivery:false})+'\n');
}
module.exports={controlledMains,terminalProof,authority};
if(require.main===module){try{run();}catch(error){process.stderr.write(String(error.message)+'\n');process.exitCode=1;}}
