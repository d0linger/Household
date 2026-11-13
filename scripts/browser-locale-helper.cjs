'use strict';

// Existing acceptance scripts assert reviewed German copy. Select it through
// the real user setting instead of relying on a previous suite's preference.
async function setBrowserLocale(page, locale = 'de') {
  if (!['de', 'en'].includes(locale)) throw Error('Unsupported acceptance locale');
  await page.evaluate(async locale => {
    const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
    const response = await fetch('/settings/language', {
      method: 'POST',
      headers: {'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRF-Token': csrf},
      body: new URLSearchParams({csrf, locale, return_to: location.pathname}),
    });
    if (!response.ok) throw Error('Could not select acceptance language: ' + response.status);
  }, locale);
  await page.reload({waitUntil: 'load'});
  const actual = await page.locator('html').getAttribute('data-locale');
  if (actual !== locale) throw Error('Acceptance language did not change');
}

module.exports = {setBrowserLocale};
