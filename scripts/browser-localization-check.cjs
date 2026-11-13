'use strict';

// Acceptance uses only the isolated synthetic stack. The only persisted change
// is this fixture account's language preference, restored in finally.
const fs = require('node:fs/promises');
const path = require('node:path');
const assert = require('node:assert/strict');
const {chromium} = require('playwright');
const {catalog} = require('./check-localization.cjs');
const english = catalog();
const origin = new URL(process.env.HOUSEHOLD_BROWSER_URL || 'http://127.0.0.1:8101');
if (!['http://localhost:8101', 'http://127.0.0.1:8101'].includes(origin.origin) || origin.pathname !== '/' || origin.search || origin.hash || origin.username || origin.password) throw Error('Localization acceptance requires isolated HTTP loopback 8101');
const output = path.resolve('outputs/i18n/browser');
const report = {scope: 'Synthetic account only; read-only module checks and reversible locale preference updates. No business record changes.', checks: [], failures: [], screenshots: [], blockedRequests: [], pageErrors: []};
let browser, context, publicContext, page, originalLocale;
function text(locale, source, ...values) {
  if (!Object.hasOwn(english, source)) throw Error('Uncataloged acceptance source: ' + source);
  let index = 0;
  return (locale === 'en' ? english[source] : source).replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(values[index++] ?? ''));
}
function must(condition, label) {if (!condition) throw Error(label); report.checks.push(label);}
async function check(name, run) {
  try {await run();} catch (error) {report.failures.push({name, message: String(error.message || error).slice(0, 1200)}); console.error('FAIL ' + name);}
}
async function json(route) {
  return page.evaluate(async route => {const response = await fetch(route, {cache: 'no-store'}); if (!response.ok) throw Error('Synthetic discovery failed: ' + response.status); return response.json();}, route);
}
async function setLocale(locale) {
  const result = await page.evaluate(async locale => {
    const response = await fetch('/api/v1/core/preferences', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': document.querySelector('meta[name="csrf-token"]').content}, body: JSON.stringify({locale})});
    return {status: response.status, body: await response.json()};
  }, locale);
  if (result.status !== 200 || !result.body.saved) throw Error('Fixture locale update failed');
}
async function visit(route, ready) {
  const response = await page.goto(origin.origin + route, {waitUntil: 'load'});
  must(response?.status() === 200 && new URL(page.url()).pathname === route.split('?')[0], 'Authenticated page ' + route.split('?')[0]);
  if (ready) await page.locator(ready).first().waitFor({state: 'visible'});
}
async function copy(selector, source, locale, attribute) {
  const item = page.locator(selector).first();
  const actual = attribute ? await item.getAttribute(attribute) : await item.textContent();
  assert.equal(actual.trim(), text(locale, source).trim(), selector + ' localized in ' + locale);
  report.checks.push(locale + ' copy: ' + selector);
}
async function fit(name) {must(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), name + ' fits the viewport');}
async function capture(name) {
  await page.screenshot({path: path.join(output, name + '.png'), fullPage: true, animations: 'disabled'});
  report.screenshots.push(name + '.png');
}
function protocol(descriptors) {
  return descriptors.map(({title, short, entities, ...module}) => ({...module, entities: entities.map(({title, fields, ...entity}) => ({...entity, fields: fields.map(({label, ...field}) => field)}))}));
}
const routes = [
  {id: 'shopping', route: '/m/shopping/lists', ready: '.record-list', heading: 'Einkaufsliste', controls: [['.search button', 'Suchen'], ['[data-open="create-dialog"]', '＋ Hinzufügen']], generic: 'lists'},
  {id: 'fuel', route: '/m/fuel/workspace/overview', ready: '#fm-status', heading: 'Fahrzeuge & Tanken', controls: [['#fm-refresh', 'Neu laden'], ['#fm-summary-title', 'Ausgewählter Datenstand']]},
  {id: 'recipes', route: '/m/recipes/recipes', ready: '.record-list', heading: 'Kochbuch', controls: [['.search button', 'Suchen'], ['.page-heading a.primary', 'Neues Rezept']]},
  {id: 'pantry', route: '/m/pantry/stock', ready: '#pantry-products:not([aria-busy])', heading: 'Vorratsbestand', controls: [['#pantry-refresh', 'Aktualisieren'], ['#pantry-filter input[name=q]', 'Name des Produkts', 'placeholder'], ['#pantry-products', 'Produktbestände', 'aria-label']]},
  {id: 'expenses', route: '/m/expenses/workspace/overview', ready: '#finance-workspace[aria-busy=false]', controls: [['#finance-new-entry', 'Buchung erfassen'], ['[data-finance-pane=accounts]', 'Konten'], ['#finance-filter-reset', 'Filter zurücksetzen']]},
  {id: 'chores', route: '/m/chores/workspace/today', ready: '#chores-workspace[aria-busy=false]', heading: 'Heute für mich', controls: [['.page-heading a.primary', 'Neue Serie']]},
  {id: 'meters', route: '/m/meters/workspace/overview', ready: '#meters-workspace[aria-busy=false]', heading: 'Zähler & Energie', controls: [['#energy-capture-link', 'Ablesen'], ['label[for=energy-register]', 'Zählerregister']]},
  {id: 'documents', route: '/m/documents/workspace/inbox', ready: '#documents-workspace[aria-busy=false]', heading: 'Dokumente & Fristen', controls: [['#doc-new', 'Neues Dokument'], ['[data-doc-pane=library]', 'Bibliothek'], ['[data-doc-pane=deadlines]', 'Fristen']]},
  {id: 'mealplan', route: '/m/mealplan/plans', ready: '.record-list', heading: 'Mahlzeitenplanung', controls: [['.search button', 'Suchen'], ['[data-open="create-dialog"]', '＋ Hinzufügen']], generic: 'plans'}
];
async function modulePages(locale, baseline) {
  const current = await json('/api/v1/core/bootstrap');
  assert.deepEqual(protocol(current.modules), protocol(baseline.modules), 'Locale preserves all module/entity/field identifiers, options and defaults');
  for (const module of baseline.modules) {
    const localized = current.modules.find(item => item.id === module.id);
    assert.equal(localized.title, text(locale, module.title));
    for (const entity of module.entities) {
      const rendered = localized.entities.find(item => item.id === entity.id);
      assert.equal(rendered.title, text(locale, entity.title));
      entity.fields.forEach((field, index) => assert.equal(rendered.fields[index].label, text(locale, field.label)));
    }
  }
  report.checks.push(locale + ' all nine module/entity/form descriptor labels with unchanged canonical values');
  for (const item of routes) await check(locale + ' ' + item.id, async () => {
    await visit(item.route, item.ready);
    if (item.id === 'fuel') await page.waitForFunction(() => /^\d+ /.test(document.querySelector('#fm-status')?.textContent || ''));
    assert.equal(await page.locator('html').getAttribute('lang'), locale === 'en' ? 'en-GB' : 'de-AT');
    assert.equal(await page.evaluate(() => HouseholdI18n.language()), locale);
    const scripts = await page.locator('script[src]').evaluateAll(nodes => nodes.map(node => ({src: node.src, defer: node.defer})));
    assert.equal(scripts.some(script => script.src.endsWith('/i18n-catalog.js')), locale === 'en');
    assert.ok(scripts.find(script => script.src.endsWith('/i18n.js'))?.defer);
    if (item.heading) await copy('main h1', item.heading, locale);
    for (const [selector, source, attribute] of item.controls) await copy(selector, source, locale, attribute);
    await copy('.skip', 'Zum Inhalt', locale);
    if (item.generic) {
      const entity = baseline.modules.find(module => module.id === item.id).entities.find(entity => entity.id === item.generic);
      assert.equal(await page.locator('#query').getAttribute('placeholder'), text(locale, '%s durchsuchen …', text(locale, entity.title)));
      await page.locator('[data-open="create-dialog"]').first().click();
      await page.locator('#create-dialog[open]').waitFor();
      await copy('#create-dialog .actions .primary', 'Speichern', locale);
      await copy('#create-dialog .actions [data-close]', 'Abbrechen', locale);
      await copy('#create-dialog .dialog-heading [data-close]', 'Schließen', locale, 'aria-label');
      const label = page.locator('#create-dialog label').first();
      must((await label.textContent()).includes(text(locale, entity.fields[0].label)), locale + ' ' + item.id + ' form field labels');
      await page.keyboard.press('Escape');
    }
    for (const width of [390, 1440]) {await page.setViewportSize({width, height: 900}); await fit(locale + ' ' + item.id + '/' + width);}
    await page.setViewportSize({width: 390, height: 844});
    if (locale === 'en') await capture(item.id + '-english-390');
    const disclosure = {
      fuel: ['.workspace-primary [data-pane=history]', '#fm-status', '[data-pane=charging]', 'Ladevorgänge'],
      chores: ['.workspace-primary [data-chores-pane=plan]', '#chores-workspace[aria-busy=false]', '[data-chores-pane=series]', 'Serien'],
      meters: ['.workspace-manage-link', '.workspace-management-grid', '[data-energy-pane=settings]', 'Einrichten'],
    }[item.id];
    if (disclosure) {
      await page.locator(disclosure[0]).click();
      await page.locator(disclosure[1]).waitFor();
      await copy(disclosure[2], disclosure[3], locale);
      must(await page.locator(disclosure[2]).isVisible(), locale + ' ' + item.id + ' contextual tool remains discoverable');
    }

  });
}
async function editorsAndContent(locale, fixtures) {
  await check(locale + ' shopping generated board and unchanged list title', async () => {
    await visit('/m/shopping/lists/' + fixtures.list.id, '#shopping-quick-add');
    await page.waitForFunction(initial => document.querySelector('#shopping-progress')?.textContent !== initial, text(locale, 'Artikel werden geladen …'));
    assert.equal(await page.locator('#shopping-list-title').textContent(), fixtures.list.name);
    await copy('#shopping-quick-add button.primary', 'Hinzufügen', locale);
    await copy('#shopping-quick-add input[name=name]', 'Zum Beispiel 2 × 500 g Nudeln', locale, 'placeholder');
    await copy('#shopping-progress-bar', 'Einkaufsfortschritt', locale, 'aria-label');
    await fit(locale + ' shopping generated board');
  });
  await check(locale + ' meal planning generated calendar and dialog', async () => {
    await visit('/m/mealplan/workspace/' + fixtures.plan.id + '?week=' + fixtures.plan.starts_on, '#planning-days .planning-day');
    assert.equal(await page.locator('#planning-title').textContent(), fixtures.plan.name);
    assert.equal(await page.locator('#planning-week-form input').inputValue(), fixtures.plan.starts_on);
    await copy('#planning-previous', 'Vorige Woche', locale);
    await copy('#planning-next', 'Nächste Woche', locale);
    await copy('#planning-days', 'Sieben Tage der ausgewählten Woche', locale, 'aria-label');
    must(await page.getByRole('button', {name: text(locale, 'Mahlzeit planen'), exact: true}).count() >= 7, locale + ' generated meal planning slots');
    await page.locator('#planning-copy').click();
    await page.locator('#planning-copy-dialog[open]').waitFor();
    await copy('#planning-copy-title', 'Unabhängige Woche erstellen', locale);
    await copy('#planning-copy-cancel', 'Abbrechen', locale);
    await page.locator('#planning-copy-cancel').click();
    await fit(locale + ' generated planning calendar');
  });
  await check(locale + ' recipe form and user content', async () => {
    await visit('/m/recipes/recipes/' + fixtures.recipe.id + '/workspace', '#recipe-form:not([hidden])');
    assert.equal(await page.locator('#recipe-heading').textContent(), fixtures.recipe.title);
    assert.equal(await page.locator('#recipe-form [name=title]').inputValue(), fixtures.recipe.title);
    await copy('#recipe-basics-heading', 'Grunddaten', locale);
    await copy('#recipe-add-ingredient', 'Zutat ergänzen', locale);
    await page.locator('#recipe-add-ingredient').click();
    must(await page.locator('#recipe-ingredients input').count() > 0, locale + ' recipe generated ingredient controls');
    await fit(locale + ' recipe editor');
  });
  await check(locale + ' finance form', async () => {
    await visit('/m/expenses/workspace/entries', '#finance-workspace[aria-busy=false]');
    await page.locator('#finance-new-entry').click();
    await page.locator('#finance-entry-editor:not([hidden])').waitFor();
    await copy('#finance-entry-save', 'Entwurf prüfen', locale);
    await copy('#finance-entry-post', 'Verbindliches Buchen prüfen', locale);
    await copy('#finance-add-line', 'Kategoriezeile ergänzen', locale);
    await fit(locale + ' finance generated editor');
  });
  await check(locale + ' chores generated form', async () => {
    await visit('/m/chores/workspace/series/new', '#chores-workspace[aria-busy=false]');
    await copy('#chores-heading', 'Neue Aufgabenserie', locale);
    must(await page.getByRole('button', {name: text(locale, 'Vollständige Vorschau prüfen'), exact: true}).isVisible(), locale + ' chores preview action');
    await fit(locale + ' chores generated editor');
  });
  await check(locale + ' documents generated form', async () => {
    await visit('/m/documents/workspace/review', '#doc-content form');
    await copy('#doc-status', 'Neue Dokumente beginnen ungeprüft.', locale);
    must(await page.locator('#doc-content label').count() >= 4, locale + ' document form populated');
    await fit(locale + ' document generated editor');
  });
  await check(locale + ' real validation and empty states', async () => {
    const invalid = await page.evaluate(async () => {const response = await fetch('/api/v1/core/preferences', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': document.querySelector('meta[name="csrf-token"]').content}, body: JSON.stringify({locale: '__invalid__'})}); return {status: response.status, body: await response.json()};});
    assert.equal(invalid.status, 422); assert.equal(invalid.body.error, text(locale, 'Ungültige Sprache'));
    await visit('/m/recipes/recipes?q=localization-no-match-317615', '.empty');
    await copy('.empty h2', 'Platz für deinen Alltag', locale);
    await copy('.empty p', 'Hier passt gerade kein Eintrag zur Suche.', locale);
  });
  await check(locale + ' report error and recovery', async () => {
    const endpoint = origin.origin + '/api/v1/m/fuel/reports/summary';
    let fail = true;
    const intercept = route => fail ? (fail = false, route.fulfill({status: 503, contentType: 'application/json', body: JSON.stringify({error: 'Synthetic temporary refusal'})})) : route.continue();
    await page.route(endpoint, intercept);
    try {
      await visit('/m/fuel/reports', '#report[aria-busy=false]');
      await copy('#report [role=status]', 'Auswertung nicht verfügbar.', locale);
      must(await page.locator('#report [role=alert]').isVisible(), locale + ' report failure visible');
      await page.getByRole('button', {name: text(locale, 'Erneut laden'), exact: true}).click();
      await page.waitForFunction(expected => document.querySelector('#report [role=status]')?.textContent === expected, text(locale, 'Auswertung geladen.'));
      must(await page.locator('#report [role=alert]').count() === 0, locale + ' report retry removes error');
    } finally {await page.unroute(endpoint, intercept);}
  });
}
async function offlineShells() {
  publicContext = await browser.newContext({viewport: {width: 390, height: 844}, serviceWorkers: 'allow', reducedMotion: 'reduce'});
  const offline = await publicContext.newPage();
  offline.on('pageerror', error => report.pageErrors.push(String(error).slice(0, 1000)));
  const shells = [['/offline', 'Du bist offline'], ['/offline/scans', 'Scanentwürfe auf diesem Gerät'], ['/offline/shopping', 'Einkaufslisten offline lesen'], ['/offline/recipes', 'Rezepte offline lesen']];
  for (const locale of ['de', 'en']) for (const [route, heading] of shells) await check(locale + ' public shell ' + route, async () => {
    const response = await offline.goto(origin.origin + route + '?lang=' + locale, {waitUntil: 'load'});
    assert.equal(response.status(), 200);
    assert.equal(await offline.locator('html').getAttribute('lang'), locale === 'en' ? 'en-GB' : 'de-AT');
    assert.equal(await offline.locator('main h1').textContent(), text(locale, heading));
    assert.equal(await offline.locator('body').getAttribute('data-user'), '');
    assert.equal(await offline.locator('meta[name=csrf-token]').getAttribute('content'), '');
    assert.equal(await offline.locator('input[name=csrf]').count(), 0);
    assert.equal(await offline.locator('.language-control a').getAttribute('lang'), locale === 'en' ? 'de' : 'en');
    must(true, locale + ' anonymous offline shell has no private identity or mutation token');
  });
  await check('Real service worker retains language across offline navigation', async () => {
    await offline.goto(origin.origin + '/offline/recipes?lang=en', {waitUntil: 'load'});
    await offline.evaluate(async () => {await navigator.serviceWorker.ready;});
    await offline.waitForFunction(async () => navigator.serviceWorker.controller && (await (await caches.match('/__household_public_locale'))?.text()) === 'en');
    await publicContext.setOffline(true);
    await offline.goto(origin.origin + '/m/recipes/recipes', {waitUntil: 'load'});
    assert.equal(await offline.locator('html').getAttribute('lang'), 'en-GB');
    assert.equal(await offline.locator('main h1').textContent(), text('en', 'Rezepte offline lesen'));
    await offline.locator('.language-control a[lang=de]').click();
    await offline.waitForFunction(() => document.documentElement.lang === 'de-AT');
    assert.equal(await offline.locator('main h1').textContent(), 'Rezepte offline lesen');
    await require('./browser-public-cache.cjs')(offline);
    must(true, 'Service worker caches exactly eight anonymous locale shells, hashed public assets and one bounded language preference');
    await publicContext.setOffline(false);
  });
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
    context = await browser.newContext({storageState: state, viewport: {width: 390, height: 844}, reducedMotion: 'reduce', serviceWorkers: 'block'});
    context.setDefaultTimeout(12000); context.setDefaultNavigationTimeout(20000);
    await context.route('**/*', route => {
      const request = route.request(), url = new URL(request.url());
      const locale = request.method() === 'POST' && url.pathname === '/api/v1/core/preferences' && Object.keys(request.postDataJSON() || {}).every(key => key === 'locale');
      if (url.origin !== origin.origin || (!['GET', 'HEAD'].includes(request.method()) && !locale)) {report.blockedRequests.push({method: request.method(), path: url.origin + url.pathname}); return route.abort();}
      return route.continue();
    });
    page = await context.newPage();
    page.on('pageerror', error => report.pageErrors.push(String(error).slice(0, 1000)));
    page.on('dialog', dialog => dialog.accept());
    await visit('/settings', '#preferences');
    originalLocale = (await json('/api/v1/core/bootstrap')).locale;
    await setLocale('de'); await page.reload({waitUntil: 'load'});
    const baseline = await json('/api/v1/core/bootstrap');
    must(routes.every(item => baseline.states[item.id]?.state === 'enabled'), 'All nine synthetic modules are enabled');
    const recipeData = await json('/api/v1/m/recipes/recipes');
    const recipe = recipeData.records?.[0];
    must(recipe && /^[0-9a-f-]{36}$/i.test(recipe.id), 'Synthetic recipe discovered through the actual list API');
    const list = (await json('/api/v1/m/shopping/lists')).records?.[0];
    const plan = (await json('/api/v1/m/mealplan/plans')).records?.[0];
    must(list && plan, 'Synthetic shopping list and meal plan discovered through actual list APIs');
    for (const locale of ['de', 'en']) {
      await setLocale(locale); await page.reload({waitUntil: 'load'});
      await modulePages(locale, baseline);
      await editorsAndContent(locale, {recipe, list, plan});
      assert.deepEqual((await json('/api/v1/m/recipes/recipes')).records, recipeData.records, 'Language selection leaves stored recipe records unchanged');
      report.checks.push(locale + ' recipe payloads retain original user content');
    }
    await offlineShells();
    must(report.blockedRequests.length === 0, 'No external or business-record mutation requests attempted');
    must(report.pageErrors.length === 0, 'No JavaScript runtime errors');
  } catch (error) {report.failures.push({name: 'Harness', message: String(error.message || error).slice(0, 1200)});}
  finally {
    if (page && originalLocale) try {await setLocale(originalLocale); report.checks.push('Original fixture language restored');} catch {report.failures.push({name: 'Cleanup', message: 'Could not restore the fixture language preference'});}
    await publicContext?.close(); await context?.close(); await browser?.close();
    report.passed = report.failures.length === 0;
    await fs.writeFile(path.join(output, 'localization-report.json'), JSON.stringify(report, null, 2) + '\n');
  }
  console.log(JSON.stringify({checks: report.checks.length, failures: report.failures.length, screenshots: report.screenshots.length, passed: report.passed}));
  if (!report.passed) process.exitCode = 1;
})().catch(error => {console.error(error.message); process.exitCode = 1;});
