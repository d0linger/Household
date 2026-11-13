'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../internal/web/static/theme.js'), 'utf8');

function fixture({saved = null, blockedRead = false, blockedWrite = false, dark = false, legacyMedia = false} = {}) {
  const windowEvents = new Map(), documentEvents = new Map(), writes = [];
  let mediaChange;
  const root = {dataset: {}, removeAttribute(name) {assert.equal(name, 'data-theme'); delete this.dataset.theme;}};
  const meta = {content: 'original'};
  const state = {dark, loaded: false, preferredChrome: ''};
  const storage = {
    getItem(name) {assert.equal(name, 'household-theme'); if (blockedRead) throw Error('Blocked'); return saved;},
    setItem(name, value) {if (blockedWrite) throw Error('Quota'); writes.push([name, value]); saved = value;}
  };
  const media = legacyMedia
    ? {addListener(callback) {mediaChange = callback;}}
    : {addEventListener(name, callback) {assert.equal(name, 'change'); mediaChange = callback;}};
  Object.defineProperty(media, 'matches', {get: () => state.dark});
  const window = {
    addEventListener(name, callback) {windowEvents.set(name, callback);},
    matchMedia(query) {assert.equal(query, '(prefers-color-scheme: dark)'); return media;}
  };
  const document = {
    documentElement: root,
    querySelector(selector) {assert.equal(selector, 'meta[name="theme-color"]'); return meta;},
    addEventListener(name, callback, options) {assert.equal(options.once, true); documentEvents.set(name, callback);}
  };
  function computed() {
    return {getPropertyValue(name) {
      if (!state.loaded) return '';
      if (name === '--theme-color') return state.preferredChrome;
      assert.equal(name, '--ground');
      const mode = root.dataset.theme || (state.dark ? 'dark' : 'light');
      return {'light':'#f5f8fa', 'dark':'#101820', 'pink-light':'#fff0f5', 'pink-dark':'#261525'}[mode];
    }};
  }
  vm.runInNewContext(source, {window, document, localStorage: storage, getComputedStyle: computed});
  return {
    controller: window.HouseholdTheme, root, meta, writes, state, storage,
    ready() {state.loaded = true; documentEvents.get('DOMContentLoaded')();},
    storageEvent(key, newValue, area = storage) {windowEvents.get('storage')({key, newValue, storageArea: area});},
    systemDark(value) {state.dark = value; mediaChange();}
  };
}

test('theme initialization restores every supported mode before content without persisting on load', () => {
  for (const saved of ['system', 'light', 'dark', 'pink-system', 'pink-light', 'pink-dark']) {
    const f = fixture({saved});
    assert.equal(f.controller.get(), saved);
    assert.equal(f.root.dataset.theme, saved === 'system' ? undefined : saved === 'pink-system' ? 'pink-light' : saved);
    assert.deepEqual(f.writes, []);
    assert.equal(f.meta.content, 'original');
    f.ready();
    assert.notEqual(f.meta.content, 'original');
  }
});

test('invalid or unreadable persisted theme safely falls back to system', () => {
  for (const saved of [null, '', 'LIGHT', 'rose', '<script>', 'constructor']) {
    const f = fixture({saved, dark: true}); f.ready();
    assert.equal(f.controller.get(), 'system');
    assert.equal(f.root.dataset.theme, undefined);
    assert.equal(f.meta.content, '#101820');
  }
  const f = fixture({saved: 'pink-dark', blockedRead: true});
  assert.equal(f.controller.get(), 'system');
});

test('explicit apply changes all palettes but persists only by request', () => {
  const f = fixture(); f.ready();
  for (const mode of ['light', 'dark', 'pink-light', 'pink-dark', 'pink-system', 'system']) {
    const result = f.controller.apply(mode);
    assert.equal(result.theme, mode); assert.equal(result.persisted, false);
    assert.equal(f.controller.get(), mode);
  }
  assert.deepEqual(f.writes, []);
  const result = f.controller.apply('pink-dark', {persist: true});
  assert.equal(result.persisted, true);
  assert.deepEqual(f.writes, [['household-theme', 'pink-dark']]);
});

test('invalid apply does not overwrite the current palette or stored preference', () => {
  const f = fixture({saved: 'pink-light'}); f.ready();
  for (const invalid of [undefined, null, '', 'auto', {}, 1, 'Pink-Dark']) {
    assert.throws(() => f.controller.apply(invalid, {persist: true}), /Unsupported Household theme/);
    assert.equal(f.controller.get(), 'pink-light');
    assert.equal(f.root.dataset.theme, 'pink-light');
    assert.equal(f.meta.content, '#fff0f5');
  }
  assert.deepEqual(f.writes, []);
});

test('storage write failure retains the selected in-memory palette and reports no persistence', () => {
  const f = fixture({blockedWrite: true}); f.ready();
  const result = f.controller.apply('pink-dark', {persist: true});
  assert.equal(result.persisted, false);
  assert.equal(f.root.dataset.theme, 'pink-dark');
  assert.equal(f.meta.content, '#261525');
});

test('cross-tab updates and storage clearing synchronize without echo writes', () => {
  const f = fixture({saved: 'light', dark: true}); f.ready();
  f.storageEvent('household-theme', 'pink-light');
  assert.equal(f.controller.get(), 'pink-light');
  f.storageEvent('unrelated-key', 'dark');
  assert.equal(f.controller.get(), 'pink-light');
  f.storageEvent('household-theme', 'dark', {});
  assert.equal(f.controller.get(), 'pink-light');
  f.storageEvent(null, null);
  assert.equal(f.controller.get(), 'system'); assert.equal(f.meta.content, '#101820');
  f.storageEvent('household-theme', 'unsupported');
  assert.equal(f.controller.get(), 'system');
  assert.deepEqual(f.writes, []);
});

test('system changes update chrome while explicit palettes remain explicit', () => {
  for (const legacyMedia of [false, true]) {
    const f = fixture({legacyMedia}); f.ready();
    f.systemDark(true); assert.equal(f.meta.content, '#101820');
    f.controller.apply('pink-light'); f.systemDark(false); f.systemDark(true);
    assert.equal(f.root.dataset.theme, 'pink-light'); assert.equal(f.meta.content, '#fff0f5');
    f.controller.apply('pink-dark'); f.systemDark(false);
    assert.equal(f.root.dataset.theme, 'pink-dark'); assert.equal(f.meta.content, '#261525');
    f.controller.apply('system'); assert.equal(f.meta.content, '#f5f8fa');
  }
});

test('browser chrome uses stylesheet tokens and never invents a theme color', () => {
  const f = fixture(); f.ready();
  f.state.preferredChrome = '  #123456  '; f.controller.syncChrome();
  assert.equal(f.meta.content, '#123456');
  f.state.loaded = false; f.controller.syncChrome();
  assert.equal(f.meta.content, '#123456');
  assert.ok(Object.isFrozen(f.controller));
});

function preferenceFixture({responseOK = true, persisted = true, cleanupFails = false, available = true} = {}) {
  const app = fs.readFileSync(path.join(__dirname, '../internal/web/static/app.js'), 'utf8');
  const start = app.indexOf("  const preferences = $('#preferences');");
  const end = app.indexOf('  const choiceText=', start);
  assert.ok(start >= 0 && end > start);
  const events = [], messages = [], requests = [], responses = [], timers = new Map();
  let submit, timerID = 0;
  const save = {disabled: false}, disabledSubmit = {disabled: true}, review = {disabled: false};
  const attributes = new Map(), children = [];
  const form = {
    elements: {theme: {value: ''}, offline: {checked: false}},
    setAttribute(name, value) {attributes.set(name, value);},
    removeAttribute(name) {attributes.delete(name);},
    append(...nodes) {children.push(...nodes);},
    addEventListener(name, callback) {assert.equal(name, 'submit'); submit = callback;}
  };
  const controller = {
    get() {return 'dark';},
    apply(mode, options) {events.push(['apply', mode, options.persist]); return {theme: mode, persisted};}
  };
  vm.runInNewContext(app.slice(0, app.indexOf('\n')) + '\n' + app.slice(start, end), {
    $: selector => {assert.equal(selector, '#preferences'); return form;},
    all: (selector, root) => {
      assert.equal(root, form); assert.equal(selector, 'button[type=submit],button:not([type]),input[type=submit]');
      return [save, disabledSubmit];
    },
    document: {createElement(tag) {assert.equal(tag, 'p'); return {setAttribute(name, value) {this[name] = value;}};}},
    window: {HouseholdTheme: available ? controller : undefined}, user: 'synthetic-owner',
    AbortController,
    setTimeout(callback, delay) {assert.equal(delay, 15000); timers.set(++timerID, callback); return timerID;},
    clearTimeout(id) {timers.delete(id);},
    localStorage: {
      getItem(key) {assert.equal(key, 'household-offline-owner'); return null;},
      setItem(key, value) {events.push(['store', key, value]);},
      removeItem(key) {events.push(['remove', key]);}
    },
    fetch: (url, options) => {
      requests.push({url, ...options});
      return new Promise((resolve, reject) => {
        responses.push({resolve, reject});
        options.signal.addEventListener('abort', () => reject(Error('Aborted')));
      });
    },
    csrf: () => 'synthetic-csrf',
    clearPrivateDeviceData: async () => {events.push(['clear']); if(cleanupFails)throw Error('Cleanup failed');},
    toast: message => messages.push(message)
  });
  return {
    form, events, messages, requests, attributes, save, disabledSubmit, review, timers,
    status: children.find(node => node.id === 'preferences-status'), error: children.find(node => node.id === 'preferences-error'),
    confirmResponse(index = 0, ok = responseOK) {responses[index].resolve({ok});},
    failResponse(index = 0) {responses[index].reject(Error('Failed to fetch'));},
    submit: () => submit({preventDefault() {}})
  };
}

test('shared page loads the hashed nonce-bearing theme controller before its stylesheet and deferred app', () => {
  const page = fs.readFileSync(path.join(__dirname, '../internal/web/templates/page.html'), 'utf8');
  const script = '<script nonce="{{.Nonce}}" src="{{index .Asset "theme.js"}}"></script>';
  const position = page.indexOf(script);
  assert.ok(position > 0);
  assert.equal(page.split(script).length, 2);
  assert.ok(position < page.indexOf('<link rel="stylesheet"'));
  assert.ok(position < page.indexOf('{{index .Asset "app.js"}}'));
});

test('preferences apply and persist theme only after confirmed server save while retaining offline consent', async () => {
  const f = preferenceFixture();
  assert.equal(f.form.elements.theme.value, 'dark');
  f.form.elements.theme.value = 'light'; f.form.elements.offline.checked = true;
  const pending = f.submit();
  assert.deepEqual(f.events, []);
  assert.equal(f.requests[0].url, '/api/v1/core/preferences');
  assert.equal(f.requests[0].headers['X-CSRF-Token'], 'synthetic-csrf');
  assert.deepEqual(JSON.parse(f.requests[0].body), {theme: 'light', offline: true});
  f.confirmResponse(); await pending;
  assert.deepEqual(f.events, [['apply', 'light', true], ['store', 'household-offline-owner', 'synthetic-owner']]);
  assert.equal(f.status.textContent, 'Geräteeinstellungen gespeichert.'); assert.equal(f.status.hidden, false);
  assert.deepEqual(f.messages, []);
});

test('preferences preserve device reset and distinguish local theme storage refusal from success', async () => {
  const f = preferenceFixture({persisted: false});
  f.form.elements.theme.value = 'system';
  const pending = f.submit(); f.confirmResponse(); await pending;
  assert.deepEqual(f.events, [['apply', 'system', true], ['remove', 'household-offline-owner'], ['clear']]);
  assert.match(f.status.textContent, /nicht dauerhaft speichern/); assert.equal(f.status.hidden, false);
  assert.deepEqual(f.messages, []);
  const failed = preferenceFixture({cleanupFails: true});
  const saving = failed.submit(); failed.confirmResponse(); await saving;
  assert.equal(failed.error.textContent, 'Cleanup failed'); assert.equal(failed.error.hidden, false);
  assert.deepEqual(failed.messages, []);
});

test('refused preference save or unavailable theme controller cannot mutate device choices', async () => {
  const f = preferenceFixture({responseOK: false});
  const pending = f.submit(); f.confirmResponse(); await pending;
  assert.deepEqual(f.events, []);
  assert.equal(f.error.textContent, 'Einstellungen konnten nicht gespeichert werden. Bitte erneut versuchen.'); assert.equal(f.error.hidden, false);
  assert.deepEqual(f.messages, []);
  const missing = preferenceFixture({available: false});
  await missing.submit();
  assert.deepEqual(missing.requests, []); assert.deepEqual(missing.events, []);
  assert.match(missing.error.textContent, /Bitte neu laden/); assert.equal(missing.error.hidden, false);
  assert.deepEqual(missing.messages, []);
});

test('preferences prevent parallel saves, announce pending state, and restore only submit controls', async () => {
  const f = preferenceFixture();
  f.form.elements.offline.checked = true;
  const pending = f.submit();
  await f.submit(); await f.submit();
  assert.equal(f.requests.length, 1);
  assert.equal(f.attributes.get('aria-busy'), 'true');
  assert.equal(f.save.disabled, true); assert.equal(f.review.disabled, false);
  assert.equal(f.status.role, 'status'); assert.equal(f.status.hidden, false);
  assert.match(f.status.textContent, /werden gespeichert/);
  assert.equal(f.error.hidden, true); assert.deepEqual(f.events, []);
  f.confirmResponse(); await pending;
  assert.equal(f.attributes.has('aria-busy'), false);
  assert.equal(f.save.disabled, false); assert.equal(f.disabledSubmit.disabled, true);
  assert.equal(f.review.disabled, false); assert.equal(f.timers.size, 0);
  assert.equal(f.status.textContent, 'Geräteeinstellungen gespeichert.');
});

test('preference failure remains inline, keeps edits, and allows a successful retry', async () => {
  const f = preferenceFixture({responseOK: false});
  f.form.elements.theme.value = 'pink-dark'; f.form.elements.offline.checked = true;
  const first = f.submit(); f.confirmResponse(); await first;
  assert.equal(f.error.role, 'alert'); assert.equal(f.error.hidden, false);
  assert.match(f.error.textContent, /Bitte erneut versuchen/);
  assert.equal(f.status.hidden, true); assert.equal(f.save.disabled, false);
  assert.equal(f.form.elements.theme.value, 'pink-dark'); assert.equal(f.form.elements.offline.checked, true);
  assert.deepEqual(f.events, []);
  const retry = f.submit();
  assert.equal(f.error.hidden, true); assert.equal(f.error.textContent, '');
  assert.equal(f.requests.length, 2);
  assert.deepEqual(JSON.parse(f.requests[1].body), {theme:'pink-dark', offline:true});
  f.confirmResponse(1, true); await retry;
  assert.equal(f.status.textContent, 'Geräteeinstellungen gespeichert.');
  assert.deepEqual(f.events, [['apply', 'pink-dark', true], ['store', 'household-offline-owner', 'synthetic-owner']]);
});

test('network failure and request timeout leave device choices unchanged and expose retry guidance', async () => {
  for (const timeout of [false, true]) {
    const f = preferenceFixture();
    const pending = f.submit();
    if (timeout) for (const callback of f.timers.values()) callback();
    else f.failResponse();
    await pending;
    assert.equal(f.requests[0].signal.aborted, timeout);
    assert.deepEqual(f.events, []); assert.equal(f.error.hidden, false);
    assert.match(f.error.textContent, /Prüfe deine Verbindung und versuche es erneut/);
    assert.equal(f.save.disabled, false); assert.equal(f.attributes.has('aria-busy'), false); assert.equal(f.timers.size, 0);
  }
});

test('preference save reports input changes made while pending without overwriting them', async () => {
  const f = preferenceFixture();
  f.form.elements.theme.value = 'light'; f.form.elements.offline.checked = true;
  const pending = f.submit();
  f.form.elements.theme.value = 'pink-dark'; f.form.elements.offline.checked = false;
  f.confirmResponse(); await pending;
  assert.equal(f.form.elements.theme.value, 'pink-dark'); assert.equal(f.form.elements.offline.checked, false);
  assert.deepEqual(f.events, [['apply', 'light', true], ['store', 'household-offline-owner', 'synthetic-owner']]);
  assert.match(f.status.textContent, /weiteren Änderungen sind noch nicht gespeichert/);
});

test('device cleanup errors remain visible after a preference save', async () => {
  const f = preferenceFixture({cleanupFails: true});
  const pending = f.submit(); f.confirmResponse(); await pending;
  assert.deepEqual(f.events, [['apply', 'dark', true], ['remove', 'household-offline-owner'], ['clear']]);
  assert.equal(f.error.hidden, false); assert.equal(f.error.textContent, 'Cleanup failed');
  assert.equal(f.status.hidden, true); assert.equal(f.save.disabled, false);
});

function navigationFixture({moduleCount = 7, currentModule = 6, tabWidth = 300, activeLeft = 390, activeWidth = 80, asyncCurrent = false} = {}) {
  const app = fs.readFileSync(path.join(__dirname, '../internal/web/static/app.js'), 'utf8');
  const start = app.indexOf("  const nav = $('.mobile-nav');"), end = app.indexOf("  all('[data-open]')", start);
  assert.ok(start >= 0 && end > start);
  function element(textContent = '') {
    const attributes = new Map(), classes = new Set();
    return {textContent, attributes, hidden:false, classList:{add: value => classes.add(value), contains: value => classes.has(value)},
      hasAttribute: name => attributes.has(name), getAttribute: name => attributes.get(name), setAttribute: (name, value) => attributes.set(name, value)};
  }
  const links = Array.from({length:moduleCount}, (_, index) => element('Modul ' + (index + 1)));
  if (links[currentModule]) links[currentModule].setAttribute('aria-current', 'page');
  const nav = {hidden:false, clientWidth:300, clientLeft:0, scrollLeft:0, getBoundingClientRect: () => ({left:0})};
  const currentLink = links[currentModule];
  if (currentLink) Object.assign(currentLink, {getBoundingClientRect: () => ({left: currentModule * 80 - nav.scrollLeft, right: (currentModule + 1) * 80 - nav.scrollLeft, width:80}), closest: () => nav});
  const windowEvents = new Map(), frames = new Map(), observed = [], geometry = {activeLeft, activeWidth};
  let frameID = 0, mutations, active = !asyncCurrent, disconnected = false;
  const tab = {getBoundingClientRect: () => ({left:geometry.activeLeft, right:geometry.activeLeft + geometry.activeWidth, width:geometry.activeWidth}), closest: () => tabs};
  const tabs = {clientWidth:tabWidth, clientLeft:0, scrollLeft:0, getBoundingClientRect: () => ({left:0}), closest: () => tabs};
  vm.runInNewContext(app.slice(0, app.indexOf('\n')) + '\n' + app.slice(start, end), {
    $: (selector, root) => {
      if (selector === '.mobile-nav') return nav;
      if (root === nav) {assert.ok(selector.startsWith('a')); return currentLink || null;}
      assert.equal(root, tabs); assert.ok(selector.startsWith('a'));
      return active ? tab : null;
    },
    all: (selector, root) => {
      if (selector === 'a') {assert.equal(root, nav); return links;}
      assert.deepEqual(selector.split(',').map(value => value.trim()).sort(), ['.entity-tabs', '.mobile-nav']); return [tabs, nav];
    },
    document: {createElement: () => element()},
    MutationObserver: class {
      constructor(callback) {mutations = callback;}
      observe(target, options) {observed.push({target, options}); disconnected = false;}
      disconnect() {disconnected = true;}
    },
    requestAnimationFrame(callback) {frames.set(++frameID, callback); return frameID;},
    cancelAnimationFrame(id) {frames.delete(id);},
    addEventListener(name, callback) {windowEvents.set(name, callback);},
    removeEventListener(name) {windowEvents.delete(name);}
  });
  return {
    links, tabs, nav, observed, frames, geometry,
    get disconnected() {return disconnected;},
    flush() {const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback());},
    activate() {active = true; mutations([{target:tab}, {target:tab}]);},
    event(name, event = {}) {windowEvents.get(name)?.(event);}
  };
}

test('mobile module rail keeps every enabled destination and reveals the current one without hiding links', () => {
  const f = navigationFixture();
  assert.equal(f.links.length, 7); assert.equal(f.links.some(link => link.hidden), false);
  assert.equal(f.links[6].getAttribute('aria-current'), 'page');
  f.flush(); assert.equal(f.nav.scrollLeft, 260);
});

test('mobile navigation keeps direct current destinations and small module sets unchanged', () => {
  const direct = navigationFixture({currentModule:1});
  direct.flush(); assert.equal(direct.nav.scrollLeft, 0); assert.equal(direct.links.some(link => link.hidden), false);
  const small = navigationFixture({moduleCount:5, currentModule:4});
  small.flush(); assert.equal(small.nav.scrollLeft, 100); assert.equal(small.links.some(link => link.hidden), false);
  assert.equal(navigationFixture({moduleCount:0}).nav.hidden, true);
});

test('horizontal tabs reveal the active link by moving only their scroll container', () => {
  const right = navigationFixture(); right.flush(); assert.equal(right.tabs.scrollLeft, 170);
  const visible = navigationFixture({activeLeft:20}); visible.flush(); assert.equal(visible.tabs.scrollLeft, 0);
  const left = navigationFixture({activeLeft:-30}); left.tabs.scrollLeft = 100; left.flush(); assert.equal(left.tabs.scrollLeft, 70);
  const oversized = navigationFixture({activeLeft:100, activeWidth:400}); oversized.flush(); assert.equal(oversized.tabs.scrollLeft, 100);
});

test('late tab state changes coalesce and observe navigation only, with page lifecycle cleanup', () => {
  const f = navigationFixture({asyncCurrent:true});
  f.flush(); assert.equal(f.tabs.scrollLeft, 0);
  f.activate(); f.activate(); assert.equal(f.frames.size, 1);
  assert.equal(f.observed.length, 2); assert.equal(f.observed[0].target, f.tabs); assert.equal(f.observed[1].target, f.nav);
  assert.deepEqual(Array.from(f.observed[0].options.attributeFilter), ['class', 'aria-current']);
  f.flush(); assert.equal(f.tabs.scrollLeft, 170);
  f.event('resize'); assert.equal(f.frames.size, 1);
  f.event('pagehide'); assert.equal(f.disconnected, true); assert.equal(f.frames.size, 0);
  f.event('resize'); assert.equal(f.frames.size, 0);
  f.event('pageshow', {persisted:true}); assert.equal(f.disconnected, false); assert.equal(f.frames.size, 1);
});

test('Pink automatic follows live system changes, keeps family and persists only the selected mode', () => {
 for (const legacyMedia of [false,true]) {
 const f=fixture({saved:'pink-system',dark:true,legacyMedia});f.ready();
 assert.equal(f.controller.get(),'pink-system');assert.equal(f.root.dataset.theme,'pink-dark');
 f.systemDark(false);assert.equal(f.root.dataset.theme,'pink-light');assert.equal(f.meta.content,'#fff0f5');
 f.systemDark(true);assert.equal(f.root.dataset.theme,'pink-dark');assert.equal(f.meta.content,'#261525');
 assert.deepEqual(f.writes,[]);f.controller.apply('pink-system',{persist:true});
 assert.deepEqual(f.writes,[['household-theme','pink-system']]);
 f.storageEvent('household-theme','system');assert.equal(f.controller.get(),'system');assert.equal(f.root.dataset.theme,undefined);
 }
});
