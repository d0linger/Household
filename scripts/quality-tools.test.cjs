'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {createHash} = require('node:crypto');
const {summarize} = require('./check-coverage.cjs');
const {inspect} = require('./check-oci.cjs');
const {checkActions} = require('./check-workflows.cjs');

test('workflow guard rejects movable tags and privileged PR triggers', () => {
  assert.deepEqual(checkActions(`- uses: actions/checkout@${'a'.repeat(40)} # immutable\n- uses: ./local-action`), []);
  assert.equal(checkActions('- uses: actions/checkout@v6').length, 1);
  assert.equal(checkActions('uses: org/reusable/.github/workflows/check.yml@main').length, 1);
  assert.equal(checkActions('on:\n  pull_request_target:').length, 1);
  assert.equal(checkActions('on: [pull_request_target]').length, 1);
  assert.equal(checkActions('on: pull_request_target').length, 1);
  assert.equal(checkActions('- {uses: actions/checkout@v6}').length, 1);
});

test('coverage weights statements and merges duplicate instrumentation', () => {
  const report = summarize('mode: atomic\na/x.go:1.1,2.2 3 0\na/x.go:1.1,2.2 3 2\nb/y.go:1.1,2.2 1 0\n');
  assert.equal(report.percent, 75);
  assert.equal(report.statements, 4);
  assert.equal(report.packages.a.percent, 100);
  assert.equal(report.packages.b.percent, 0);
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
