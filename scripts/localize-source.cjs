'use strict';
// One-time, explicit source migration. Never run on record data or generated
// output. Review its diff and catalog inventory before accepting new messages.
const fs = require('node:fs');
const path = require('node:path');
const {jsInventory, templateInventory} = require('./localization-inventory.cjs');
const root=path.resolve(__dirname,'..');
function decodeEntities(value){return value.replace(/&(?:amp|lt|gt|quot|apos|nbsp|#39|#x[0-9a-f]+|#\d+);/gi,entity=>({'&amp;':'&','&lt;':'<','&gt;':'>','&quot;':'"','&apos;':"'",'&#39;':"'",'&nbsp;':'\u00a0'}[entity]||String.fromCodePoint(parseInt(entity.slice(entity.startsWith('&#x')?3:2,-1),entity.startsWith('&#x')?16:10))));}
for(const input of process.argv.slice(2)){
 const file=path.resolve(root,input);
 if(!file.startsWith(path.join(root,'internal/web')+path.sep)||!['.js','.html'].includes(path.extname(file)))throw Error('Explicit web source path required');
 let source=fs.readFileSync(file,'utf8');
 const rows=file.endsWith('.html')?templateInventory(file):jsInventory(file);
 if(!rows.length){console.log(input+': no raw authored messages');continue;}
 if(file.endsWith('.html')){
  for(const r of [...rows].sort((a,b)=>b.start-a.start))source=source.slice(0,r.start)+'{{t '+JSON.stringify(decodeEntities(r.source))+'}}'+source.slice(r.end);
 }else{
  const helper='_householdText_'+path.basename(file).replace(/[^A-Za-z0-9]/g,'_');
  function transform(start,end){let part=source.slice(start,end);const matches=rows.filter(r=>r.start>=start&&r.end<=end).filter(r=>!rows.some(other=>other!==r&&other.start>=start&&other.end<=end&&other.start<=r.start&&other.end>=r.end));for(const r of matches.sort((a,b)=>b.start-a.start)){let expression='('+helper+'('+(r.kind==='literal'?source.slice(r.start,r.end):JSON.stringify(r.source));if(r.kind==='template')expression+=r.expressions.map(e=>', '+transform(e.start,e.end)).join('');expression+='))';part=part.slice(0,r.start-start)+expression+part.slice(r.end-start);}return part;}
  const hasHelper=source.includes('const '+helper+' =');
  source=transform(0,source.length);
  if(!hasHelper){
   const helperCode='const '+helper+' = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === \'%%\' ? \'%\' : String(args[index++] ?? \'\')) : source; };\n';
   source=source.startsWith("'use strict';")?source.replace("'use strict';","'use strict';\n"+helperCode):helperCode+source;
  }
 }
 fs.writeFileSync(file,source);
 console.log(input+': '+rows.length+' authored message calls');
}
