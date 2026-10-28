'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const {explain,formatQuantity}=require('../internal/web/static/shopping-repurchase.js');
test('explains actual distinct dates and gaps without predicting quantity or stock',()=>{
 const text=explain({dates:['2026-04-05','2026-03-29','2026-03-22'],gaps:[7,7],interval_days:7,last_on:'2026-04-05',next_on:'2026-04-12'});
 assert.match(text,/3 unterschiedliche Kaufdaten/);assert.match(text,/Abstände 7, 7 Tage/);assert.match(text,/typischer Abstand 7 Tage/);assert.match(text,/2026-04-12/);
 assert.doesNotMatch(text,/automatisch|Packungen hinzufügen/);
});
test('shows exact local quantities without floating point rounding or redundant zeros',()=>{
 for(const [value,expected] of [['500.000000','500'],['0.000001','0,000001'],['1.230000','1,23'],['999999999999.999999','999999999999,999999'],['1','1']]){
  assert.equal(formatQuantity(value),expected);
 }
});
