'use strict';

const fs = require('node:fs/promises');
const path = require('node:path');
const AxeBuilder = require('@axe-core/playwright').default;

// Runs against the already started disposable review server. It never starts or
// resets a database and never accepts the live application's port 8100.
module.exports = async function checkAccessibilityHardening(page, report = {checks: [], accessibility: []}) {
  const base = new URL(page.url()).origin;
  requireReviewOrigin(base);
  const must = (value, message) => {
    if (!value) throw new Error(message);
    report.checks.push(message);
  };
  const fixtures = await page.evaluate(async () => {
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    let bootstrap = await fetch('/api/v1/core/bootstrap').then(response => response.json());
    for (const module of bootstrap.modules) {
      const state = bootstrap.states[module.id];
      if (state.state === 'enabled') continue;
      const response = await fetch('/settings/modules/' + module.id, {
        method: 'POST', body: new URLSearchParams({csrf, enabled: 'true', version: String(state.version)})
      });
      if (!response.ok) throw new Error('Unable to enable isolated browser module');
    }
    bootstrap = await fetch('/api/v1/core/bootstrap').then(response => response.json());
    const create = async (module, entity, data) => {
      const response = await fetch(`/api/v1/m/${module}/${entity}`, {
        method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID()},
        body: JSON.stringify({data, generation: bootstrap.states[module].generation})
      });
      if (!response.ok) throw new Error('Unable to create isolated accessibility fixture: ' + response.status);
      return response.json();
    };
    const suffix = Date.now();
    const list = await create('shopping', 'lists', {name: 'Accessibility list ' + suffix});
    const recipe = await create('recipes', 'recipes', {title: 'Accessibility recipe ' + suffix, base_servings: '2'});
    await create('recipes', 'ingredients', {recipe_id: recipe.id, name: 'Synthetic ingredient', quantity: '1', unit_code: 'piece', position: '1'});
    const tagName = 'Accessibility tag ' + suffix;
    const tag = await create('recipes', 'tags', {name: tagName});
    const currentRecipe = await fetch('/api/v1/m/recipes/recipes/' + recipe.id).then(response => response.json());
    const assigned = await fetch('/api/v1/m/recipes/recipes/' + recipe.id + '/actions/tags', {
      method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID()},
      body: JSON.stringify({data: {tag_ids: [tag.id]}, version: currentRecipe.version, generation: bootstrap.states.recipes.generation})
    });
    if (!assigned.ok) throw new Error('Unable to assign existing accessibility tag: ' + assigned.status);
    const expense = await create('expenses', 'entries', {occurred_on: '2026-10-24', description: 'Accessibility expense ' + suffix, amount_minor: '1.00', kind: 'expense'});
    return {list: list.id, recipe: recipe.id, tag: tag.id, tagName, expense: expense.id};
  });
  async function scanDialog(name, selector) {
    const inspect = () => page.locator(selector).evaluate(dialog => ({
      opacity: getComputedStyle(dialog).opacity,
      color: getComputedStyle(dialog).color,
      backgroundColor: getComputedStyle(dialog).backgroundColor,
      animations: dialog.getAnimations({subtree: true}).map(animation => ({name: animation.animationName || animation.constructor.name, state: animation.playState, currentTime: animation.currentTime, duration: animation.effect.getComputedTiming().duration}))
    }));
    const before = await inspect();
    await page.locator(selector).evaluate(async dialog => {
      await Promise.allSettled(dialog.getAnimations({subtree: true}).map(animation => animation.finished));
    });
    const settled = await inspect();
    must(Number(settled.opacity) === 1 && settled.animations.every(animation => animation.state === 'finished'), 'Dialog visual state settled before axe: ' + name);
    const result = await new AxeBuilder({page}).include(selector).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    const after = await inspect();
    report.accessibility.push({name, before, settled, after, violations: result.violations});
    if (result.violations.length) {
      await fs.mkdir(path.resolve('outputs/qa'), {recursive: true});
      await page.screenshot({path: path.resolve('outputs/qa/a11y-dialog-' + name.toLowerCase() + '.png'), fullPage: true});
    }
    must(result.violations.length === 0, 'Open-dialog accessibility scan: ' + name);
  }

  await page.setViewportSize({width: 390, height: 844});
  await page.goto(base + '/settings');
  await page.getByRole('button', {name: 'Mehr', exact: true}).click();
  must(await page.getByRole('dialog', {name: 'Deine Module', exact: true}).isVisible(), 'More dialog has its visible heading as accessible name');
  await scanDialog('More', '#more-dialog');
  await page.keyboard.press('Escape');
  await page.goto(base + '/m/shopping/lists');
  await page.getByRole('button', {name: '＋ Hinzufügen', exact: true}).click();
  const createDialog = page.getByRole('dialog', {name: 'Listen', exact: true});
  must(await createDialog.isVisible(), 'Create dialog has its visible heading as accessible name');
  await scanDialog('Create', '#create-dialog');
  const typedName = 'Preserved accessibility input';
  await createDialog.locator('[name=name]').fill(typedName);
  let errorStatus = 422;
  const rejectedSave = route => route.fulfill({status: errorStatus, contentType: 'application/json', body: JSON.stringify({error: 'Test: Eintrag prüfen und erneut speichern.'})});
  await page.route('**/api/v1/m/shopping/lists', rejectedSave);
  try {
    for (errorStatus of [422, 409]) {
      await createDialog.getByRole('button', {name: 'Speichern', exact: true}).click();
      await page.waitForFunction(() => document.activeElement?.matches('#create-dialog .form-error[tabindex="-1"]'));
      must(await createDialog.locator('[name=name]').inputValue() === typedName, `Input survives ${errorStatus} response and error summary receives keyboard focus`);
    }
  } finally {
    await page.unroute('**/api/v1/m/shopping/lists', rejectedSave);
  }
  await page.keyboard.press('Escape');
  await page.goto(base + '/m/shopping/lists/' + fixtures.list);
  await page.locator('[data-delete]').click();
  must(await page.getByRole('dialog', {name: 'Eintrag löschen?', exact: true}).isVisible(), 'Delete dialog has an accessible name and warning description');
  must(await page.locator('#confirm-dialog').getAttribute('aria-describedby') === 'confirm-dialog-description', 'Delete dialog explicitly associates its irreversible-action warning');
  await scanDialog('Delete', '#confirm-dialog');
  await page.keyboard.press('Escape');

  for (const failure of ['http', 'network']) {
    let attempts = 0, tagAttempts = 0, tagMutations = 0;
    const failReference = route => failure === 'network' ? route.abort('failed') : route.fulfill({status: 503, contentType: 'application/json', body: '{"error":"temporary"}'});
    const requiredRoute = async route => {
      attempts++;
      if (attempts === 1) return failReference(route);
      return route.continue();
    };
    const tagsRoute = route => ++tagAttempts === 1 ? failReference(route) : route.continue();
    const tagActionPath = '/api/v1/m/recipes/recipes/' + fixtures.recipe + '/actions/tags';
    const countTagMutations = request => { if (request.method() === 'POST' && new URL(request.url()).pathname === tagActionPath) tagMutations++; };
    page.on('request', countTagMutations);
    await page.route('**/api/v1/core/reference-options/shopping/lists?*', requiredRoute);
    await page.route('**/api/v1/core/reference-options/recipes/tags?*', tagsRoute);
    await page.route('**/api/v1/core/reference-options/expenses/categories?*', failReference);
    try {
      await page.goto(base + '/m/recipes/recipes/' + fixtures.recipe);
      const required = page.locator('form[action$="/actions/shopping"]');
      const tags = page.locator('form[action$="/actions/tags"]');
      await required.locator('.reference-error:not([hidden])').waitFor();
      await tags.locator('.reference-error:not([hidden])').waitFor();
      await required.locator('[name=servings]').fill('3');
      must(await required.locator('button:not([type])').isDisabled(), `${failure} reference failure blocks the required action`);
      must(await tags.locator('button:not([type])').isDisabled(), `${failure} failed multiple reference blocks destructive tag replacement`);
      await tags.evaluate(form => form.dispatchEvent(new Event('submit', {bubbles: true, cancelable: true})));
      must(await tags.locator('.reference-error').evaluate(notice => notice === document.activeElement), `${failure} programmatic submit is blocked and focuses the tag recovery message`);
      const retainsExistingTag = await page.evaluate(async ({recipe, tagName}) => {
        const response = await fetch('/api/v1/m/recipes/recipes?q=' + encodeURIComponent('#' + tagName));
        if (!response.ok) throw new Error('Unable to verify existing recipe tag');
        return (await response.json()).records.some(record => record.id === recipe);
      }, fixtures);
      must(retainsExistingTag && tagMutations === 0, `${failure} blocked tag submission leaves the existing server-side tag assigned`);
      must(await required.locator('.reference-error').innerText().then(text => text.includes('Deine Eingaben bleiben erhalten')), `${failure} reference error explains recovery and retained input`);
      await required.getByRole('button', {name: 'Erneut laden', exact: true}).click();
      await required.locator('[name=list_id][data-reference-state=ready]').waitFor();
      must(await required.locator('button:not([type])').isEnabled(), `${failure} required reference retry re-enables submission`);
      must(await required.locator('[name=servings]').inputValue() === '3', `${failure} retry preserves other form values`);
      await required.locator('[name=list_id]').selectOption(fixtures.list);
      must(await required.locator('[name=list_id]').inputValue() === fixtures.list, `${failure} retry restores usable reference options`);
      must(await required.locator('.reference-error').isHidden(), `${failure} retry clears its visible error`);
      must(attempts === 2, `${failure} retry issues exactly one replacement request`);
      await tags.getByRole('button', {name: 'Erneut laden', exact: true}).click();
      await tags.locator('[name=tag_ids][data-reference-state=ready]').waitFor();
      must(await tags.locator('button:not([type])').isEnabled(), `${failure} loaded multiple reference re-enables explicit tag replacement`);
      await tags.locator('[name=tag_ids]').selectOption(fixtures.tag);
      must(await tags.locator('[name=tag_ids]').inputValue() === fixtures.tag && tagAttempts === 2, `${failure} tag retry restores the existing tag as a selectable option`);
      await page.goto(base + '/m/expenses/entries/' + fixtures.expense);
      const optionalScalar = page.locator('form[action$="/actions/post"]');
      await optionalScalar.locator('.reference-error:not([hidden])').waitFor();
      must(await optionalScalar.locator('[name=category_id]').isDisabled() && await optionalScalar.locator('button:not([type])').isEnabled(), `${failure} optional scalar reference failure leaves its action available`);
    } finally {
      page.off('request', countTagMutations);
      await page.unroute('**/api/v1/core/reference-options/shopping/lists?*', requiredRoute);
      await page.unroute('**/api/v1/core/reference-options/recipes/tags?*', tagsRoute);
      await page.unroute('**/api/v1/core/reference-options/expenses/categories?*', failReference);
    }
  }
  return report;
};

function requireReviewOrigin(base) {
  const target = new URL(base);
  if (!['http:', 'https:'].includes(target.protocol) || !['localhost', '127.0.0.1', '[::1]'].includes(target.hostname) || target.port !== '8101' || target.username || target.password || target.pathname !== '/' || target.search || target.hash) {
    throw new Error('Accessibility checks require the isolated loopback review server on port 8101');
  }
}

if (require.main === module) {
  (async () => {
    const requested = process.env.HOUSEHOLD_BROWSER_URL || 'http://localhost:8101';
    requireReviewOrigin(requested);
    const base = new URL(requested).origin;
    const {chromium} = require('playwright');
    const browser = await chromium.launch({headless: true, executablePath: process.env.CHROME_PATH || (process.platform === 'win32' ? 'C:/Program Files/Google/Chrome/Application/chrome.exe' : undefined)});
    const report = {checks: [], accessibility: [], errors: []};
    try {
      const context = await browser.newContext({serviceWorkers: 'block'});
      await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
      const page = await context.newPage();
      page.on('pageerror', error => report.errors.push(String(error)));
      async function login(password) {
        await page.goto(base + '/login');
        await page.locator('[name=username]').fill('bootstrap');
        await page.locator('[name=password]').fill(password);
        await Promise.all([page.waitForURL(url => !url.pathname.endsWith('/login')), page.getByRole('button', {name: /^Anmelden/}).click()]);
      }
      const password = process.env.HOUSEHOLD_BROWSER_PASSWORD || 'Browser review only 1234';
      await login(password);
      if (new URL(page.url()).pathname === '/change-password') {
        await page.locator('[name=current]').fill(password);
        await page.locator('[name=next]').fill('Browser review only 1234');
        await Promise.all([page.waitForURL('**/login'), page.getByRole('button', {name: 'Passwort speichern', exact: true}).click()]);
        await login('Browser review only 1234');
      }
      await module.exports(page, report);
      if (report.errors.length) throw new Error('Unexpected page errors: ' + report.errors.join('; '));
      report.passed = true;
      console.log(`Accessibility hardening passed: ${report.checks.length} checks, ${report.accessibility.length} dialog scans`);
    } catch (error) {
      report.passed = false; report.failure = String(error); throw error;
    } finally {
      await fs.mkdir(path.resolve('outputs/qa'), {recursive: true});
      await fs.writeFile(path.resolve('outputs/qa/a11y-hardening.json'), JSON.stringify(report, null, 2));
      await browser.close();
    }
  })().catch(error => {console.error(error); process.exitCode = 1;});
}
