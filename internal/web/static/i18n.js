/* Authored application text only. Loaded before module scripts; no DOM or
 * response-data rewriting, network requests, or external translation service. */
(() => {
  'use strict';
  const root = typeof document === 'undefined' ? null : document.documentElement;
  const language = () => /^en(?:-|$)/i.test(root?.lang || '') ? 'en' : 'de';
  const locale = () => language() === 'en' ? 'en-GB' : 'de-AT';
  const catalog = globalThis.HouseholdEnglish || Object.create(null);
  function t(source, ...args) {
    const translated = language() === 'en' && Object.hasOwn(catalog, source) ? catalog[source] : source;
    if (!args.length) return translated;
    let index = 0;
    return translated.replace(/%%|%[sdv]/g, marker => marker === '%%' ? '%' : String(args[index++] ?? ''));
  }
  function exactDecimal(value, {group = false} = {}) {
    const raw = String(value);
    if (!/^-?\d+(?:\.\d+)?$/.test(raw)) throw new RangeError('Invalid exact decimal');
    const negative = raw.startsWith('-'), unsigned = negative ? raw.slice(1) : raw;
    let [whole, fraction] = unsigned.split('.');
    if (group) whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, language() === 'en' ? ',' : '.');
    return (negative ? '-' : '') + whole + (fraction === undefined ? '' : (language() === 'en' ? '.' : ',') + fraction);
  }
  function moneyExact(value, currency = 'EUR', exponent = 2) {
    if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new RangeError('Inexact monetary amount');
    const raw = String(value);
    if (!/^-?\d{1,80}$/.test(raw) || !Number.isInteger(exponent) || exponent < 0 || exponent > 6) throw new RangeError('Invalid exact monetary amount');
    const minor = BigInt(raw), magnitude = minor < 0n ? -minor : minor;
    const scale = 10n ** BigInt(exponent);
    const decimal = (minor < 0n ? '-' : '') + (magnitude / scale).toString() + (exponent ? '.' + (magnitude % scale).toString().padStart(exponent, '0') : '');
    const amount = exactDecimal(decimal, {group:true});
    return language() === 'en' && currency === 'EUR'
      ? (minor < 0n ? '-€' + amount.slice(1) : '€' + amount)
      : amount + '\u00a0' + (currency === 'EUR' ? '€' : currency);
  }
  function date(value, options = {}) {
    const parsed = value instanceof Date ? value : new Date(value);
    if (!Number.isFinite(parsed.getTime())) return t('Nicht bekannt');
    return new Intl.DateTimeFormat(locale(), options).format(parsed);
  }
  function number(value, options = {}) { return new Intl.NumberFormat(locale(), options).format(value); }
  globalThis.HouseholdI18n = Object.freeze({t, locale, language, exactDecimal, moneyExact, date, number});
  // The worker stores only this public device preference, never account data.
  // A freshly installed worker receives the same language after claiming pages.
  const workers = typeof navigator === 'undefined' ? null : navigator.serviceWorker;
  if (workers) {
    const send = () => workers.controller?.postMessage({type:'SET_LOCALE', locale:language()});
    workers.addEventListener('controllerchange', send);
    workers.ready.then(send).catch(() => {});
    send();
  }
})();
