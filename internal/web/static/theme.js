/* Load synchronously in the shared head before styles/content.
 * get() returns the selected mode; apply(mode, {persist: true}) stores a
 * confirmed device preference. Without persist, changes remain in memory.
 * CSS owns palettes and native color-scheme; this controller sets no inline style.
 */
(() => {
  'use strict';
  const key = 'household-theme';
  const modes = new Set(['system', 'light', 'dark', 'pink-system', 'pink-light', 'pink-dark']);
  const root = document.documentElement;
  const appearance = typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  let current = 'system';

  function savedTheme() {
    try {
      const saved = localStorage.getItem(key);
      return modes.has(saved) ? saved : 'system';
    } catch {return 'system';}
  }

  function syncChrome() {
    const meta = document.querySelector('meta[name="theme-color"]');
    if (!meta) return;
    const style = getComputedStyle(root);
    const color = style.getPropertyValue('--theme-color').trim()
      || style.getPropertyValue('--ground').trim();
    // Before the stylesheet has loaded there may be no palette yet.
    if (color) meta.content = color;
  }

  function apply(mode, {persist = false} = {}) {
    if (!modes.has(mode)) throw new RangeError('Unsupported Household theme');
    current = mode;
    if (mode === 'system') root.removeAttribute('data-theme');
    else root.dataset.theme = mode === 'pink-system'
      ? (appearance?.matches ? 'pink-dark' : 'pink-light') : mode;
    let persisted = false;
    if (persist) {
      try {localStorage.setItem(key, mode); persisted = true;} catch {}
    }
    syncChrome();
    return {theme: current, persisted};
  }

  window.HouseholdTheme = Object.freeze({get: () => current, apply, syncChrome});
  apply(savedTheme());
  document.addEventListener('DOMContentLoaded', syncChrome, {once: true});
  window.addEventListener('storage', event => {
    if (event.key !== key && event.key !== null) return;
    try {if (event.storageArea && event.storageArea !== localStorage) return;} catch {return;}
    apply(modes.has(event.newValue) ? event.newValue : 'system');
  });
  const systemChanged = () => {if (current === 'pink-system') apply(current); else syncChrome();};
  if (appearance?.addEventListener) appearance.addEventListener('change', systemChanged);
  else if (appearance?.addListener) appearance.addListener(systemChanged);
})();
