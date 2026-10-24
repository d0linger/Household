'use strict';
const fs = require('node:fs/promises');

// Bounded single-household workload, separate from UI timings and from a production SLA.
module.exports = async function check(page, bootstrap) {
  const result = await page.evaluate(async states => {
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    const durations = {seed: [], reads: [], writes: []}, statuses = {};
    async function request(phase, url, data) {
      const start = performance.now();
      const response = await fetch(url, data ? {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({data, generation: states.shopping.generation})} : {});
      const body = await response.json();
      durations[phase].push(performance.now() - start);
      statuses[response.status] = (statuses[response.status] || 0) + 1;
      if (!response.ok) throw Error(`Load request failed: ${response.status}`);
      return body;
    }
    async function batch(count, concurrency, run) {
      let next = 0;
      await Promise.all(Array.from({length: concurrency}, async () => {while (next < count) {const index = next++; await run(index);}}));
    }
    const list = await request('seed', '/api/v1/m/shopping/lists', {name: 'Synthetic load ' + Date.now()});
    const started = performance.now();
    await batch(1000, 4, index => request('seed', '/api/v1/m/shopping/items', {list_id: list.id, name: `Load item ${String(index).padStart(4,'0')}`, quantity: '2,50', unit_code: 'kg'}));
    const reads = ['/api/v1/m/shopping/items?offset=0', '/api/v1/m/shopping/items?offset=500', '/api/v1/core/search?q=Load', '/api/v1/m/fuel/reports/summary', '/api/v1/core/bootstrap'];
    const measured = performance.now();
    await Promise.all([
      batch(300, 10, index => request('reads', reads[index % reads.length])),
      batch(50, 2, index => request('writes', '/api/v1/m/shopping/items', {list_id: list.id, name: `Concurrent load ${index}`, quantity: '1', unit_code: 'piece'})),
    ]);
    const elapsedMS = performance.now() - measured;
    const summary = values => {values.sort((a,b)=>a-b);return {count: values.length, p50MS: values[Math.ceil(values.length*.5)-1], p95MS: values[Math.ceil(values.length*.95)-1], maxMS: values.at(-1)};};
    return {fixtureRows: 1000, readConcurrency: 10, writeConcurrency: 2, elapsedMS, totalMS: performance.now()-started, requestsPerSecond: 350/(elapsedMS/1000), reads: summary(durations.reads), writes: summary(durations.writes), statuses};
  }, bootstrap.states);
  result.observedAt = new Date().toISOString();
  result.scope = 'Isolated review server, synthetic single-household workload; not a production capacity guarantee';
  await fs.mkdir('outputs/qa', {recursive:true});
  await fs.writeFile('outputs/qa/load-report.json', JSON.stringify(result,null,2)+'\n');
  if (result.reads.p95MS > 1000 || result.writes.p95MS > 1500) throw Error('Local workload exceeded read/write p95 budgets; inspect outputs/qa/load-report.json');
  return result;
};
