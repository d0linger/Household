/* Deterministic, local quantity suggestions. No input is applied without review. */
((root) => {
  'use strict';
  function parseShoppingInput(input) {
    if (typeof input !== 'string' || input.length > 200) return null;
    const match = input.trim().match(/^(?:(\d{1,6})\s*[x×]\s*)?(\d{1,12}(?:[.,]\d{1,6})?)\s*(kg|ml|g|l|Stück|Stk\.?|pieces?|pcs\.?)?\s+(.+)$/iu);
    if (!match) return null;
    const [, packs, amount, rawUnit, name] = match;
    if (!rawUnit && /^(?:kg|ml|g|l|Stück|Stk\.?|pieces?|pcs\.?)$/iu.test(name.trim())) return null;
    const [whole, fraction = ''] = amount.replace(',', '.').split('.');
    const scaled = (BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, '0'))) * BigInt(packs || 1);
    if (scaled <= 0n || scaled >= 1000000000000000000n || !name.trim()) return null;
    const digits = String(scaled).padStart(7, '0');
    const quantity = (digits.slice(0, -6) + '.' + digits.slice(-6)).replace(/\.?0+$/, '');
    const unit = !rawUnit || /^(?:st|piece|pcs)/i.test(rawUnit) ? 'piece' : rawUnit.toLowerCase();
    return {name: name.trim(), quantity, unit_code: unit};
  }
  if (typeof module === 'object' && module.exports) module.exports = {parseShoppingInput};
  else root.HouseholdShoppingInput = {parseShoppingInput};
})(globalThis);
