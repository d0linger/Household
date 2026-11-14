'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const {spawnSync} = require('node:child_process');
const {validateEvent, isPromotionPR, createGitHubAPI, verifyPromotion, readEvent} = require('./release-guard.cjs');

const sha = 'a'.repeat(40), newer = 'b'.repeat(40), repository = 'd0linger/Household';
const env = () => ({GITHUB_REPOSITORY: repository, GITHUB_EVENT_NAME: 'push', GITHUB_REF: 'refs/heads/main',
  GITHUB_SHA: sha, GH_TOKEN: 'synthetic-token-never-print'});
const event = () => ({ref: 'refs/heads/main', after: sha, deleted: false, created: false, forced: false,
  repository: {full_name: repository}, head_commit: {id: sha}});
const promotion = () => ({number: 42, state: 'closed', merged_at: '2026-11-03T12:00:00Z', merge_commit_sha: sha,
  base: {ref: 'main', repo: {full_name: repository}}, head: {ref: 'dev', repo: {full_name: repository}}});
const main = (value = sha) => ({ref: 'refs/heads/main', object: {type: 'commit', sha: value}});
const json = (value, options = {}) => new Response(JSON.stringify(value), {status: 200, headers: {'content-type': 'application/json'}, ...options});

test('release guard accepts only a normal Household main push matching its event identity', () => {
  assert.equal(validateEvent(env(), event()), sha);
  const invalidEnvironments = [
    {GITHUB_REPOSITORY: 'attacker/Household'}, {GITHUB_REPOSITORY: 'd0linger/household'},
    {GITHUB_EVENT_NAME: 'pull_request'}, {GITHUB_EVENT_NAME: 'workflow_dispatch'}, {GITHUB_EVENT_NAME: 'workflow_run'},
    {GITHUB_REF: 'refs/heads/dev'}, {GITHUB_REF: 'refs/tags/v1.0.0'}, {GITHUB_SHA: 'a'.repeat(39)},
    {GITHUB_SHA: 'A'.repeat(40)}, {GITHUB_SHA: '../main'}
  ];
  for (const change of invalidEnvironments) assert.throws(() => validateEvent({...env(), ...change}, event()));
  assert.throws(() => validateEvent(env(), {}));
  const invalidEvents = [null, [], {ref: 'refs/heads/dev'}, {after: newer}, {deleted: true}, {deleted: undefined},
    {created: true}, {forced: true}, {repository: {full_name: 'attacker/Household'}}, {head_commit: null}, {head_commit: {id: newer}}];
  for (const change of invalidEvents) assert.throws(() => validateEvent(env(), change === null || Array.isArray(change) ? change : {...event(), ...change}));
});

test('promotion evidence excludes unmerged, forked, wrong-source and wrong-commit pull requests', () => {
  assert.equal(isPromotionPR(promotion(), sha), true);
  const changes = [{number: 0}, {number: '42'}, {state: 'open'}, {merged_at: null}, {merged_at: 'invalid'}, {merge_commit_sha: newer}, {merge_commit_sha: null}, {merge_commit_sha: undefined},
    {base: {ref: 'dev', repo: {full_name: repository}}}, {base: {ref: 'main', repo: {full_name: 'attacker/Household'}}},
    {head: {ref: 'feature', repo: {full_name: repository}}}, {head: {ref: 'dev', repo: {full_name: 'attacker/Household'}}}, {head: {ref: 'dev', repo: null}}];
  for (const change of changes) assert.equal(isPromotionPR({...promotion(), ...change}, sha), false);
  assert.equal(isPromotionPR(null, sha), false);
});

test('successful promotion uses only fixed GitHub endpoints, no redirects, and checks main again', async () => {
  const requests = [];
  const result = await verifyPromotion({env: env(), event: event(), fetchImpl: async (url, options) => {
    requests.push(url);
    assert.equal(options.method, 'GET'); assert.equal(options.redirect, 'error'); assert.equal(options.credentials, 'omit');
    assert.equal(options.headers.Authorization, 'Bearer synthetic-token-never-print');
    assert.equal(options.headers['X-GitHub-Api-Version'], '2022-11-28');
    assert.equal(options.signal.aborted, false);
    return json(url.endsWith('/git/ref/heads/main') ? main() : [promotion()]);
  }});
  assert.deepEqual(result, {repository, sha, pullRequest: 42});
  assert.deepEqual(requests, [
    'https://api.github.com/repos/d0linger/Household/git/ref/heads/main',
    'https://api.github.com/repos/d0linger/Household/commits/' + sha + '/pulls?per_page=100&page=1',
    'https://api.github.com/repos/d0linger/Household/git/ref/heads/main'
  ]);
});

test('pull request lookup advances bounded pages without trusting response links', async () => {
  let pages = 0;
  const result = await verifyPromotion({env: {...env(), GH_TOKEN: undefined, GITHUB_TOKEN: 'synthetic-fallback-token'}, event: event(), fetchImpl: async url => {
    if (url.endsWith('/git/ref/heads/main')) return json(main());
    pages++;
    assert.equal(new URL(url).searchParams.get('page'), String(pages));
    return pages === 1 ? json(Array.from({length: 100}, (_, i) => ({...promotion(), number: i + 1, merged_at: null})),
      {headers: {'content-type': 'application/json', link: '<https://attacker.invalid/secret>; rel="next"'}}) : json([promotion()]);
  }});
  assert.equal(pages, 2); assert.equal(result.pullRequest, 42);
});

test('stale main and main moving during verification both block production', async () => {
  for (const initiallyStale of [true, false]) {
    let mainReads = 0, pages = 0;
    await assert.rejects(verifyPromotion({env: env(), event: event(), fetchImpl: async url => {
      if (url.endsWith('/git/ref/heads/main')) return json(main(++mainReads > (initiallyStale ? 0 : 1) ? newer : sha));
      pages++; return json([promotion()]);
    }}), /no longer the current/);
    assert.equal(pages, initiallyStale ? 0 : 1);
  }
});

test('no matching PR, malformed lists and exhausted pagination fail closed', async () => {
  for (const value of [[], [ {...promotion(), merged_at: null} ], {}, Array(101).fill(promotion())]) {
    await assert.rejects(verifyPromotion({env: env(), event: event(), fetchImpl: async url => json(url.endsWith('/git/ref/heads/main') ? main() : value)}));
  }
  let pages = 0;
  await assert.rejects(verifyPromotion({env: env(), event: event(), fetchImpl: async url => {
    if (url.endsWith('/git/ref/heads/main')) return json(main());
    pages++; return json(Array(100).fill({...promotion(), merge_commit_sha: newer}));
  }}), /page limit/);
  assert.equal(pages, 10);
});

test('forged event and missing token never perform network requests', async () => {
  let calls = 0;
  const fetchImpl = async () => {calls++; throw Error('Must not fetch');};
  await assert.rejects(verifyPromotion({env: env(), event: {...event(), after: newer}, fetchImpl}));
  await assert.rejects(verifyPromotion({env: {...env(), GH_TOKEN: ''}, event: event(), fetchImpl}));
  assert.equal(calls, 0);
});

test('API errors, redirects and invalid JSON never expose response bodies or token exceptions', async () => {
  const sensitive = 'synthetic-secret-response-body';
  const badResponses = [
    () => new Response(sensitive, {status: 403}), () => new Response(sensitive, {status: 429}),
    () => new Response(sensitive, {status: 500}), () => new Response(sensitive, {status: 200, headers: {'content-type': 'text/html'}}),
    () => new Response(sensitive, {headers: {'content-type': 'application/json'}}),
    () => {const response = json({}); Object.defineProperty(response, 'redirected', {value: true}); return response;},
    () => {const response = json({}); Object.defineProperty(response, 'url', {value: 'https://attacker.invalid/secret'}); return response;},
    () => {throw Error(sensitive);}
  ];
  for (const make of badResponses) {
    const api = createGitHubAPI('synthetic-token', {fetchImpl: async () => make()});
    await assert.rejects(api.main(), error => !error.message.includes(sensitive) && !error.message.includes('synthetic-token'));
  }
});

test('API bounds both advertised length and actual streamed response bytes', async () => {
  const big = 'x'.repeat(1024 * 1024 + 1);
  for (const response of [
    new Response('{}', {headers: {'content-type': 'application/json', 'content-length': String(big.length)}}),
    new Response(big, {headers: {'content-type': 'application/json'}}),
    new Response('{}', {headers: {'content-type': 'application/json', 'content-length': '-1'}})
  ]) {
    await assert.rejects(createGitHubAPI('synthetic', {fetchImpl: async () => response}).main(), /permitted size/);
  }
});

test('a rejected HTTP response cancels its unread body', async () => {
  let cancelled = false;
  const response = new Response(new ReadableStream({cancel() {cancelled = true;}}), {status: 403});
  await assert.rejects(createGitHubAPI('synthetic', {fetchImpl: async () => response}).main());
  assert.equal(cancelled, true);
});

test('request and overall deadlines abort verification even if an injected fetch stalls', async () => {
  let signal;
  const stalled = async (_, options) => {signal = options.signal; return new Promise(() => {});};
  await assert.rejects(createGitHubAPI('synthetic', {fetchImpl: stalled, timeoutMs: 15}).main(), /deadline/);
  assert.equal(signal.aborted, true);
  await assert.rejects(verifyPromotion({env: env(), event: event(), fetchImpl: stalled, totalTimeoutMs: 15}), /deadline/);
  assert.equal(signal.aborted, true);
});

test('lookup parameters cannot change the GitHub host or escape the fixed repository', async () => {
  const api = createGitHubAPI('synthetic', {fetchImpl: async () => {throw Error('Unexpected call');}});
  for (const candidate of ['../main', 'https://attacker.invalid', 'a'.repeat(41)]) assert.throws(() => api.pulls(candidate, 1));
  for (const page of [0, 11, '1', 1.5]) assert.throws(() => api.pulls(sha, page));
  for (const token of ['', undefined, 'token\nheader']) assert.throws(() => createGitHubAPI(token));
});

test('event-file reading rejects oversized, malformed or missing files without leaking contents', async t => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'household-release-guard-'));
  t.after(async () => {
    assert.equal(path.dirname(directory), os.tmpdir());
    assert.ok(path.basename(directory).startsWith('household-release-guard-'));
    await fs.rm(directory, {recursive: true, force: true});
  });
  const file = path.join(directory, 'event.json');
  await fs.writeFile(file, JSON.stringify(event()));
  assert.deepEqual(await readEvent(file), event());
  await fs.writeFile(file, 'synthetic-event-secret');
  await assert.rejects(readEvent(file), error => !error.message.includes('synthetic-event-secret'));
  await fs.writeFile(file, Buffer.alloc(2 * 1024 * 1024 + 1));
  await assert.rejects(readEvent(file), /too large/);
  await assert.rejects(readEvent(path.join(directory, 'missing')), /cannot be read/);
});

test('CLI exits nonzero with sanitized refusal before reading an untrusted event or contacting GitHub', () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, 'release-guard.cjs')], {encoding: 'utf8', timeout: 5000,
    windowsHide: true, env: {...env(), GITHUB_REF: 'refs/heads/dev', GITHUB_EVENT_PATH: 'synthetic-private-path'}});
  assert.equal(result.status, 1); assert.match(result.stderr, /Release refused:/);
  assert.equal(result.stdout, ''); assert.ok(!result.stderr.includes('synthetic-private-path'));
  assert.ok(!result.stderr.includes('synthetic-token-never-print'));
});
