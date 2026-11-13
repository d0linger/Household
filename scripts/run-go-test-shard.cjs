'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {createHash} = require('node:crypto');
const {spawn, execFileSync} = require('node:child_process');
const {StringDecoder} = require('node:string_decoder');
const {controlledSourceInventory, validateControlledInventory} = require('./controlled-test-inventory.cjs');

const SHARDS = 16;
const MAX_LOG = 128 << 20;
const MAX_LINE = 2 << 20;
const NAME = /^(Test|Fuzz|Example)[\p{L}\p{N}_]*$/u;
const SETTINGS = Object.freeze({race: true, parallelPackages: 1, tags: 'integration,testtools', shuffle: 'on', count: 1, timeout: '60m', coverpkg: './internal/...', covermode: 'atomic'});
const sha = value => createHash('sha256').update(value).digest('hex');
const canonical = value => JSON.stringify(value);
const identity = test => test.package + '/' + test.name;
const shardFor = name => Number.parseInt(sha(name).slice(0, 8), 16) % SHARDS;

function jsonLines(text) {
  if (Buffer.byteLength(text) > MAX_LOG) throw Error('Go JSON evidence exceeds limit');
  return text.split(/\r?\n/).filter(Boolean).map(line => {
    if (Buffer.byteLength(line) > MAX_LINE) throw Error('Go JSON line exceeds limit');
    let value;
    try { value = JSON.parse(line); } catch { throw Error('Malformed Go JSON evidence'); }
    if (!value || typeof value !== 'object' || Array.isArray(value) || typeof value.Action !== 'string') throw Error('Invalid Go JSON event');
    return value;
  });
}

function discoverEvents(events) {
  const packages = new Map(), tests = new Map();
  for (const event of events) {
    if (!event.Package) continue;
    if (!/^[\w./~-]+$/.test(event.Package)) throw Error('Invalid discovered package');
    if (event.Test || event.Action === 'fail') throw Error('Discovery unexpectedly ran or failed a test');
    const state = packages.get(event.Package) || {terminal: false};
    if (event.Action === 'pass' || event.Action === 'skip') state.terminal = true;
    packages.set(event.Package, state);
    if (event.Action !== 'output') continue;
    for (const line of String(event.Output || '').split(/\r?\n/)) {
      if (!NAME.test(line)) continue;
      const test = {package: event.Package, name: line, shard: shardFor(line)};
      if (tests.has(identity(test))) throw Error('Duplicate discovered test identity');
      tests.set(identity(test), test);
    }
  }
  if (!packages.size || !tests.size || tests.size > 20000 || [...packages.values()].some(value => !value.terminal)) throw Error('Incomplete discovered package/test inventory');
  return {packages: [...packages.keys()].sort(), tests: [...tests.values()].sort((a, b) => identity(a).localeCompare(identity(b), 'en'))};
}

function sourceSnapshot(root, expectedCommit) {
  const files = [];
  const visit = relative => {
    const filename = path.join(root, relative), stat = fs.lstatSync(filename);
    if (stat.isSymbolicLink()) throw Error('Source fingerprint refuses symlinks');
    if (stat.isDirectory()) {
      for (const name of fs.readdirSync(filename).sort()) visit(relative + '/' + name);
    } else {
      if (!stat.isFile() || stat.size > 32 << 20 || files.length >= 10000) throw Error('Source fingerprint exceeds limits');
      files.push({path: relative, sha256: sha(fs.readFileSync(filename))});
    }
  };
  for (const relative of ['cmd', 'internal', 'go.mod', 'go.sum', 'scripts/run-go-test-shard.cjs', 'scripts/merge-go-test-shards.cjs', 'scripts/check-coverage.cjs', 'scripts/controlled-test-inventory.cjs', '.github/workflows/quality.yml']) visit(relative);
  files.sort((a, b) => a.path.localeCompare(b.path, 'en'));
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], {cwd: root, encoding: 'utf8'}).trim();
  if (!/^[a-f0-9]{40,64}$/.test(commit) || (expectedCommit && commit !== expectedCommit)) throw Error('Checkout commit differs from expected source');
  const dirty = Boolean(execFileSync('git', ['status', '--porcelain', '--untracked-files=all'], {cwd: root, encoding: 'utf8'}).trim());
  if (process.env.CI === 'true' && dirty) throw Error('CI source checkout is dirty');
  const moduleName = /^module (\S+)$/m.exec(fs.readFileSync(path.join(root, 'go.mod'), 'utf8'))?.[1];
  if (!moduleName) throw Error('Go module identity missing');
  return {commit, dirty, module: moduleName, manifest: files, digest: sha(canonical(files))};
}

function makeInventory(discovery, source, go, controlled) {
  const body = {schema: 1, algorithm: 'sha256-name-first32-mod16-v1', shards: SHARDS, settings: SETTINGS, source, go, controlled, packages: discovery.packages, tests: discovery.tests};
  const result = {...body, digest: sha(canonical(body))};
  validateInventory(result);
  return result;
}

function validateInventory(inventory) {
  if (!inventory || inventory.schema !== 1 || inventory.algorithm !== 'sha256-name-first32-mod16-v1' || inventory.shards !== SHARDS || canonical(inventory.settings) !== canonical(SETTINGS)) throw Error('Unsupported shard inventory/settings');
  const {digest, ...body} = inventory;
  if (digest !== sha(canonical(body))) throw Error('Inventory digest mismatch');
  const source = inventory.source;
  if (!source || !/^[a-f0-9]{40,64}$/.test(source.commit) || typeof source.dirty !== 'boolean' || !source.module || !Array.isArray(source.manifest) || !source.manifest.length || source.digest !== sha(canonical(source.manifest))) throw Error('Invalid source manifest');
  const paths = new Set();
  for (const entry of source.manifest) {
    if (typeof entry.path !== 'string' || !/^[\w./-]+$/.test(entry.path) || entry.path.split('/').includes('..') || entry.path.startsWith('/') || paths.has(entry.path) || !/^[a-f0-9]{64}$/.test(entry.sha256)) throw Error('Invalid/duplicate source file identity');
    paths.add(entry.path);
  }
  if (!inventory.go || !/^go\d+\.\d+(?:\.\d+)?$/.test(inventory.go.version) || !/^[a-z0-9]+$/.test(inventory.go.os) || !/^[a-z0-9]+$/.test(inventory.go.arch) || inventory.go.cgo !== '1') throw Error('Invalid race-capable Go target');
  if (!Array.isArray(inventory.packages) || !inventory.packages.length || new Set(inventory.packages).size !== inventory.packages.length || !Array.isArray(inventory.tests) || !inventory.tests.length || inventory.tests.length > 20000) throw Error('Invalid complete package/test inventory');
  if (inventory.packages.some(name => typeof name !== 'string' || !/^[\w./~-]+$/.test(name) || !(name === source.module || name.startsWith(source.module + '/')))) throw Error('Invalid package identity');
  const seen = new Set();
  for (const test of inventory.tests) {
    if (!test || !inventory.packages.includes(test.package) || !NAME.test(test.name) || test.shard !== shardFor(test.name) || seen.has(identity(test))) throw Error('Invalid test identity/assignment');
    seen.add(identity(test));
  }
  validateControlledInventory(inventory.controlled, source, inventory.tests);
  if (new Set(inventory.tests.map(test => test.shard)).size !== SHARDS) throw Error('Empty shard assignment cannot pass');
}

function exactPattern(inventory, shard) {
  const names = [...new Set(inventory.tests.filter(test => test.shard === shard).map(test => test.name))].sort();
  if (!names.length) throw Error('Empty shard cannot be accepted');
  return '^(' + names.map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') + ')$';
}

function discoveryEnvironment(env) {
  return {...env, HOUSEHOLD_REQUIRE_INTEGRATION: '0', HOUSEHOLD_TEST_OWNER_URL: '', HOUSEHOLD_TEST_APP_URL: '', HOUSEHOLD_DATABASE_URL: ''};
}

function discoveryArguments() {
  return ['test', '-race', '-list', '.', '-json', '-tags=integration,testtools', './...'];
}

function validateBuildParity(discovery, execution) {
  const selection = args => ({race: args.includes('-race'), tags: args.filter(value => value.startsWith('-tags=')), packages: args.filter(value => value === './...')});
  const listed = selection(discovery), executed = selection(execution);
  if (!listed.race || !executed.race || canonical(listed) !== canonical(executed) || canonical(listed.tags) !== canonical(['-tags=integration,testtools']) || listed.packages.length !== 1) throw Error('Discovery/execution race build selection differs');
}

function executionArguments(inventory, shard, profile) {
  return ['test', '-race', '-p', '1', '-tags=integration,testtools', '-shuffle=on', '-coverpkg=./internal/...', '-covermode=atomic', '-coverprofile=' + profile, './...', '-count=1', '-timeout=' + SETTINGS.timeout, '-json', '-run', exactPattern(inventory, shard)];
}

class ExecutionLedger {
  constructor(inventory, shard) {
    validateInventory(inventory);
    if (!Number.isInteger(shard) || shard < 0 || shard >= SHARDS) throw Error('Invalid shard index');
    this.inventory = inventory;
    this.expected = new Set(inventory.tests.filter(test => test.shard === shard).map(identity));
    this.assignedPackages = new Set(inventory.tests.filter(test => test.shard === shard).map(test => test.package));
    this.states = new Map();
    this.packages = new Set();
  }
  accept(event) {
    if (!event.Package) return;
    if (!this.inventory.packages.includes(event.Package)) throw Error('Unexpected Go package in execution');
    if (!event.Test) {
      if (event.Action === 'fail') throw Error('Go package failed');
      if (event.Action === 'pass' || event.Action === 'skip') {
        if (event.Action === 'skip' && this.assignedPackages.has(event.Package)) throw Error('Assigned package skipped');
        if (this.packages.has(event.Package)) throw Error('Duplicate Go package terminal event');
        this.packages.add(event.Package);
      }
      return;
    }
    const main = event.Test.split('/')[0], top = event.Package + '/' + main;
    if (!this.expected.has(top)) throw Error('Unexpected or unassigned test executed');
    const key = event.Package + '/' + event.Test;
    if (event.Action === 'fail' || event.Action === 'skip') throw Error('Test/subtest failed or skipped');
    if (event.Action === 'run') {
      if (this.states.has(key)) throw Error('Duplicate test run');
      if (event.Test.includes('/') && this.states.get(top) !== 'run') throw Error('Subtest ran without its parent');
      this.states.set(key, 'run');
    } else if (event.Action === 'pass') {
      if (this.states.get(key) !== 'run') throw Error('Missing run or duplicate test terminal event');
      this.states.set(key, 'pass');
    }
  }
  finish() {
    if (this.packages.size !== this.inventory.packages.length || [...this.expected].some(key => this.states.get(key) !== 'pass') || [...this.states.values()].some(state => state !== 'pass')) throw Error('Missing package/test/subtest terminal pass');
    return [...this.expected].sort();
  }
}

function runGo(args, env, prefix, consume) {
  return new Promise((resolve, reject) => {
    let stdout, stderr;
    try {
      stdout = fs.openSync(prefix + '.jsonl', 'wx');
      stderr = fs.openSync(prefix + '.stderr.log', 'wx');
    } catch (error) {
      if (stdout !== undefined) fs.closeSync(stdout);
      reject(error);
      return;
    }
    const decoder = new StringDecoder('utf8');
    let pending = '', bytes = 0, errorBytes = 0, failure;
    const child = spawn('go', args, {env, shell: false, stdio: ['ignore', 'pipe', 'pipe']});
    const fail = error => { failure ||= error; child.kill('SIGTERM'); };
    const line = text => {
      if (!text) return;
      if (Buffer.byteLength(text) > MAX_LINE) throw Error('Go JSON line limit');
      const events = jsonLines(text);
      for (const event of events) consume(event);
    };
    child.stdout.on('data', chunk => {
      try {
        bytes += chunk.length;
        if (bytes > MAX_LOG) throw Error('Go JSON log limit');
        fs.writeSync(stdout, chunk);
        pending += decoder.write(chunk);
        let end;
        while ((end = pending.indexOf('\n')) >= 0) { line(pending.slice(0, end)); pending = pending.slice(end + 1); }
        if (Buffer.byteLength(pending) > MAX_LINE) throw Error('Go JSON line limit');
      } catch (error) { fail(error); }
    });
    child.stderr.on('data', chunk => {
      try {
        errorBytes += chunk.length;
        if (errorBytes > 16 << 20) throw Error('Go stderr log limit');
        fs.writeSync(stderr, chunk);
      } catch (error) { fail(error); }
    });
    child.on('error', fail);
    child.on('close', code => {
      try { pending += decoder.end(); line(pending); } catch (error) { failure ||= error; }
      fs.closeSync(stdout); fs.closeSync(stderr);
      if (failure) reject(failure);
      else if (code !== 0) reject(Error('Go command failed; inspect saved evidence'));
      else resolve();
    });
  });
}

async function main(args) {
  const [rawShard, rawCount, rawOutput, expectedCommit] = args;
  const shard = Number(rawShard);
  if (args.length < 3 || args.length > 4 || !/^(?:[0-9]|1[0-5])$/.test(rawShard) || rawCount !== String(SHARDS) || !rawOutput) throw Error('Usage: run-go-test-shard.cjs INDEX 16 OUTPUT [EXPECTED_COMMIT]');
  const root = process.cwd(), output = path.resolve(rawOutput), allowed = path.join(root, 'outputs') + path.sep;
  if (!output.startsWith(allowed)) throw Error('Shard output must be inside workspace outputs');
  if (process.env.HOUSEHOLD_REQUIRE_INTEGRATION !== '1' || !process.env.HOUSEHOLD_TEST_OWNER_URL || !process.env.HOUSEHOLD_TEST_APP_URL) throw Error('Required isolated integration authority is missing');
  fs.mkdirSync(output, {recursive: true});
  const source = sourceSnapshot(root, expectedCommit);
  const envValues = execFileSync('go', ['env', 'GOVERSION', 'GOOS', 'GOARCH', 'CGO_ENABLED'], {encoding: 'utf8'}).trim().split(/\r?\n/);
  const go = {version: envValues[0], os: envValues[1], arch: envValues[2], cgo: envValues[3]};
  const discoveryEvents = [];
  const discoveryEnv = discoveryEnvironment(process.env);
  const listing = discoveryArguments();
  await runGo(listing, discoveryEnv, path.join(output, 'discovery'), event => discoveryEvents.push(event));
  const inventory = makeInventory(discoverEvents(discoveryEvents), source, go, controlledSourceInventory(root, source));
  fs.writeFileSync(path.join(output, 'inventory.json'), canonical(inventory) + '\n', {flag: 'wx'});
  const ledger = new ExecutionLedger(inventory, shard), profile = path.join(output, 'coverage.out');
  const command = executionArguments(inventory, shard, profile);
  validateBuildParity(listing, command);
  await runGo(command, process.env, path.join(output, 'tests'), event => {
    ledger.accept(event);
    if (event.Action === 'pass' && event.Test && !event.Test.includes('/')) console.log(event.Package + '/' + event.Test + ' passed');
  });
  const passed = ledger.finish();
  if (canonical(sourceSnapshot(root, expectedCommit)) !== canonical(source)) throw Error('Source changed during shard execution');
  const fileHash = name => sha(fs.readFileSync(path.join(output, name)));
  const result = {schema: 1, shard, shards: SHARDS, exitCode: 0, inventoryDigest: inventory.digest, sourceDigest: source.digest, commit: source.commit, passed,
    inventorySha256: fileHash('inventory.json'), discoverySha256: fileHash('discovery.jsonl'), testsSha256: fileHash('tests.jsonl'), coverageSha256: fileHash('coverage.out')};
  fs.writeFileSync(path.join(output, 'result.json'), canonical(result) + '\n', {flag: 'wx'});
  console.log(`Shard ${shard + 1}/${SHARDS}: ${passed.length} registered mains passed; source ${source.digest}`);
}

module.exports = {SHARDS, SETTINGS, sha, canonical, identity, shardFor, jsonLines, discoverEvents, sourceSnapshot, makeInventory, validateInventory, exactPattern, discoveryEnvironment, discoveryArguments, validateBuildParity, executionArguments, ExecutionLedger};
if (require.main === module) main(process.argv.slice(2)).catch(error => { console.error(error.message); process.exitCode = 1; });
