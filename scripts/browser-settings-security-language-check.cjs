'use strict';

// Mutates only dedicated synthetic accounts on the owned review stack. The one
// direct SQL mutation ages an exact account/session/token to exercise real reauth.
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const {spawnSync} = require('node:child_process');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const {resolveBrowserPrivateRoot} = require('./browser-private-root.cjs');

const workspace = path.resolve(__dirname, '..');
const origin = new URL(process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101');
if (origin.origin !== 'http://localhost:8101' || origin.pathname !== '/' || origin.search || origin.hash || origin.username || origin.password) {
  throw Error('Settings security checks require the isolated localhost:8101 WebAuthn origin');
}
const output = path.resolve(process.env.HOUSEHOLD_SETTINGS_BROWSER_OUTPUT || path.join(workspace, 'outputs/settings-security-language-review'));
if (!output.startsWith(path.join(workspace, 'outputs') + path.sep)) throw Error('Browser evidence must stay inside workspace outputs');
const report = {checks: [], accessibility: [], screenshots: [], errors: [], externalRequests: [], passed: false};
const sensitive = [];
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
let browser, diagnosticPage;
const must = (value, message) => {if (!value) throw Error(message); report.checks.push(message);};
const redact = text => sensitive.reduce((result, secret) => secret ? result.split(secret).join('[redacted]') : result, String(text));
const token = () => crypto.randomBytes(16).toString('hex');
const secret = () => {const value = 'Synthetic review ' + token(); sensitive.push(value); return value;};

async function privateFixture() {
  const local = resolveBrowserPrivateRoot(workspace, process.env.HOUSEHOLD_BROWSER_PRIVATE_ROOT);
  if (!process.env.HOUSEHOLD_BROWSER_HANDOFF) throw Error('An existing private fixture handoff is required');
  const handoff = await fs.realpath(process.env.HOUSEHOLD_BROWSER_HANDOFF);
  if (!handoff.startsWith(local + path.sep)) throw Error('Fixture handoff leaves Household .local');
  const directory = await fs.realpath((await fs.readFile(path.join(workspace, '.local/ocean-app-review-current.txt'), 'utf8')).trim());
  if (!directory.startsWith(local + path.sep) || path.dirname(handoff) !== directory) throw Error('Handoff is not in the current owned review directory');
  const resources = JSON.parse(await fs.readFile(path.join(directory, 'resources.json'), 'utf8'));
  if (!/^[a-f0-9]{12,32}$/.test(resources.id) || !/^[a-f0-9]{64}$/.test(resources.container_id) ||
      resources.app_port !== 8101 || resources.port !== 55434 ||
      resources.container_name !== 'household-ocean-app-review-' + resources.id || path.resolve(resources.directory) !== directory) {
    throw Error('Fixture manifest does not identify the owned review stack');
  }
  const environment = JSON.parse(await fs.readFile(path.join(directory, 'environment.json'), 'utf8'));
  if (environment.HOUSEHOLD_ORIGIN !== origin.origin) throw Error('Fixture origin does not match the WebAuthn browser origin');
  const owner = new URL(environment.HOUSEHOLD_TEST_OWNER_URL);
  if (!['127.0.0.1', 'localhost'].includes(owner.hostname) || owner.port !== '55434' || owner.pathname !== '/household_test' ||
      !/^[a-z_][a-z0-9_]*$/.test(owner.username)) throw Error('Fixture database URL does not identify isolated household_test');
  sensitive.push(decodeURIComponent(owner.password));
  const state = JSON.parse(await fs.readFile(handoff, 'utf8'));
  if (!state.cookies?.length || !Array.isArray(state.origins)) throw Error('Prepared authenticated fixture state is missing');
  state.cookies.forEach(cookie => sensitive.push(cookie.value));
  return {resources, owner, state};
}

function inspectDatabase(resources) {
  const result = spawnSync('docker', ['inspect', resources.container_id], {encoding: 'utf8', timeout: 15000, windowsHide: true, maxBuffer: 1 << 20});
  if (result.status !== 0) throw Error('Cannot inspect the owned synthetic database');
  const [container] = JSON.parse(result.stdout);
  const ports = container.HostConfig?.PortBindings?.['5432/tcp'];
  if (container.Id !== resources.container_id || container.Name !== '/' + resources.container_name ||
      container.Config?.Labels?.['com.household.review-id'] !== resources.id || !container.State?.Running ||
      ports?.length !== 1 || ports[0].HostIp !== '127.0.0.1' || ports[0].HostPort !== '55434') {
    throw Error('Synthetic database identity, owner label or loopback bind changed');
  }
}

async function ageSession(fixture, context, page, expectedUsername) {
  const current = await bootstrap(page);
  const user = current.user;
  if (user.username !== expectedUsername || !expectedUsername.startsWith('settings-review-') || !uuid.test(user.id) || !uuid.test(user.session_id)) {
    throw Error('Session aging requires the exact dedicated synthetic account');
  }
  const cookies = await context.cookies(origin.origin);
  const cookie = cookies.find(item => item.name === 'household_dev' || item.name === '__Host-household');
  if (!cookie || !/^[A-Za-z0-9_-]{43}$/.test(cookie.value)) throw Error('Dedicated synthetic session cookie is unavailable');
  sensitive.push(cookie.value);
  const hash = crypto.createHash('sha256').update(cookie.value).digest('hex');
  inspectDatabase(fixture.resources);
  // All interpolated SQL values have closed UUID/hash syntax and the session is
  // additionally bound to the authenticated browser token. Never update a set.
  const sql = `WITH aged AS (UPDATE core.sessions SET reauthenticated_at=clock_timestamp()-interval '11 minutes'
 WHERE id='${user.session_id}'::uuid AND user_id='${user.id}'::uuid AND token_hash=decode('${hash}','hex')
 AND revoked_at IS NULL RETURNING id) SELECT count(*) FROM aged;`;
  const result = spawnSync('docker', ['exec', '-i', '-e', 'PGPASSWORD', fixture.resources.container_id,
    'psql', '-U', decodeURIComponent(fixture.owner.username), '-d', 'household_test', '-X', '-At', '-v', 'ON_ERROR_STOP=1'], {
    input: sql, encoding: 'utf8', timeout: 15000, windowsHide: true, maxBuffer: 1 << 20,
    env: {...process.env, PGPASSWORD: decodeURIComponent(fixture.owner.password)}
  });
  if (result.status !== 0 || result.stdout.trim() !== '1') throw Error('Could not age exactly one dedicated synthetic session');
  const fresh = await page.evaluate(async () => (await fetch('/api/v1/security/freshness')).json());
  must(fresh.recent === false, 'An actual dedicated session older than ten minutes requires confirmation');
}

async function bootstrap(page) {
  return page.evaluate(async () => {
    const response = await fetch('/api/v1/core/bootstrap', {cache: 'no-store'});
    if (!response.ok) throw Error('Current synthetic authentication unavailable');
    return response.json();
  });
}

async function formPost(page, route, fields) {
  return page.evaluate(async ({route, fields}) => {
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    const response = await fetch(route, {method: 'POST', headers: {Accept: 'application/json', 'X-CSRF-Token': csrf},
      body: new URLSearchParams({csrf, ...fields})});
    return {status: response.status, ok: response.ok};
  }, {route, fields});
}

async function login(page, username, password) {
  await page.goto(origin.origin + '/login');
  await page.locator('form[action="/login"] [name=username]').fill(username);
  await page.locator('form[action="/login"] [name=password]').fill(password);
  await Promise.all([page.waitForURL(url => url.pathname !== '/login'), page.locator('form[action="/login"] button[type=submit], form[action="/login"] button.primary').click()]);
}

async function changeInitialPassword(page, initial, next) {
  must(new URL(page.url()).pathname === '/change-password', 'New synthetic member must change the initial password');
  await page.locator('[name=current]').fill(initial);
  await page.locator('[name=next]').fill(next);
  await Promise.all([page.waitForURL('**/login'), page.locator('form[action="/change-password"] button').click()]);
}

async function language(page, locale) {
  if (await page.locator('html').getAttribute('data-locale') === locale) return;
  await page.locator('.language-control button[value=' + locale + ']').click();
  await page.waitForFunction(locale => document.documentElement.dataset.locale === locale, locale);
}

async function capture(page, name) {
  await page.screenshot({path: path.join(output, name + '.png'), fullPage: true, animations: 'disabled'});
  report.screenshots.push(name + '.png');
}

async function axe(page, name) {
  const result = await new AxeBuilder({page}).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
  report.accessibility.push({name, violations: result.violations.map(item => ({id: item.id, impact: item.impact, targets: item.nodes.map(node => node.target)}))});
  must(result.violations.length === 0, 'No accessibility violations: ' + name);
  must(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'No horizontal page overflow: ' + name);
}

async function theme(page, family, mode) {
  await page.locator('.header-tools [data-open="appearance-dialog"]').click();
  await page.locator('#appearance-form [name=family]').selectOption(family);
  await page.locator('#appearance-form [name=mode][value=' + mode + ']').check();
  await page.locator('#appearance-form button.primary').click();
  const expected = (family === 'pink' ? 'pink-' : '') + mode;
  await page.waitForFunction(expected => window.HouseholdTheme.get() === expected &&
    document.querySelector('#appearance-form').getAttribute('aria-busy') !== 'true' &&
    document.querySelector('#appearance-error').hidden, expected);
  must(await page.evaluate(expected => localStorage.getItem('household-theme') === expected, expected), 'Selected appearance persists: ' + expected);
  await page.locator('#appearance-dialog [data-close]').first().click();
  return expected;
}

function totp(secretValue) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const character of secretValue.replace(/=+$/, '').toUpperCase()) {
    const value = alphabet.indexOf(character);
    if (value < 0) throw Error('Synthetic TOTP seed has invalid encoding');
    bits += value.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8) bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const digest = crypto.createHmac('sha1', Buffer.from(bytes)).update(counter).digest();
  const offset = digest[digest.length - 1] & 15;
  return String((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0');
}

(async () => {
  const fixture = await privateFixture();
  await fs.mkdir(output, {recursive: true});
  const rootPassword = process.env.HOUSEHOLD_BROWSER_PASSWORD;
  if (!rootPassword) throw Error('Explicit prepared synthetic administrator password is required');
  sensitive.push(rootPassword);
  browser = await chromium.launch({headless: true,
    ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : process.platform === 'win32' ? {channel: 'msedge'} : {}),
    args: ['--disable-background-networking', '--disable-component-update', '--no-first-run']});
  async function context(state) {
    const result = await browser.newContext({...(state ? {storageState: state} : {}), viewport: {width: 1440, height: 1000},
      colorScheme: 'light', reducedMotion: 'reduce', serviceWorkers: 'block'});
    result.setDefaultTimeout(20000);
    await result.route('**/*', route => {
      const url = new URL(route.request().url());
      if (url.protocol === 'http:' || url.protocol === 'https:') {
        if (url.origin !== origin.origin) {report.externalRequests.push(url.origin + url.pathname); return route.abort();}
      }
      return route.continue();
    });
    result.on('page', page => page.on('pageerror', error => report.errors.push(redact(error.message))));
    return result;
  }
  const ownerContext = await context(fixture.state), owner = await ownerContext.newPage();
  await owner.goto(origin.origin + '/settings/users');
  const initial = await bootstrap(owner);
  must(initial.user.role === 'admin', 'Prepared handoff has current administrator authority');
  must((await formPost(owner, '/settings/security/reauth', {password: rootPassword})).ok, 'Fixture administrator explicitly confirms account creation');
  const suffix = token().slice(0, 12), username = 'settings-review-' + suffix, firstPassword = secret(), password = secret();
  must((await formPost(owner, '/settings/users', {username, display_name: 'Sicherheit', password: firstPassword, role: 'admin'})).ok, 'Dedicated settings test administrator created through the real route');
  await ownerContext.close();
  console.log(JSON.stringify({stage: 'dedicated-user-created'}));

  const accountContext = await context(), page = await accountContext.newPage();
  diagnosticPage = page;
  report.securityResponses = [];
  page.on('response', response => {
    const pathname = new URL(response.url()).pathname;
    if (pathname.includes('/passkeys/')) report.securityResponses.push({path: pathname, status: response.status()});
  });
  await login(page, username, firstPassword);
  await changeInitialPassword(page, firstPassword, password);
  await login(page, username, password);
  await page.goto(origin.origin + '/settings');
  await language(page, 'de');
  must(await page.locator('.topbar > .navigation-trigger').isVisible() && await page.locator('.navigation-trigger').count() === 1, 'Settings menu has one top-right opener');
  must(await page.locator('.header-logout,.sidebar-menu').count() === 0, 'Redundant header logout and sidebar opener are absent');
  await page.locator('.navigation-trigger').click();
  must(await page.locator('#more-dialog form[action="/logout"] button').isVisible(), 'Named logout is directly reachable in the settings tray');
  await page.keyboard.press('Escape');
  const originalViewport = page.viewportSize();
  report.installHeader = [];
  await page.locator('#install').evaluate(button => {button.hidden = false;});
  try {
    for (const width of [320, 390]) {
      await page.setViewportSize({width, height: 844});
      await page.evaluate(() => scrollTo(0, 0));
      await page.locator('.navigation-trigger').click();
      must(await page.locator('#more-dialog #install').isVisible(), 'Available installation is reachable in the navigation tray: ' + width);
      await page.keyboard.press('Escape');
      await capture(page, 'settings-install-available-' + width);
      report.installHeader.push(await page.evaluate(() => {
        const header = document.querySelector('.topbar');
        const controls = ['.navigation-trigger', '.header-tools .language-control button', '.header-tools .appearance-trigger'].map(selector => {
          const button = document.querySelector(selector), rect = button.getBoundingClientRect(), style = getComputedStyle(button);
          return {selector, left: rect.left, right: rect.right, width: rect.width, height: rect.height,
            visible: style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0 &&
              rect.left >= 0 && rect.right <= innerWidth + 1 && rect.top >= 0 && rect.bottom <= innerHeight};
        });
        return {width: innerWidth, pageWidth: document.documentElement.scrollWidth, headerWidth: header.clientWidth,
          headerContentWidth: header.scrollWidth, controls};
      }));
    }
  } finally {
    await page.locator('#install').evaluate(button => {button.hidden = true;});
    await page.setViewportSize(originalViewport);
  }
  for (const state of report.installHeader) {
    must(state.pageWidth <= state.width + 1 && state.headerContentWidth <= state.headerWidth + 1,
      'Available install control causes no horizontal header or page overflow: ' + state.width);
    must(state.controls.every(control => control.visible),
      'Navigation, language and appearance controls stay visible in the viewport: ' + state.width);
  }
  const destinations = ['/settings', '/settings/modules', '/settings/security', '/settings/notifications', '/settings/users', '/settings/audit'];
  for (const route of destinations) {
    await page.goto(origin.origin + route);
    must(await page.locator('.settings-nav a').count() === 6, 'Administrator has all six settings destinations: ' + route);
    must(await page.locator('.settings-nav a[aria-current=page]').getAttribute('href') === route, 'Current settings section is identified: ' + route);
  }

  await page.goto(origin.origin + '/settings/users');
  const readerName = 'settings-reader-' + suffix, readerInitial = secret(), readerPassword = secret();
  const newUser = page.locator('#new-household-user form');
  const createDialog = page.locator('#new-household-user'), createOpener = page.locator('[data-open-user]');
  must(!(await createDialog.isVisible()) && await newUser.locator('[name=username]').count() === 1, 'New-user form is initially hidden in one native dialog');
  for (const locale of ['de', 'en']) {
    await language(page, locale);
    await page.waitForFunction(() => !!window.HouseholdTheme && document.querySelector('[data-open-user]')?.disabled === false);
    for (const mode of ['light', 'dark', 'pink-light', 'pink-dark']) {
      await page.evaluate(mode => window.HouseholdTheme.apply(mode, {persist: false}), mode);
      await page.setViewportSize({width: locale === 'de' ? 320 : 1440, height: 844});
      must(!(await createDialog.isVisible()), 'User creation stays closed until requested: ' + locale + '/' + mode);
      await createOpener.click();
      must(await newUser.locator('[name=username]').evaluate(input => input === document.activeElement), 'Opening user dialog focuses username: ' + locale + '/' + mode);
      const geometry = await createDialog.evaluate(dialog => {const r=dialog.getBoundingClientRect();return {modal:dialog.matches(':modal'),left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:innerWidth,height:innerHeight};});
      must(geometry.modal && geometry.left>=0 && geometry.right<=geometry.width+1 && geometry.top>=0 && geometry.bottom<=geometry.height+1, 'Native user modal stays inside viewport: ' + locale + '/' + mode);
      await axe(page, 'user-create-' + locale + '-' + mode);
      await capture(page, 'user-create-' + locale + '-' + mode);
      await page.keyboard.press('Escape');
      await createDialog.waitFor({state:'hidden'});
      must(await createOpener.evaluate(button => button === document.activeElement), 'Closing user dialog restores its opener: ' + locale + '/' + mode);
    }
  }
  await language(page, 'de'); await page.setViewportSize({width:1440,height:1000});
  await createOpener.click();
  await newUser.locator('[name=username]').fill('discarded-draft');
  await newUser.locator('[name=password]').fill(readerInitial);
  await newUser.locator('[data-close-user]').last().click();
  await createDialog.waitFor({state:'hidden'});
  await page.waitForFunction(() => {
    const form=document.querySelector('#new-household-user form');
    return form.elements.password.value === '' && form.elements.username.value === '';
  });
  must(await newUser.locator('[name=password]').inputValue() === '' && await newUser.locator('[name=username]').inputValue() === '', 'Cancelling creation clears credentials and the draft from the dialog');
  await createOpener.click();
  await page.mouse.click(2,2);
  await createDialog.waitFor({state:'hidden'});
  must(await createOpener.evaluate(button => button === document.activeElement), 'Backdrop closes only the requested user dialog and restores focus');
  await createOpener.click();
  await newUser.locator('[name=username]').fill(readerName);
  await newUser.locator('[name=display_name]').fill('Nicht bekannt');
  await newUser.locator('[name=password]').fill(readerInitial);
  await newUser.locator('[name=role]').selectOption('reader');
  await ageSession(fixture, accountContext, page, username);
  let releaseCreation;
  const creationGate = new Promise(resolve => {releaseCreation = resolve;});
  const holdCreation = async route => {
    if (route.request().method() === 'POST') await creationGate;
    await route.continue();
  };
  await page.route(origin.origin + '/settings/users', holdCreation);
  await newUser.locator('button.primary').click();
  try {
    await page.keyboard.press('Escape');
    must(await createDialog.evaluate(dialog => dialog.open && dialog.matches(':modal')) &&
      await newUser.evaluate(form => form.getAttribute('aria-busy') === 'true' && [...form.querySelectorAll('button')].every(button => button.disabled)),
      'In-flight creation remains modal and prevents duplicate submission or accidental dismissal');
  } finally {releaseCreation();}
  await page.locator('#security-reauth[open]').waitFor();
  await page.unroute(origin.origin + '/settings/users', holdCreation);
  must(await newUser.locator('[name=username]').inputValue() === readerName && await newUser.locator('[name=password]').inputValue() === readerInitial,
    'Stale administrator confirmation retains the complete unsaved form');
  await page.locator('[data-close-reauth]').click();
  must(await newUser.locator('[name=display_name]').inputValue() === 'Nicht bekannt', 'Cancelling confirmation preserves user-entered content');
  await page.waitForFunction(() => document.activeElement === document.querySelector('#new-household-user form button.primary'));
  must(await createDialog.evaluate(dialog => dialog.open && dialog.matches(':modal')) && await newUser.locator('button.primary').evaluate(button => button === document.activeElement), 'Cancelling nested reauthentication returns to the open creation dialog and submit control');
  await newUser.locator('button.primary').click();
  await page.locator('#security-reauth[open] [name=password]').fill(password);
  await page.locator('#security-reauth form button.primary').click();
  await page.locator('.security-user').filter({hasText: readerName}).waitFor();
  must(await page.locator('.security-user').filter({hasText: readerName}).count() === 1, 'Confirmed administrator mutation runs once with the preserved inputs');
  must(!(await createDialog.isVisible()) && await newUser.locator('[name=password]').inputValue() === '', 'Successful creation returns to the member list with a closed empty dialog');

  await page.goto(origin.origin + '/settings/security');
  must(await page.locator('form[action="/settings/security/totp/setup"]').isVisible(), 'Actual disabled TOTP state offers setup');
  must(await page.locator('[data-passkey-register]').isVisible(), 'Configured localhost origin offers passkey enrollment');
  const cdp = await accountContext.newCDPSession(page);
  await cdp.send('WebAuthn.enable', {enableUI: false});
  const {authenticatorId} = await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {protocol: 'ctap2', transport: 'internal',
    hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true}});
  const keyName = 'Synthetic device ' + suffix, renamed = 'Renamed device ' + suffix;
  await page.locator('[data-passkey-register] [name=name]').fill(keyName);
  await page.locator('[data-passkey-register] button').click();
  await page.locator('.security-key h3').filter({hasText: keyName}).waitFor();
  const credentials = await cdp.send('WebAuthn.getCredentials', {authenticatorId});
  must(credentials.credentials.length === 1 && credentials.credentials[0].isResidentCredential, 'Real WebAuthn enrollment creates one discoverable virtual credential');
  let key = page.locator('.security-key').filter({hasText: keyName});
  await key.locator('summary').click();
  await key.locator('form[action$="/rename"] [name=name]').fill(renamed);
  await key.locator('form[action$="/rename"] button').click();
  await page.locator('.security-key h3').filter({hasText: renamed}).waitFor();
  must(true, 'Passkey name changes through the authenticated interface');
  await ageSession(fixture, accountContext, page, username);
  await page.locator('[data-open-reauth]').click();
  await page.locator('#security-reauth [data-passkey-reauth]').click();
  await page.locator('#security-reauth').waitFor({state: 'hidden'});
  must((await page.evaluate(async () => (await fetch('/api/v1/security/freshness')).json())).recent, 'Actual passkey assertion restores strong authentication freshness');

  await page.locator('.navigation-trigger').click();
  await page.locator('#more-dialog form[action="/logout"] button').click();
  await page.waitForURL('**/login');
  must((await accountContext.request.get(origin.origin + '/api/v1/core/bootstrap')).status() === 401, 'Shared tray logout revokes the current private session');
  await page.locator('[data-passkey-login]').click();
  await page.waitForURL(url => url.pathname !== '/login');
  must((await bootstrap(page)).user.username === username, 'Discoverable passkey login resolves the correct account');

  await page.goto(origin.origin + '/settings/security');
  await page.locator('form[action="/settings/security/totp/setup"] button').click();
  await page.locator('code.secret').waitFor();
  const seed = await page.locator('code.secret').innerText();
  sensitive.push(seed);
  await page.locator('form[action="/settings/security/totp/enable"] [name=code]').fill(totp(seed));
  await page.locator('form[action="/settings/security/totp/enable"] button').click();
  await page.locator('.codes code').first().waitFor();
  const recovery = await page.locator('.codes code').allTextContents();
  sensitive.push(...recovery);
  must(recovery.length === 8 && new Set(recovery).size === 8, 'TOTP activation presents eight unique one-use recovery codes');
  await page.goto(origin.origin + '/settings/security');
  must(await page.locator('form[action="/settings/security/totp/disable"]').count() === 1 &&
    await page.locator('form[action="/settings/security/totp/setup"]').count() === 0, 'Security page reflects actual active TOTP state');
  const disable = page.locator('form[action="/settings/security/totp/disable"]');
  await disable.locator('..').locator('summary').click();
  await disable.locator('[name=confirm]').check();
  await disable.locator('button').click();
  await page.locator('form[action="/settings/security/totp/setup"]').waitFor();
  must(true, 'Explicit TOTP removal returns the page to the real unconfigured state');
  key = page.locator('.security-key').filter({hasText: renamed});
  await key.locator('summary').click();
  await key.locator('form[action$="/delete"] [name=confirm]').check();
  await key.locator('form[action$="/delete"] button').click();
  await page.locator('.security-empty').waitFor();
  must(await page.locator('.security-key').count() === 0, 'Explicit passkey deletion removes the registered credential');

  const record = await page.evaluate(async () => {
    const current = await (await fetch('/api/v1/core/bootstrap')).json();
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    const write = async (route, method, data, version) => {
      const response = await fetch(route, {method, headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID()},
        body: JSON.stringify({generation: current.states.shopping.generation, data, version})});
      if (!response.ok) throw Error('Synthetic audit fixture mutation failed');
      return response.json();
    };
    const row = await write('/api/v1/m/shopping/lists', 'POST', {name: 'Sicherheit', description: 'settings-private-audit-sentinel'});
    await write('/api/v1/m/shopping/lists/' + row.id, 'PATCH', {name: 'Nicht bekannt', description: 'settings-private-audit-sentinel-changed'}, row.version);
    return row;
  });
  const auditPath = '/settings/audit?' + new URLSearchParams({module: 'shopping', action: 'UPDATE', resource: record.id, q: username});
  await page.goto(origin.origin + auditPath);
  await language(page, 'en');
  must(await page.locator('.audit-entry').count() === 1, 'Combined literal username and resource filters find the actual update');
  const summary = page.locator('.audit-entry > summary');
  const summaryText = await summary.innerText();
  must(summaryText.includes(username) && !summaryText.includes(record.id), 'Compact audit summary identifies the username and conceals technical IDs');
  must(await page.locator('.audit-entry[open]').count() === 0, 'Audit field details start collapsed');
  await summary.focus(); await page.keyboard.press('Enter');
  const bodyText = await page.locator('.audit-entry-body').innerText();
  must(bodyText.includes('Sicherheit') && bodyText.includes('Nicht bekannt') && !bodyText.includes('settings-private-audit-sentinel'),
    'English audit preserves exact before/after user content and hides protected notes');
  const api = await accountContext.request.get(origin.origin + '/api/v1/core/audit?resource=' + record.id);
  const auditText = await api.text();
  must(api.ok() && api.headers()['cache-control'] === 'no-store' && auditText.includes(username) &&
    auditText.includes('***REDACTED***') && !auditText.includes('settings-private-audit-sentinel'), 'Audit API resolves current usernames without exposing protected fields');
  await capture(page, 'audit-english-desktop'); await axe(page, 'English expanded audit desktop');
  await page.setViewportSize({width: 320, height: 844});
  await capture(page, 'audit-english-mobile'); await axe(page, 'English expanded audit 320px');

  for (const locale of ['en', 'de']) {
    await language(page, locale);
    for (const route of destinations) {
      await page.goto(origin.origin + route);
      must(await page.locator('html').getAttribute('data-locale') === locale, 'Language persists across settings navigation: ' + locale + route);
      must(await page.locator('.settings-nav a[aria-current=page]').getAttribute('href') === route, 'Localized navigation retains route semantics: ' + locale + route);
      await axe(page, locale + ' mobile ' + route);
    }
  }
  await page.goto(origin.origin + '/settings/security');
  await language(page, 'en');
  await page.setViewportSize({width: 1440, height: 1000});
  must(await page.locator('h1').innerText() === 'Security', 'English changes actual server-rendered security copy');
  must((await page.locator('.topbar').innerText()).includes('Sicherheit'), 'Language changes leave the member display name unchanged');
  for (const width of [1440, 390]) {
    await page.setViewportSize({width, height: width === 1440 ? 1000 : 844});
    for (const family of ['ocean', 'pink']) {
      for (const mode of ['light', 'dark', 'system']) {
        await page.emulateMedia({colorScheme: 'light'});
        const selected = await theme(page, family, mode);
        const light = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--ground').trim());
        if (mode === 'system') {
          await page.emulateMedia({colorScheme: 'dark'});
          await page.waitForFunction(light => getComputedStyle(document.documentElement).getPropertyValue('--ground').trim() !== light, light);
          must(await page.evaluate(selected => window.HouseholdTheme.get() === selected, selected), 'OS brightness changes retain the selected family: ' + selected + '/' + width);
        }
        await axe(page, selected + '/' + width);
        await capture(page, 'security-' + selected + '-' + width);
      }
    }
  }
  await page.reload();
  must(await page.evaluate(() => window.HouseholdTheme.get()) === 'pink-system', 'Automatic Pink preference survives a full page reload');

  const readerContext = await context(), reader = await readerContext.newPage();
  await login(reader, readerName, readerInitial);
  await changeInitialPassword(reader, readerInitial, readerPassword);
  await login(reader, readerName, readerPassword);
  await reader.goto(origin.origin + '/settings/security');
  must(await reader.locator('.settings-nav a').count() === 4 && await reader.locator('.settings-nav a[href="/settings/audit"], .settings-nav a[href="/settings/users"]').count() === 0,
    'Reader settings omit administrator destinations');
  must((await readerContext.request.get(origin.origin + '/api/v1/core/audit')).status() === 403, 'Reader cannot access audit data by direct API request');
  const saveOrder = (target, order) => target.evaluate(async module_order => {
    const response = await fetch('/api/v1/core/preferences', {method:'POST', headers:{'Content-Type':'application/json', 'X-CSRF-Token':document.querySelector('meta[name=csrf-token]').content}, body:JSON.stringify({module_order})});
    return response.status;
  }, order);
  const readOrder = target => target.evaluate(async () => (await fetch('/api/v1/core/preferences')).json());
  const adminBefore = await readOrder(page), readerBefore = await readOrder(reader);
  const ids = initial.modules.map(module => module.id), adminOrder = [...ids].reverse(), readerOrder = [...ids.slice(1), ids[0]];
  must(await saveOrder(page, adminOrder) === 200 && await saveOrder(reader, readerOrder) === 200, 'Administrators and readers can each save a personal module order');
  const adminAfter = await readOrder(page), readerAfter = await readOrder(reader);
  must(JSON.stringify(adminAfter.module_order) === JSON.stringify(adminOrder) && JSON.stringify(readerAfter.module_order) === JSON.stringify(readerOrder), 'Distinct account module orders remain isolated');
  must(adminAfter.theme === adminBefore.theme && adminAfter.locale === adminBefore.locale && adminAfter.offline === adminBefore.offline && readerAfter.theme === readerBefore.theme && readerAfter.locale === readerBefore.locale && readerAfter.offline === readerBefore.offline, 'Ordering changes preserve each account appearance, language and offline preference');
  await reader.goto(origin.origin + '/settings');
  must(JSON.stringify(await reader.locator('#module-order-list [data-module-id]').evaluateAll(rows => rows.map(row => row.dataset.moduleId))) === JSON.stringify(readerOrder), 'Reader ordering is rendered from that account on the next request');
  must(await reader.locator('#more-dialog a[href^="/m/"]').count() === 0, 'Settings tray contains no duplicated module destinations');
  must(await saveOrder(page, adminBefore.module_order) === 200, 'Synthetic administrator order restored after account-isolation check');
  await readerContext.close();
  await page.locator('.navigation-trigger').click();
  await page.locator('#more-dialog form[action="/logout"] button').click();
  await page.waitForURL('**/login');
  await language(page, 'en');
  const rejectedPasskey = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/auth/passkeys/login/finish');
  await page.locator('[data-passkey-login]').click();
  const rejectedResponse = await rejectedPasskey;
  must(rejectedResponse.status() >= 400 && rejectedResponse.status() < 500, 'The deleted credential cannot authenticate despite a valid device assertion');
  await page.locator('[data-passkey-status].security-error').waitFor({state: 'visible'});
  must(await page.locator('[data-passkey-status]').innerText(), 'Failed passkey login exposes its recovery message');
  must(new URL(page.url()).pathname === '/login' && (await accountContext.request.get(origin.origin + '/api/v1/core/bootstrap')).status() === 401,
    'Rejected passkey login leaves the browser unauthenticated');
  await capture(page, 'login-deleted-passkey-error');
  await cdp.send('WebAuthn.removeVirtualAuthenticator', {authenticatorId});
  must(report.errors.length === 0, 'No browser JavaScript errors');
  must(report.externalRequests.length === 0, 'No external frontend requests');
  report.passed = true;
})().catch(async error => {
  report.failure = redact(error.message || error);
  process.exitCode = 1;
  if (diagnosticPage && !diagnosticPage.isClosed()) {
    report.failurePath = new URL(diagnosticPage.url()).pathname;
    report.securityStatus = (await diagnosticPage.locator('[data-security-status], [data-passkey-status]').allTextContents()).map(redact);
    const file = 'failure-settings-security.png';
    await diagnosticPage.screenshot({path: path.join(output, file), fullPage: true, animations: 'disabled',
      mask: [diagnosticPage.locator('input[type=password], code.secret, .codes code')]});
    report.screenshots.push(file);
  }
}).finally(async () => {
  if (browser) await browser.close();
  await fs.mkdir(output, {recursive: true});
  await fs.writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({passed: report.passed, checks: report.checks.length, accessibility: report.accessibility.length,
    screenshots: report.screenshots.length, ...(report.failure ? {failure: report.failure} : {})}));
});
