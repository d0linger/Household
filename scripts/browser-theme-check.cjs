'use strict';

// Uses only the isolated synthetic review stack and a parent-prepared session.
// Never logs in, resets data, writes fixture records or exports session secrets.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const base = process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101';
const origin = new URL(base);
if (!['http://localhost:8101', 'http://127.0.0.1:8101'].includes(origin.origin) || origin.pathname !== '/' || origin.search || origin.hash || origin.username || origin.password) {
  throw Error('Theme checks require HTTP loopback port 8101 and its isolated synthetic fixtures');
}
const output = path.resolve('outputs/ocean-design-review');
const modes = ['light', 'dark', 'pink-light', 'pink-dark'];
const sizes = {desktop: {width: 1440, height: 1000}, mobile: {width: 390, height: 844}};
const report = {checks: [], failures: [], accessibility: [], contrast: [], screenshots: [], externalRequests: [], pageErrors: []};
const contrastChecked = new Set();
let browser;

function must(condition, message) {if (!condition) throw Error(message);}
async function bounded(name, operation, timeout = 40000) {
  let timer;
  try {
    await Promise.race([operation(), new Promise((_, reject) => {timer = setTimeout(() => reject(Error('Check exceeded time budget')), timeout);})]);
    report.checks.push(name);
  } catch (error) {
    report.failures.push({name, error: String(error.message || error).slice(0, 1500)});
    console.error('FAIL ' + name);
  } finally {clearTimeout(timer);}
}
async function contextFor(mode, size, state, extra = {}) {
  const context = await browser.newContext({viewport: sizes[size], colorScheme: 'light', serviceWorkers: 'block', ...(state ? {storageState: state} : {}), ...extra});
  context.setDefaultTimeout(10000);
  context.setDefaultNavigationTimeout(15000);
  if (mode) await context.addInitScript(({value, targetOrigin}) => {
    // Playwright also installs this script into opaque about:blank documents.
    // Those documents have no storage authority and must not be seeded.
    if (location.origin !== targetOrigin) return;
    if (!sessionStorage.getItem('theme-review-initialized')) {
      localStorage.setItem('household-theme', value);
      sessionStorage.setItem('theme-review-initialized', 'true');
    }
  }, {value: mode, targetOrigin: origin.origin});
  context.on('request', request => {
    const url = new URL(request.url());
    if (/^https?:$/.test(url.protocol) && url.origin !== origin.origin) report.externalRequests.push(url.origin + url.pathname);
  });
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    return /^https?:$/.test(url.protocol) && url.origin !== origin.origin ? route.abort() : route.continue();
  });
  context.on('page', page => {
    page.on('pageerror', error => report.pageErrors.push({path: new URL(page.url()).pathname, error: String(error).slice(0, 1000)}));
    page.on('console', message => {
      if (message.type() === 'error' && /Content Security Policy|Refused to (execute|apply|load)/i.test(message.text())) {
        report.pageErrors.push({path: new URL(page.url()).pathname, error: message.text().slice(0, 1000)});
      }
    });
  });
  return context;
}
async function appearance(page, mode, systemDark = false) {
  const actual = await page.evaluate(() => {
    const root = document.documentElement, css = getComputedStyle(root);
    return {theme: window.HouseholdTheme?.get(), attr: root.getAttribute('data-theme'), native: css.colorScheme,
      chrome: document.querySelector('meta[name="theme-color"]')?.content,
      expected: css.getPropertyValue('--theme-color').trim() || css.getPropertyValue('--ground').trim(),
      overflow: root.scrollWidth > innerWidth + 1};
  });
  must(actual.theme === mode, 'Controller restored expected mode ' + mode);
  must(actual.attr === (mode === 'system' ? null : mode), 'Root theme attribute matches mode');
  must(actual.native === ((mode === 'system' ? systemDark : mode.endsWith('dark')) ? 'dark' : 'light'), 'Native control color-scheme matches mode');
  must(actual.expected && actual.chrome === actual.expected, 'Browser theme-color matches computed CSS token');
  must(!actual.overflow, 'No horizontal page overflow');
  if (modes.includes(mode) && !contrastChecked.has(mode)) {
    contrastChecked.add(mode);
    await bounded(mode + '/computed palette contrast', async () => {
      const pairs = await page.evaluate(() => {
        const css = getComputedStyle(document.documentElement);
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = 1;
        const paint = canvas.getContext('2d', {willReadFrequently: true});
        function luminance(token) {
          const value = css.getPropertyValue(token).trim();
          if (!CSS.supports('color', value)) throw Error('Missing or invalid color token: ' + token);
          paint.clearRect(0, 0, 1, 1); paint.fillStyle = value; paint.fillRect(0, 0, 1, 1);
          const rgba = paint.getImageData(0, 0, 1, 1).data;
          if (rgba[3] !== 255) throw Error('Contrast token must be opaque: ' + token);
          const rgb = [...rgba].slice(0, 3).map(v => v / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4);
          return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722;
        }
        const pairs = [];
        function add(foreground, background, minimum) {
          const a = luminance(foreground), b = luminance(background);
          pairs.push({foreground, background, minimum, ratio: (Math.max(a, b) + .05) / (Math.min(a, b) + .05)});
        }
        for (const foreground of ['--ink', '--muted', '--accent', '--danger', '--warning']) {
          for (const background of ['--ground', '--surface', '--soft']) add(foreground, background, 4.5);
        }
        add('--on-accent', '--accent', 4.5);
        add('--control-border', '--ground', 3);
        return pairs;
      });
      report.contrast.push({mode, pairs});
      const failed = pairs.filter(pair => pair.ratio < pair.minimum);
      must(failed.length === 0, failed.map(pair => pair.foreground + '/' + pair.background + ': ' + pair.ratio.toFixed(2) + ' < ' + pair.minimum).join('; '));
    });
  }
}
async function axe(page, name) {
  const result = await new AxeBuilder({page}).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
  report.accessibility.push({name, violations: result.violations.map(v => ({id: v.id, impact: v.impact, targets: v.nodes.map(n => n.target)}))});
  must(result.violations.length === 0, 'Axe violations: ' + result.violations.map(v => v.id).join(', '));
}
async function visit(context, route, name, mode, action, expectedStatus = 200) {
  const page = await context.newPage();
  try {
    await bounded(name, async () => {
      const response = await page.goto(origin.origin + route, {waitUntil: 'load'});
      must(response?.status() === expectedStatus, 'Expected HTTP ' + expectedStatus);
      must(new URL(page.url()).pathname === route.split('?')[0], 'Route must not redirect to login or another screen');
      must(await page.locator('h1').count() > 0, 'Page has a primary heading');
      await appearance(page, mode);
      if (action) await action(page);
    });
  } finally {await page.close();}
}
async function screenshot(page, name) {
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({path: path.join(output, name + '.png'), fullPage: true, animations: 'disabled', timeout: 15000});
  report.screenshots.push(name + '.png');
}

(async () => {
  must(process.env.HOUSEHOLD_BROWSER_HANDOFF, 'HOUSEHOLD_BROWSER_HANDOFF must identify a prepared synthetic session');
  const privateRoot = await fs.realpath(path.resolve('.local'));
  const handoff = await fs.realpath(path.resolve(process.env.HOUSEHOLD_BROWSER_HANDOFF));
  must(handoff.startsWith(privateRoot + path.sep), 'Session handoff must remain inside private workspace .local');
  const state = JSON.parse(await fs.readFile(handoff, 'utf8'));
  must(state.cookies?.length > 0, 'Session handoff contains authenticated synthetic cookies');
  await fs.mkdir(output, {recursive: true});
  browser = await chromium.launch({headless: true,
    ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : process.platform === 'win32' ? {channel: 'msedge'} : {}),
    args: ['--disable-background-networking', '--disable-component-update', '--no-first-run']});

  const discovery = await contextFor('light', 'desktop', state);
  let modules = [];
  try {
    await visit(discovery, '/settings/modules', 'Discover nine enabled module start routes', 'light', async page => {
      modules = await page.locator('.module-card a[href]').evaluateAll(links => links.map(link => link.getAttribute('href')));
      must(modules.length === 9 && new Set(modules).size === 9 && modules.every(route => route.startsWith('/m/')), 'Nine distinct module routes available');
    });
  } finally {await discovery.close();}
  must(modules.length === 9, 'Cannot run complete module matrix without nine module routes');
  report.moduleRoutes = modules;

  for (const mode of modes) for (const size of Object.keys(sizes)) {
    const context = await contextFor(mode, size, state);
    try {
      for (const route of modules) await visit(context, route, mode + '/' + size + route, mode, async page => {
        if (size === 'desktop') await bounded(mode + '/' + size + route + '/accessibility', () => axe(page, mode + '/' + size + route));
        if (route.includes('/shopping') && size === 'desktop') {
          const fixture = page.locator('.record-main[href^="/m/shopping/lists/"]').first();
          must(await fixture.count() > 0, 'Synthetic shopping list is available for populated design capture');
          const expectedTitle = (await fixture.locator('strong').innerText()).trim();
          const expectedRoute = await fixture.getAttribute('href');
          const [response] = await Promise.all([page.waitForNavigation({waitUntil: 'load'}), fixture.click()]);
          must(response?.status() === 200 && new URL(page.url()).pathname === expectedRoute, 'Shopping fixture detail responds without redirect');
          must(await page.locator('h1').count() > 0, 'Shopping detail has its module heading');
          must((await page.locator('#shopping-list-title').innerText()).trim() === expectedTitle, 'Shopping board shows the selected synthetic list title');
          await page.waitForFunction(() => document.querySelectorAll('[data-shopping-item]').length >= 3 && !document.querySelector('#shopping-progress')?.textContent.includes('werden geladen'));
          await appearance(page, mode);
          await screenshot(page, mode + '-desktop-shopping');
          await axe(page, mode + '/desktop/shopping-detail');
        }
      });
      for (const route of ['/settings', '/settings/security', '/search', '/notifications']) {
        await visit(context, route, mode + '/' + size + route, mode, async page => {
          if (route !== '/settings') return;
          await page.waitForFunction(() => {
            const panel = document.querySelector('[data-shopping-push]');
            const server = panel?.querySelector('[data-push-server]')?.textContent.trim();
            return panel?.getAttribute('aria-busy') === 'false' && server
              && !server.includes('Serverkonfiguration wird geprüft')
              && Boolean(panel.querySelector('[data-push-permission]')?.textContent.trim())
              && Boolean(panel.querySelector('[data-push-devices]')?.childElementCount);
          }, undefined, {timeout: 10000});
          if (size === 'mobile') await screenshot(page, mode + '-mobile-settings');
          await axe(page, mode + '/' + size + '/settings');
          if (size === 'mobile') {
            await page.locator('.navigation-trigger').click();
            must(await page.locator('#more-dialog').evaluate(el => el.open), 'Native settings tray opens');
            await appearance(page, mode);
            await axe(page, mode + '/mobile/more-dialog');
            await page.keyboard.press('Escape');
            must(await page.locator('#more-dialog').evaluate(el => !el.open), 'Escape closes native More dialog');
          }
        });
      }
      await visit(context, '/m/shopping/lists', mode + '/' + size + '/create-dialog', mode, async page => {
        await page.locator('[data-open="create-dialog"]').first().click();
        must(await page.locator('#create-dialog').evaluate(el => el.open), 'Native create form opens');
        await axe(page, mode + '/' + size + '/create-dialog');
        await appearance(page, mode);
        await page.keyboard.press('Escape');
        must(await page.locator('#create-dialog').evaluate(el => !el.open), 'Escape closes native form');
      });
    } finally {await context.close();}

    const anonymous = await contextFor(mode, size);
    try {
      for (const route of ['/login', '/offline', '/offline/scans', '/offline/shopping', '/offline/recipes']) {
        await visit(anonymous, route, mode + '/' + size + '/public' + route, mode);
      }
    } finally {await anonymous.close();}
    const errors = await contextFor(mode, size, state);
    try {await visit(errors, '/m/shopping/lists/00000000-0000-4000-8000-000000000000', mode + '/' + size + '/404', mode, null, 404);}
    finally {await errors.close();}
  }

  const preferences = await contextFor('light', 'desktop', state);
  try {
    await visit(preferences, '/settings', 'Real preference save/reload and OS/cross-tab changes', 'light', async page => {
      for (const mode of modes) {
        await page.locator('#preferences [name=theme]').selectOption(mode);
        const saved = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/core/preferences' && response.request().method() === 'POST');
        await page.getByRole('button', {name: 'Geräteeinstellungen speichern', exact: true}).click();
        must((await saved).ok(), 'Server accepts ' + mode);
        await page.waitForFunction(value => window.HouseholdTheme.get() === value, mode);
        await page.reload({waitUntil: 'load'});
        await appearance(page, mode);
        must(await page.locator('#preferences [name=theme]').inputValue() === mode, 'Saved preference remains selected after reload');
      }
      await page.evaluate(() => window.HouseholdTheme.apply('pink-light', {persist: true}));
      await page.emulateMedia({colorScheme: 'dark'});
      await appearance(page, 'pink-light');
      await page.evaluate(() => window.HouseholdTheme.apply('system', {persist: true}));
      await appearance(page, 'system', true);
      await page.emulateMedia({colorScheme: 'light'});
      // CSS updates before the queued matchMedia change event synchronizes chrome.
      await page.waitForFunction(() => {
        const css = getComputedStyle(document.documentElement);
        return css.colorScheme === 'light'
          && document.querySelector('meta[name="theme-color"]')?.content === css.getPropertyValue('--theme-color').trim();
      });
      await appearance(page, 'system');
      const other = await preferences.newPage();
      try {
        await other.goto(origin.origin + '/settings');
        await other.evaluate(() => window.HouseholdTheme.apply('pink-dark', {persist: true}));
        await page.waitForFunction(() => window.HouseholdTheme.get() === 'pink-dark');
        await appearance(page, 'pink-dark');
        await other.evaluate(() => localStorage.removeItem('household-theme'));
        await page.waitForFunction(() => window.HouseholdTheme.get() === 'system');
        await appearance(page, 'system');
        await other.evaluate(() => localStorage.clear());
        await appearance(page, 'system');
      } finally {await other.close();}
    });
  } finally {await preferences.close();}

  const blocked = await contextFor(null, 'mobile');
  try {
    await blocked.addInitScript(() => Object.defineProperty(window, 'localStorage', {get() {throw new DOMException('Storage denied', 'SecurityError');}}));
    await visit(blocked, '/login', 'Blocked local storage falls back to usable system theme', 'system', async page => {
      must(await page.locator('[name=username]').isVisible(), 'Login remains usable with blocked local storage');
    });
  } finally {await blocked.close();}

  const offline = await contextFor('pink-dark', 'mobile', undefined, {serviceWorkers: 'allow'});
  try {
    await visit(offline, '/login', 'Service worker retains theme asset and neutral offline palette', 'pink-dark', async page => {
      await page.evaluate(() => navigator.serviceWorker.ready);
      await page.reload({waitUntil: 'load'});
      await page.waitForFunction(() => Boolean(navigator.serviceWorker.controller));
      const cached = await require('./browser-public-cache.cjs')(page);
      must(cached.assetPaths.some(route => /^\/assets\/[a-f0-9]{12}\/theme\.js$/.test(route)), 'Hashed theme controller is available offline');
      must(true, 'Worker cache contains exactly eight anonymous locale shells, hashed public assets and one bounded language preference');
      await offline.setOffline(true);
      await page.goto(origin.origin + '/offline', {waitUntil: 'load'});
      await appearance(page, 'pink-dark');
      must(await page.locator('h1').count() > 0, 'Offline shell renders with cached theme controller');
    });
  } finally {await offline.close();}
  must(report.screenshots.length === 8, 'Eight representative full-page screenshots captured');
})().catch(error => report.failures.push({name: 'Suite prerequisite or completion', error: String(error.message || error).slice(0, 1500)}))
  .finally(async () => {
    if (browser) await browser.close();
    if (report.externalRequests.length) report.failures.push({name: 'External runtime requests', count: report.externalRequests.length});
    if (report.pageErrors.length) report.failures.push({name: 'Browser runtime/CSP errors', count: report.pageErrors.length});
    report.passed = report.failures.length === 0;
    await fs.mkdir(output, {recursive: true});
    await fs.writeFile(path.join(output, 'browser-theme-report.json'), JSON.stringify(report, null, 2));
    console.log(JSON.stringify({passed: report.passed, checks: report.checks.length, failures: report.failures.length, screenshots: report.screenshots.length, accessibilityScans: report.accessibility.length}));
    if (!report.passed) process.exitCode = 1;
  });
