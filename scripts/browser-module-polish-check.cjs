'use strict';

// Read-only presentation acceptance against the isolated synthetic stack.
// The sole POST permitted is Chores' non-persisting preview; no login or fixture write.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const {runChoresEditorPolishChecks} = require('./browser-chores-editor-polish-check.cjs');
const origin = new URL(process.env.HOUSEHOLD_BROWSER_URL || 'http://127.0.0.1:8101');
if (!['http://localhost:8101', 'http://127.0.0.1:8101'].includes(origin.origin) || origin.pathname !== '/' || origin.search || origin.hash || origin.username || origin.password) throw Error('Module polish requires isolated HTTP loopback 8101');
const output = path.resolve('outputs/deep-polish-review/acceptance');
const report = {scope: 'Synthetic presentation and non-persisting Chores previews only; no record writes or live household access.', checks: [], failures: [], screenshots: [], accessibility: [], searchMeasurements: [], presentationOverrides: [], blockedRequests: [], pageErrors: []};
const modes = ['light', 'dark', 'pink-light', 'pink-dark'];
const widths = [320, 390, 768, 1440];
let browser, context;
function must(value, message) {if (!value) throw Error(message); report.checks.push(message);}
async function check(name, run) {
  try {await run();} catch (error) {report.failures.push({name, message: String(error.message || error).slice(0, 1500)}); console.error('FAIL ' + name);}
}
async function visit(page, route, ready) {
  const response = await page.goto(origin.origin + route, {waitUntil: 'load'});
  must(response?.status() === 200 && new URL(page.url()).pathname === route.split('?')[0], 'Authenticated route ' + route.split('?')[0]);
  if (ready) await page.locator(ready).waitFor({state: 'visible'});
}
async function json(page, route) {
  return page.evaluate(async route => {const response = await fetch(route, {cache: 'no-store'}); if (!response.ok) throw Error('Synthetic discovery failed: ' + response.status); return response.json();}, route);
}
async function screenshot(page, name) {
  await page.screenshot({path: path.join(output, name + '.png'), fullPage: true, animations: 'disabled'});
  report.screenshots.push(name + '.png');
}
async function noOverflow(page, name) {must(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), name + ' stays within the viewport');}
async function axe(page, name) {
  const result = await new AxeBuilder({page}).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
  report.accessibility.push({name, violations: result.violations.map(v => ({id: v.id, impact: v.impact, targets: v.nodes.map(n => n.target)}))});
  must(!result.violations.length, name + ' has no Axe violations');
}
async function recipeAndSearch(page, recipe) {
  for (const mode of modes) {
    await check('Recipe and generic controls: ' + mode, async () => {
    await page.evaluate(mode => {localStorage.setItem('household-theme', mode);}, mode);
    await visit(page, '/m/recipes/recipes/' + recipe.id + '/workspace', '#recipe-form:not([hidden])');
    for (const width of widths) {
      await page.setViewportSize({width, height: width >= 768 ? 1000 : 844});
      await page.waitForFunction(mode => document.documentElement.dataset.theme === mode, mode);
      const sizes = await page.locator('#recipe-workspace > .actions a').evaluateAll(nodes => nodes.map(node => {const r = node.getBoundingClientRect(); return {text: node.textContent, width: r.width, height: r.height};}));
      must(sizes.length === 6 && sizes.every(s => s.width >= 120 && s.height < 160), mode + '/' + width + ' Recipe actions have readable width and height');
      await noOverflow(page, mode + '/' + width + ' Recipe');
      if (width === 320) await screenshot(page, 'recipe-' + mode + '-320');
    }
    await visit(page, '/m/mealplan/plans', '.search button');
    for (const width of widths) {
      await page.setViewportSize({width, height: 844});
      const search = await page.locator('.search button').evaluate(button => {const range = document.createRange(); range.selectNodeContents(button); const r = button.getBoundingClientRect(); return {lines: range.getClientRects().length, width: r.width, height: r.height};});
      report.searchMeasurements.push({mode, viewport: width, ...search});
      if (search.lines !== 1) await screenshot(page, 'search-failure-' + mode + '-' + width);
      must(search.lines === 1, mode + '/' + width + ' generic search label remains one line');
      await noOverflow(page, mode + '/' + width + ' generic search');
    }
    });
  }
  await visit(page, '/m/recipes/recipes/' + recipe.id + '/workspace', '#recipe-form:not([hidden])');
  await page.setViewportSize({width: 320, height: 844});
  must(!await page.locator('.recipe-contents').evaluate(node => node.open), 'Recipe contents start collapsed');
  await page.locator('.recipe-contents summary').focus();
  await page.keyboard.press('Enter');
  for (const id of ['recipe-basics-heading', 'recipe-ingredients-heading', 'recipe-steps-heading', 'recipe-tags-heading', 'recipe-save-heading']) {
    await page.locator('.recipe-section-nav a[href="#' + id + '"]').focus();
    await page.keyboard.press('Enter');
    must(await page.locator('#' + id).evaluate(node => {const r = node.getBoundingClientRect(); return node === document.activeElement && r.top >= 64 && r.top < innerHeight - 80;}), 'Native Recipe jump focuses visible ' + id);
  }
}
async function reports(page) {
  for (const module of ['fuel', 'meters']) {
    await page.setViewportSize({width: 390, height: 844});
    const endpoint = '/api/v1/m/' + module + '/reports/summary';
    let failNext = true;
    const intercept = async route => {
      if (failNext) {failNext = false; return route.fulfill({status: 503, contentType: 'application/json', body: JSON.stringify({error: 'Synthetische Auswertung vorübergehend nicht verfügbar.'})});}
      return route.continue();
    };
    await page.route(origin.origin + endpoint, intercept);
    try {
      await visit(page, '/m/' + module + '/reports', '#report[aria-busy=false]');
      must(await page.locator('#report [role=status]').innerText() === 'Auswertung nicht verfügbar.', module + ' failure keeps persistent status');
      must(await page.locator('#report [role=alert]').isVisible(), module + ' failure has a visible error');
      await page.getByRole('button', {name: 'Erneut laden', exact: true}).click();
      await page.locator('#report[aria-busy=false] .table-scroll tbody tr').first().waitFor();
      must(await page.locator('#report [role=status]').innerText() === 'Auswertung geladen.', module + ' retry reaches persistent success');
      must(await page.locator('#report [role=alert]').count() === 0, module + ' retry removes stale error');
      const region = page.locator('#report .table-scroll').first();
      await region.focus(); await page.keyboard.press('ArrowRight');
      await page.waitForFunction(() => document.querySelector('#report .table-scroll').scrollLeft > 0);
      must(true, module + ' report scrolls with ArrowRight');
      for (const width of [390, 1440, 2560]) {
        await page.setViewportSize({width, height: 1000});
        await page.waitForFunction(() => [...document.querySelectorAll('#report .table-scroll')].every(wrap => {const hint = document.getElementById(wrap.getAttribute('aria-describedby')); return hint && hint.hidden === !(wrap.scrollWidth > wrap.clientWidth + 1);}));
        const measured = await page.locator('#report .table-scroll').evaluateAll(nodes => nodes.map(node => ({overflow: node.scrollWidth > node.clientWidth + 1, hint: !document.getElementById(node.getAttribute('aria-describedby')).hidden})));
        must(measured.length > 0 && measured.every(x => x.hint === x.overflow), module + '/' + width + ' visible hints match actual table overflow');
        if (width === 390) must(measured.every(x => x.overflow), module + ' populated mobile report exercises real overflow');
        if (module === 'meters' && width === 2560) must(measured.some(x => !x.overflow), 'Wide Meters report hides a no-longer-needed hint');
      }
      await page.setViewportSize({width: 390, height: 844});
      await screenshot(page, module + '-report-390');
    } finally {await page.unroute(origin.origin + endpoint, intercept);}
  }
}
async function meterWorkspace(page) {
  await page.setViewportSize({width: 390, height: 844});
  await visit(page, '/m/meters/workspace/overview', '#meters-workspace[aria-busy=false]');
  const overview = await json(page, '/api/v1/meters/overview');
  const syntheticID = '00000000-0000-4000-8000-000000000041';
  const register = overview.registers[0] || {id: syntheticID, meter_id: syntheticID, name: 'Synthetische Darstellungsprobe', unit_code: 'kWh', direction: 'import', input_mode: 'manual', measurement_kind: 'cumulative', timezone: 'Europe/Vienna', version: 1};
  const overviewRoute = origin.origin + '/api/v1/meters/overview';
  const historyRoute = origin.origin + '/api/v1/meters/history?**';
  let historyOverride = false;
  if (!overview.registers.length) {
    report.presentationOverrides.push('Meters overview: isolated presentation-only register; no server record created.');
    await page.route(overviewRoute, route => route.fulfill({json: {...overview, meters: [{id: syntheticID, name: register.name, unit_code: 'kWh'}], registers: [register]}}));
  }
  await page.route(historyRoute, async route => {
    let body = {rows: []};
    if (overview.registers.length) {const response = await route.fetch(); if (!response.ok()) throw Error('Meters history GET failed'); body = await response.json();}
    if (body.rows.length) return route.fulfill({json: body});
    historyOverride = true;
    return route.fulfill({json: {rows: [{id: 'r/' + syntheticID, measured_at: new Date().toISOString(), value: '125.123456', kind: 'actual', origin: 'manual'}], next_cursor: ''}});
  });
  try {
    await visit(page, '/m/meters/workspace/history?register=' + register.id, '.energy-table-wrap table tbody tr');
    const region = page.locator('.energy-table-wrap').first();
    must(await region.getAttribute('tabindex') === '0' && await region.getAttribute('role') === 'region' && !!await region.getAttribute('aria-label'), 'Meters workspace table is a named keyboard region');
    await region.focus(); await page.keyboard.press('ArrowRight');
    await page.waitForFunction(() => document.querySelector('.energy-table-wrap').scrollLeft > 0);
    must(true, 'Meters workspace table scrolls with ArrowRight');
  } finally {await page.unroute(historyRoute); if (!overview.registers.length) await page.unroute(overviewRoute);}
  if (historyOverride) report.presentationOverrides.push('Meters history: synthetic GET row for presentation/keyboard coverage only; no persisted reading.');
  await visit(page, '/m/meters/workspace/settings', '#meters-workspace[aria-busy=false]');
  await page.getByRole('button', {name: 'Zähler anlegen', exact: true}).click();
  const distances = await page.locator('#energy-editor > form > button').evaluateAll(nodes => nodes.map(node => {const r = node.getBoundingClientRect(); return {left:r.left,right:r.right,top:r.top,bottom:r.bottom};}));
  must(distances.length === 2 && (distances[1].left - distances[0].right >= 10 || distances[1].top - distances[0].bottom >= 10), 'Meter form save/discard controls have visible separation');
  await page.getByRole('button', {name: 'Eingaben verwerfen', exact: true}).click();
}
async function financeDocuments(page) {
  for (const width of [768, 820, 1024]) {
    await page.setViewportSize({width, height: 1000});
    await visit(page, '/m/documents/workspace/review', '#doc-content form');
    await noOverflow(page, 'Documents editor/' + width);
    must(await page.locator('.doc-review').evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').length === 1), 'Documents editor/' + width + ' uses one column');
    must(await page.locator('.doc-review input,.doc-review textarea').evaluateAll(nodes => nodes.filter(n => n.getClientRects().length).every(n => n.getBoundingClientRect().right <= innerWidth + 1)), 'Documents editor/' + width + ' fields fit');
  }
  await screenshot(page, 'documents-editor-1024');
  await page.setViewportSize({width: 390, height: 844});
  await visit(page, '/m/documents/workspace/library', '#documents-workspace[aria-busy=false]');
  must((await page.locator('#doc-status').innerText()).includes('Ansicht geladen.'), 'Empty Documents view finishes loading');
  must(await page.locator('.doc-selection').count() === 0, 'Empty Documents view omits selection controls');
  for (const selector of ['.doc-filter-options', '.doc-view-options']) must(await page.locator(selector).getAttribute('open') === null, 'Documents ' + selector + ' starts collapsed');
  for (const name of ['search', 'due_before']) must(await page.locator('#doc-filters [name=' + name + ']').isVisible(), 'Primary Documents ' + name + ' remains visible');
  for (const name of ['document_type', 'state', 'issuer', 'category']) must(await page.locator('#doc-filters [name=' + name + ']').evaluate(node => !node.disabled && !!node.form), 'Collapsed Documents ' + name + ' retains form association');
  await page.locator('.doc-filter-options summary').click();
  await page.locator('#doc-filters [name=document_type]').selectOption('invoice');
  await page.locator('#doc-filters [name=issuer]').fill('Synthetic filter');
  await page.locator('.doc-filter-options summary').click();
  const request = page.waitForRequest(request => new URL(request.url()).pathname === '/api/v1/documents/library' && request.method() === 'GET');
  await page.locator('#doc-filters form').getByRole('button', {name: 'Filter anwenden', exact: true}).click();
  const params = new URL((await request).url()).searchParams;
  must(params.get('document_type') === 'invoice' && params.get('issuer') === 'Synthetic filter', 'Collapsed Documents filters keep selected GET values');
  await page.waitForFunction(() => document.querySelector('#doc-status').textContent.includes('Ansicht geladen.'));
  must((await page.locator('.doc-filter-options summary').innerText()).includes('2 aktiv'), 'Documents filter summary reports active choices');
  await page.locator('.doc-view-options summary').click();
  for (const name of ['view', 'view_name']) must(await page.locator('#doc-filters [name=' + name + ']').isVisible(), 'Disclosed Documents ' + name + ' is available');
  must(await page.getByRole('button', {name: 'Ansicht speichern', exact: true}).isVisible(), 'View save remains available without submitting');
  await page.setViewportSize({width: 320, height: 844});
  await visit(page, '/m/expenses/workspace/entries', '#finance-workspace[aria-busy=false]');
  const summary = page.locator('#finance-summary > details.finance-summary-disclosure');
  must(await summary.getAttribute('open') === null, 'Finance entries summary starts collapsed');
  must(await page.locator('#finance-selection-scope').isVisible(), 'Finance scope stays visible outside evaluation');
  await summary.locator('summary').click(); must(await summary.locator('dl').isVisible(), 'Finance complete evaluation remains available');
  await page.locator('#finance-new-entry').click();
  const payer = page.getByRole('combobox', {name: 'Bezahlt von · unabhängig von Begünstigten', exact: true});
  await payer.waitFor();
  must(await payer.evaluate(node => node.labels.length === 1 && node.labels[0].querySelectorAll('input,select,textarea,button').length === 1 && node.closest('.reference-field').querySelector('.reference-tools').parentElement === node.closest('.reference-field')), 'Finance payer has one unambiguous label and sibling search tools');
  for (const selector of ['#finance-entry-current-actions', '.finance-receipt-switch']) must(await page.locator(selector).getAttribute('role') === 'group', selector + ' uses group semantics');
  for (const selector of ['#finance-filters .actions', '#finance-booking-fields > .actions']) {
    must(await page.locator(selector).evaluate(group => {const rects = [...group.children].filter(node => node.getClientRects().length).map(node => node.getBoundingClientRect()); return rects.length >= 2 && rects.every((rect, index) => Math.abs(rect.left - rects[0].left) <= 1 && Math.abs(rect.right - rects[0].right) <= 1 && (index === 0 || rect.top >= rects[index - 1].bottom));}), selector + ' forms one readable column at 320px');
  }
  await noOverflow(page, 'Finance booking editor/320'); await axe(page, 'finance-editor-320'); await screenshot(page, 'finance-editor-320');
  await visit(page, '/m/expenses/workspace/accounts', '#finance-workspace[aria-busy=false]');
  await page.locator('#finance-extra-filters summary').click();
  for (const name of ['person', 'category']) must(await page.locator('#finance-filters [name=' + name + ']').evaluate(node => node.closest('.reference-field').hidden && !node.closest('.reference-field').querySelector('.reference-tools').getClientRects().length), 'Finance accounts hides entire ' + name + ' reference controls');
  must(await page.locator('#finance-filters [name=account]').isVisible(), 'Finance accounts keeps account selection visible');
}
(async () => {
  if (!process.env.HOUSEHOLD_BROWSER_HANDOFF) throw Error('Prepared synthetic HOUSEHOLD_BROWSER_HANDOFF is required');
  const privateRoot = await fs.realpath(path.resolve('.local'));
  const handoff = await fs.realpath(path.resolve(process.env.HOUSEHOLD_BROWSER_HANDOFF));
  if (!handoff.startsWith(privateRoot + path.sep)) throw Error('Handoff must remain inside private workspace .local');
  const resources = JSON.parse(await fs.readFile(path.join(path.dirname(handoff), 'resources.json'), 'utf8'));
  if (resources.app_port !== 8101 || resources.container_name !== 'household-ocean-app-review-' + resources.id || path.resolve(resources.directory) !== path.dirname(handoff)) throw Error('Handoff ownership does not match the isolated synthetic stack');
  const state = JSON.parse(await fs.readFile(handoff, 'utf8'));
  if (!state.cookies?.length) throw Error('Prepared authenticated synthetic session is missing');
  await fs.mkdir(output, {recursive: true});
  browser = await chromium.launch({headless: true, ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : process.platform === 'win32' ? {channel: 'msedge'} : {}), args: ['--disable-background-networking', '--disable-component-update', '--no-first-run']});
  try {
    context = await browser.newContext({storageState: state, viewport: {width: 390, height: 844}, colorScheme: 'light', reducedMotion: 'reduce', serviceWorkers: 'block'});
    context.setDefaultTimeout(10000); context.setDefaultNavigationTimeout(15000);
    await context.route('**/*', route => {
      const request = route.request(), url = new URL(request.url());
      const preview = request.method() === 'POST' && url.pathname === '/api/v1/chores/series/preview';
      if (url.origin !== origin.origin || (!['GET', 'HEAD'].includes(request.method()) && !preview)) {report.blockedRequests.push({method: request.method(), path: url.origin + url.pathname}); return route.abort();}
      return route.continue();
    });
    const page = await context.newPage();
    page.on('pageerror', error => report.pageErrors.push(String(error).slice(0, 1000)));
    await visit(page, '/m/recipes/recipes', 'h1');
    const data = await json(page, '/api/v1/m/recipes/recipes');
    const recipe = data.records?.[0];
    must(recipe && /^[0-9a-f-]{36}$/i.test(recipe.id), 'Actual synthetic recipe discovered through list API');
    await check('Recipe and generic controls matrix', () => recipeAndSearch(page, recipe));
    await check('Generic reports scrolling and retry', () => reports(page));
    await check('Meters keyboard region and editor spacing', () => meterWorkspace(page));
    await check('Finance and Documents polish', () => financeDocuments(page));
    await check('Chores conditional editor and unsaved changes', async () => {const result = await runChoresEditorPolishChecks(page, {preview: true, persist: false}); report.checks.push(...result.checks); report.chores = {persisted: result.persisted, notRun: result.notRun};});
    must(report.blockedRequests.length === 0, 'No external or persistent mutation requests attempted');
    must(report.pageErrors.length === 0, 'No JavaScript runtime errors');
  } catch (error) {report.failures.push({name: 'Harness', message: String(error.message || error).slice(0, 1500)});}
  finally {await context?.close(); await browser?.close(); report.passed = report.failures.length === 0; await fs.writeFile(path.join(output, 'browser-module-polish-report.json'), JSON.stringify(report, null, 2) + '\n');}
  console.log(JSON.stringify({checks: report.checks.length, failures: report.failures.length, screenshots: report.screenshots.length, accessibility: report.accessibility.length, passed: report.passed}));
  if (!report.passed) process.exitCode = 1;
})().catch(error => {console.error(error.message); process.exitCode = 1;});
