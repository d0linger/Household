'use strict';
// CI and local review are separate explicit authorities; neither disables ownership checks.
const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const {resolveBrowserPrivateRoot}=require('./browser-private-root.cjs');
const SUITES=Object.freeze(['settings','fuel','finance','meters','documents','mealplan']);
const PG_IMAGE='postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5';
const origin='http://localhost:8101';
function need(value,message){if(!value)throw Error(message);}
function boundedJSON(file){const stat=fs.lstatSync(file);need(stat.isFile()&&!stat.isSymbolicLink()&&stat.size<=(1<<20),'Fixture JSON must be a bounded regular file');return JSON.parse(fs.readFileSync(file,'utf8'));}
function validateURLs(env){
 for(const [name,role] of [['HOUSEHOLD_TEST_OWNER_URL','household_owner'],['HOUSEHOLD_TEST_APP_URL','household_app']]){
  let url;try{url=new URL(env[name]);}catch{throw Error('Explicit isolated database authority required');}
  need(['postgres:','postgresql:'].includes(url.protocol)&&['localhost','127.0.0.1'].includes(url.hostname)&&url.port==='55434'&&url.pathname==='/household_test'&&url.username===role&&!!url.password&&!url.hash&&JSON.stringify([...url.searchParams])===JSON.stringify([['sslmode','disable']]),'CI database authority leaves the exact isolated test fixture');
 }
}
function validateCI(manifest,container,env){
 need(env.GITHUB_ACTIONS==='true'&&env.CI==='true'&&/^\d+$/.test(env.GITHUB_RUN_ID||'')&&/^\d+$/.test(env.GITHUB_RUN_ATTEMPT||'')&&SUITES.includes(env.HOUSEHOLD_NOTIFICATION_BROWSER_SUITE),'Explicit GitHub notification fixture authority required');
 const label=env.GITHUB_RUN_ID+'-'+env.GITHUB_RUN_ATTEMPT+'-'+env.HOUSEHOLD_NOTIFICATION_BROWSER_SUITE;
 need(manifest.schema===1&&manifest.mode==='github-actions'&&manifest.run_id===env.GITHUB_RUN_ID&&manifest.run_attempt===env.GITHUB_RUN_ATTEMPT&&manifest.suite===env.HOUSEHOLD_NOTIFICATION_BROWSER_SUITE&&manifest.origin===origin&&manifest.container_id===env.HOUSEHOLD_NOTIFICATION_BROWSER_PG_ID&&/^[a-f0-9]{64}$/.test(manifest.container_id||''),'CI fixture identity differs');
 need(container.Id===manifest.container_id&&container.State?.Running===true&&!container.State.OOMKilled&&container.Config?.Image===PG_IMAGE&&container.Config?.Labels?.['com.household.notification-browser-ci']===label,'CI service image, run label or actual container differs');
 need(container.HostConfig?.NanoCpus===2000000000&&container.HostConfig.Memory===(1<<30)&&container.HostConfig.MemorySwap===(1<<30)&&container.HostConfig.PidsLimit===128,'CI service resource bounds differ');
 const ports=container.HostConfig?.PortBindings?.['5432/tcp'];
 need(ports?.length===1&&ports[0].HostIp==='127.0.0.1'&&ports[0].HostPort==='55434','CI PostgreSQL must publish only the dedicated loopback port');
 validateURLs(env);
}
function validateLocal(resources,container,directory,pinned){
 need(/^[a-f0-9]{12,32}$/.test(resources.id||'')&&/^[a-f0-9]{64}$/.test(resources.container_id||'')&&resources.app_port===8101&&resources.port===55434&&resources.container_name==='household-ocean-app-review-'+resources.id&&path.resolve(resources.directory)===directory,'Owned local fixture manifest differs');
 if(pinned)need(resources.id==='66be6de20312'&&resources.container_id==='664e69fc20765f38cbb344bca5ac690c09e30a2052ab258e58362414aa27a993','Pinned local fixture identity differs');
 const ports=container.HostConfig?.PortBindings?.['5432/tcp'];
 need(container.Id===resources.container_id&&container.Name==='/'+resources.container_name&&container.State?.Running===true&&container.Config?.Labels?.['com.household.review-id']===resources.id&&ports?.length===1&&ports[0].HostIp==='127.0.0.1'&&ports[0].HostPort==='55434','Owned local container identity or loopback port changed');
}
function inspect(id){
 need(/^[a-f0-9]{64}$/.test(id||''),'Exact database container ID required');
 const result=spawnSync('docker',['inspect',id],{encoding:'utf8',timeout:15000,windowsHide:true,maxBuffer:1<<20});
 need(result.status===0&&!result.error,'Cannot inspect owned notification fixture');const rows=JSON.parse(result.stdout);need(rows.length===1,'Ambiguous database container');return rows[0];
}
function containedFile(local,file){
 const full=path.resolve(file),real=fs.realpathSync(full);
 need(real.startsWith(local+path.sep)&&real===full,'Fixture path escapes private root or uses an alias');
 for(let current=full;current!==local;current=path.dirname(current))need(!fs.lstatSync(current).isSymbolicLink(),'Fixture symlink refused');
 return real;
}
function fixtureState(workspace,secrets,{pinned=false,env=process.env}={}){
 const local=resolveBrowserPrivateRoot(workspace,env.HOUSEHOLD_BROWSER_PRIVATE_ROOT);
 need(env.HOUSEHOLD_BROWSER_HANDOFF,'Explicit private browser handoff required');
 const handoff=containedFile(local,env.HOUSEHOLD_BROWSER_HANDOFF);
 if(env.HOUSEHOLD_NOTIFICATION_BROWSER_CI_MANIFEST){
  const file=containedFile(local,env.HOUSEHOLD_NOTIFICATION_BROWSER_CI_MANIFEST),manifest=boundedJSON(file);
  need(path.dirname(file)===path.dirname(handoff)&&manifest.directory===path.dirname(file)&&manifest.handoff===handoff,'CI handoff does not belong to the declared private fixture');
  validateCI(manifest,inspect(manifest.container_id),env);
 }else{
  need(env.GITHUB_ACTIONS!=='true','CI cannot inherit local fixture authority');
  const directory=fs.realpathSync(fs.readFileSync(path.join(workspace,'.local/ocean-app-review-current.txt'),'utf8').trim());
  need(directory.startsWith(local+path.sep)&&path.dirname(handoff)===directory,'Handoff does not belong to the current owned local fixture');
  const resources=boundedJSON(containedFile(local,path.join(directory,'resources.json')));validateLocal(resources,inspect(resources.container_id),directory,pinned);
 }
 const state=boundedJSON(handoff);
 need(Array.isArray(state.cookies)&&state.cookies.length>0&&state.cookies.length<=32&&Array.isArray(state.origins),'Authenticated fixture state missing');
 for(const cookie of state.cookies){need(cookie&&cookie.domain==='localhost'&&typeof cookie.value==='string'&&cookie.value.length<=8192,'Cookie does not belong to the isolated origin');secrets.push(cookie.value);}
 need(state.origins.every(item=>item.origin===origin),'Browser state contains a foreign origin');return state;
}
module.exports={SUITES,PG_IMAGE,origin,need,boundedJSON,validateURLs,validateCI,validateLocal,inspect,containedFile,fixtureState};
