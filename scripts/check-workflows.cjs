'use strict';
const fs = require('node:fs');
const path = require('node:path');
const YAML = require('yaml');

function checkActions(text) {
  const errors = [];
  let document;
  try {document = YAML.parse(text, {uniqueKeys:true, maxAliasCount:50});} catch {return ['Invalid workflow YAML'];}
  function visit(value) {
    if (!value || typeof value !== 'object') return;
    if ('uses' in value && (typeof value.uses !== 'string' || (!value.uses.startsWith('./') && !/@[a-fA-F0-9]{40}$/.test(value.uses)))) errors.push('External action is not pinned to an immutable SHA');
    for (const child of Object.values(value)) visit(child);
  }
  visit(document);
  const triggers = typeof document?.on === 'string' ? [document.on] : Array.isArray(document?.on) ? document.on : Object.keys(document?.on || {});
  if (triggers.includes('pull_request_target')) errors.push('Untrusted checkout must not run through pull_request_target');
  return errors;
}
module.exports = {checkActions};
if (require.main === module) {
  const errors = [];
  for (const name of fs.readdirSync('.github/workflows').filter(name => /\.ya?ml$/.test(name))) {
    const text = fs.readFileSync(path.join('.github/workflows', name), 'utf8');
    errors.push(...checkActions(text).map(error => `${name}: ${error}`));
  }
  const version = /^go (\S+)$/m.exec(fs.readFileSync('go.mod', 'utf8'))?.[1];
  const dockerVersion = /^FROM golang:([^\s-]+)-[^\s]+@sha256:[a-f0-9]{64}/m.exec(fs.readFileSync('Dockerfile', 'utf8'))?.[1];
  if (!version || version !== dockerVersion) errors.push('Docker builder and go.mod must use the same pinned Go version');
  if (errors.length) {console.error(errors.join('\n')); process.exitCode = 1;} else console.log('Workflow action pins, trusted trigger policy and Go toolchain alignment passed');
}
