'use strict';

const fs = require('node:fs/promises');

const REPOSITORY = 'd0linger/Household';
const REF = 'refs/heads/main';
const API = 'https://api.github.com/repos/' + REPOSITORY;
const MAX_RESPONSE_BYTES = 1024 * 1024;
const MAX_EVENT_BYTES = 2 * 1024 * 1024;
const PAGE_SIZE = 100;
const MAX_PAGES = 10;
const SHA = /^[a-f0-9]{40}$/;

class ReleaseRefused extends Error {}

function refuse(message) { throw new ReleaseRefused(message); }

function validateEnvironment(env) {
  if (env.GITHUB_REPOSITORY !== REPOSITORY || env.GITHUB_EVENT_NAME !== 'push' || env.GITHUB_REF !== REF) {
    refuse('Only a push to Household main can publish production images');
  }
  if (typeof env.GITHUB_SHA !== 'string' || !SHA.test(env.GITHUB_SHA)) refuse('Candidate commit identity is invalid');
  return env.GITHUB_SHA;
}

function validateEvent(env, event) {
  const sha = validateEnvironment(env);
  if (!event || typeof event !== 'object' || Array.isArray(event) || event.ref !== REF || event.after !== sha
      || event.deleted !== false || event.created !== false || event.forced !== false
      || event.repository?.full_name !== REPOSITORY || event.head_commit?.id !== sha) {
    refuse('Push event does not describe the expected non-forced main update');
  }
  return sha;
}

function isPromotionPR(pr, sha) {
  return !!pr && Number.isSafeInteger(pr.number) && pr.number > 0 && pr.state === 'closed'
    && typeof pr.merged_at === 'string' && Number.isFinite(Date.parse(pr.merged_at))
    && pr.merge_commit_sha === sha && pr.base?.ref === 'main' && pr.head?.ref === 'dev'
    && pr.base?.repo?.full_name === REPOSITORY && pr.head?.repo?.full_name === REPOSITORY;
}

async function boundedJSON(response) {
  if (response.status !== 200) refuse('GitHub API did not return a successful response');
  if (!/^application\/(?:json|vnd\.github\+json)(?:\s*;|$)/i.test(response.headers.get('content-type') || '')) {
    refuse('GitHub API response is not JSON');
  }
  const length = response.headers.get('content-length');
  if (length !== null && (!/^\d+$/.test(length) || Number(length) > MAX_RESPONSE_BYTES)) {
    refuse('GitHub API response exceeds the permitted size');
  }
  if (!response.body || typeof response.body.getReader !== 'function') refuse('GitHub API response body is missing');
  const reader = response.body.getReader(), chunks = [];
  let size = 0;
  try {
    while (true) {
      const chunk = await reader.read();
      if (chunk.done) break;
      size += chunk.value.byteLength;
      if (size > MAX_RESPONSE_BYTES) {
        await reader.cancel();
        refuse('GitHub API response exceeds the permitted size');
      }
      chunks.push(Buffer.from(chunk.value));
    }
  } finally { reader.releaseLock(); }
  try { return JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(Buffer.concat(chunks, size))); }
  catch { refuse('GitHub API returned invalid JSON'); }
}

function createGitHubAPI(token, {fetchImpl = globalThis.fetch, timeoutMs = 10000, signal} = {}) {
  if (typeof token !== 'string' || !token || token.trim() !== token || /[\r\n]/.test(token)) {
    refuse('A GitHub Actions token is required');
  }
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 10000 || typeof fetchImpl !== 'function') {
    refuse('Invalid bounded GitHub API configuration');
  }
  async function request(suffix) {
    const url = API + suffix, controller = new AbortController();
    let timer, abortHandler;
    const work = async () => {
      const response = await fetchImpl(url, {method: 'GET', redirect: 'error', credentials: 'omit', cache: 'no-store',
        signal: controller.signal, headers: {Accept: 'application/vnd.github+json', Authorization: 'Bearer ' + token,
          'X-GitHub-Api-Version': '2026-03-10', 'User-Agent': 'Household-release-guard'}});
      try {
        if (response.redirected || (response.url && response.url !== url)) refuse('GitHub API redirect is not permitted');
        return await boundedJSON(response);
      } finally {
        // Stop reading rejected HTTP/redirect bodies as well as accepted streams.
        if (response.body && !response.body.locked) await response.body.cancel().catch(() => {});
      }
    };
    try {
      const deadline = new Promise((_, reject) => {
        const abort = () => {controller.abort(); reject(new ReleaseRefused('GitHub API verification exceeded its deadline'));};
        timer = setTimeout(abort, timeoutMs);
        if (signal) {
          abortHandler = abort;
          signal.addEventListener('abort', abortHandler, {once: true});
          if (signal.aborted) abort();
        }
      });
      return await Promise.race([deadline, work()]);
    } catch (error) {
      if (error instanceof ReleaseRefused) throw error;
      refuse('GitHub API verification failed');
    } finally {
      clearTimeout(timer);
      if (abortHandler) signal.removeEventListener('abort', abortHandler);
    }
  }
  return Object.freeze({
    main: () => request('/git/ref/heads/main'),
    pulls: (sha, page) => {
      if (!SHA.test(sha) || !Number.isSafeInteger(page) || page < 1 || page > MAX_PAGES) refuse('Invalid promotion lookup');
      return request('/commits/' + sha + '/pulls?per_page=' + PAGE_SIZE + '&page=' + page);
    }
  });
}

async function verifyPromotion({env, event, fetchImpl = globalThis.fetch, timeoutMs = 10000, totalTimeoutMs = 30000}) {
  const sha = validateEvent(env, event);
  if (!Number.isInteger(totalTimeoutMs) || totalTimeoutMs < 1 || totalTimeoutMs > 30000) refuse('Invalid total verification deadline');
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), totalTimeoutMs);
  try {
    const api = createGitHubAPI(env.GH_TOKEN || env.GITHUB_TOKEN, {fetchImpl, timeoutMs, signal: controller.signal});
    const assertCurrent = async () => {
      const current = await api.main();
      if (current?.ref !== REF || current.object?.type !== 'commit' || current.object?.sha !== sha) {
        refuse('Candidate is no longer the current Household main commit');
      }
    };
    await assertCurrent();
    for (let page = 1; page <= MAX_PAGES; page++) {
      const pulls = await api.pulls(sha, page);
      if (!Array.isArray(pulls) || pulls.length > PAGE_SIZE) refuse('GitHub API returned an invalid pull request list');
      const promotion = pulls.find(pr => isPromotionPR(pr, sha));
      if (promotion) {
        await assertCurrent();
        return {repository: REPOSITORY, sha, pullRequest: promotion.number};
      }
      if (pulls.length < PAGE_SIZE) refuse('No merged same-repository dev-to-main pull request matches this commit');
    }
    refuse('Promotion lookup exceeded its bounded page limit');
  } finally { clearTimeout(timer); }
}

async function readEvent(file) {
  let handle;
  try {
    handle = await fs.open(file, 'r');
    const info = await handle.stat();
    if (!info.isFile() || info.size > MAX_EVENT_BYTES) refuse('Push event file is invalid or too large');
    const bytes = Buffer.alloc(MAX_EVENT_BYTES + 1);
    let length = 0;
    while (length < bytes.length) {
      const {bytesRead} = await handle.read(bytes, length, bytes.length - length, null);
      if (!bytesRead) break;
      length += bytesRead;
    }
    if (length > MAX_EVENT_BYTES) refuse('Push event file is invalid or too large');
    return JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(bytes.subarray(0, length)));
  } catch (error) {
    if (error instanceof ReleaseRefused) throw error;
    refuse('Push event file cannot be read as JSON');
  } finally { await handle?.close(); }
}

module.exports = {validateEnvironment, validateEvent, isPromotionPR, createGitHubAPI, verifyPromotion, readEvent};
if (require.main === module) {
  (async () => {
    validateEnvironment(process.env);
    const event = await readEvent(process.env.GITHUB_EVENT_PATH);
    const result = await verifyPromotion({env: process.env, event});
    console.log('Verified current Household main promotion from dev via PR #' + result.pullRequest + ' at ' + result.sha);
  })().catch(error => {
    console.error('Release refused: ' + (error instanceof ReleaseRefused ? error.message : 'Unexpected verification failure'));
    process.exitCode = 1;
  });
}
