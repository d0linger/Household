const test=require('node:test'),assert=require('node:assert/strict');
const {convert,minor,money}=require('../internal/web/static/shopping-checkout.js');
test('checkout base units remain exact above binary float precision',()=>{
 assert.equal(convert('9007199254.740993','g','g'),'9007199254.740993');
 assert.equal(convert('1,234567','kg','g'),'1234.567000');
 assert.equal(convert('0.001','kg','g',true),'1.000000');
});
test('checkout refuses rounding, overflow, density guesses and fractional integer products',()=>{
 for(const args of [['0.000001','g','kg'],['999999999999','kg','g'],['1','g','ml'],['1.5','piece','piece',true],['0','g','g'],['-1','g','g'],['1','crate','crate']])assert.throws(()=>convert(...args));
});
test('checkout unknown money is distinct from a free item and preserves signed large totals',()=>{
 assert.equal(minor(''),null);assert.equal(minor('0'),0n);assert.equal(minor('12,34'),1234n);
 for(const value of ['-1','1.001','10000000000.01','1e2'])assert.throws(()=>minor(value));
 assert.equal(money('-1201'),'−12,01 €');assert.equal(money(null),'unbekannt');
 assert.ok(money('9007199254740993').endsWith(',93 €'));
});
