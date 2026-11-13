'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {execFileSync} = require('node:child_process');
const {babelParse, traverse} = require('../node_modules/playwright/lib/transform/babelBundle.js');
const {human, jsInventory, templateInventory} = require('./localization-inventory.cjs');
const root = path.resolve(__dirname, '..');
function catalog() {
  const messages = Object.create(null);
  for (const name of fs.readdirSync(path.join(root,'internal/i18n')).filter(n=>n.endsWith('.json')).sort()) {
    const values = JSON.parse(fs.readFileSync(path.join(root,'internal/i18n',name),'utf8'));
    for(const [source,translation] of Object.entries(values)) {
      if(typeof translation!=='string'||!translation.trim()) throw Error('Empty translation in '+name+': '+source);
      if(Object.hasOwn(messages,source)&&messages[source]!==translation) throw Error('Conflicting translation in '+name+': '+source);
      const parameters = text => [...text.matchAll(/%%|%[sdvqw]/g)].map(m=>m[0]).filter(token=>token!=='%%').join(',');
      if(parameters(source)!==parameters(translation)) throw Error('Translation changes parameters in '+name+': '+source);
      messages[source]=translation;
    }
  }
  return messages;
}
function sources() {
  const result = {};
  for(const name of fs.readdirSync(path.join(root,'internal/web/templates')).filter(n=>n.endsWith('.html')).sort()) {
    const file='internal/web/templates/'+name, source=fs.readFileSync(path.join(root,file),'utf8');
    result[file]=[...source.matchAll(/{{\s*t\s+("(?:[^"\\]|\\.)*")/g)].map(m=>JSON.parse(m[1]));
  }
  for(const name of fs.readdirSync(path.join(root,'internal/web/static')).filter(n=>n.endsWith('.js')&&!['i18n.js','i18n-catalog.js'].includes(n)).sort()) {
    const file='internal/web/static/'+name, source=fs.readFileSync(path.join(root,file),'utf8'), rows=[];
    traverse(babelParse(source,file),{StringLiteral(p){const parent=p.findParent(parent=>parent.node.type==='CallExpression'&&(parent.node.callee.name?.startsWith('_householdText_')||['t','tr'].includes(parent.node.callee.name)||parent.node.callee.property?.name==='t')&&parent.node.arguments[0]?.start<=p.node.start&&parent.node.arguments[0]?.end>=p.node.end);if(parent&&(parent.node.arguments[0]===p.node||human(p.node.value)))rows.push(p.node.value);}});
    result[file]=rows;
  }
  return result;
}
function audit() {
  const messages=catalog(), used=sources(), missing=new Map();
  for(const[file,values]of Object.entries(used))for(const source of values)if(!Object.hasOwn(messages,source)){if(!missing.has(source))missing.set(source,[]);missing.get(source).push(file);}
  const raw=[];
  for(const file of Object.keys(used)) {
    if(file.endsWith('/theme.js'))continue; // controller exceptions are programming errors, not interface copy
    const rows=file.endsWith('.html')?templateInventory(path.join(root,file)):jsInventory(path.join(root,file));
    for(const row of rows)raw.push({file,source:row.source});
  }
  return {messages,used,missing,raw};
}
function goAudit() {
  // The Go AST identifies descriptor labels and explicit message boundaries,
  // including both keyed and unkeyed Invalid values and formatted messages.
  const rows=JSON.parse(execFileSync('go',['run','./scripts/localization-go-inventory.go'],{cwd:root,encoding:'utf8',timeout:120000,maxBuffer:16*1024*1024,env:{...process.env,GOMAXPROCS:'2'}}));
  const messages=catalog(), exclusions=require('./localization-go-exclusions.json');
  for(const[key,reason]of Object.entries(exclusions))if(typeof reason!=='string'||!reason.trim())throw Error('Undocumented Go source exclusion: '+key);
  return rows.filter(row=>!Object.hasOwn(messages,row.source)&&!Object.hasOwn(exclusions,[row.kind,row.file,row.source].join('\n')));
}
function technicalTranslations() {
  const found=[];
  for(const name of fs.readdirSync(path.join(root,'internal/web/static')).filter(n=>n.endsWith('.js')&&!n.startsWith('i18n'))) {
    const file='internal/web/static/'+name;
    traverse(babelParse(fs.readFileSync(path.join(root,file),'utf8'),file),{CallExpression(p){
      if(!p.node.callee.name?.startsWith('_householdText_')||p.node.arguments[0]?.type!=='StringLiteral')return;
      const parent=p.parent;
      const technical=parent.type==='BinaryExpression'&&['in','===','!==','==','!='].includes(parent.operator)
        ||parent.type==='AssignmentExpression'&&['className','id','name','type','method','href','src'].includes(parent.left.property?.name)
        ||parent.type==='CallExpression'&&['querySelector','querySelectorAll','getElementById'].includes(parent.callee.property?.name)
        ||parent.type==='CallExpression'&&parent.callee.object?.property?.name==='classList'
        ||parent.type==='CallExpression'&&parent.callee.property?.name==='setAttribute'&&['class','id','name','type','method','aria-labelledby','aria-describedby'].includes(parent.arguments[0]?.value)
        ||parent.type==='MemberExpression'&&parent.computed;
      if(technical)found.push({file,line:p.node.loc.start.line,source:p.node.arguments[0].value});
    }});
  }
  return found;
}
if(require.main===module){
 const result=audit(), unique=new Set(Object.values(result.used).flat());
 if(process.argv.includes('--inventory')){
  const out=path.join(root,'outputs/i18n');fs.mkdirSync(out,{recursive:true});
  const groups={};
  for(const[file,values]of Object.entries(result.used)){
   const name=path.basename(file), group=/^(?:page|settings|app|theme|security|users|reauth|audit)/.test(name)?'core':/finance|expense/.test(name)?'finance':/fuel/.test(name)?'fuel':/chores|meters|documents/.test(name)?'extensions':/recipe|cooking|ingredient/.test(name)?'recipes':/planning|reservations/.test(name)?'planning':'shopping-pantry';
   groups[group]||=new Set();values.forEach(value=>groups[group].add(value));
  }
  for(const[group,values]of Object.entries(groups))fs.writeFileSync(path.join(out,group+'-sources.json'),JSON.stringify([...values].sort(),null,2)+'\n');
  fs.writeFileSync(path.join(out,'missing.json'),JSON.stringify([...result.missing.keys()].sort(),null,2)+'\n');
  console.log(JSON.stringify({messages:unique.size,translated:Object.keys(result.messages).length,missing:result.missing.size,groups:Object.fromEntries(Object.entries(groups).map(([key,v])=>[key,v.size]))}));
 }else{
  if(result.missing.size){console.error('Missing '+result.missing.size+' application translations:');for(const[source,files]of [...result.missing].slice(0,30))console.error(JSON.stringify(source)+' ('+files[0]+')');process.exitCode=1;}
  if(result.raw.length){console.error('Unwrapped source copy: '+JSON.stringify(result.raw.slice(0,30)));process.exitCode=1;}
  const technical=technicalTranslations();if(technical.length){console.error('Translated technical values: '+JSON.stringify(technical));process.exitCode=1;}
  if(process.argv.includes('--go')){const missing=goAudit();if(missing.length){console.error('Uncataloged Go application messages: '+JSON.stringify(missing));process.exitCode=1;}else console.log('Go descriptor, validation and HTTP message source coverage is complete.');}
  if(!result.missing.size&&!result.raw.length)console.log('All '+unique.size+' authored template/JavaScript messages have reviewed English copy; source candidate audit is clear.');
 }
}
module.exports={catalog,sources,audit,goAudit,technicalTranslations};
