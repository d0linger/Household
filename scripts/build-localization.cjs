'use strict';
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const folder = path.join(root, 'internal/i18n');
const result = Object.create(null);
for (const name of fs.readdirSync(folder).filter(name=>name.endsWith('.json')).sort()) {
  const catalog = JSON.parse(fs.readFileSync(path.join(folder,name),'utf8'));
  for (const [source, translation] of Object.entries(catalog)) {
    if (!source.trim() || typeof translation !== 'string' || !translation.trim()) throw Error('Empty translation: '+name);
    if (Object.hasOwn(result,source) && result[source] !== translation) throw Error('Conflicting translation: '+source+' in '+name);
    result[source] = translation;
  }
}
const sorted = Object.fromEntries(Object.entries(result).sort(([a],[b])=>a.localeCompare(b,'en')));
const output = "/* Generated from internal/i18n/*.json by scripts/build-localization.cjs. */\n'use strict';\nglobalThis.HouseholdEnglish = Object.freeze("+JSON.stringify(sorted,null,2)+');\n';
const target = path.join(root, 'internal/web/static/i18n-catalog.js');
if (process.argv.includes('--check')) {
  if (!fs.existsSync(target) || fs.readFileSync(target,'utf8') !== output) throw Error('Generated translation catalog is stale; run node scripts/build-localization.cjs');
} else fs.writeFileSync(target, output);
console.log('Validated '+Object.keys(result).length+' application translations.');
