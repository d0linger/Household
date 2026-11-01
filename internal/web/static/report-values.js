'use strict';
const HouseholdReportValues = (() => {
  function euroMinor(value) {
    if (value === null || value === undefined) return 'Nicht bekannt';
    if (typeof value === 'number') {
      if (!Number.isSafeInteger(value)) throw new Error('Berichtsbetrag ist nicht exakt.');
      value = String(value);
    }
    if (typeof value !== 'string' || !/^-?(?:0|[1-9]\d{0,59})$/.test(value) || value === '-0') throw new Error('Ungültiger Berichtsbetrag.');
    const minor = BigInt(value), magnitude = minor < 0n ? -minor : minor;
    const whole = (magnitude / 100n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, '.');
    return (minor < 0n ? '-' : '') + whole + ',' + (magnitude % 100n).toString().padStart(2, '0') + '\u00a0€';
  }
  return Object.freeze({euroMinor});
})();
if (typeof module !== 'undefined' && module.exports) module.exports = HouseholdReportValues;
if (typeof window !== 'undefined') window.HouseholdReportValues = HouseholdReportValues;
