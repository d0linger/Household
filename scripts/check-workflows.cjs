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
function readOnlyPermissions(value) {
  return value && typeof value === 'object' && !Array.isArray(value)
    && Object.values(value).every(permission => permission === 'read' || permission === 'none');
}

function checkWorkflow(text) {
  const errors = checkActions(text);
  if (errors.includes('Invalid workflow YAML')) return errors;
  const document = YAML.parse(text, {uniqueKeys:true, maxAliasCount:50});
  if (!readOnlyPermissions(document?.permissions) || document.permissions.contents !== 'read') {
    errors.push('Check workflows must declare read-only permissions with contents: read');
  }
  if (typeof document?.concurrency?.group !== 'string' || !document.concurrency.group.trim()
      || document.concurrency['cancel-in-progress'] !== true) {
    errors.push('Check workflows must cancel superseded runs in an explicit concurrency group');
  }
  for (const [name, job] of Object.entries(document?.jobs || {})) {
    if (job.permissions !== undefined && !readOnlyPermissions(job.permissions)) {
      errors.push(`${name}: check jobs must not grant write permissions`);
    }
    if (job['runs-on'] !== undefined && (!Number.isInteger(job['timeout-minutes'])
        || job['timeout-minutes'] < 1 || job['timeout-minutes'] > 360)) {
      errors.push(`${name}: runner jobs must have an explicit timeout from 1 to 360 minutes`);
    }
    for (const step of job.steps || []) {
      if (typeof step.uses === 'string' && /^actions\/checkout@/i.test(step.uses)
          && step.with?.['persist-credentials'] !== false) {
        errors.push(`${name}: checkout must set persist-credentials: false`);
      }
    }
  }
  return errors;
}

module.exports = {checkActions, checkWorkflow};
if (require.main === module) {
  const errors = [];
  for (const name of fs.readdirSync('.github/workflows').filter(name => /\.ya?ml$/.test(name))) {
    const text = fs.readFileSync(path.join('.github/workflows', name), 'utf8');
    errors.push(...checkWorkflow(text).map(error => `${name}: ${error}`));
  }
  const version = /^go (\S+)$/m.exec(fs.readFileSync('go.mod', 'utf8'))?.[1];
  const dockerVersion = /^FROM golang:([^\s-]+)-[^\s]+@sha256:[a-f0-9]{64}/m.exec(fs.readFileSync('Dockerfile', 'utf8'))?.[1];
  if (!version || version !== dockerVersion) errors.push('Docker builder and go.mod must use the same pinned Go version');
  if (errors.length) {console.error(errors.join('\n')); process.exitCode = 1;} else console.log('Workflow pins, read-only permissions, checkout isolation, timeouts, cancellation and Go toolchain alignment passed');
}
