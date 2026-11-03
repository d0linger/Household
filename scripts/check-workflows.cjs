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

function checkReleaseWorkflow(document) {
  const errors=[];
  const jobs=document?.jobs||{};
  const gates={quality:'quality',browser:'browser',shopping:'shopping-extensions',recovery:'fuel-ocr-recovery',candidate:'container'};
  const exact=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
  if(document.name!=='Production image' || !exact(document.on,{push:{branches:['main']}}))errors.push('Release must run only on main pushes');
  if(!exact(Object.keys(jobs).sort(),[...Object.keys(gates),'publish'].sort()))errors.push('Release jobs must match the complete production gate set');
  for(const [name,file] of Object.entries(gates)){
    const job=jobs[name];
    if(!job || job.uses!==`./.github/workflows/${file}.yml` || job.if!==undefined || job.secrets!==undefined || job['continue-on-error']!==undefined || !exact(job.permissions,{contents:'read'}))errors.push(`${name}: release must run the same read-only checks unconditionally`);
  }
  const publish=jobs.publish;
  if(!publish || !exact([...(publish.needs||[])].sort(),Object.keys(gates).sort()) ||
    publish.if!=="github.event_name == 'push' && github.ref == 'refs/heads/main' && github.repository == 'd0linger/Household'")errors.push('Publishing requires all five successful gates and the protected main event');
  if(!exact(publish?.permissions,{contents:'read','pull-requests':'read',packages:'write'}))errors.push('Only publication may receive the narrowly scoped registry token');
  if(!exact(publish?.concurrency,{group:'household-production-publication','cancel-in-progress':false}))errors.push('Publication must serialize mutable tags without cancelling in-flight writes');
  const run=publish?.steps?.filter(step=>step.run==='bash scripts/publish-production.sh');
  if(run?.length!==1 || run[0].env?.GH_TOKEN!=='${{ secrets.GITHUB_TOKEN }}')errors.push('Publication must use the reviewed exact-candidate promotion script');
  return errors;
}

function checkWorkflow(text, {release=false}={}) {
  const errors = checkActions(text);
  if (errors.includes('Invalid workflow YAML')) return errors;
  const document = YAML.parse(text, {uniqueKeys:true, maxAliasCount:50});
  if(release)errors.push(...checkReleaseWorkflow(document));
  if (!readOnlyPermissions(document?.permissions) || document.permissions.contents !== 'read') {
    errors.push('Check workflows must declare read-only permissions with contents: read');
  }
  if (typeof document?.concurrency?.group !== 'string' || !document.concurrency.group.trim()
      || (document.concurrency['cancel-in-progress'] !== true && !(release && document.concurrency['cancel-in-progress'] === false))) {
    errors.push('Check workflows must cancel superseded runs in an explicit concurrency group');
  }
  if (!document?.jobs || typeof document.jobs !== 'object' || Array.isArray(document.jobs)
      || Object.keys(document.jobs).length === 0) errors.push('Check workflows must define at least one job');
  for (const [name, job] of Object.entries(document?.jobs || {})) {
    if (!job || typeof job !== 'object' || Array.isArray(job)) {
      errors.push(`${name}: invalid job definition`);
      continue;
    }
    if (job['continue-on-error'] !== undefined && job['continue-on-error'] !== false) {
      errors.push(`${name}: required check failures must not be suppressed`);
    }
    if (job.permissions !== undefined && !readOnlyPermissions(job.permissions) && !(release && name==='publish')) {
      errors.push(`${name}: check jobs must not grant write permissions`);
    }
    if (job['runs-on'] !== undefined && (!Number.isInteger(job['timeout-minutes'])
        || job['timeout-minutes'] < 1 || job['timeout-minutes'] > 360)) {
      errors.push(`${name}: runner jobs must have an explicit timeout from 1 to 360 minutes`);
    }
    const containers = {...job.services};
    if (job.container !== undefined) containers['job container'] = job.container;
    for (const [service, container] of Object.entries(containers)) {
      const image = typeof container === 'string' ? container : container?.image;
      if (typeof image !== 'string' || !/^\S+@sha256:[a-f0-9]{64}$/.test(image)) {
        errors.push(`${name}/${service}: container image must be pinned to a SHA-256 digest`);
      }
    }
    for (const step of job.steps || []) {
      if (step['continue-on-error'] !== undefined && step['continue-on-error'] !== false) {
        errors.push(`${name}: required check failures must not be suppressed`);
      }
      if (typeof step.uses === 'string' && /^actions\/checkout@/i.test(step.uses)
          && step.with?.['persist-credentials'] !== false) {
        errors.push(`${name}: checkout must set persist-credentials: false`);
      }
      if (typeof step.uses === 'string' && /^actions\/setup-go@/i.test(step.uses)
          && (step.with?.['go-version-file'] !== 'go.mod' || step.with?.['go-version'] !== undefined)) {
        errors.push(`${name}: Go setup must derive its version exclusively from go.mod`);
      }
    }
  }
  return errors;
}

module.exports = {checkActions, checkWorkflow, checkReleaseWorkflow};
if (require.main === module) {
  const errors = [];
  for (const name of fs.readdirSync('.github/workflows').filter(name => /\.ya?ml$/.test(name))) {
    const text = fs.readFileSync(path.join('.github/workflows', name), 'utf8');
    errors.push(...checkWorkflow(text,{release:name==='release.yml'}).map(error => `${name}: ${error}`));
  }
  const version = /^go (\S+)$/m.exec(fs.readFileSync('go.mod', 'utf8'))?.[1];
  const dockerVersion = /^FROM golang:([^\s-]+)-[^\s]+@sha256:[a-f0-9]{64}/m.exec(fs.readFileSync('Dockerfile', 'utf8'))?.[1];
  if (!version || version !== dockerVersion) errors.push('Docker builder and go.mod must use the same pinned Go version');
  if (errors.length) {console.error(errors.join('\n')); process.exitCode = 1;} else console.log('Workflow pins, read-only permissions, checkout isolation, timeouts, cancellation and Go toolchain alignment passed');
}
