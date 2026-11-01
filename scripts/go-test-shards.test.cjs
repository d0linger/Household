'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const {parse} = require('yaml');
const {SHARDS, SETTINGS, sha, canonical, identity, shardFor, jsonLines, discoverEvents, makeInventory, validateInventory, exactPattern, discoveryEnvironment, discoveryArguments, validateBuildParity, executionArguments, ExecutionLedger} = require('./run-go-test-shard.cjs');
const {parseCoverage, validateAndMerge} = require('./merge-go-test-shards.cjs');

function fixture() {
  const packages = ['household/internal/a', 'household/internal/b'];
  const bucketNames = new Map();
  for (let index = 0; bucketNames.size < SHARDS && index < 10000; index++) {
    const name = 'TestCase' + index;
    bucketNames.set(shardFor(name), name);
  }
  assert.equal(bucketNames.size, SHARDS);
  const names = [...bucketNames.values()].concat(['FuzzExactAmount', 'ExampleExactAmount']);
  const events = [];
  for (const pkg of packages) {
    events.push({Action: 'start', Package: pkg});
    for (const name of pkg === packages[0] ? names : [names[0]]) events.push({Action: 'output', Package: pkg, Output: name + '\n'});
    events.push({Action: 'pass', Package: pkg});
  }
  const discovery = discoverEvents(events);
  const manifest = [{path: 'internal/a/a.go', sha256: 'a'.repeat(64)}, {path: 'internal/b/b.go', sha256: 'b'.repeat(64)}];
  const source = {commit: 'c'.repeat(40), dirty: false, module: 'household', manifest, digest: sha(canonical(manifest))};
  const inventory = makeInventory(discovery, source, {version: 'go1.27.3', os: 'linux', arch: 'amd64', cgo: '1'});
  const lines = events => events.map(canonical).join('\n') + '\n';
  const artifacts = Array.from({length: SHARDS}, (_, shard) => {
    const tests = [];
    for (const pkg of packages) {
      tests.push({Action: 'start', Package: pkg});
      for (const item of inventory.tests.filter(item => item.shard === shard && item.package === pkg)) {
        tests.push({Action: 'run', Package: pkg, Test: item.name});
        tests.push({Action: 'run', Package: pkg, Test: item.name + '/boundary'});
        tests.push({Action: 'pass', Package: pkg, Test: item.name + '/boundary'});
        tests.push({Action: 'pass', Package: pkg, Test: item.name});
      }
      tests.push({Action: 'pass', Package: pkg});
    }
    const artifact = {inventory: canonical(inventory) + '\n', discovery: lines(events), tests: lines(tests),
      coverage: `mode: atomic\nhousehold/internal/a/a.go:1.1,2.2 2 ${shard === 0 ? '9007199254740993' : '0'}\nhousehold/internal/b/b.go:1.1,2.2 1 ${shard === 1 ? '9' : '0'}\n`};
    artifact.result = canonical({schema: 1, shard, shards: SHARDS, exitCode: 0, inventoryDigest: inventory.digest, sourceDigest: source.digest, commit: source.commit,
      passed: inventory.tests.filter(item => item.shard === shard).map(identity).sort(), inventorySha256: sha(artifact.inventory), discoverySha256: sha(artifact.discovery), testsSha256: sha(artifact.tests), coverageSha256: sha(artifact.coverage)}) + '\n';
    return artifact;
  });
  return {inventory, source, artifacts};
}

function rebind(artifact) {
  const result = JSON.parse(artifact.result);
  result.inventorySha256 = sha(artifact.inventory);
  result.discoverySha256 = sha(artifact.discovery);
  result.testsSha256 = sha(artifact.tests);
  result.coverageSha256 = sha(artifact.coverage);
  artifact.result = canonical(result) + '\n';
}

test('name hashing keeps identical package-local registrations together and exact regex never matches prefixes', () => {
  const {inventory} = fixture();
  const sameName = inventory.tests.find(item => inventory.tests.some(other => other.name === item.name && other.package !== item.package)).name;
  const repeated = inventory.tests.filter(item => item.name === sameName);
  assert.equal(repeated.length, 2);
  for (const item of repeated) assert.equal(item.shard, repeated[0].shard);
  for (let shard = 0; shard < SHARDS; shard++) {
    const regex = new RegExp(exactPattern(inventory, shard), 'u');
    for (const item of inventory.tests) {
      assert.equal(regex.test(item.name), item.shard === shard);
      assert.equal(regex.test(item.name + 'Extra'), false);
      assert.equal(regex.test('Prefix' + item.name), false);
    }
  }
});

test('discovery extracts actual listed Test/Fuzz/Example names and refuses test execution, duplicates or incomplete packages', () => {
  const {inventory, artifacts} = fixture();
  const events = jsonLines(artifacts[0].discovery);
  assert.equal(discoverEvents(events).tests.length, inventory.tests.length);
  assert.ok(inventory.tests.some(item => item.name.startsWith('Fuzz')));
  assert.ok(inventory.tests.some(item => item.name.startsWith('Example')));
  assert.throws(() => discoverEvents(events.concat([{Action: 'run', Package: inventory.packages[0], Test: 'TestRan'}])), /ran/);
  assert.throws(() => discoverEvents(events.concat([{Action: 'output', Package: inventory.tests[0].package, Output: inventory.tests[0].name + '\n'}])), /Duplicate/);
  assert.throws(() => discoverEvents(events.filter(event => event.Action !== 'pass')), /Incomplete/);
  assert.throws(() => jsonLines('{bad JSON'), /Malformed/);
});

test('discovery strips database authority without mutating the integration execution environment', () => {
  const before = {HOUSEHOLD_REQUIRE_INTEGRATION: '1', HOUSEHOLD_TEST_OWNER_URL: 'synthetic-owner', HOUSEHOLD_TEST_APP_URL: 'synthetic-app', HOUSEHOLD_DATABASE_URL: 'synthetic-runtime', PATH: 'unchanged'};
  const result = discoveryEnvironment(before);
  assert.equal(before.HOUSEHOLD_REQUIRE_INTEGRATION, '1');
  assert.equal(result.HOUSEHOLD_REQUIRE_INTEGRATION, '0');
  for (const name of ['HOUSEHOLD_TEST_OWNER_URL', 'HOUSEHOLD_TEST_APP_URL', 'HOUSEHOLD_DATABASE_URL']) assert.equal(result[name], '');
  assert.equal(result.PATH, before.PATH);
});

test('execution preserves race, sequential packages, full internal coverage, shuffle and the bounded sixty-minute package budget', () => {
  const {inventory} = fixture();
  const args = executionArguments(inventory, 0, 'outputs/qa/shard-0/coverage.out');
  assert.equal(args[0], 'test');
  for (const argument of ['-race', '-tags=integration', '-shuffle=on', '-coverpkg=./internal/...', '-covermode=atomic', '-count=1', '-timeout=60m', '-json', './...']) assert.ok(args.includes(argument), argument);
  assert.equal(args[args.indexOf('-p') + 1], '1');
  assert.equal(args[args.indexOf('-run') + 1], exactPattern(inventory, 0));
  assert.equal(SETTINGS.timeout, '60m');
});

test('registration discovery uses the execution race build selection and fails closed if race-only cases could disappear', () => {
  const {inventory} = fixture();
  const listing = discoveryArguments(), execution = executionArguments(inventory, 0, 'outputs/qa/coverage.out');
  assert.equal(listing[listing.indexOf('-list') + 1], '.');
  assert.doesNotThrow(() => validateBuildParity(listing, execution));
  // A //go:build race file participates only when -race is present. Otherwise
  // a self-consistent six-shard inventory could silently omit every such test.
  assert.throws(() => validateBuildParity(listing.filter(value => value !== '-race'), execution), /race build selection/);
  assert.throws(() => validateBuildParity(listing.map(value => value === '-tags=integration' ? '-tags=testtools' : value), execution), /build selection/);
  assert.throws(() => validateBuildParity(listing, execution.filter(value => value !== '-race')), /build selection/);
});

test('six actual execution ledgers merge exactly once into presence-only coverage without losing large atomic counter precision', () => {
  const {source, inventory, artifacts} = fixture();
  const merged = validateAndMerge(artifacts, source);
  assert.equal(merged.summary.mainCount, inventory.tests.length);
  assert.equal(merged.summary.coverage.percent, 100);
  assert.equal(merged.profile, 'mode: set\nhousehold/internal/a/a.go:1.1,2.2 2 1\nhousehold/internal/b/b.go:1.1,2.2 1 1\n');
  assert.ok(artifacts[0].coverage.includes('9007199254740993'));
  assert.equal(merged.summary.gitDirty, false);
});

test('merger refuses missing, duplicate or failed shard artifacts', () => {
  const {artifacts, source} = fixture();
  assert.throws(() => validateAndMerge(artifacts.slice(1), source), /six/);
  const duplicated = artifacts.slice();
  duplicated[5] = duplicated[0];
  assert.throws(() => validateAndMerge(duplicated, source), /duplicate/);
  const failed = artifacts.map(item => ({...item}));
  const result = JSON.parse(failed[0].result);
  result.exitCode = 1;
  failed[0].result = canonical(result);
  assert.throws(() => validateAndMerge(failed, source), /failed/);
});

for (const field of ['inventory', 'discovery', 'tests', 'coverage']) test('merger rejects changed ' + field + ' bytes even when semantically harmless', () => {
  const {artifacts, source} = fixture();
  artifacts[0][field] += '\n';
  assert.throws(() => validateAndMerge(artifacts, source), /hash/);
});

test('merger rejects different commit, source, target, inventory or claimed passes', () => {
  const {artifacts, source} = fixture();
  assert.throws(() => validateAndMerge(artifacts, {...source, commit: 'd'.repeat(40)}), /checkout/);
  const changed = JSON.parse(artifacts[1].inventory);
  changed.go.os = 'windows';
  const {digest, ...body} = changed;
  changed.digest = sha(canonical(body));
  artifacts[1].inventory = canonical(changed);
  rebind(artifacts[1]);
  assert.throws(() => validateAndMerge(artifacts, source), /differ/);
  const second = fixture();
  const result = JSON.parse(second.artifacts[0].result);
  result.passed = [];
  second.artifacts[0].result = canonical(result);
  assert.throws(() => validateAndMerge(second.artifacts, second.source), /passing identities/);
});

for (const action of ['skip', 'fail']) test('execution rejects ' + action + ' for a nested case, even with an otherwise passing main', () => {
  const {inventory, artifacts} = fixture();
  const event = jsonLines(artifacts[0].tests).find(item => item.Test?.includes('/'));
  const ledger = new ExecutionLedger(inventory, 0);
  ledger.accept({Action: 'run', Package: event.Package, Test: event.Test.split('/')[0]});
  assert.throws(() => ledger.accept({...event, Action: action}), /failed or skipped/);
});

test('execution refuses missing, duplicate, unexpected and unassigned registrations', () => {
  const {inventory, artifacts, source} = fixture();
  const events = jsonLines(artifacts[0].tests);
  const run = events.find(event => event.Action === 'run' && !event.Test.includes('/'));
  const ledger = new ExecutionLedger(inventory, 0);
  ledger.accept(run);
  assert.throws(() => ledger.accept(run), /Duplicate/);
  assert.throws(() => ledger.accept({...run, Test: 'TestUnregistered'}), /Unexpected/);
  const other = inventory.tests.find(item => item.shard !== 0);
  assert.throws(() => ledger.accept({Action: 'run', Package: other.package, Test: other.name}), /unassigned/);
  artifacts[0].tests = events.filter(event => !(event.Action === 'pass' && event.Test === run.Test)).map(canonical).join('\n') + '\n';
  rebind(artifacts[0]);
  assert.throws(() => validateAndMerge(artifacts, source), /terminal pass/);
});

test('no-test parent packages can skip while their distinct child packages contain assigned mains', () => {
  const {inventory, artifacts} = fixture();
  const expanded = makeInventory({packages: ['household/internal', ...inventory.packages], tests: inventory.tests}, inventory.source, inventory.go);
  const ledger = new ExecutionLedger(expanded, 0);
  ledger.accept({Action: 'skip', Package: 'household/internal'});
  for (const event of jsonLines(artifacts[0].tests)) ledger.accept(event);
  assert.equal(ledger.finish().length, inventory.tests.filter(item => item.shard === 0).length);
});

test('claimed inventory cannot omit a main present in the actual discovered registration list', () => {
  const {artifacts, source} = fixture();
  const removed = JSON.parse(artifacts[0].inventory);
  removed.tests.pop();
  const {digest, ...body} = removed;
  removed.digest = sha(canonical(body));
  for (const artifact of artifacts) {
    artifact.inventory = canonical(removed);
    const result = JSON.parse(artifact.result);
    result.inventoryDigest = removed.digest;
    artifact.result = canonical(result);
    rebind(artifact);
  }
  assert.throws(() => validateAndMerge(artifacts, source), /discovery differs/);
});

test('coverage definitions and source bindings must match across all six observations', () => {
  const {artifacts, source, inventory} = fixture();
  artifacts[5].coverage = artifacts[5].coverage.replace('1.1,2.2 2', '1.1,3.2 2');
  rebind(artifacts[5]);
  assert.throws(() => validateAndMerge(artifacts, source), /definitions differ/);
  assert.throws(() => parseCoverage('mode: atomic\nother/internal/a/a.go:1.1,2.2 1 1\n', inventory), /source file/);
  assert.throws(() => parseCoverage('mode: atomic\nhousehold/internal/missing.go:1.1,2.2 1 1\n', inventory), /source file/);
  assert.throws(() => parseCoverage('mode: set\nhousehold/internal/a/a.go:1.1,2.2 1 1\n', inventory), /atomic/);
  assert.throws(() => parseCoverage('mode: atomic\n', inventory), /Empty/);
});

test('coverage rejects malformed counters, changed statement counts and invalid coordinates', () => {
  const {inventory} = fixture();
  for (const count of ['-1', '1.5', '1e3', '01', '18446744073709551616']) assert.throws(() => parseCoverage(`mode: atomic\nhousehold/internal/a/a.go:1.1,2.2 2 ${count}\n`, inventory), /counter/);
  assert.throws(() => parseCoverage('mode: atomic\nhousehold/internal/a/a.go:1.1,2.2 2 1\nhousehold/internal/a/a.go:1.1,2.2 3 0\n', inventory), /definition/);
  assert.throws(() => parseCoverage('mode: atomic\nhousehold/internal/a/a.go:0.1,2.2 2 1\n', inventory), /coordinates/);
});

test('inventory digest binds source files, compiler target and command settings', () => {
  const {inventory} = fixture();
  for (const mutate of [value => { value.source.manifest[0].sha256 = '0'.repeat(64); }, value => { value.go.version = 'go1.28.0'; }, value => { value.settings.timeout = 'unbounded'; }, value => { value.tests[0].shard = 99; }]) {
    const value = JSON.parse(canonical(inventory));
    mutate(value);
    assert.throws(() => validateInventory(value), /digest|settings/);
  }
});

test('quality workflow uses isolated six-job matrix and validates complete artifacts before fifty-five-percent coverage and HTML', () => {
  const workflow = parse(fs.readFileSync('.github/workflows/quality.yml', 'utf8'));
  const tests = workflow.jobs.Tests, coverage = workflow.jobs.Coverage;
  assert.deepEqual(tests.strategy.matrix.shard, [0, 1, 2, 3, 4, 5]);
  assert.equal(tests.strategy['fail-fast'], false);
  assert.equal(tests['timeout-minutes'], 75);
  assert.equal(tests.services.postgres.env.POSTGRES_DB, 'household_test');
  assert.equal(tests.env.HOUSEHOLD_REQUIRE_INTEGRATION, '1');
  assert.ok(tests.env.HOUSEHOLD_TEST_OWNER_URL.includes('household_owner:'));
  assert.ok(tests.env.HOUSEHOLD_TEST_APP_URL.includes('household_app:'));
  assert.equal(coverage.needs, 'Tests');
  const commands = coverage.steps.map(step => step.run || '').join('\n');
  assert.ok(commands.indexOf('merge-go-test-shards.cjs') < commands.indexOf('check-coverage.cjs'));
  assert.match(commands, /check-coverage\.cjs outputs\/qa\/coverage\.out 55 /);
  assert.match(commands, /go tool cover -html=outputs\/qa\/coverage\.out/);
  const download = coverage.steps.find(step => step.uses?.startsWith('actions/download-artifact@'));
  assert.match(download.uses, /@[a-f0-9]{40}$/);
  assert.equal(download.with['merge-multiple'], false);
  assert.equal(download.with['digest-mismatch'], 'error');
});
