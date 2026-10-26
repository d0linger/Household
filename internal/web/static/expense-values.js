'use strict';
const HouseholdExpenseValues = (() => {
  const limit = 1000000000000n;
  function euroMinor(raw) {
    if (typeof raw !== 'string' || raw.length > 32) return null;
    const text = raw.trim();
    if (!/^-?\d{1,12}(?:[.,]\d{1,6})?$/.test(text)) return null;
    const negative = text.startsWith('-'), parts = text.replace('-', '').replace(',', '.').split('.');
    const micros = BigInt(parts[0]) * 1000000n + BigInt((parts[1] || '').padEnd(6, '0'));
    if (micros % 10000n !== 0n) return null;
    const minor = micros / 10000n * (negative ? -1n : 1n);
    return minor < -limit || minor > limit ? null : minor;
  }
  function minorText(raw) {
    if (typeof raw !== 'string' || !/^-?\d{1,18}$/.test(raw)) return null;
    const value = BigInt(raw), positive = value < 0n ? -value : value;
    return (value < 0n ? '-' : '') + (positive / 100n).toString() + ',' + (positive % 100n).toString().padStart(2, '0');
  }
  function totals(amount, amounts, kind) {
    const total = euroMinor(amount), lines = amounts.map(euroMinor);
    const validHeader = total !== null && (kind === 'expense' && total > 0n || kind === 'refund' && total < 0n);
    const validLines = lines.length <= 100 && lines.every(line => line !== null && line !== 0n);
    const sum = validLines ? lines.reduce((value, line) => value + line, 0n) : null;
    const rest = total !== null && sum !== null ? total - sum : null;
    return {total, sum, rest, validHeader, validLines, canPost: validHeader && validLines && lines.length > 0 && rest === 0n};
  }
  function safeVersions(bundle) {
    const valid = value => Number.isSafeInteger(value) && value > 0;
    return valid(bundle.generation) && (bundle.header.id ? valid(bundle.header.version) : bundle.header.version === 0) &&
      bundle.lines.every(line => valid(line.version)) && bundle.baseline.every(line => valid(line.version));
  }
  return {euroMinor, minorText, totals, safeVersions};
})();
if (typeof module !== 'undefined') module.exports = HouseholdExpenseValues;
