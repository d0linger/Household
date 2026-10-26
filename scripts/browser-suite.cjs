'use strict';
const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const root=fs.realpathSync(path.resolve(__dirname,'..'));
fs.mkdirSync(path.join(root,'.local'),{recursive:true});const privateRoot=fs.realpathSync(path.join(root,'.local'));
if(!privateRoot.startsWith(root+path.sep))throw Error('Private browser directory leaves the workspace');
const components=['browser-check','browser-a11y-check','browser-audit-check','browser-expansion-check','browser-shopping-catalog-check','browser-shopping-reuse-check','browser-shopping-merge-check','browser-pantry-check','browser-shopping-checkout-check','browser-recipe-workspace-check','browser-cooking-check','browser-planning-check','browser-pantry-daily-check','browser-ingredient-pantry-check','browser-cooking-stock-check','browser-pantry-batches-check','browser-pantry-replenishment-check','browser-reservations-check','browser-planning-demand-check','browser-planning-purchases-check','browser-planning-participants-check'];
const privateDirectory=fs.mkdtempSync(path.join(privateRoot,'browser-auth-'));
try{
 for(const component of components){
  const env={...process.env};
  // The final three components share one real authenticated session so the full
  // suite stays inside the unchanged production per-account login budget.
  if(['browser-planning-demand-check','browser-planning-purchases-check','browser-planning-participants-check'].includes(component))env.HOUSEHOLD_BROWSER_HANDOFF=path.join(privateDirectory,'state.json');
  const result=spawnSync(process.execPath,[path.join(__dirname,component+'.cjs')],{cwd:root,env,stdio:'inherit',timeout:5*60*1000});
  if(result.error)throw result.error;
  if(result.status!==0){process.exitCode=result.status||1;break;}
 }
}finally{
 const target=fs.realpathSync(privateDirectory);
 if(!target.startsWith(privateRoot+path.sep))throw Error('Unsafe browser private cleanup target');
 fs.rmSync(target,{recursive:true,force:true});
}
