'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const {euroMinor,minorText,totals,safeVersions}=require('../internal/web/static/expense-values.js');
test('expense inputs use exact explicit EUR with no rounding or implicit numeric cents',()=>{
 for(const [raw,want] of [['12,50',1250n],['-0.01',-1n],['1.230000',123n],['0.010000',1n],['10000000000.00',1000000000000n]])assert.equal(euroMinor(raw),want,raw);
 for(const raw of ['',null,1250,'1e3','+1.00','1.001','1.234,50','10000000000.01','0.000001','x'.repeat(33)])assert.equal(euroMinor(raw),null,String(raw));
});
test('draft rest differs from complete explicit posting in both financial directions',()=>{
 const partial=totals('10.00',['9.00'],'expense');assert.equal(partial.rest,100n);assert.equal(partial.canPost,false);
 const discount=totals('10.00',['11.00','-1.00'],'expense');assert.equal(discount.sum,1000n);assert.equal(discount.canPost,true);
 const refund=totals('-10.00',['-11.00','1.00'],'refund');assert.equal(refund.rest,0n);assert.equal(refund.canPost,true);
 assert.equal(totals('-10.00',['-10.00'],'expense').canPost,false);assert.equal(totals('10.00',['10.00'],'refund').canPost,false);
});
test('empty, zero and invalid allocation amounts never become an invented complete balance',()=>{
 const empty=totals('10.00',[],'expense');assert.equal(empty.sum,0n);assert.equal(empty.rest,1000n);assert.equal(empty.canPost,false);
 for(const raw of ['','0','0.001','invalid']){const result=totals('10.00',['10.00',raw],'expense');assert.equal(result.sum,null);assert.equal(result.rest,null);assert.equal(result.canPost,false);}
 assert.equal(totals('',[],'expense').total,null);
});
test('allocation aggregates remain exact and bounded to100 rows without mutating callers',()=>{
 const amounts=Array(100).fill('10000000000.00'),copy=[...amounts],result=totals('10000000000.00',amounts,'expense');
 assert.equal(result.sum,100000000000000n);assert.equal(result.rest,-99000000000000n);assert.deepEqual(amounts,copy);
 assert.equal(totals('1.00',Array(101).fill('0.01'),'expense').sum,null);
});
test('stored and aggregate cent strings format without binary numeric conversion',()=>{
 assert.equal(minorText('9007199254740993'),'90071992547409,93');assert.equal(minorText('-1'),'-0,01');assert.equal(minorText('0'),'0,00');
 for(const raw of [123,'1.01','no',''])assert.equal(minorText(raw),null);
});
test('unsafe browser versions keep saved data out of the writable protocol',()=>{
 const current={generation:1,header:{id:'saved',version:2},lines:[{version:3}],baseline:[{version:3}]};assert.equal(safeVersions(current),true);
 for(const changed of [{...current,generation:Number.MAX_SAFE_INTEGER+1},{...current,header:{id:'saved',version:0}},{...current,lines:[{version:Number.MAX_SAFE_INTEGER+1}]},{...current,baseline:[{version:-1}]}])assert.equal(safeVersions(changed),false);
 assert.equal(safeVersions({generation:1,header:{id:'',version:0},lines:[],baseline:[]}),true);
});
