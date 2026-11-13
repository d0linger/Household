'use strict';
// Uses only the disposable Household fixture. Run after browser-check.cjs has
// prepared its nine modules and completed the bootstrap password change.
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const AxeBuilder = require('@axe-core/playwright').default;
const base = process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101';
if (!['http://localhost:8101', 'http://127.0.0.1:8101'].includes(base)) throw Error('Requires isolated loopback port 8101');
const output = path.resolve(process.env.HOUSEHOLD_NAVIGATION_BROWSER_OUTPUT || 'outputs/navigation-personalization/browser');
if (!output.startsWith(path.resolve('outputs') + path.sep)) throw Error('Browser evidence must stay within workspace outputs');
const report = {checks: [], accessibility: [], screenshots: [], errors: [], externalRequests: [], passed: false};
let browser, page;
const must = (value, label) => {if (!value) throw Error(label); report.checks.push(label);};
async function open() {
  await page.locator('.navigation-trigger').click();
  await page.locator('#more-dialog').evaluate(async dialog => {await Promise.all(dialog.getAnimations().map(animation => animation.finished));});
}
async function preferences(values) {
  const status = await page.evaluate(async values => (await fetch('/api/v1/core/preferences', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': document.querySelector('meta[name=csrf-token]').content}, body: JSON.stringify(values)})).status, values);
  must(status === 200, 'Synthetic preferences accepted: ' + JSON.stringify(values));
}
async function checkModuleOrder(target, context) {
  await target.setViewportSize({width:1440, height:1000});
  await target.goto(base + '/settings');
  const list = target.locator('#module-order-list'), form = target.locator('#module-order-form');
  const read = () => target.evaluate(async () => {const response = await fetch('/api/v1/core/preferences'); if (!response.ok) throw Error('Preference read failed'); return response.json();});
  const ids = () => list.locator('[data-module-id]').evaluateAll(rows => rows.map(row => row.dataset.moduleId));
  const rails = () => target.evaluate(() => ['.sidebar nav', '.mobile-nav'].map(selector => [...document.querySelectorAll(selector + ' a[data-module-id]')].map(link => link.dataset.moduleId)));
  const saved = await read(), initial = await ids();
  must(initial.length === 9 && new Set(initial).size === 9, 'Every known module has one ordering row');
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  const save = async reset => {
    const response = target.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/core/preferences' && response.request().method() === 'POST');
    await form.locator(reset ? '[data-order-reset]' : 'button.primary').click();
    must((await response).status() === 200, reset ? 'Reset reaches the real preference endpoint' : 'Order save reaches the real preference endpoint');
    await target.waitForFunction(() => document.querySelector('#module-order-form').getAttribute('aria-busy') !== 'true', undefined, {timeout:20000});
    must(await target.locator(reset ? '[data-order-reset]' : '#module-order-status').evaluate(node => node === document.activeElement), reset ? 'Reset retains keyboard focus on its action' : 'Successful save moves keyboard focus to its confirmation');
  };
  let paused = false;
  async function setMetersEnabled(enabled) {
    return target.evaluate(async enabled => {
      const bootstrap = await (await fetch('/api/v1/core/bootstrap')).json();
      const state = bootstrap.states.meters;
      const response = await fetch('/settings/modules/meters', {method:'POST', body:new URLSearchParams({csrf:document.querySelector('meta[name=csrf-token]').content, enabled:String(enabled), version:String(state.version)})});
      return response.status;
    }, enabled);
  }
  try {
    must(await form.locator('button.primary').isDisabled(), 'Unchanged order does not offer a redundant save');
    const third = initial[2], row = list.locator('[data-module-id="' + third + '"]');
    await row.locator('[data-order-up]').focus(); await target.keyboard.press('Enter');
    const draft = await ids();
    must(draft[1] === third && same([...draft].sort(), [...initial].sort()), 'Keyboard arrow action moves only the chosen module');
    must(await row.locator('[data-order-up]').evaluate(button => button === document.activeElement), 'Reorder retains the acting control focus');
    must(same((await read()).module_order, saved.module_order) && (await rails()).every(order => same(order, initial)), 'Unsaved draft changes neither stored preference nor active navigation');
    const failSave = route => route.fulfill({status:503, contentType:'application/json', body:JSON.stringify({error:'Synthetic rejected preference save'})});
    await target.route('**/api/v1/core/preferences', failSave);
    try {
      await form.locator('button.primary').click();
      await target.locator('#module-order-error').waitFor({state:'visible'});
      must(await target.locator('#module-order-error').innerText() === 'Synthetic rejected preference save' && same(await ids(), draft), 'Failed save exposes the error and preserves the editable draft');
      must(await form.locator('button.primary').isEnabled(), 'Failure re-enables a real retry');
      must(await form.locator('button.primary').evaluate(button => button === document.activeElement), 'Failed save restores keyboard focus to retry');
    } finally {await target.unroute('**/api/v1/core/preferences', failSave);}
    must(same((await read()).module_order, saved.module_order), 'Rejected synthetic save leaves the real stored preference untouched');
    await save(false);
    const confirmed = await read();
    must(same(confirmed.module_order, draft) && (await rails()).every(order => same(order, draft)), 'Successful retry updates both rails and the account preference');
    must(confirmed.theme === saved.theme && confirmed.locale === saved.locale && confirmed.offline === saved.offline, 'Order save preserves theme, language and offline consent');
    await target.reload();
    must(same(await ids(), draft) && (await rails()).every(order => same(order, draft)), 'Personal order survives a full server render');
    const peer = await browser.newContext({storageState:await context.storageState(), viewport:{width:1440,height:1000}, serviceWorkers:'block'});
    try {
      const second = await peer.newPage();
      await second.goto(base + '/settings');
      must(same(await second.locator('#module-order-list [data-module-id]').evaluateAll(rows => rows.map(row => row.dataset.moduleId)), draft), 'A second browser context for the same account sees the saved order');
    } finally {await peer.close();}
    const first = draft[0];
    await list.locator('[data-module-id="' + first + '"] [data-order-drag]').focus(); await target.keyboard.press('ArrowDown');
    must((await ids())[1] === first && await list.locator('[data-module-id="' + first + '"] [data-order-drag]').evaluate(button => button === document.activeElement), 'Drag handle also supports arrow-key movement with retained focus');
    await save(false);
    const beforeDrag = await ids(), source = list.locator('[data-module-id="' + beforeDrag[0] + '"] [data-order-drag]'), destination = list.locator('[data-module-id="' + beforeDrag[3] + '"]');
    await source.dragTo(destination);
    const dragged = await ids();
    must(!same(dragged, beforeDrag) && same([...dragged].sort(), [...beforeDrag].sort()), 'Native desktop dragging reorders without losing or duplicating a module');
    await save(false);
    must(same((await read()).module_order, dragged), 'Dragged order is persisted through the real endpoint');
    must(await setMetersEnabled(false) === 200, 'Synthetic Meter module can be paused for retention verification'); paused = true;
    await target.reload();
    must(same(await ids(), dragged) && (await rails()).every(order => same(order, dragged.filter(id => id !== 'meters'))), 'Paused module retains its ordered row while both navigation rails omit it');
    must(await setMetersEnabled(true) === 200, 'Synthetic Meter module is re-enabled'); paused = false;
    await target.reload();
    must((await rails()).every(order => same(order, dragged)), 'Re-enabled module returns to its personal position');
    for (const width of [320,390]) {
      await target.setViewportSize({width,height:844});
      const geometry = await target.locator('.mobile-nav').evaluate(nav => ({width:nav.clientWidth,scrollWidth:nav.scrollWidth,count:nav.querySelectorAll('a').length,modules:nav.querySelectorAll('a[data-module-id]').length,home:nav.querySelectorAll('a[data-home-link][href="/"]').length,hidden:[...nav.querySelectorAll('a')].filter(link => link.hidden).length,pageOverflow:document.documentElement.scrollWidth>innerWidth+1}));
      must(geometry.count === 10 && geometry.modules === 9 && geometry.home === 1 && geometry.hidden === 0 && geometry.scrollWidth > geometry.width && !geometry.pageOverflow, 'All enabled destinations are horizontally scrollable without page overflow: ' + width);
      const last = target.locator('.mobile-nav a[data-module-id]').last();
      await last.focus();
      const visible = await last.evaluate(link => {const r=link.getBoundingClientRect(), nav=link.closest('nav').getBoundingClientRect();return r.left>=nav.left-1&&r.right<=nav.right+1&&r.width>=44&&r.height>=44;});
      must(visible, 'Keyboard focus reveals the final mobile module with a full touch target: ' + width);
    }
    await target.setViewportSize({width:1440,height:1000}); await save(true);
    const reset = await read();
    must(same(reset.module_order, []) && same(await ids(), reset.effective_module_order) && (await rails()).every(order => same(order, reset.effective_module_order)), 'Immediate reset clears the override and restores canonical order in both rails');
    await target.reload();
    must(same(await ids(), reset.effective_module_order), 'Reset persists across page reload');
    for (const locale of ['de','en']) {
      await preferences({locale}); await target.reload();
      for (const theme of ['light','dark','pink-light','pink-dark']) {
        await target.evaluate(theme => window.HouseholdTheme.apply(theme,{persist:true}), theme);
        await target.setViewportSize({width:locale==='de'?320:1440,height:locale==='de'?844:1000});
        const scan = await new AxeBuilder({page:target}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
        report.accessibility.push({label:'module-order/'+locale+'/'+theme, violations:scan.violations});
        must(scan.violations.length===0, 'Ordering page accessibility: '+locale+'/'+theme);
        must(await target.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1), 'Ordering rows fit localized narrow/desktop layout: '+locale+'/'+theme);
        await target.evaluate(()=>{document.activeElement?.blur();scrollTo({top:0,behavior:'instant'});});
        const file='order-'+locale+'-'+theme+'.png';await target.screenshot({path:path.join(output,file),fullPage:true,animations:'disabled'});report.screenshots.push(file);
      }
    }
  } finally {
    if (paused) must(await setMetersEnabled(true)===200, 'Paused fixture module restored after failure');
    await preferences({module_order:saved.module_order,locale:saved.locale,theme:saved.theme});
  }
}
(async () => {
  await fs.mkdir(output, {recursive: true});
  browser = await chromium.launch({headless: true, ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : {channel: 'msedge'})});
  const context = await browser.newContext({viewport: {width: 1440, height: 1000}, serviceWorkers: 'block'});
  await context.route('**/*', route => {const url = new URL(route.request().url()); if (url.origin !== base) {report.externalRequests.push(url.origin); return route.abort();} return route.continue();});
  page = await context.newPage(); page.on('pageerror', error => report.errors.push(error.message));
  await page.goto(base + '/login');
  await page.locator('[name=username]').fill('bootstrap');
  await page.locator('[name=password]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD || 'Browser review only 1234');
  await Promise.all([page.waitForURL(url => !url.pathname.endsWith('/login')), page.locator('form[action="/login"] button.primary').click()]);
  await page.goto(base + '/settings/security');
  must(await page.locator('.navigation-trigger').count() === 1 && await page.locator('.topbar > .navigation-trigger').count() === 1, 'Settings has one direct top-bar opener');
  must(await page.locator('.sidebar-menu,#header-logout,.header-logout,.mobile-nav button').count() === 0, 'Removed sidebar/menu/logout duplicates and mobile More button');
  must(await page.locator('form[action="/logout"]').count() === 1 && await page.locator('#more-dialog form[action="/logout"]').count() === 1, 'Only the settings tray contains logout');
  must(await page.locator('main .settings-nav').count() === 0, 'Settings tabs have been removed from the page content');
  for (const route of ['/settings', '/settings/modules', '/settings/security', '/settings/notifications', '/settings/users', '/settings/audit']) {
    await open();
    must(await page.locator('#more-dialog .settings-nav a').count() === 6, 'Six independent administrator destinations');
    await page.locator('#more-dialog .settings-nav a[href="' + route + '"]').click();
    await page.waitForURL(base + route);
    must(await page.locator('#more-dialog .settings-nav [aria-current=page]').getAttribute('href') === route, 'Direct tray route and active state: ' + route);
    must(await page.locator('main h1').count() === 1, 'Destination has one page heading: ' + route);
  }
  await open();
  must(await page.locator('#more-dialog a[href^="/m/"],#more-dialog .tray-links').count() === 0, 'Modules are absent from the settings tray');
  const moduleRoutes = await page.locator('.sidebar nav a[data-module-id]').evaluateAll(links => links.map(link => link.getAttribute('href')));
  must(moduleRoutes.length === 9, 'All enabled modules remain in the desktop rail');
  await page.keyboard.press('Escape');
  for (const route of moduleRoutes) {
    await page.goto(base + route);
    must(await page.locator('.sidebar nav [aria-current=page]').getAttribute('href') === route, 'Current module is marked in the desktop rail: ' + route);
    must(await page.locator('.mobile-nav a[data-module-id]').count() === 9, 'Mobile rail keeps every enabled module: ' + route);
  }
  await page.goto(base + '/settings/security');
  for (const locale of ['de', 'en']) {
    await preferences({locale}); await page.reload();
    for (const theme of ['light', 'dark', 'pink-light', 'pink-dark']) {
      await page.evaluate(theme => window.HouseholdTheme.apply(theme, {persist: true}), theme);
      for (const viewport of [{width: 1440, height: 1000}, {width: 768, height: 1024}, {width: 390, height: 844}, {width: 320, height: 568}, {width: 844, height: 390}]) {
        await page.setViewportSize(viewport);
        await page.mouse.move(0, 0);
        const label = locale + '/' + theme + '/' + viewport.width;
        const opener = await page.locator('.navigation-trigger').evaluate(node => {const rect = node.getBoundingClientRect(), tools = document.querySelector('.header-tools').getBoundingClientRect(); return {left:rect.left,right:rect.right,top:rect.top,width:rect.width,height:rect.height,toolsRight:tools.right};});
        must(opener.right <= viewport.width + 1 && opener.width >= 44 && opener.height >= 44 && opener.top < 32 && (viewport.width <= 760 || opener.left >= opener.toolsRight), 'Menu stays at the top-right edge: ' + label);
        const controls = await page.locator('.header-tools .appearance-trigger,.header-tools .language-control button').evaluateAll(nodes => nodes.map(node => {const r = node.getBoundingClientRect(), css = getComputedStyle(node); return {width: r.width, height: r.height, right: r.right, background: css.backgroundColor, border: css.borderTopWidth};}));
        must(controls.length === 2 && controls.every(c => c.width >= 44 && c.height >= 44 && c.right <= viewport.width + 1 && c.background === 'rgba(0, 0, 0, 0)' && c.border === '0px'), 'Quiet tools retain usable targets: ' + label);
        must(await page.locator('.language-control button').innerText() === (locale === 'en' ? 'DE' : 'EN'), 'Compact language label: ' + label);
        await open();
        const geometry = await page.locator('#more-dialog').evaluate(dialog => {const r = dialog.getBoundingClientRect(), content = dialog.querySelector('.tray-content'), footer = dialog.querySelector('.tray-session').getBoundingClientRect(); return {right: r.right, top: r.top, height: r.height, width: r.width, footer: footer.bottom, content: content.clientHeight, overflow: document.documentElement.scrollWidth > innerWidth + 1, bodyOverflow: getComputedStyle(document.body).overflow};});
        must(Math.abs(geometry.right - viewport.width) <= 1 && geometry.top === 0 && Math.abs(geometry.height - viewport.height) <= 1 && geometry.width <= 344 && geometry.content > 0 && geometry.footer <= viewport.height + 1 && !geometry.overflow && geometry.bodyOverflow === 'hidden', 'Bounded right-side drawer with visible session actions: ' + label);
        must(await page.locator('#more-dialog .tray-close').evaluate(node => node === document.activeElement), 'Close control receives focus: ' + label);
        await page.keyboard.press('Shift+Tab');
        // Chromium may visit browser chrome at the modal boundary. It must
        // never focus the inert page; returning backwards reaches tray logout.
        if (await page.evaluate(() => !document.hasFocus() && document.activeElement === document.body)) await page.keyboard.press('Shift+Tab');
        must(await page.locator('#more-dialog form[action="/logout"] button').evaluate(node => node === document.activeElement), 'Native modal traps reverse Tab on session action: ' + label);
        await page.keyboard.press('Escape');
        await page.waitForFunction(() => {const opener = document.querySelector('.navigation-trigger'); return opener === document.activeElement && opener.getAttribute('aria-expanded') === 'false';});
        must(await page.locator('.navigation-trigger').evaluate(node => node === document.activeElement && node.getAttribute('aria-expanded') === 'false'), 'Escape restores opener focus and collapsed state: ' + label);
      }
      await page.setViewportSize({width: locale === 'en' ? 1440 : 390, height: locale === 'en' ? 1000 : 844});
      await open();
      const result = await new AxeBuilder({page}).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
      report.accessibility.push({label: locale + '/' + theme, violations: result.violations});
      must(result.violations.length === 0, 'Drawer accessibility: ' + locale + '/' + theme);
      const file = locale + '-' + theme + '.png';
      await page.screenshot({path: path.join(output, file), animations: 'disabled'}); report.screenshots.push(file);
      await page.mouse.click(8, 120);
      must(await page.locator('#more-dialog').evaluate(dialog => !dialog.open), 'Backdrop dismisses drawer: ' + locale + '/' + theme);
    }
  }
  await page.emulateMedia({reducedMotion: 'reduce'}); await open();
  must(await page.locator('#more-dialog').evaluate(dialog => dialog.getAnimations().length === 0), 'Reduced motion suppresses drawer animation');
  await page.evaluate(() => {window.trayInstallPrompted = 0; const event = new Event('beforeinstallprompt', {cancelable: true}); event.prompt = async () => {window.trayInstallPrompted++;}; dispatchEvent(event);});
  must(await page.locator('#more-dialog #install').isVisible(), 'Install becomes available inside tray after platform event');
  await page.locator('#install').click();
  must(await page.evaluate(() => window.trayInstallPrompted === 1 && document.querySelector('#install').hidden), 'Install invokes the platform prompt once and hides the consumed action');
  await page.keyboard.press('Escape');
  await preferences({locale: 'de', theme: 'light'}); await page.reload();
  await checkModuleOrder(page, context);
  await page.goto(base + '/settings/security');
  // Test a real language form submission, preserving the current route.
  await page.locator('.header-tools .language-control button').click();
  await page.waitForFunction(() => document.documentElement.dataset.locale === 'en');
  must(new URL(page.url()).pathname === '/settings/security', 'Language switch preserves the current page');
  await page.locator('.header-tools .language-control button').click();
  await page.waitForFunction(() => document.documentElement.dataset.locale === 'de');
  await open();
  await page.locator('#more-dialog form[action="/logout"] button').click();
  await page.waitForURL('**/login');
  must((await context.request.get(base + '/api/v1/core/bootstrap')).status() === 401, 'Tray logout revokes the actual server session');
  if (process.env.HOUSEHOLD_BROWSER_HANDOFF) {
    const handoff = path.resolve(process.env.HOUSEHOLD_BROWSER_HANDOFF), privateRoot = path.resolve('.local') + path.sep;
    must(handoff.startsWith(privateRoot), 'Authenticated handoff stays in private local fixture');
    await page.locator('[name=username]').fill('bootstrap');
    await page.locator('[name=password]').fill(process.env.HOUSEHOLD_BROWSER_PASSWORD || 'Browser review only 1234');
    await Promise.all([page.waitForURL(url => !url.pathname.endsWith('/login')), page.locator('form[action="/login"] button.primary').click()]);
    await context.storageState({path: handoff});
  }
  must(report.errors.length === 0 && report.externalRequests.length === 0, 'No JavaScript errors or external frontend requests');
  report.passed = true;
})().catch(async error => {report.failure = error.message; process.exitCode = 1; if (page && !page.isClosed()) await page.screenshot({path: path.join(output, 'failure.png'), animations: 'disabled'});}).finally(async () => {if (browser) await browser.close(); await fs.mkdir(output, {recursive: true}); await fs.writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2)); console.log(JSON.stringify({passed: report.passed, checks: report.checks.length, axe: report.accessibility.length, failure: report.failure}));});
