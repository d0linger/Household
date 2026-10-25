'use strict';
const {test}=require('node:test');const assert=require('node:assert/strict');
const {amount,shifted,display}=require('../internal/web/static/planning.js');
for(const[name,raw,base,servings,want]of[
 ['exact large','9007199254.740993','2','4','18014398509.481986'],
 ['third','100.000001','3','1','≈ 33.333334'],
 ['tiny positive','0.000001','1000000','0.000001','<0.000001'],
 ['unknown',null,'3','1','Menge offen'],
 ['comma','1,25','2,5','5','2.5'],
 ['invalid source','NaN','2','1','Menge prüfen'],
 ['zero base','1','0','1','Menge prüfen'],
 ['invalid precision','1','2','0.0000001','Menge prüfen'],
 ['zero portions','1','2','0','Menge prüfen'],
])test(name,()=>assert.equal(amount(raw,base,servings),want));
test('week changes preserve calendar dates across DST and year boundaries',()=>{
 assert.equal(shifted('2026-03-25',7),'2026-04-01');assert.equal(shifted('2026-10-22',7),'2026-10-29');assert.equal(shifted('2026-12-29',7),'2027-01-05');assert.equal(shifted('2028-02-27',3),'2028-03-01');
});
test('display retains all significant digits without numeric conversion',()=>{
 assert.equal(display('9007199254.740993'),'9007199254.740993');assert.equal(display('2.500000'),'2.5');assert.equal(display('100.000000'),'100');assert.equal(display('0.000001'),'0.000001');
});
