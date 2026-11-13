'use strict';

// Read the real worker caches; never seed a missing entry to satisfy the check.
module.exports = async function assertPublicCache(page) {
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.waitForFunction(async () => {
    if (!navigator.serviceWorker.controller) return false;
    const response = await caches.match('/__household_public_locale', {cacheName: 'household-public-preferences-v1'});
    return response && ['de', 'en'].includes(await response.text());
  });
  return page.evaluate(async () => {
    const requireValue = (value, message) => {if (!value) throw Error('Public cache contract: ' + message);};
    const preferenceName = 'household-public-preferences-v1';
    const names = await caches.keys();
    requireValue(names.length === 2 && names.includes(preferenceName) && names.filter(name => /^household-[a-f0-9]{16}$/.test(name)).length === 1, 'one versioned asset cache and one public preference cache');
    const expectedShells = new Set(['/offline', '/offline/scans', '/offline/shopping', '/offline/recipes'].flatMap(route => ['de', 'en'].map(locale => route + '?lang=' + locale)));
    const seenShells = new Set(), assetPaths = [];
    const privateValues = [document.body.dataset.user, document.body.dataset.household, document.querySelector('meta[name=csrf-token]')?.content].filter(Boolean);
    for (const name of names) {
      const cache = await caches.open(name), requests = await cache.keys();
      requireValue(requests.length > 0 && requests.length <= 512, 'bounded nonempty cache');
      if (name === preferenceName) requireValue(requests.length === 1, 'exactly one public preference entry');
      for (const request of requests) {
        const url = new URL(request.url), key = url.pathname + url.search;
        requireValue(request.method === 'GET' && url.origin === location.origin && !url.hash, 'same-origin GET without fragment');
        const response = await cache.match(request);
        requireValue(response?.status === 200 && !response.redirected, 'successful nonredirected response');
        const contentType = response.headers.get('content-type') || '';
        if (name === preferenceName) {
          requireValue(key === '/__household_public_locale', 'exact public preference URL without query');
          requireValue(contentType === 'text/plain' && ['de', 'en'].includes(await response.text()), 'preference contains only plaintext de or en');
          continue;
        }
        requireValue(response.type === 'basic', 'same-origin public response');
        if (expectedShells.has(key)) {
          requireValue(!seenShells.has(key), 'one cached response per locale-qualified shell');
          seenShells.add(key);
          requireValue(contentType.startsWith('text/html') && response.headers.get('cache-control') === 'public,max-age=0,must-revalidate', 'public HTML shell response');
          const body = await response.text();
          requireValue(body.length <= 1024 * 1024 && !privateValues.some(value => body.includes(value)), 'bounded shell without current private identity or CSRF value');
          const shell = new DOMParser().parseFromString(body, 'text/html');
          requireValue(shell.body.getAttribute('data-user') === '' && shell.body.getAttribute('data-household') === '', 'anonymous shell identity');
          requireValue(shell.querySelector('meta[name=csrf-token]')?.getAttribute('content') === '' && !shell.querySelector('input[name=csrf], #more-dialog, .sidebar, .desktop-greeting, form[method=post i]'), 'shell has no session token or authenticated controls');
          const locale = url.searchParams.get('lang');
          requireValue(shell.documentElement.lang === (locale === 'en' ? 'en-GB' : 'de-AT') && shell.documentElement.dataset.locale === locale, 'shell language matches its cache key');
          continue;
        }
        requireValue(!url.search && /^\/assets\/[a-f0-9]{12}\/[a-z0-9][a-z0-9.-]*\.(?:js|css|svg|png)$/.test(url.pathname), 'only hashed asset URLs without query');
        requireValue(response.headers.get('cache-control') === 'public,max-age=31536000,immutable', 'immutable public asset response');
        const expectedType = {js: 'text/javascript', css: 'text/css', svg: 'image/svg+xml', png: 'image/png'}[url.pathname.split('.').at(-1)];
        requireValue(contentType.split(';')[0] === expectedType, 'asset MIME type excludes private HTML and JSON responses');
        assetPaths.push(url.pathname);
      }
    }
    requireValue(seenShells.size === 8 && [...expectedShells].every(key => seenShells.has(key)), 'all eight exact locale-qualified anonymous shells');
    requireValue(assetPaths.length > 0, 'public hashed assets are present');
    return {shells: [...seenShells], assetPaths};
  });
};
