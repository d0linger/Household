'use strict';
const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const {resolveBrowserPrivateRoot}=require('./browser-private-root.cjs');
const root=fs.realpathSync(path.resolve(__dirname,'..'));
const privateRoot=resolveBrowserPrivateRoot(root,process.env.HOUSEHOLD_BROWSER_PRIVATE_ROOT);
const components=['browser-check','browser-a11y-check','browser-audit-check','browser-expansion-check','browser-shopping-catalog-check','browser-shopping-reuse-check','browser-shopping-merge-check','browser-pantry-check','browser-shopping-checkout-check','browser-recipe-workspace-check','browser-cooking-check','browser-planning-check','browser-pantry-daily-check','browser-ingredient-pantry-check','browser-cooking-stock-check','browser-pantry-batches-check','browser-pantry-replenishment-check','browser-reservations-check','browser-planning-demand-check','browser-planning-purchases-check','browser-planning-participants-check','browser-planning-suggestions-check','browser-pantry-suggestions-check','browser-recipe-expansion-check','browser-expense-editor-check','browser-shopping-repurchase-check','browser-shopping-barcode-check','browser-pantry-waste-check','browser-fuel-overview-check'];
const privateDirectory=fs.mkdtempSync(path.join(privateRoot,'browser-auth-'));
let activePassword=process.env.HOUSEHOLD_BROWSER_PASSWORD;
try{
 for(const component of components){
  const env={...process.env,HOUSEHOLD_BROWSER_URL:process.env.HOUSEHOLD_BROWSER_URL||process.env.HOUSEHOLD_ORIGIN||'http://localhost:8101'};
  if(activePassword!==undefined)env.HOUSEHOLD_BROWSER_PASSWORD=activePassword;
  // The final eleven components share one real authenticated session so the full
  // suite stays inside the unchanged production per-account login budget.
  if(['browser-planning-demand-check','browser-planning-purchases-check','browser-planning-participants-check','browser-planning-suggestions-check','browser-pantry-suggestions-check','browser-recipe-expansion-check','browser-expense-editor-check','browser-shopping-repurchase-check','browser-shopping-barcode-check','browser-pantry-waste-check','browser-fuel-overview-check'].includes(component))env.HOUSEHOLD_BROWSER_HANDOFF=path.join(privateDirectory,'state.json');
  const result=spawnSync(process.execPath,[path.join(__dirname,component+'.cjs')],{cwd:root,env,stdio:'inherit',timeout:5*60*1000});
  if(result.error)throw result.error;
  if(result.status!==0){process.exitCode=result.status||1;break;}
  if(component==='browser-check'){
   const report=JSON.parse(fs.readFileSync(path.join(root,'outputs/runtime-review/browser-report.json'),'utf8'));
   if(report.checks.includes('Bootstrap forces password change and fresh login'))activePassword='Browser review only 1234';
  }
 }
}finally{
 const target=fs.realpathSync(privateDirectory);
 if(!target.startsWith(privateRoot+path.sep))throw Error('Unsafe browser private cleanup target');
 fs.rmSync(target,{recursive:true,force:true});
}
