'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const V=require('../internal/web/static/finance-values.js');
const a='00000000-0000-4000-8000-000000000001',b='00000000-0000-4000-8000-000000000002',c='00000000-0000-4000-8000-000000000003';

test('Finance money rejects every numeric or coercible input before decimal parsing',()=>{
 for(const input of[1,0,1.1,NaN,Infinity,1n,true,{},[],{toString(){throw Error('must not coerce');}},null,undefined]){
  assert.throws(()=>V.majorToMinor(input,2));assert.throws(()=>V.integer(input));
 }
});
test('Finance explicit denomination keeps zero, two and three decimal units distinct',()=>{
 assert.equal(V.majorToMinor('123',0),'123');assert.equal(V.majorToMinor('123,45',2),'12345');assert.equal(V.majorToMinor('123.456',3),'123456');
 assert.equal(V.majorToMinor('1,230000',2),'123');assert.throws(()=>V.majorToMinor('1,234',2));assert.throws(()=>V.majorToMinor('1.1',0));assert.throws(()=>V.majorToMinor('1',1));
});
test('Finance form amounts retain the individual exact limit and canonical sign',()=>{
 assert.equal(V.majorToMinor('10000000000,00',2),'1000000000000');assert.equal(V.majorToMinor('-10000000000,00',2),'-1000000000000');
 assert.throws(()=>V.majorToMinor('10000000000,01',2));assert.equal(V.majorToMinor('-0,00',2),'0');assert.throws(()=>V.integer('-0'));assert.throws(()=>V.majorToMinor('1.234,56',2));
});
test('Finance large aggregate display never passes through floating point',()=>{
 assert.equal(V.money('9007199254740993','EUR',2),'90.071.992.547.409,93 EUR');
 assert.equal(V.add(Array.from({length:10000},()=> '1000000000000')),'10000000000000000');
 assert.equal(V.money('-9007199254740993','JPY',0),'−9.007.199.254.740.993 JPY');
});
test('Finance null unknown and actual zero are separate display states',()=>{
 assert.equal(V.money(null,'EUR',2),'Nicht bekannt');assert.equal(V.money('0','EUR',2),'0,00 EUR');assert.throws(()=>V.money(0,'EUR',2));
 assert.deepEqual(V.convert('1',3,'1','1'),{minor:'0',status:'below_base_precision'});
 assert.deepEqual(V.convert('0',3,'1','1'),{minor:'0',status:'known'});
});
test('Finance rational FX rounds actual half units away from zero with bounded rate strings',()=>{
 assert.deepEqual(V.convert('5',3,'1','1'),{minor:'1',status:'known'});assert.deepEqual(V.convert('-5',3,'1','1'),{minor:'-1',status:'known'});
 assert.equal(V.convert('100',2,'1','3').minor,'33');assert.throws(()=>V.convert('100',2,'0','1'));assert.throws(()=>V.convert('100',2,'1','0'));assert.throws(()=>V.convert('100',2,'1000000000000000000','1'));assert.throws(()=>V.convert('1',2,1,'1'));
});
test('Finance signed category quotas use floor and deterministic UUID ties',()=>{
 assert.deepEqual(V.allocateConverted([{id:b,original_minor:'5'},{id:a,original_minor:'5'}],3,'1','1'),[{id:b,minor:'0'},{id:a,minor:'1'}]);
 const discount=V.allocateConverted([{id:a,original_minor:'15'},{id:b,original_minor:'-5'}],3,'1','1');assert.deepEqual(discount,[{id:a,minor:'2'},{id:b,minor:'-1'}]);assert.equal(V.add(discount.map(row=>row.minor)),'1');
});
test('Finance category conversion preserves zero-sum positive and negative fractions',()=>{
 const rows=V.allocateConverted([{id:a,original_minor:'-1'},{id:b,original_minor:'1'}],0,'1','3');assert.equal(V.add(rows.map(row=>row.minor)),'0');assert.deepEqual(rows,[{id:a,minor:'-33'},{id:b,minor:'33'}]);
 assert.throws(()=>V.allocateConverted([{id:a,original_minor:'1'},{id:a,original_minor:'2'}],2,'1','1'));
 assert.throws(()=>V.allocateConverted([{id:a,original_minor:'1000000000000'}],2,'2','1'));
 assert.throws(()=>V.allocateConverted([{id:a,original_minor:'1000000000000'},{id:b,original_minor:'-1000000000000'}],2,'2','1'));
});
test('Finance equal person shares conserve positive and negative cents using UUID byte order',()=>{
 assert.deepEqual(V.shares('1',[{membership_id:b,value:'1'},{membership_id:a,value:'1'}],'equal'),[{membership_id:b,minor:'0'},{membership_id:a,minor:'1'}]);
 assert.deepEqual(V.shares('-1',[{membership_id:b,value:'1'},{membership_id:a,value:'1'}],'equal'),[{membership_id:b,minor:'0'},{membership_id:a,minor:'-1'}]);
});
test('Finance percent precision is explicit ppm and must total one million',()=>{
 assert.equal(V.percentage('0,0001'),'1');assert.equal(V.percentage('100,0000'),'1000000');assert.throws(()=>V.percentage('100.0001'));assert.throws(()=>V.percentage('1.00001'));
 assert.deepEqual(V.shares('1',[{membership_id:a,value:'25'},{membership_id:b,value:'75'}],'percent'),[{membership_id:a,minor:'0'},{membership_id:b,minor:'1'}]);assert.throws(()=>V.shares('10',[{membership_id:a,value:'99.9999'}],'percent'));
});
test('Finance weighted and fixed splits reject duplicate people, wrong signs and invented remainder',()=>{
 assert.deepEqual(V.shares('7',[{membership_id:a,value:'2'},{membership_id:b,value:'1'}],'weights'),[{membership_id:a,minor:'5'},{membership_id:b,minor:'2'}]);
 assert.deepEqual(V.shares('-7',[{membership_id:a,value:'-7'},{membership_id:b,value:'0'}],'fixed'),[{membership_id:a,minor:'-7'},{membership_id:b,minor:'0'}]);
 assert.throws(()=>V.shares('7',[{membership_id:a,value:'8'},{membership_id:b,value:'-1'}],'fixed'));assert.throws(()=>V.shares('7',[{membership_id:a,value:'6'}],'fixed'));assert.throws(()=>V.shares('7',[{membership_id:a,value:'0'}],'weights'));assert.throws(()=>V.shares('7',[{membership_id:a,value:'1'},{membership_id:a,value:'1'}],'equal'));
});
test('Finance person allocations do not conflate a foreign obligation with a zero EUR presentation',()=>{
 const original=V.shares('1',[{membership_id:a,value:'1'},{membership_id:b,value:'1'}],'equal');assert.equal(V.add(original.map(row=>row.minor)),'1');assert.equal(V.convert('1',3,'1','1').minor,'0');assert.equal(V.money(original[0].minor,'KWD',3),'0,001 KWD');
});
test('Finance CSV preserves quoted multiline fields, delimiters, escaped quotes and UTF8 BOM',()=>{
 const parsed=V.csv('\ufeffDatum;Text;Betrag\r\n2026-10-30;"Markt; \"\"Nord\"\"\nZweite Zeile";-1,00\r\n',';');assert.deepEqual(parsed.headers,['Datum','Text','Betrag']);assert.equal(parsed.count,1);assert.deepEqual(parsed.rows[0],['2026-10-30','Markt; "Nord"\nZweite Zeile','-1,00']);
});
test('Finance CSV validates the actual logical row budget and never silently truncates',()=>{
 const raw='Datum;Betrag\n'+Array.from({length:500},(_,i)=>'2026-10-30;'+i).join('\n');assert.equal(V.csv(raw,';').count,500);assert.throws(()=>V.csv(raw+'\n2026-10-30;501',';'));assert.throws(()=>V.csv('a;a\n1;2',';'));assert.throws(()=>V.csv('a\n"unclosed',';'));assert.throws(()=>V.csv('a\n"x"suffix',';'));
});
test('Finance CSV byte budget counts UTF8 bytes rather than characters',()=>{
 assert.equal(V.bytes('ä€'),5);assert.throws(()=>V.csv('Spalte\n'+'ä'.repeat(524289),';'));assert.throws(()=>V.csv('a\0b\n1',';'));
});
test('Finance canonical identifiers and metadata use a separate safe integer boundary',()=>{
 assert.equal(V.uuid(a.toUpperCase()),a);assert.throws(()=>V.uuid('00000000-0000-0000-0000-000000000000'));assert.throws(()=>V.uuid(a+' '));assert.throws(()=>V.count(Number.MAX_SAFE_INTEGER+1));assert.throws(()=>V.count('1'));assert.equal(V.count(50,50),50);assert.throws(()=>V.count(51,50));
});
test('Finance civil dates do not invent DST times or normalize invalid days',()=>{
 assert.equal(V.day('2024-02-29'),'2024-02-29');assert.equal(V.day('2026-10-25'),'2026-10-25');assert.throws(()=>V.day('1900-02-29'));assert.throws(()=>V.day('2026-02-30'));assert.throws(()=>V.day('2026-10-25T00:00:00Z'));
});
test('Finance saved month criteria freeze the whole civil month without an implicit future month',()=>{
 assert.deepEqual(V.monthRange('2024-02'),{from:'2024-02-01',to:'2024-02-29'});assert.deepEqual(V.monthRange('2026-10'),{from:'2026-10-01',to:'2026-10-31'});assert.throws(()=>V.monthRange('2026-13'));assert.throws(()=>V.monthRange(' 2026-10'));
});
test('Finance review serialization keeps complete arrays and exact string facts stable',()=>{
 const input={generation:2,payload:{money:{minor:'9007199254740993',currency:'EUR',exponent:2},lines:[{id:c,minor:'-1'}]}};
 assert.equal(V.stable(input),V.stable({payload:input.payload,generation:2}));assert.notEqual(V.stable(input),V.stable({...input,payload:{...input.payload,lines:[]}}));assert.throws(()=>V.stable({version:Number.MAX_SAFE_INTEGER+1}));assert.throws(()=>V.stable({value:undefined}));
});
