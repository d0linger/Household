'use strict';
const {execFileSync} = require('node:child_process');

const privateDirectories = new Set([
  '.local', '.cache', '.hallmark', 'node_modules', 'outputs', 'graphify-out',
  'tmp', '.tmp', 'temp', 'logs', 'backups', 'uploads', 'screenshots', 'captures',
  'recordings', 'playwright-report', 'test-results', 'blob-report'
]);
const rootDirectories = new Set(['bin', 'out', 'dist', 'build', 'data', 'pgdata', 'postgres-data', 'exports', 'mockups', 'mockup-data']);

function classifyPath(name) {
  if (typeof name !== 'string' || !name || /[\x00-\x1f\x7f\\:]/.test(name)
      || name.startsWith('/') || name.split('/').some(part => !part || part === '.' || part === '..')) {
    return 'Unsafe repository path';
  }
  const lower = name.toLowerCase(), parts = lower.split('/'), base = parts.at(-1);
  if (parts.slice(0, -1).some(part => privateDirectories.has(part))
      || parts.length > 1 && rootDirectories.has(parts[0])
      || /^\.impeccable\/(?:critique|review)\//.test(lower)
      || /^\.claude\/worktrees\//.test(lower)) return 'Local state or generated evidence';
  if ((base === '.env' || base.startsWith('.env.'))
      && !['.env.example', '.env.production.example'].includes(name)) return 'Private environment file';
  if (/\.(?:pem|key|p12|pfx|jks|exe|dll|so|dylib|test|dump|backup|sqlite|sqlite3|db|db-wal|db-shm|har|mp4|webm|log|prof|cover|tmp|swp|swo|bak)$/i.test(base)
      || base.endsWith('~')) return 'Private material or generated binary/report';
  if (['go.work', 'go.work.sum', 'household'].includes(lower)
      || /^\.impeccable\/(?:config\.local\.json|[^/]+\.html)$/.test(lower)
      || lower === '.claude/settings.local.json') return 'Machine-local configuration or build output';
  return null;
}

function inspectIndex(cwd = process.cwd()) {
  const inventory = execFileSync('git', ['ls-files', '--stage', '-z'], {
    cwd, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024, timeout: 15000, windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe']
  });
  if (!inventory || !inventory.endsWith('\0')) throw Error('Missing or incomplete Git index inventory');
  const records = inventory.slice(0, -1).split('\0'), violations = [];
  for (const record of records) {
    const match = /^(\d{6}) [a-f0-9]{40,64} ([0-3])\t([\s\S]+)$/.exec(record);
    if (!match) throw Error('Malformed Git index entry');
    const [, mode, stage, name] = match;
    const reason = stage !== '0' ? 'Unresolved merge entry'
      : !['100644', '100755'].includes(mode) ? 'Only regular tracked files are permitted'
      : classifyPath(name);
    if (reason) violations.push({path: name, reason});
  }
  return {files: records.length, violations};
}

module.exports = {classifyPath, inspectIndex};
if (require.main === module) {
  try {
    if (process.argv.slice(2).some(argument => argument !== '--index') || process.argv.length > 3) {
      throw Error('Usage: node scripts/check-local-artifacts.cjs [--index]');
    }
    const result = inspectIndex();
    if (result.violations.length) {
      for (const violation of result.violations) console.error(`${violation.reason}: ${JSON.stringify(violation.path)}`);
      process.exitCode = 1;
    } else console.log(`Checked ${result.files} indexed paths: no local/private artifacts`);
  } catch (failure) {
    // Git errors can contain local configuration; do not echo command stderr.
    console.error(failure.code || failure.signal ? 'Could not inspect the Git index' : failure.message);
    process.exitCode = 1;
  }
}
