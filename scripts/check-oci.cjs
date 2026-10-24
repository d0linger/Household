'use strict';
const fs = require('node:fs/promises');
const {createReadStream} = require('node:fs');
const path = require('node:path');
const {createHash} = require('node:crypto');

// Inspect the exported candidate itself, including every blob digest and attestation subject.
async function inspect(directory) {
  const root = await fs.realpath(directory), checked = new Set(), images = new Map(), attestations = [];
  const layout = JSON.parse(await fs.readFile(path.join(root, 'oci-layout'), 'utf8'));
  if (layout.imageLayoutVersion !== '1.0.0') throw Error('Unsupported OCI layout');
  async function blob(descriptor, json = true) {
    if (!/^sha256:[0-9a-f]{64}$/.test(descriptor.digest) || !Number.isSafeInteger(descriptor.size) || descriptor.size < 0) throw Error('Invalid blob descriptor');
    const file = path.join(root, 'blobs', 'sha256', descriptor.digest.slice(7));
    const actual = await fs.realpath(file);
    const info = await fs.lstat(file);
    if (actual !== file || !info.isFile()) throw Error('Blob must be a regular file within the layout');
    if (info.size !== descriptor.size) throw Error('Blob digest or size mismatch');
    if (!checked.has(descriptor.digest)) {
      const hash = createHash('sha256'); let size = 0;
      for await (const chunk of createReadStream(file)) {hash.update(chunk); size += chunk.length;}
      if (size !== descriptor.size || hash.digest('hex') !== descriptor.digest.slice(7)) throw Error('Blob digest or size mismatch');
      checked.add(descriptor.digest);
    }
    if (!json) return;
    if (descriptor.size > 80 * 1024 * 1024) throw Error('Metadata exceeds limit');
    return JSON.parse(await fs.readFile(file, 'utf8'));
  }
  async function visit(descriptor, depth = 0) {
    if (depth > 5 || checked.size > 1000) throw Error('Unexpected OCI graph size');
    const document = await blob(descriptor);
    if (document.schemaVersion !== 2) throw Error('Invalid OCI document');
    if (Array.isArray(document.manifests)) {
      for (const child of document.manifests) await visit(child, depth + 1);
      return;
    }
    if (!Array.isArray(document.layers) || !document.config) throw Error('Invalid image manifest');
    const config = await blob(document.config);
    const isAttestation = document.artifactType === 'application/vnd.docker.attestation.manifest.v1+json' || descriptor.annotations?.['vnd.docker.reference.type'] === 'attestation-manifest';
    if (isAttestation) {
      const target = document.subject?.digest || descriptor.annotations?.['vnd.docker.reference.digest'];
      const annotatedTarget = descriptor.annotations?.['vnd.docker.reference.digest'];
      if (document.subject && annotatedTarget && document.subject.digest !== annotatedTarget) throw Error('Conflicting attestation targets');
      if (document.subject) await blob(document.subject, false);
      for (const layer of document.layers) {
        const statement = await blob(layer);
        if (layer.mediaType !== 'application/vnd.in-toto+json' || !['https://in-toto.io/Statement/v0.1','https://in-toto.io/Statement/v1'].includes(statement._type)) throw Error('Invalid attestation statement');
        if (!statement.subject?.some(subject => 'sha256:' + subject.digest?.sha256 === target)) throw Error('Attestation subject differs from image');
        attestations.push({target, statement});
      }
    } else {
      if (config.os !== 'linux' || config.config?.User !== '10001:10001' || config.config?.Entrypoint?.[0] !== 'household') throw Error('Candidate runtime policy differs');
      for (const layer of document.layers) await blob(layer, false);
      images.set(descriptor.digest, {digest: descriptor.digest, platform: `${config.os}/${config.architecture}`, user: config.config.User});
    }
  }
  const index = JSON.parse(await fs.readFile(path.join(root, 'index.json'), 'utf8'));
  if (index.schemaVersion !== 2 || !Array.isArray(index.manifests)) throw Error('Invalid OCI index');
  for (const descriptor of index.manifests) await visit(descriptor);
  if (!images.size) throw Error('No runtime image');
  for (const {target} of attestations) if (!images.has(target)) throw Error('Attestation references missing runtime image');
  for (const image of images.values()) {
    const statements = attestations.filter(a => a.target === image.digest).map(a => a.statement);
    const sbom = statements.find(a => a.predicateType === 'https://spdx.dev/Document');
    const provenance = statements.find(a => /^https:\/\/slsa\.dev\/provenance\/v(0\.2|1)$/.test(a.predicateType));
    if (!/^SPDX-2\.[23]$/.test(sbom?.predicate?.spdxVersion) || !Array.isArray(sbom.predicate.packages) || !sbom.predicate.packages.length || sbom.predicate.packages.some(pkg=>typeof pkg.name!=='string'||!pkg.name)) throw Error('Candidate has no populated SPDX SBOM');
    const builder = provenance?.predicate?.builder || provenance?.predicate?.runDetails?.builder;
    const buildType = provenance?.predicate?.buildType || provenance?.predicate?.buildDefinition?.buildType;
    // BuildKit may leave builder.id empty for local exports. This verifies evidence
    // structure and subject integrity, not a trusted signature or builder identity.
    if (!builder || typeof builder.id !== 'string' || typeof buildType !== 'string' || !buildType.startsWith('https://')) throw Error('Candidate has no build provenance');
    image.sbomPackages = sbom.predicate.packages.length;
    image.provenance = provenance.predicateType;
  }
  return {verified: true, blobs: checked.size, images: [...images.values()]};
}
module.exports = {inspect};
if (require.main === module) {
  inspect(process.argv[2] || '').then(async report => {
    const text = JSON.stringify(report, null, 2) + '\n';
    if (process.argv[3]) await fs.writeFile(process.argv[3], text);
    console.log(text);
  }).catch(error => {console.error(error.message); process.exitCode = 1;});
}
