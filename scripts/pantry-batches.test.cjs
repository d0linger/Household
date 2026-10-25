'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),math=require('../internal/web/static/pantry-batches.js');
test('physical zero differs from unknown and remains exact beyond Number precision',()=>{
 assert.throws(()=>math.amount('',true));assert.equal(math.decimal(math.amount('0',true)),'0.000000');
 assert.equal(math.decimal(math.amount('999999999999999999999999.999999',true)),'999999999999999999999999.999999');
 for(const raw of ['-1','1e3','0.0000001','1000000000000000000000000'])assert.throws(()=>math.amount(raw,true));
});
test('counts compare exact wide balances but reject unbookable differences',()=>{
 assert.deepEqual(math.difference('1000000000000.000002','1000000000000.000001'),{before:'1000000000000.000002',counted:'1000000000000.000001',delta:'-0.000001'});
 assert.equal(math.difference('999999999999999999999999.999999','999999999999999999999999.999999').delta,'0.000000');
 assert.throws(()=>math.difference('0','1000000000000'));assert.throws(()=>math.difference('1','0.5',true));
});
test('intake converts own and fractional pack amounts without rounding',()=>{
 assert.equal(math.intake('0,5','','kg',{quantity:'500',unit_code:'g'}),'0.250000');
 assert.equal(math.intake('1','g','kg',null),'0.001000');
 assert.throws(()=>math.intake('0.000001','g','kg',null));assert.throws(()=>math.intake('1','l','kg',null));
 assert.throws(()=>math.intake('0','kg','kg',null));assert.throws(()=>math.intake('0.5','piece','piece',null,true));
});
test('count review preserves intervening same-balance conflict and unit binding',()=>{
 const lot={version:1,product_version:2,location_version:3,movement_count:4,stock:'3',unit:'kg',integer_quantities:false};
 const line={lot_version:1,product_version:2,location_version:3,movement_count:4,balance:'3.000000',quantity:'0',unit:'kg',reason:'Counted empty'};
 assert.equal(math.countLine(line,lot).delta,'-3.000000');
 assert.throws(()=>math.countLine({...line,movement_count:2},lot));assert.throws(()=>math.countLine({...line,unit:'g'},lot));
 assert.throws(()=>math.countLine({...line,reason:''},lot));assert.throws(()=>math.countLine({...line,quantity:''},lot));
 assert.equal(math.countLine({...line,quantity:'3',reason:''},lot).delta,'0.000000');
});
