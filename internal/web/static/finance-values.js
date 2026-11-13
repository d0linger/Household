'use strict';
const _householdText_finance_values_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };

const HouseholdFinanceValues = (() => {
  const maxMinor = 1000000000000n;
  const fail = message => { throw new Error(message); };
  function text(value, max = 256) {
    if (typeof value !== 'string' || value.length > max || value.includes('\0')) fail(_householdText_finance_values_js("Ungültiger Textwert."));
    return value;
  }
  function exponent(value) {
    if (!Number.isSafeInteger(value) || ![0, 2, 3].includes(value)) fail(_householdText_finance_values_js("Währungseinheit nicht unterstützt."));
    return value;
  }
  function integer(raw, {positive = false, bounded = true} = {}) {
    if (typeof raw !== 'string' || !/^-?(?:0|[1-9]\d{0,59})$/.test(raw) || raw === '-0') fail(_householdText_finance_values_js("Betrag muss eine exakte Ganzzahl sein."));
    const result = BigInt(raw);
    if (positive && result <= 0n || bounded && (result > maxMinor || result < -maxMinor)) fail(_householdText_finance_values_js("Betrag außerhalb der erlaubten Grenze."));
    return result;
  }
  function majorToMinor(raw, decimals) {
    exponent(decimals); text(raw, 64);
    const value = raw.trim();
    if (!/^-?\d{1,18}(?:[.,]\d{1,6})?$/.test(value)) fail(_householdText_finance_values_js("Betrag mit Komma oder Punkt eingeben, ohne Tausenderzeichen."));
    const negative = value.startsWith('-'), parts = value.replace(/^-/, '').replace(',', '.').split('.'), fraction = parts[1] || '';
    if (fraction.slice(decimals).replace(/0/g, '')) fail(_householdText_finance_values_js("Betrag hat mehr Nachkommastellen als die Währung."));
    const result = (BigInt(parts[0]) * 10n ** BigInt(decimals) + BigInt(fraction.slice(0, decimals).padEnd(decimals, '0') || '0')) * (negative ? -1n : 1n);
    if (result > maxMinor || result < -maxMinor) fail(_householdText_finance_values_js("Betrag außerhalb der erlaubten Grenze."));
    return result.toString();
  }
  function minorToMajor(raw, decimals, grouping = false) {
    exponent(decimals); const value = integer(raw, {bounded:false}), magnitude = value < 0n ? -value : value, scale = 10n ** BigInt(decimals);
    let whole = (magnitude / scale).toString();
    const english = globalThis.HouseholdI18n?.language() === 'en';
    if (grouping) whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, english ? ',' : '.');
    return (value < 0n ? '−' : '') + whole + (decimals ? (english ? '.' : ',') + (magnitude % scale).toString().padStart(decimals, '0') : '');
  }
  function inputMajor(raw, decimals) { return minorToMajor(raw, decimals).replace('−', '-'); }
  function money(raw, currency, decimals) {
    if (raw === null || raw === undefined) return _householdText_finance_values_js("Nicht bekannt");
    if (typeof currency !== 'string' || !/^[A-Z]{3}$/.test(currency)) fail(_householdText_finance_values_js("Währung fehlt."));
    return minorToMajor(raw, decimals, true) + ' ' + currency;
  }
  function add(values) {
    if (!Array.isArray(values) || values.length > 10000) fail(_householdText_finance_values_js("Zu viele Beträge."));
    return values.reduce((sum, item) => sum + integer(item, {bounded:false}), 0n).toString();
  }
  function uuid(value) {
    if (typeof value !== 'string' || !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(value) || /^0{8}-0{4}-0{4}-0{4}-0{12}$/.test(value)) fail(_householdText_finance_values_js("Gültige Kennung erforderlich."));
    return value.toLowerCase();
  }
  function count(value, max = Number.MAX_SAFE_INTEGER) {
    if (!Number.isSafeInteger(value) || value < 0 || value > max) fail(_householdText_finance_values_js("Zähl- oder Versionswert außerhalb der sicheren Grenze."));
    return value;
  }
  function rate(raw) {
    if (typeof raw !== 'string' || !/^[1-9]\d{0,17}$/.test(raw)) fail(_householdText_finance_values_js("Kurszähler und Nenner als positive Ganzzahlen mit höchstens 18 Stellen eingeben."));
    return BigInt(raw);
  }
  function halfAway(numerator, denominator) {
    if (denominator <= 0n) fail(_householdText_finance_values_js("Kursnenner muss positiv sein."));
    const sign = numerator < 0n ? -1n : 1n, absolute = numerator < 0n ? -numerator : numerator;
    return (absolute / denominator + (absolute % denominator * 2n >= denominator ? 1n : 0n)) * sign;
  }
  function convert(original, decimals, numerator, denominator) {
    exponent(decimals);
    const source = integer(original), converted = halfAway(source * rate(numerator) * 100n, rate(denominator) * 10n ** BigInt(decimals));
    if (converted > maxMinor || converted < -maxMinor) fail(_householdText_finance_values_js("Umrechnung überschreitet die erlaubte Betragsgrenze."));
    return {minor:converted.toString(), status:converted === 0n && source !== 0n ? 'below_base_precision' : 'known'};
  }
  function allocateConverted(lines, decimals, numerator, denominator) {
    if (!Array.isArray(lines) || lines.length < 1 || lines.length > 100) fail(_householdText_finance_values_js("Eine bis 100 Kategoriezeilen erforderlich."));
    exponent(decimals); const n = rate(numerator) * 100n, d = rate(denominator) * 10n ** BigInt(decimals), seen = new Set();
    const values = lines.map(line => {
      const id = uuid(line.id); if (seen.has(id)) fail(_householdText_finance_values_js("Doppelte Kategoriezeile.")); seen.add(id);
      const original = integer(line.original_minor), quota = original * n;
      let floor = quota / d, remainder = quota % d;
      if (remainder < 0n) { floor -= 1n; remainder += d; }
      return {id, original, floor, remainder};
    });
    const target = halfAway(values.reduce((sum, line) => sum + line.original * n, 0n), d);
    if (target > maxMinor || target < -maxMinor) fail(_householdText_finance_values_js("Umrechnung überschreitet die erlaubte Betragsgrenze."));
    let left = target - values.reduce((sum, line) => sum + line.floor, 0n);
    const ranked = [...values].sort((a, b) => a.remainder === b.remainder ? (a.id < b.id ? -1 : a.id > b.id ? 1 : 0) : a.remainder > b.remainder ? -1 : 1);
    for (const line of ranked) { if (left <= 0n) break; line.floor += 1n; left -= 1n; }
    if (left !== 0n) fail(_householdText_finance_values_js("Exakte Verteilung konnte nicht abgeschlossen werden."));
    if (values.some(line => line.floor > maxMinor || line.floor < -maxMinor)) fail(_householdText_finance_values_js("Umgerechnete Kategoriezeile überschreitet die erlaubte Betragsgrenze."));
    return values.map(line => ({id:line.id, minor:line.floor.toString()}));
  }
  function percentage(raw) {
    text(raw, 16); const value = raw.trim().replace(',', '.');
    if (!/^\d{1,3}(?:\.\d{1,4})?$/.test(value)) fail(_householdText_finance_values_js("Prozentwert mit höchstens vier Nachkommastellen eingeben."));
    const [whole, fraction = ''] = value.split('.'), result = BigInt(whole) * 10000n + BigInt(fraction.padEnd(4, '0'));
    if (result > 1000000n) fail(_householdText_finance_values_js("Prozentwert darf 100 nicht überschreiten."));
    return result.toString();
  }
  function shares(total, participants, method) {
    if (!Array.isArray(participants) || !participants.length || participants.length > 50 || !['equal','percent','weights','fixed'].includes(method)) fail(_householdText_finance_values_js("Eine bis 50 Personen und eine gültige Aufteilungsart erforderlich."));
    const amount = integer(total), absolute = amount < 0n ? -amount : amount, seen = new Set();
    const rows = participants.map(row => {
      const id = uuid(row.membership_id); if (seen.has(id)) fail(_householdText_finance_values_js("Person wurde mehrfach ausgewählt.")); seen.add(id);
      const weight = method === 'equal' ? 1n : method === 'percent' ? integer(percentage(row.value), {bounded:false}) : integer(row.value, {positive:method === 'weights'});
      return {id, weight, minor:0n, remainder:0n};
    });
    if (method === 'fixed') {
      if (rows.some(row => row.weight !== 0n && (row.weight < 0n) !== (amount < 0n)) || rows.reduce((sum, row) => sum + row.weight, 0n) !== amount) fail(_householdText_finance_values_js("Festbeträge müssen exakt zum Gesamtbetrag und seiner Richtung passen."));
      return rows.map(row => ({membership_id:row.id, minor:row.weight.toString()}));
    }
    const denominator = rows.reduce((sum, row) => sum + row.weight, 0n);
    if (!denominator || method === 'percent' && denominator !== 1000000n) fail(_householdText_finance_values_js("Prozente müssen genau 100 ergeben; Gewichte müssen positiv sein."));
    for (const row of rows) { row.minor = absolute * row.weight / denominator; row.remainder = absolute * row.weight % denominator; }
    let left = absolute - rows.reduce((sum, row) => sum + row.minor, 0n);
    for (const row of [...rows].sort((a, b) => a.remainder === b.remainder ? (a.id < b.id ? -1 : a.id > b.id ? 1 : 0) : a.remainder > b.remainder ? -1 : 1)) { if (!left) break; row.minor += 1n; left -= 1n; }
    return rows.map(row => ({membership_id:row.id, minor:(row.minor * (amount < 0n ? -1n : 1n)).toString()}));
  }
  function bytes(value, max = 1048576) {
    text(value, max); const result = new TextEncoder().encode(value).byteLength;
    if (result > max) fail(_householdText_finance_values_js("Datei oder Eingabe überschreitet die erlaubte Bytegröße."));
    return result;
  }
  function csv(raw, delimiter) {
    bytes(raw); if (![',',';','\t'].includes(delimiter)) fail(_householdText_finance_values_js("Trennzeichen ausdrücklich wählen."));
    const rows = []; let row = [], cell = '', quoted = false, closed = false;
    const pushCell = () => { row.push(cell); if (row.length > 50) fail(_householdText_finance_values_js("CSV enthält mehr als 50 Spalten.")); cell = ''; closed = false; };
    const pushRow = () => { pushCell(); rows.push(row); if (rows.length > 501) fail(_householdText_finance_values_js("CSV enthält mehr als 500 Datenzeilen.")); row = []; };
    for (let i = raw.charCodeAt(0) === 0xfeff ? 1 : 0; i < raw.length; i++) {
      const char = raw[i];
      if (quoted) { if (char === '"') { if (raw[i+1] === '"') { cell += '"'; i++; } else { quoted = false; closed = true; } } else cell += char; continue; }
      if (char === '"') { if (cell || closed) fail(_householdText_finance_values_js("CSV-Anführungszeichen sind nicht eindeutig.")); quoted = true; }
      else if (char === delimiter) pushCell();
      else if (char === '\n' || char === '\r') { if (char === '\r' && raw[i+1] === '\n') i++; pushRow(); }
      else { if (closed) fail(_householdText_finance_values_js("Unerwartete Zeichen nach einem CSV-Feld.")); cell += char; }
    }
    if (quoted) fail(_householdText_finance_values_js("CSV enthält ein nicht abgeschlossenes Textfeld."));
    if (cell || row.length || closed) pushRow();
    if (!rows.length || rows[0].some(header => !header) || new Set(rows[0]).size !== rows[0].length) fail(_householdText_finance_values_js("CSV-Kopfzeile fehlt oder enthält doppelte Namen."));
    return {headers:rows[0], rows:rows.slice(1), count:rows.length - 1};
  }
  function day(raw) {
    if (typeof raw !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(raw)) fail(_householdText_finance_values_js("Datum als JJJJ-MM-TT erforderlich."));
    const parsed = new Date(raw + 'T12:00:00Z');
    if (!Number.isFinite(parsed.getTime()) || parsed.toISOString().slice(0,10) !== raw || raw < '1900-01-01' || raw > '2200-12-31') fail(_householdText_finance_values_js("Ungültiger Kalendertag."));
    return raw;
  }
  function monthRange(raw) {
    if (typeof raw !== 'string' || !/^\d{4}-(?:0[1-9]|1[0-2])$/.test(raw) || raw < '1900-01' || raw > '2200-12') fail(_householdText_finance_values_js("Gültigen Kalendermonat auswählen."));
    const year=Number(raw.slice(0,4)),month=Number(raw.slice(5)),last=new Date(Date.UTC(year,month,0)).getUTCDate();
    return {from:raw+'-01',to:raw+'-'+String(last).padStart(2,'0')};
  }
  function stable(value, depth = 0) {
    if (depth > 16) fail(_householdText_finance_values_js("Eingabe ist zu tief verschachtelt."));
    if (value === null || typeof value === 'string' || typeof value === 'boolean') return JSON.stringify(value);
    if (typeof value === 'number') { count(value); return JSON.stringify(value); }
    if (Array.isArray(value)) { if (value.length > 1000) fail(_householdText_finance_values_js("Zu viele Eingabewerte.")); return '[' + value.map(item => stable(item, depth+1)).join(',') + ']'; }
    if (value && Object.getPrototypeOf(value) === Object.prototype) return '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + stable(value[key], depth+1)).join(',') + '}';
    fail(_householdText_finance_values_js("Eingabe enthält einen nicht unterstützten Wert."));
  }
  return {text, exponent, integer, majorToMinor, minorToMajor, inputMajor, money, add, uuid, count, convert, allocateConverted, percentage, shares, bytes, csv, day, monthRange, stable};
})();
if (typeof module !== 'undefined') module.exports = HouseholdFinanceValues;
