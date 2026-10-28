'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {summarize} = require('./check-coverage.cjs');
const {SHARDS, sha, canonical, identity, jsonLines, discoverEvents, sourceSnapshot, validateInventory, ExecutionLedger} = require('./run-go-test-shard.cjs');

function parseCoverage(text, inventory) {
  const lines = text.trim().split(/\r?\n/);
  if (lines.shift() !== 'mode: atomic') throw Error('Shard coverage must preserve actual atomic counters');
  const blocks = new Map(), source = new Set(inventory.source.manifest.map(file => file.path));
  for (const line of lines) {
    const match = /^([^\s:]+):(\d+)\.(\d+),(\d+)\.(\d+) (0|[1-9]\d*) (0|[1-9]\d*)$/.exec(line);
    if (!match) throw Error('Invalid coverage block/counter format');
    const [, file, sl, sc, el, ec, statements, count] = match;
    const coords = [sl, sc, el, ec].map(Number), statementCount = Number(statements);
    if (coords.some(value => !Number.isSafeInteger(value) || value < 1) || coords[2] < coords[0] || (coords[2] === coords[0] && coords[3] < coords[1]) || !Number.isSafeInteger(statementCount) || BigInt(count) > 18446744073709551615n) throw Error('Invalid coverage coordinates/counter bounds');
    const prefix = inventory.source.module + '/';
    const relative = file.startsWith(prefix) ? file.slice(prefix.length) : '';
    if (!relative.startsWith('internal/') || !relative.endsWith('.go') || relative.endsWith('_test.go') || !source.has(relative)) throw Error('Coverage block is not bound to an instrumented source file');
    const key = `${file}:${sl}.${sc},${el}.${ec}`;
    const previous = blocks.get(key);
    if (previous && previous.statements !== statementCount) throw Error('Inconsistent duplicate coverage block definition');
    blocks.set(key, {statements: statementCount, covered: BigInt(count) > 0n || Boolean(previous?.covered)});
  }
  if (!blocks.size) throw Error('Empty coverage blocks cannot pass');
  return blocks;
}

function validateAndMerge(shards, expectedSource) {
  if (!Array.isArray(shards) || shards.length !== SHARDS) throw Error('Exactly six complete shard artifacts are required');
  const seenShards = new Set(), executed = new Set(), inputs = [];
  let reference, definitions, merged;
  for (const artifact of shards) {
    const inventory = JSON.parse(artifact.inventory), result = JSON.parse(artifact.result);
    validateInventory(inventory);
    if (!reference) reference = inventory;
    if (inventory.digest !== reference.digest || canonical(inventory) !== canonical(reference)) throw Error('Shard inventories/source/commit/Go target differ');
    if (expectedSource && canonical(inventory.source) !== canonical(expectedSource)) throw Error('Artifact source/commit differs from current checkout');
    if (result.schema !== 1 || result.shards !== SHARDS || !Number.isInteger(result.shard) || result.shard < 0 || result.shard >= SHARDS || seenShards.has(result.shard) || result.exitCode !== 0) throw Error('Missing/duplicate/failed shard result');
    seenShards.add(result.shard);
    if (result.inventoryDigest !== inventory.digest || result.sourceDigest !== inventory.source.digest || result.commit !== inventory.source.commit || result.inventorySha256 !== sha(artifact.inventory) || result.discoverySha256 !== sha(artifact.discovery) || result.testsSha256 !== sha(artifact.tests) || result.coverageSha256 !== sha(artifact.coverage)) throw Error('Shard evidence hash/source binding differs');
    inputs.push({shard: result.shard, inventorySha256: result.inventorySha256, discoverySha256: result.discoverySha256, testsSha256: result.testsSha256, atomicCoverageSha256: result.coverageSha256});
    const listed = discoverEvents(jsonLines(artifact.discovery));
    if (canonical(listed.packages) !== canonical(inventory.packages) || canonical(listed.tests) !== canonical(inventory.tests)) throw Error('Actual discovery differs from claimed full inventory');
    const ledger = new ExecutionLedger(inventory, result.shard);
    for (const event of jsonLines(artifact.tests)) ledger.accept(event);
    const passed = ledger.finish();
    if (canonical(passed) !== canonical(result.passed)) throw Error('Claimed passing identities differ from observed terminal passes');
    for (const name of passed) {
      if (executed.has(name)) throw Error('Test identity executed more than once');
      executed.add(name);
    }
    const blocks = parseCoverage(artifact.coverage, inventory);
    const blockDefinitions = [...blocks].map(([key, value]) => [key, value.statements]).sort(([a], [b]) => a.localeCompare(b, 'en'));
    if (!definitions) { definitions = blockDefinitions; merged = blocks; }
    else {
      if (canonical(blockDefinitions) !== canonical(definitions)) throw Error('Coverage block definitions differ between shards');
      for (const [key, block] of blocks) merged.get(key).covered ||= block.covered;
    }
  }
  const expected = reference.tests.map(identity).sort();
  if (canonical([...executed].sort()) !== canonical(expected)) throw Error('Complete registered inventory was not executed exactly once');
  // Raw atomic execution profiles remain immutable evidence. The merged file
  // claims only observed statement presence, never fabricated execution counts.
  const profile = 'mode: set\n' + definitions.map(([key, statements]) => `${key} ${statements} ${merged.get(key).covered ? 1 : 0}`).join('\n') + '\n';
  return {profile, summary: {schema: 1, shards: SHARDS, mainCount: expected.length, inventoryDigest: reference.digest, sourceDigest: reference.source.digest,
    commit: reference.source.commit, gitDirty: reference.source.dirty, target: reference.go, settings: reference.settings,
    inputs: inputs.sort((a, b) => a.shard - b.shard), blockDefinitionsSha256: sha(canonical(definitions)), coverage: summarize(profile)}};
}

function readEvidence(directory, name, limit) {
  const filename = path.join(directory, name), stat = fs.lstatSync(filename);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size < 1 || stat.size > limit) throw Error('Invalid/missing/bounded evidence file: ' + name);
  return fs.readFileSync(filename, 'utf8');
}

function main(args) {
  const [rawInput, rawOutput, commit] = args;
  if (args.length !== 3 || !/^[a-f0-9]{40,64}$/.test(commit)) throw Error('Usage: merge-go-test-shards.cjs ARTIFACT_DIRECTORY OUTPUT EXPECTED_COMMIT');
  const root = process.cwd(), input = path.resolve(rawInput), output = path.resolve(rawOutput), allowed = path.join(root, 'outputs') + path.sep;
  if (!input.startsWith(allowed) || !output.startsWith(allowed)) throw Error('Evidence paths must be inside workspace outputs');
  const names = fs.readdirSync(input).sort();
  const expectedNames = Array.from({length: SHARDS}, (_, index) => 'go-test-shard-' + index);
  if (canonical(names) !== canonical(expectedNames)) throw Error('Missing or unexpected shard artifact directories');
  const artifacts = names.map(name => {
    const directory = path.join(input, name), stat = fs.lstatSync(directory);
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw Error('Invalid shard artifact directory');
    const artifact = {inventory: readEvidence(directory, 'inventory.json', 8 << 20), result: readEvidence(directory, 'result.json', 8 << 20), discovery: readEvidence(directory, 'discovery.jsonl', 128 << 20), tests: readEvidence(directory, 'tests.jsonl', 128 << 20), coverage: readEvidence(directory, 'coverage.out', 64 << 20)};
    if (JSON.parse(artifact.result).shard !== Number(name.slice('go-test-shard-'.length))) throw Error('Artifact directory/shard identity differs');
    return artifact;
  });
  const source = sourceSnapshot(root, commit);
  const merged = validateAndMerge(artifacts, source);
  if (canonical(sourceSnapshot(root, commit)) !== canonical(source)) throw Error('Source changed during coverage validation');
  fs.mkdirSync(output, {recursive: true});
  fs.writeFileSync(path.join(output, 'coverage.out'), merged.profile, {flag: 'wx'});
  fs.writeFileSync(path.join(output, 'shards.json'), canonical(merged.summary) + '\n', {flag: 'wx'});
  console.log(`Verified six shards and ${merged.summary.mainCount} exactly-once terminal passes; merged mode:set observation profile`);
}

module.exports = {parseCoverage, validateAndMerge};
if (require.main === module) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
