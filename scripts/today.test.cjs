'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {safeURL,sectionModel,dayLabel}=require('../internal/web/static/today.js');
test('Today rejects external, protocol-relative and control-character destinations',()=>{
 for(const value of ['https://evil.invalid','//evil.invalid','/\\evil.invalid','javascript:alert(1)','/x\nLocation:evil',null,{}])assert.equal(safeURL(value),'/notifications');
 assert.equal(safeURL('/m/recipes/cooking/123?x=1'),'/m/recipes/cooking/123?x=1');
});
test('Today keeps partial errors and disabled modules empty, never retaining supplied stale data',()=>{
 const old=[{title:'private old title',url:'/notifications'}];
 for(const state of ['error','disabled','loading',null])assert.deepEqual(sectionModel({state,items:old},6),{state:state==='disabled'?'disabled':'error',items:[],more:false});
 assert.equal(sectionModel({state:'ready',items:null},6).state,'error');
});
test('Today bounds response items, preserves literal text and validates links',()=>{
 const input={state:'ready',items:Array.from({length:20},()=>({title:'<img onerror=alert(1)>',url:'//evil.invalid',meta:'<b>literal</b>',read:false}))};
 const model=sectionModel(input,3);assert.equal(model.items.length,3);assert.equal(model.more,true);assert.equal(model.items[0].title,'<img onerror=alert(1)>');assert.equal(model.items[0].url,'/notifications');assert.equal(model.items[0].read,false);
 assert.equal(sectionModel({state:'ready',items:[null,{title:3,url:'/'}]},6).items.length,0);
});
test('Today displays the server household civil day without device-timezone drift',()=>{
 assert.match(dayLabel('2026-03-29','en'),/Sunday.*29|Sunday.*March/);
 assert.match(dayLabel('2026-10-25','de'),/Sonntag.*25/);
 for(const value of ['',null,'2026-02-30','2026-11-11T23:00:00Z','2026-1-01'])assert.equal(dayLabel(value,'de'),'');
});
