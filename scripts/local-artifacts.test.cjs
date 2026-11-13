'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {execFileSync, spawnSync} = require('node:child_process');
const {classifyPath, inspectIndex} = require('./check-local-artifacts.cjs');

test('publication guard preserves authored source, icons, examples and migrations', () => {
  for (const name of ['README.md', '.env.example', '.env.production.example', '.github/workflows/quality.yml',
    'internal/web/static/icon-192.png', 'internal/web/static/icon.svg', 'internal/backup/backup.go',
    'internal/database/migrations/core/001_init.sql', 'docs/backup.md',
    'design-preview/whole-page-concepts.fragment.html', 'design-preview/designVariantsConfig.ts']) {
    assert.equal(classifyPath(name), null, name);
  }
});

test('publication guard rejects private material and generated paths without hiding all images or SQL', () => {
  for (const name of ['.local/secrets/password', '.ENV', '.env.production', 'deploy/.env.example',
    'node_modules/pkg/index.js', 'outputs/qa/report.json', 'graphify-out/graph.json', '.hallmark/report.json',
    'tmp/session.json', 'temp/token', 'logs/server.txt', 'backups/archive.tar', 'uploads/receipt.png',
    'docs/screenshots/private.png', 'captures/page.png', 'recordings/session.json', 'private.pem', 'private.key',
    'private.p12', 'private.pfx', 'private.jks', 'household.exe', 'db.dump', 'snapshot.backup', 'state.sqlite',
    'state.db', '.impeccable/review/index.html', '.claude/worktrees/copy/README.md', 'go.work', 'dist/app.js']) {
    assert.equal(typeof classifyPath(name), 'string', name);
  }
});

test('publication guard refuses unsafe and log-injecting path names', () => {
  for (const name of ['', '../outside', '/absolute', 'C:/outside', 'a\\b', 'a//b', 'a/./b', 'file\nforged', 'file\tforged', null]) {
    assert.equal(classifyPath(name), 'Unsafe repository path');
  }
});

async function fixture(t) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'household-artifact-guard-'));
  t.after(async () => {
    if (path.dirname(directory) !== os.tmpdir() || !path.basename(directory).startsWith('household-artifact-guard-')) throw Error('Unsafe fixture cleanup');
    await fs.rm(directory, {recursive: true, force: true});
  });
  const git = (args, input) => execFileSync('git', args, {cwd: directory, input, encoding: 'utf8', timeout: 10000, maxBuffer: 1024 * 1024, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe']});
  git(['init', '--quiet']);
  return {directory, git};
}

test('real index inspection catches force-added ignored files and newline paths without reading contents', async t => {
  const {directory, git} = await fixture(t);
  await fs.writeFile(path.join(directory, '.gitignore'), '.local/\n');
  await fs.writeFile(path.join(directory, 'README.md'), 'Synthetic fixture\n');
  git(['add', '.gitignore', 'README.md']);
  assert.deepEqual(inspectIndex(directory), {files: 2, violations: []});
  await fs.mkdir(path.join(directory, '.local'));
  await fs.writeFile(path.join(directory, '.local', 'secret'), 'SYNTHETIC-NOT-FOR-LOGS');
  git(['add', '--force', '.local/secret']);
  const oid = git(['hash-object', '-w', '--stdin'], 'Synthetic fixture').trim();
  // Model a Linux-created index without checking this invalid Windows name out.
  git(['-c', 'core.protectNTFS=false', 'update-index', '-z', '--index-info'], `100644 ${oid}\toutputs/private\nforged.txt\0`);
  const result = inspectIndex(directory);
  assert.deepEqual(result.violations.map(row => row.path).sort(), ['.local/secret', 'outputs/private\nforged.txt']);
  const cli = spawnSync(process.execPath, [path.join(__dirname, 'check-local-artifacts.cjs'), '--index'], {
    cwd: directory, encoding: 'utf8', timeout: 10000, maxBuffer: 1024 * 1024, windowsHide: true
  });
  assert.equal(cli.status, 1);
  assert.match(cli.stderr, /private\\nforged/);
  assert.doesNotMatch(cli.stderr, /SYNTHETIC-NOT-FOR-LOGS/);
  assert.equal(cli.stderr.trim().split('\n').length, 2);
});

test('index gate fails closed on empty inventory and unresolved merge entries', async t => {
  const {directory, git} = await fixture(t);
  assert.throws(() => inspectIndex(directory), /Missing or incomplete/);
  const oid = git(['hash-object', '-w', '--stdin'], 'Synthetic conflict').trim();
  git(['update-index', '--index-info'], `100644 ${oid} 1\tREADME.md\n100644 ${oid} 2\tREADME.md\n`);
  assert.equal(inspectIndex(directory).violations.length, 2);
  assert.ok(inspectIndex(directory).violations.every(row => row.reason === 'Unresolved merge entry'));
});
