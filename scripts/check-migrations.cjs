const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const {execFileSync} = require('node:child_process');
const expected = JSON.parse(fs.readFileSync('deploy/migration-checksums.json', 'utf8'));
const base = 'internal/database/migrations';
const files = fs.readdirSync(base).flatMap(schema => fs.readdirSync(path.join(base, schema)).filter(name => name.endsWith('.sql')).map(name => schema + '/' + name));
if (files.length !== Object.keys(expected).length) throw Error('Migration manifest count differs');
for (const name of files) {
  const actual = crypto.createHash('sha256').update(fs.readFileSync(path.join(base, name))).digest('hex');
  if (expected[name] !== actual) throw Error('Migration checksum mismatch: ' + name);
}
if (process.env.MIGRATION_BASE) {
  const changed = execFileSync('git', ['diff', '--name-only', '--diff-filter=MD', process.env.MIGRATION_BASE, 'HEAD', '--', base], {encoding:'utf8'}).trim();
  if (changed) throw Error('Existing migrations must not be modified or deleted:\n' + changed);
}
console.log('Verified ' + files.length + ' migration checksums' + (process.env.MIGRATION_BASE ? ' and immutable history.' : '.'));
