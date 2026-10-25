'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const {amount,barcode,display}=require('../internal/web/static/pantry-daily.js');
for(const[name,mode,quantity,stock,pack,unit,integer,want,after]of[
 ['exact beyond binary integer','amount','9007199254.740993','9007199255',null,'g',false,'9007199254.740993','0.259007'],
 ['comma quantity','amount','1,25','3.000000',null,'l',false,'1.250000','1.750000'],
 ['half','half','','3.000000',null,'kg',false,'1.500000','1.500000'],
 ['two packs','pack','2','3.000000',{quantity:'500',unit:'g'},'kg',false,'1.000000','2.000000'],
 ['partial pack','pack','0.5','1',{quantity:'500',unit:'ml'},'l',false,'0.250000','0.750000'],
 ['integer pack','pack','2','10',{quantity:'3',unit:'piece'},'piece',true,'6.000000','4.000000'],
 ['aggregate stock supports 24 integer digits','amount','1','999999999999999999999999.999999',null,'g',false,'1.000000','999999999999999999999998.999999'],
])test(name,()=>assert.deepEqual(amount(mode,quantity,stock,pack,unit,integer),{quantity:want,after}));
test('review rejects inexact, incompatible, disabled, excessive and fractional integer amounts',()=>{
 for(const args of [
  ['half','','0.000001',null,'g',false],['half','','3',null,'piece',true],
  ['amount','0','2',null,'g',false],['amount','-1','2',null,'g',false],['amount','NaN','2',null,'g',false],
  ['amount','2.000001','2',null,'g',false],['amount','0.0000001','2',null,'g',false],
  ['amount','1000000000000','2000000000000',null,'g',false],
  ['pack','0.000001','2',{quantity:'0.000001',unit:'g'},'kg',false],
  ['pack','1','2',{quantity:'1',unit:'g'},'l',false],['pack','1','2',{quantity:'1',unit:'g',disabled:true},'g',false],
 ])assert.throws(()=>amount(...args),undefined,JSON.stringify(args));
});
test('barcode preserves identity and rejects unsuitable characters',()=>{
 assert.equal(barcode('  00073AbC  '),'00073AbC');assert.equal(barcode('0'),'0');
 for(const raw of ['', 'two codes','ä','123\u0000', 'a'.repeat(65)])assert.throws(()=>barcode(raw));
});
test('display retains exact small and large decimals',()=>{assert.equal(display('9007199254.740993'),'9007199254,740993');assert.equal(display('0.000001'),'0,000001');assert.equal(display('100.000000'),'100');});
