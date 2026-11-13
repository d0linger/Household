'use strict';

// Shared navigation checks use actual sessions in an explicitly owned fixture.
// Module drivers separately cover writes, dirty forms and domain workflows.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const {fixtureState} = require('./notification-browser-fixture.cjs');

const root = path.resolve(__dirname, '..');
const moduleID = process.env.HOUSEHOLD_UX_MODULE;
const routes = {
  shopping: '/m/shopping/lists', fuel: '/m/fuel/workspace/overview',
  recipes: '/m/recipes/recipes', pantry: '/m/pantry/stock',
  expenses: '/m/expenses/workspace/entries', chores: '/m/chores/workspace/today',
  meters: '/m/meters/workspace/overview', documents: '/m/documents/workspace/inbox',
  mealplan: '/m/mealplan/plans'
};
if (!Object.hasOwn(routes, moduleID)) throw Error('HOUSEHOLD_UX_MODULE must name one supported module');
const origin = process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101';
if (origin !== 'http://localhost:8101') throw Error('Navigation review requires the isolated localhost:8101 fixture');
const output = path.resolve(root, process.env.HOUSEHOLD_UX_OUTPUT || ('outputs/ux-workspace/navigation-' + moduleID + '-' + Date.now()));
if (!output.startsWith(path.join(root, 'outputs') + path.sep)) throw Error('Evidence must stay inside project outputs');
const report = {passed: false, module: moduleID, checks: [], screenshots: [], accessibility: [], errors: [], externalRequests: [],
  scope: 'Actual shared navigation, role presentation, DE/EN, four themes, 320px/desktop and keyboard. Domain mutations require separate module drivers.'};
const secrets = [];
const hasManagement = moduleID !== 'mealplan';
let browser;
const must = (condition, message) => {if (!condition) throw Error(message); report.checks.push(message);};
const redact = text => secrets.reduce((value, secret) => secret ? value.split(secret).join('[redacted]') : value, String(text));

(async () => {
  await fs.mkdir(output, {recursive: true});
  const handoff = process.env.HOUSEHOLD_BROWSER_HANDOFF;
  if (!handoff) throw Error('Explicit owned fixture handoff required');
  browser = await chromium.launch({headless: true, ...(process.platform === 'win32' ? {channel: 'msedge'} : {}),
    args: ['--disable-background-networking', '--disable-component-update', '--no-first-run']});
  for (const role of ['admin', 'editor', 'reader']) {
    const stateFile = role === 'admin' ? handoff : path.join(path.dirname(handoff), 'handoff-' + role + '.json');
    const state = fixtureState(root, secrets, {env: {...process.env, HOUSEHOLD_BROWSER_HANDOFF: stateFile}});
    const context = await browser.newContext({storageState: state, serviceWorkers: 'block', reducedMotion: 'reduce', viewport: {width: 1440, height: 1000}});
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (['http:', 'https:'].includes(url.protocol) && url.origin !== origin) {report.externalRequests.push(url.origin + url.pathname); return route.abort();}
      return route.continue();
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20000);
    page.on('pageerror', error => report.errors.push(redact(error.message)));
    const nav = () => page.locator('[data-workspace-navigation="' + moduleID + '"]');
    async function go(route) {
      const response = await page.goto(new URL(route, origin).href);
      must(response?.status() === 200 && new URL(page.url()).pathname !== '/login', role + ': authorized route ' + route);
      await nav().waitFor();
      await page.waitForFunction(() => !document.querySelector('main [aria-busy="true"]'));
    }
    await go(routes[moduleID]);
    const originalLanguage = await page.locator('html').getAttribute('data-locale');
    const actualRole = await page.evaluate(async () => (await fetch('/api/v1/core/bootstrap').then(r => r.json())).user.role);
    must(actualRole === role, role + ': actual server identity matches');
    const primary = await nav().locator('.workspace-primary a').evaluateAll(links => links.map(a => ({href: a.getAttribute('href'), text: a.textContent.trim()})));
    must(primary.length >= 2 && primary.length <= 3, role + ': two or three primary destinations');
    must(primary.every(link => link.text && new URL(link.href, origin).origin === origin), role + ': named local primary links');
    if (hasManagement) {
      const manageURL = new URL(await nav().locator('.workspace-manage-link').getAttribute('href'), origin);
      const selector = {fuel: ['vehicle', '#fm-vehicle'], meters: ['register', '#energy-register']}[moduleID];
      must(manageURL.origin === origin && manageURL.pathname === '/m/' + moduleID + '/manage' &&
        [...manageURL.searchParams.keys()].every(key => selector && key === selector[0]), role + ': dedicated Manage destination with only its allowed selector');
      if (selector && manageURL.searchParams.has(selector[0])) {
        must(manageURL.searchParams.get(selector[0]) === await page.locator(selector[1]).inputValue(), role + ': Manage preserves the actual selected record');
      }
    } else {
      must(await nav().locator('.workspace-manage-link').count() === 0, role + ': Mealplan does not offer an empty management destination');
    }
    if (role === 'reader') must(await nav().locator('.workspace-actions').count() === 0, 'Reader receives no writing action strip');
    for (const link of primary) {
      await go(link.href);
      must(await nav().locator('.workspace-primary [aria-current="page"]').count() === 1, role + ': exactly one primary group active for ' + link.href);
      const current = nav().locator('.workspace-primary [aria-current="page"]');
      must(await current.getAttribute('href') === link.href, role + ': correct active primary destination');
    }
    await go(routes[moduleID]);
    for (const language of ['de', 'en']) {
      if (await page.locator('html').getAttribute('data-locale') !== language) {
        await page.locator('.language-control button[value="' + language + '"]').click();
        await page.waitForFunction(lang => document.documentElement.dataset.locale === lang, language);
        await nav().waitFor();
      }
      if (hasManagement) must((await nav().locator('.workspace-manage-link').innerText()).trim() === (language === 'de' ? 'Verwalten' : 'Manage'), role + ': localized Manage label ' + language);
      else must(await nav().locator('.workspace-primary').getAttribute('aria-label'), role + ': named primary navigation in ' + language);
      for (const width of [320, 1440]) for (const theme of ['light', 'dark', 'pink-light', 'pink-dark']) {
        await page.setViewportSize({width, height: 1000});
        await page.evaluate(theme => window.HouseholdTheme.apply(theme, {persist: false}), theme);
        must(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), role + ': no page overflow ' + language + '/' + theme + '/' + width);
        const label = role + '-' + language + '-' + theme + '-' + width;
        const manage = hasManagement ? nav().locator('.workspace-manage-link') : nav().locator('.workspace-primary a').nth(1);
        await page.keyboard.press('Tab');
        await manage.focus();
        must(await manage.evaluate(el => el === document.activeElement), label + ': destination keyboard focus');
        const focus = await manage.evaluate(el => {const s=getComputedStyle(el);return s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0;});
        must(focus, label + ': visible focus indicator');
        if (role === 'admin') {
          await page.screenshot({path: path.join(output, label + '.png'), fullPage: true});
          report.screenshots.push(label + '.png');
          const scan = await new AxeBuilder({page}).include('[data-workspace-navigation]').analyze();
          report.accessibility.push({label, violations: scan.violations.map(v => ({id: v.id, impact: v.impact, nodes: v.nodes.length}))});
          must(scan.violations.length === 0, label + ': shared navigation Axe has no violations');
        }
      }
    }
    if (hasManagement) {
      await nav().locator('.workspace-manage-link').focus();
      await Promise.all([page.waitForURL(url => url.pathname === '/m/' + moduleID + '/manage'), page.keyboard.press('Enter')]);
      await nav().waitFor();
      must(await nav().locator('.workspace-manage-link[aria-current="page"]').count() === 1, role + ': Enter opens current Manage page');
      must(await nav().locator('.workspace-primary [aria-current="page"]').count() === 0, role + ': Manage does not select an unrelated work group');
    } else {
      const destination = nav().locator('.workspace-primary a').nth(1);
      const href = await destination.getAttribute('href');
      await destination.focus();
      await Promise.all([page.waitForURL(url => url.pathname === new URL(href, origin).pathname), page.keyboard.press('Enter')]);
      await nav().waitFor();
      must(await nav().locator('.workspace-primary [aria-current="page"]').getAttribute('href') === href, role + ': Enter opens the current second Mealplan group');
      must(await nav().locator('.workspace-manage-link').count() === 0, role + ': Mealplan continues to omit empty management after keyboard navigation');
    }
    const first = nav().locator('.workspace-primary a').first();
    const nextHref = await nav().locator('.workspace-primary a').nth(1).getAttribute('href');
    await first.focus(); await page.keyboard.press('Tab');
    must(await page.evaluate(href => document.activeElement?.getAttribute('href') === href, nextHref), role + ': primary links follow normal keyboard tab order');
    if (await page.locator('html').getAttribute('data-locale') !== originalLanguage) {
      await page.locator('.language-control button[value="' + originalLanguage + '"]').click();
      await page.waitForFunction(lang => document.documentElement.dataset.locale === lang, originalLanguage);
    }
    must(await page.locator('html').getAttribute('data-locale') === originalLanguage, role + ': restores original account language after the matrix');
    await context.close();
  }
  must(report.errors.length === 0, 'No browser runtime errors');
  must(report.externalRequests.length === 0, 'No external browser requests');
  report.passed = true;
})().catch(error => {report.failure = redact(error.message); process.exitCode = 1;}).finally(async () => {
  if (browser) await browser.close();
  await fs.mkdir(output, {recursive: true});
  await fs.writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({passed: report.passed, module: moduleID, checks: report.checks.length, screenshots: report.screenshots.length, output, failure: report.failure}));
});
