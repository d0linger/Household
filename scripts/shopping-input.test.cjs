'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const {parseShoppingInput} = require('../internal/web/static/shopping-input.js');

for (const [name,input,expected] of [
  ['packs','2×500g Nudeln',{name:'Nudeln',quantity:'1000',unit_code:'g'}],
  ['spaces','2 x 500 g Nudeln',{name:'Nudeln',quantity:'1000',unit_code:'g'}],
  ['decimal comma','1,25 kg Äpfel',{name:'Äpfel',quantity:'1.25',unit_code:'kg'}],
  ['count','3 Milch',{name:'Milch',quantity:'3',unit_code:'piece'}],
  ['named count','4 Stück Eier',{name:'Eier',quantity:'4',unit_code:'piece'}],
  ['exact multiplication','3×0,1 l Öl',{name:'Öl',quantity:'0.3',unit_code:'l'}],
  ['micro','0.000001 g Gewürz',{name:'Gewürz',quantity:'0.000001',unit_code:'g'}],
  ['literal name','7up',null],
  ['no name','2 kg',null],
  ['zero','0 g Mehl',null],
  ['negative','-2 kg Mehl',null],
  ['overflow','999999×999999999999 kg Mehl',null],
  ['too long','2 kg '+ 'x'.repeat(200),null],
  ['not text',null,null],
]) test(name,()=>assert.deepEqual(parseShoppingInput(input),expected));

test('deterministic and bounded for arbitrary input',()=>{
  for(let i=0;i<1000;i++){
    const input=String(i)+'x'+String(i)+',123456 g Artikel';
    const actual=parseShoppingInput(input);
    assert.deepEqual(actual,parseShoppingInput(input));
    if(actual)assert.match(actual.quantity,/^\d+(?:\.\d{1,6})?$/);
  }
});
