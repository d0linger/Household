'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {createHash} = require('node:crypto');
const {summarize} = require('./check-coverage.cjs');
const {inspect} = require('./check-oci.cjs');
const {checkActions, checkWorkflow} = require('./check-workflows.cjs');
const {parseDocument} = require('yaml');
const {resolveBrowserPrivateRoot} = require('./browser-private-root.cjs');

test('Gosec keeps every package below the loader argument boundary without omitting source', async () => {
  const root = path.resolve(__dirname, '..');
  const workflow = parseDocument(await fs.readFile(path.join(root, '.github/workflows/quality.yml'), 'utf8')).toJS();
  const command = workflow.jobs.GOSec.steps.find(step => step.name === 'Scan all application packages through a short source path').run;
  assert.match(command, /mktemp -d \/tmp\/hh\.XXXXXX/);
  assert.match(command, /ln -s "\$GITHUB_WORKSPACE" "\$scan\/s"/);
  assert.match(command, /cd "\$scan\/s"/);
  assert.match(command, /-out="\$GITHUB_WORKSPACE\/gosec\.sarif"/);
  assert.match(command, /\.\/cmd\/\.\.\. \.\/internal\/\.\.\./);
  const directories = ['cmd', 'internal'];
  for (const directory of directories) {
    let bytes = 0;
    for (const entry of await fs.readdir(path.join(root, directory), {withFileTypes: true})) {
      if (entry.isDirectory()) directories.push(directory + '/' + entry.name);
      else if (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) bytes += Buffer.byteLength('/tmp/hh.123456/s/' + directory + '/' + entry.name) + 1;
    }
    assert.ok(bytes < 16383, directory + ' exceeds the scanner loader argument bound; update its package loader');
  }
});

async function privateBrowserFixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'household-browser-root-test-'));
  t.after(async () => {
    if (path.dirname(root) !== os.tmpdir() || !path.basename(root).startsWith('household-browser-root-test-')) throw Error('Unsafe private browser fixture cleanup');
    await fs.rm(root, {recursive: true, force: true});
  });
  return root;
}

test('browser private root preserves the default and uses an existing restricted directory for actual session children', async t => {
  const root = await privateBrowserFixture(t);
  const local = resolveBrowserPrivateRoot(root);
  assert.equal(local, await fs.realpath(path.join(root, '.local')));
  assert.equal(resolveBrowserPrivateRoot(root, ''), local);
  const restricted = path.join(local, 'restricted-run');
  await fs.mkdir(restricted);
  const selected = resolveBrowserPrivateRoot(root, restricted);
  assert.equal(selected, await fs.realpath(restricted));
  const child = await fs.mkdtemp(path.join(selected, 'browser-auth-'));
  await fs.writeFile(path.join(child, 'state.json'), '{"synthetic":true}');
  assert.equal(path.dirname(await fs.realpath(child)), selected);
  assert.equal(await fs.readFile(path.join(child, 'state.json'), 'utf8'), '{"synthetic":true}');
});

test('browser private root rejects missing, file and outside roots without creating an explicit directory', async t => {
  const root = await privateBrowserFixture(t), local = resolveBrowserPrivateRoot(root);
  const missing = path.join(local, 'not-created');
  assert.throws(() => resolveBrowserPrivateRoot(root, missing), /ENOENT/);
  await assert.rejects(fs.stat(missing), {code: 'ENOENT'});
  const file = path.join(local, 'regular-file');
  await fs.writeFile(file, 'synthetic');
  assert.throws(() => resolveBrowserPrivateRoot(root, file), /existing directory/);
  assert.throws(() => resolveBrowserPrivateRoot(root, root), /inside workspace/);
  assert.throws(() => resolveBrowserPrivateRoot(root, 3), /Invalid/);
});

test('browser private root refuses actual junction or symlink escapes from selected and default roots', async t => {
  const root = await privateBrowserFixture(t), local = resolveBrowserPrivateRoot(root);
  const outside = path.join(root, 'outside-local');
  await fs.mkdir(outside);
  const link = path.join(local, 'escape');
  await fs.symlink(outside, link, process.platform === 'win32' ? 'junction' : 'dir');
  assert.throws(() => resolveBrowserPrivateRoot(root, link), /inside workspace/);
  const nested = path.join(root, 'nested-workspace');
  await fs.mkdir(nested);
  await fs.symlink(outside, path.join(nested, '.local'), process.platform === 'win32' ? 'junction' : 'dir');
  assert.throws(() => resolveBrowserPrivateRoot(nested), /leaves the workspace/);
});

test('notification handoff accepts bounded owned files and rejects escaped, aliased and oversized evidence', async t => {
  const {containedFile, boundedJSON} = require('./notification-browser-fixture.cjs');
  const root = await privateBrowserFixture(t), local = resolveBrowserPrivateRoot(root);
  const owned = path.join(local, 'owned');await fs.mkdir(owned);
  const file = path.join(owned, 'fixture.json');await fs.writeFile(file, '{"synthetic":true}');
  assert.equal(containedFile(local, file), await fs.realpath(file));
  assert.deepEqual(boundedJSON(file), {synthetic:true});
  const outside = path.join(root, 'outside');await fs.mkdir(outside);
  const escaped = path.join(outside, 'fixture.json');await fs.writeFile(escaped, '{}');
  assert.throws(() => containedFile(local, escaped), /escapes/);
  const link = path.join(local, 'alias');await fs.symlink(owned, link, process.platform === 'win32' ? 'junction' : 'dir');
  assert.throws(() => containedFile(local, path.join(link, 'fixture.json')), /alias/);
  await fs.writeFile(file, Buffer.alloc((1 << 20) + 1));
  assert.throws(() => boundedJSON(file), /bounded/);
});

test('all explicit Node entry points cover every current pure test suite exactly once', async () => {
  const root = path.resolve(__dirname, '..');
  const sources = (await fs.readdir(__dirname, {withFileTypes: true}))
    .filter(entry => entry.isFile() && /^[a-z0-9-]+\.test\.cjs$/.test(entry.name))
    .map(entry => 'scripts/' + entry.name).sort();
  assert.ok(sources.length > 0, 'the pure test inventory must not be empty');
  const packageJSON = JSON.parse(await fs.readFile(path.join(root, 'package.json'), 'utf8'));
  const verify = await fs.readFile(path.join(root, 'scripts/verify.ps1'), 'utf8');
  const workflow = parseDocument(await fs.readFile(path.join(root, '.github/workflows/quality.yml'), 'utf8'));
  assert.equal(workflow.errors.length, 0, 'quality workflow must be valid YAML');
  const steps = workflow.toJS().jobs.WorkflowAndSecrets.steps;
  const commands = [
    ['npm test', packageJSON.scripts.test],
    ['verify.ps1', verify],
    ['quality WorkflowAndSecrets', steps.map(step => step.run || '').join('\n')]
  ];
  for (const [name, text] of commands) {
    const lists = [...text.matchAll(/^\s*node --test ([^\r\n]+)$/gm)];
    assert.equal(lists.length, 1, name + ' must have one explicit Node test invocation');
    const paths = lists[0][1].trim().split(/\s+/);
    assert.ok(paths.every(file => /^scripts\/[a-z0-9-]+\.test\.cjs$/.test(file)), name + ' must name concrete test sources');
    assert.equal(new Set(paths).size, paths.length, name + ' must not repeat a test source');
    assert.deepEqual(paths.sort(), sources, name + ' must not omit or invent a pure test suite');
  }
});

test('workflow guard rejects movable tags and privileged PR triggers', () => {
  assert.deepEqual(checkActions(`- uses: actions/checkout@${'a'.repeat(40)} # immutable\n- uses: ./local-action`), []);
  assert.equal(checkActions('- uses: actions/checkout@v6').length, 1);
  assert.equal(checkActions('uses: org/reusable/.github/workflows/check.yml@main').length, 1);
  assert.equal(checkActions('on:\n  pull_request_target:').length, 1);
  assert.equal(checkActions('on: [pull_request_target]').length, 1);
  assert.equal(checkActions('on: pull_request_target').length, 1);
  assert.equal(checkActions('- {uses: actions/checkout@v6}').length, 1);
});

function workflowFixture() {
  return {
    on: ['push', 'pull_request'],
    permissions: {contents: 'read'},
    concurrency: {group: 'checks-${{ github.ref }}', 'cancel-in-progress': true},
    jobs: {Checks: {
      'runs-on': 'ubuntu-latest', 'timeout-minutes': 15,
      steps: [{uses: `actions/checkout@${'a'.repeat(40)}`, with: {'persist-credentials': false}}]
    }}
  };
}

test('workflow policy preserves read-only tokens and removes persisted checkout credentials', () => {
  assert.deepEqual(checkWorkflow(JSON.stringify(workflowFixture())), []);
  for (const permissions of [undefined, 'write-all', {contents: 'write'}, {contents: 'read', 'id-token': 'write'}]) {
    const workflow = workflowFixture();
    workflow.permissions = permissions;
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /read-only permissions/);
  }
  const elevated = workflowFixture();
  elevated.jobs.Checks.permissions = {contents: 'read', 'pull-requests': 'write'};
  assert.match(checkWorkflow(JSON.stringify(elevated)).join('\n'), /must not grant write/);
  for (const persist of [undefined, true, 'false']) {
    const workflow = workflowFixture();
    workflow.jobs.Checks.steps[0].with['persist-credentials'] = persist;
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /persist-credentials: false/);
  }
});

test('workflow policy rejects unbounded runner jobs and uncancelled superseded checks', () => {
  for (const timeout of [undefined, 0, -1, 361, '15', '${{ inputs.timeout }}']) {
    const workflow = workflowFixture();
    workflow.jobs.Checks['timeout-minutes'] = timeout;
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /explicit timeout/);
  }
  for (const concurrency of [undefined, 'checks', {group: ''}, {group: 'checks', 'cancel-in-progress': false}]) {
    const workflow = workflowFixture();
    workflow.concurrency = concurrency;
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /cancel superseded/);
  }
  assert.deepEqual(checkWorkflow('name: first\nname: duplicate'), ['Invalid workflow YAML']);
});

test('coverage weights statements and merges duplicate instrumentation', () => {
  const report = summarize('mode: atomic\na/x.go:1.1,2.2 3 0\na/x.go:1.1,2.2 3 2\nb/y.go:1.1,2.2 1 0\n');
  assert.equal(report.percent, 75);
  assert.equal(report.statements, 4);
  assert.equal(report.packages.a.percent, 100);
  assert.equal(report.packages.b.percent, 0);
});

test('workflow policy refuses empty jobs and suppressed check failures', () => {
  for (const jobs of [undefined, {}, [], {Checks: null}]) {
    const workflow = workflowFixture(); workflow.jobs = jobs;
    assert.ok(checkWorkflow(JSON.stringify(workflow)).length > 0);
  }
  for (const value of [true, 'true', '${{ inputs.allow_failure }}']) {
    for (const scope of ['job', 'step']) {
      const workflow = workflowFixture();
      const target = scope === 'job' ? workflow.jobs.Checks : workflow.jobs.Checks.steps[0];
      target['continue-on-error'] = value;
      assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /must not be suppressed/);
    }
  }
});

test('workflow policy requires immutable services and a single Go version source', () => {
  const workflow = workflowFixture(), job = workflow.jobs.Checks;
  for (const image of ['postgres:18-alpine', '${{ inputs.image }}', 'postgres@sha256:123']) {
    job.services = {postgres: {image}};
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /SHA-256 digest/);
  }
  job.services.postgres.image = 'postgres:18-alpine@sha256:' + 'a'.repeat(64);
  job.container = 'alpine@sha256:' + 'b'.repeat(64);
  assert.deepEqual(checkWorkflow(JSON.stringify(workflow)), []);
  const setup = {uses: 'actions/setup-go@' + 'a'.repeat(40), with: {'go-version-file': 'go.mod'}};
  job.steps.push(setup);
  assert.deepEqual(checkWorkflow(JSON.stringify(workflow)), []);
  for (const settings of [{}, {'go-version': '1.27.3'}, {'go-version-file': 'go.mod', 'go-version': 'stable'}]) {
    setup.with = settings;
    assert.match(checkWorkflow(JSON.stringify(workflow)).join('\n'), /exclusively from go.mod/);
  }
});

test('required coverage cannot succeed after a failed unit or integration prerequisite', async () => {
  const document = parseDocument(await fs.readFile(path.join(__dirname, '../.github/workflows/quality.yml'), 'utf8')).toJS();
  assert.deepEqual(document.jobs.Coverage.needs, ['Tests', 'UnitTests']);
  const refusal = document.jobs.Coverage.steps[0];
  assert.equal(refusal.if, "${{ needs.Tests.result != 'success' || needs.UnitTests.result != 'success' }}");
  assert.match(refusal.run, /exit 1/);
  const unit = document.jobs.UnitTests.steps.map(step => step.run || '').join('\n');
  assert.match(unit, /go test -p 1 \.\/\.\.\. -count=1 -shuffle=on/);
  assert.match(unit, /check-coverage\.cjs \S+ 14 /);
  const review = document.jobs.GODep.steps.find(step => step.uses?.startsWith('actions/dependency-review-action@'));
  assert.equal(review.if, "github.event_name == 'pull_request'");
  assert.equal(review.with['fail-on-severity'], 'high');
  assert.equal(review.with['comment-summary-in-pr'], 'never');
  assert.ok(document.jobs.WorkflowAndSecrets.steps.some(step => step.run === 'node scripts/check-local-artifacts.cjs --index'));
});
test('coverage fails closed on missing or malformed observations', () => {
  for (const text of ['', 'mode: atomic\n', 'mode: fake\na/x.go:1.1,2.2 1 1', 'mode: atomic\ncorrupt']) assert.throws(() => summarize(text));
  for (const value of ['9'.repeat(400),'9007199254740992']) assert.throws(() => summarize(`mode: atomic\na/x.go:1.1,2.2 ${value} 1\n`), /counts/);
});

async function fixture(t, change = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'household-oci-test-'));
  t.after(async () => {
    if (path.dirname(dir) !== os.tmpdir() || !path.basename(dir).startsWith('household-oci-test-')) throw Error('Unsafe fixture cleanup');
    await fs.rm(dir, {recursive: true, force: true});
  });
  await fs.mkdir(path.join(dir, 'blobs', 'sha256'), {recursive: true});
  const write = async (value, mediaType = 'application/vnd.oci.image.manifest.v1+json') => {
    const bytes = Buffer.isBuffer(value) ? value : Buffer.from(JSON.stringify(value));
    const hash = createHash('sha256').update(bytes).digest('hex');
    await fs.writeFile(path.join(dir, 'blobs', 'sha256', hash), bytes);
    return {mediaType, digest: 'sha256:' + hash, size: bytes.length};
  };
  const config = await write({os: 'linux', architecture: 'amd64', config: {User: change.user || '10001:10001', Entrypoint: ['household']}});
  const layer = await write(Buffer.from('synthetic image layer'), 'application/vnd.oci.image.layer.v1.tar');
  const image = await write({schemaVersion: 2, config, layers: [layer]});
  const subject = change.emptySubject ? [] : [{name: 'household', digest: {sha256: change.subject || image.digest.slice(7)}}];
  const statement = (predicateType, predicate) => write({_type: 'https://in-toto.io/Statement/v1', subject, predicateType, predicate}, 'application/vnd.in-toto+json');
  const sbom = await statement('https://spdx.dev/Document', change.badSBOM || {spdxVersion: 'SPDX-2.3', packages: [{name: 'household'}]});
  const provenance = await statement('https://slsa.dev/provenance/v0.2', change.badProvenance || {builder: {id: 'test-builder'}, buildType: 'https://mobyproject.org/buildkit@v1'});
  const attestation = await write({schemaVersion: 2, artifactType: 'application/vnd.docker.attestation.manifest.v1+json', subject: change.badSubjectSize ? {...image,size:0} : image, config: await write({}), layers: change.missingSBOM ? [provenance] : [sbom, provenance]});
  await fs.writeFile(path.join(dir, 'oci-layout'), JSON.stringify({imageLayoutVersion: '1.0.0'}));
  await fs.writeFile(path.join(dir, 'index.json'), JSON.stringify({schemaVersion: 2, manifests: [attestation, image, ...(change.duplicateSize ? [{...image,size:0}] : [])]}));
  if (change.tamper) await fs.appendFile(path.join(dir, 'blobs', 'sha256', layer.digest.slice(7)), 'corrupt');
  return dir;
}
test('OCI inspection checks actual image, all digests and attached evidence', async t => {
  const result = await inspect(await fixture(t));
  assert.equal(result.verified, true);
  assert.equal(result.images[0].sbomPackages, 1);
  assert.equal(result.images[0].user, '10001:10001');
});
for (const [name, change, message] of [
  ['corrupt layer', {tamper: true}, /digest or size/],
  ['wrong subject', {subject: 'f'.repeat(64)}, /subject differs/],
  ['empty subject from an untagged export', {emptySubject: true}, /subject differs/],
  ['missing SBOM', {missingSBOM: true}, /no populated SPDX/],
  ['root runtime', {user: '0:0'}, /runtime policy/],
  ['incorrect repeated descriptor size', {duplicateSize:true}, /digest or size/],
  ['incorrect subject descriptor size', {badSubjectSize:true}, /digest or size/],
  ['invalid SBOM structure', {badSBOM:{spdxVersion:'garbage',packages:'not packages'}}, /no populated SPDX/],
  ['invalid provenance structure', {badProvenance:{builder:{}}}, /no build provenance/],
]) test('OCI rejects ' + name, async t => assert.rejects(inspect(await fixture(t, change)), message));
