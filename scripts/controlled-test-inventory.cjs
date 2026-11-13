'use strict';
// Source census supplements actual Go registration; neither can replace the other.
const fs=require('node:fs'),path=require('node:path'),{createHash}=require('node:crypto');
const REQUIRED_FILES=Object.freeze(['shopping_push_controlled_integration_test.go','notification_delivery_integration_test.go','notification_delivery_edges_integration_test.go','notification_chores_delivery_integration_test.go','notification_cooking_delivery_integration_test.go','notification_shopping_delivery_integration_test.go','notification_pantry_delivery_integration_test.go','notification_fuel_delivery_integration_test.go','notification_finance_delivery_integration_test.go','notification_meters_delivery_integration_test.go','notification_documents_delivery_integration_test.go','notification_mealplan_delivery_integration_test.go','notification_security_delivery_integration_test.go'].map(name=>'internal/web/'+name).sort());
const REQUIRED_SECURITY=Object.freeze(['TestNotificationSecurityControlledDeliveryAndFinalGuards','TestNotificationSecurityDeviceHooksAndNoHistoricalReplay','TestNotificationSecurityMissingRetainedKeyPreservesDeviceAndDelivery']);
const hash=raw=>createHash('sha256').update(raw).digest('hex');
function controlledMains(text){
 if(!/^\/\/go:build integration && testtools\r?$/m.test(text))throw Error('Controlled source needs both explicit build tags');
 const names=[...text.matchAll(/^func (Test[\p{L}\p{N}_]+)\(\s*\w+\s+\*testing\.T\s*\)/gmu)].map(match=>match[1]).sort();
 if(!names.length||new Set(names).size!==names.length)throw Error('Controlled source requires distinct actual mains');
 return names;
}
function controlledSourceInventory(root,source,readFile=fs.readFileSync){
 const files=[];
 for(const entry of source.manifest){
  if(!entry.path.startsWith('internal/')||!entry.path.endsWith('_test.go'))continue;
  const raw=readFile(path.join(root,entry.path)),text=raw.toString('utf8');
  if(!/^\/\/go:build[^\r\n]*\btesttools\b/m.test(text))continue;
  if(hash(raw)!==entry.sha256)throw Error('Controlled source changed during inventory');
  files.push({path:entry.path,sha256:entry.sha256,package:source.module+'/'+path.posix.dirname(entry.path),tests:controlledMains(text)});
 }
 return files.sort((a,b)=>a.path.localeCompare(b.path,'en'));
}
function validateControlledInventory(files,source,registered){
 if(!Array.isArray(files)||files.length<REQUIRED_FILES.length||files.length>10000)throw Error('Controlled source inventory missing');
 const seenFiles=new Set(),seenTests=new Set(),manifest=new Map(source.manifest.map(entry=>[entry.path,entry.sha256]));
 const actual=new Set(registered.map(test=>test.package+'/'+test.name));
 for(const file of files){
  if(!file||typeof file.path!=='string'||!/^internal\/[\w/-]+_test\.go$/.test(file.path)||seenFiles.has(file.path)||manifest.get(file.path)!==file.sha256||file.package!==source.module+'/'+path.posix.dirname(file.path)||!Array.isArray(file.tests)||!file.tests.length)throw Error('Invalid controlled source identity');
  seenFiles.add(file.path);
  for(const name of file.tests){
   const identity=file.package+'/'+name;
   if(!/^Test[\p{L}\p{N}_]+$/u.test(name)||seenTests.has(identity)||!actual.has(identity))throw Error('Controlled main missing, duplicated or absent from actual registration');
   seenTests.add(identity);
  }
 }
 if(REQUIRED_FILES.some(file=>!seenFiles.has(file))||REQUIRED_SECURITY.some(name=>!seenTests.has(source.module+'/internal/web/'+name)))throw Error('Reviewed controlled coverage was omitted');
 return seenTests.size;
}
module.exports={REQUIRED_FILES,REQUIRED_SECURITY,controlledMains,controlledSourceInventory,validateControlledInventory};
