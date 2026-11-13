'use strict';
// Development-only source inventory. This never translates runtime data.
const fs = require('node:fs');
const path = require('node:path');
const {babelParse, traverse} = require('../node_modules/playwright/lib/transform/babelBundle.js');
const root = path.resolve(__dirname, '..');
const excludedFiles = new Set(['app.js', 'theme.js', 'i18n.js', 'i18n-catalog.js', 'security.js', 'audit.js']);
const excludedTemplates = new Set(['page.html', 'settings.html', 'audit.html', 'security.html', 'users.html', 'reauth.html']);
const exclusions = require('./localization-exclusions.json');
const words = /[A-Za-zÄÖÜäöüß]/;
const machine = /^(?:https?:|\/[a-zA-Z]|#|\.[a-zA-Z_-]|\[|\^|data-|aria-|--|household-|application\/|text\/|image\/|[a-z]+[-_][a-z0-9_.-]+$)/;
const lowerCopy = new Set(['unbekannt','bekannt','ohne','mit','und','oder','bis','heute','morgen','gestern','aktiv','pausiert','offen','erledigt','abgelaufen','nicht','keine','kein','nein','ja']);
const exactMachine = new Set(['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS','ACTIVATE','PUSH_DEVICE_RESET','Content-Type','X-CSRF-Token','Idempotency-Key','Accept','Range','AbortError','TypeError','NotFoundError','InvalidStateError','QuotaExceededError','ConstraintError','SecurityError','VersionError','UnknownError','TransactionInactiveError','DataError','ReadOnlyError','InvalidAccessError','NotAllowedError','NotSupportedError','DataCloneError','NotReadableError','OverconstrainedError','SyntaxError','TIMEOUT','ASC','DESC','UTC','EUR','HTML','JSON','CSV','PNG','JPEG','WebP','PDF','UTF-8','NFC','SHA-256','SHA-384','SHA-512','AES-GCM','ECDSA','P-256','ArrayBuffer','de-AT','de-DE','en-CA','en-GB','Europe/Vienna','use strict']);
function human(value) {
  if (Object.hasOwn(exclusions,value)) return false;
  if (lowerCopy.has(value)) return true;
  if (!words.test(value) || exactMachine.has(value) || machine.test(value.trim()) || /^[a-z0-9_.:-]+$/.test(value)) return false;
  if (/^\s*(?:checked|completed|selected|disabled|hidden|readonly|active|current|error|notice|muted|fine)\s*$/.test(value)) return false;
  if (/^[A-Z][A-Z0-9_-]+$/.test(value) && !/[ÄÖÜ]/.test(value)) return false;
  if (/[{}<>\\]|\$\{|\[data-/.test(value)) return false;
  return /[ÄÖÜäöüß]|\s|^[A-Z][a-z]|^[A-Z]{2,}[- ][A-ZÄÖÜa-zäöüß]/.test(value);
}
function jsInventory(file) {
  const source = fs.readFileSync(file, 'utf8');
  const rows = [];
  traverse(babelParse(source, file), {
    StringLiteral(p) {
      const value = p.node.value;
      if (p.findParent(parent => parent.node.type === 'CallExpression' && (parent.node.callee.name?.startsWith('_householdText_') || ['t','tr'].includes(parent.node.callee.name) || parent.node.callee.property?.name === 't') && parent.node.arguments[0]?.start <= p.node.start && parent.node.arguments[0]?.end >= p.node.end)) return;
      if ((p.parent.type === 'ObjectProperty' || p.parent.type === 'ObjectMethod') && p.key === 'key' && !p.parent.computed) return;
      if (!human(value)) return;
      rows.push({source:value,start:p.node.start,end:p.node.end,line:p.node.loc.start.line,kind:'literal'});
    },
    TemplateLiteral(p) {
      if (p.parent.type === 'TaggedTemplateExpression') return;
      if (!p.node.quasis.some(q => human(q.value.cooked || ''))) return;
      rows.push({source:p.node.quasis.map(q=>q.value.cooked).join('%s'),start:p.node.start,end:p.node.end,line:p.node.loc.start.line,kind:'template',expressions:p.node.expressions.map(e=>({source:source.slice(e.start,e.end),start:e.start,end:e.end}))});
    }
  });
  return rows;
}
function templateInventory(file) {
  const source = fs.readFileSync(file, 'utf8');
  const rows = [];
  const tokens = /{{[\s\S]*?}}|<!--[\s\S]*?-->|<[^>]*>|[^<{]+|[<{]/g;
  for (const match of source.matchAll(tokens)) {
    const token = match[0], start = match.index;
    if (token.startsWith('{{') || token.startsWith('<!--')) continue;
    if (token.startsWith('<')) {
      // Mask template actions while keeping offsets. Mixed attributes can contain
      // visible copy around dynamic values or conditional branches.
      const masked = token.replace(/{{[\s\S]*?}}/g, value => ' '.repeat(value.length));
      for (const attribute of masked.matchAll(/\b(aria-label|placeholder|title|alt)="([^"]*)"/g)) {
        if (!words.test(attribute[2]) || Object.hasOwn(exclusions,attribute[2])) continue;
        const offset = attribute.index + attribute[0].indexOf('"') + 1;
        rows.push({source:token.slice(offset,offset+attribute[2].length),start:start+offset,end:start+offset+attribute[2].length,kind:'attribute'});
      }
    } else {
      const trimmed = token.trim();
      if (!words.test(trimmed) || Object.hasOwn(exclusions,trimmed)) continue;
      const offset = token.indexOf(trimmed);
      rows.push({source:trimmed,start:start+offset,end:start+offset+trimmed.length,kind:'text'});
    }
  }
  return rows;
}
function inventory() {
  const result = {};
  for (const name of fs.readdirSync(path.join(root, 'internal/web/templates')).sort()) {
    if (!name.endsWith('.html') || excludedTemplates.has(name) || /security|reauth|users/.test(name)) continue;
    const file = 'internal/web/templates/' + name;
    result[file] = templateInventory(path.join(root,file));
  }
  for (const name of fs.readdirSync(path.join(root, 'internal/web/static')).sort()) {
    if (!name.endsWith('.js') || excludedFiles.has(name)) continue;
    const file = 'internal/web/static/' + name;
    result[file] = jsInventory(path.join(root,file));
  }
  return result;
}
if (require.main === module) {
  const out = path.join(root, 'outputs/i18n'); fs.mkdirSync(out, {recursive:true});
  const result = inventory();
  fs.writeFileSync(path.join(out,'module-inventory.json'), JSON.stringify(result,null,2)+'\n');
  const unique = [...new Set(Object.values(result).flat().map(r=>r.source))].sort();
  fs.writeFileSync(path.join(out,'module-sources.json'), JSON.stringify(unique,null,2)+'\n');
  const groups = {};
  for(const [file,rows] of Object.entries(result)) {
    const name = path.basename(file), group = /finance|expense/.test(name)?'finance':/fuel/.test(name)?'fuel':/chores|meters|documents/.test(name)?'extensions':/recipe|cooking|ingredient/.test(name)?'recipes':/planning|reservations/.test(name)?'planning':'shopping-pantry';
    groups[group] ||= new Set(); rows.forEach(row=>groups[group].add(row.source));
  }
  for(const [group,values] of Object.entries(groups))fs.writeFileSync(path.join(out,group+'-sources.json'),JSON.stringify([...values].sort(),null,2)+'\n');
  console.log(JSON.stringify({files:Object.keys(result).length,unique:unique.length,groups:Object.fromEntries(Object.entries(groups).map(([group,values])=>[group,values.size]))}));
}
module.exports = {human, inventory, jsInventory, templateInventory};
