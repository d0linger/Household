'use strict';
const assert=require('node:assert/strict');
const {test}=require('node:test');
const V=require('../internal/web/static/fuel-module-values.js');

test('Fuel values retain an eighteen-digit physical decimal without float loss',()=>{
 for(const[input,wanted]of[
  ['9007199254.740993','9007199254.740993'],
  ['900719925474,099993','900719925474.099993'],
  ['000012.003400','12.003400'],
  [' 0.000001 ','0.000001'],
  ['999999999999.999999','999999999999.999999']
 ])assert.equal(V.decimal(input),wanted,input);
 assert.equal(V.exact('9007199254.740993'),'9007199254,740993');
 assert.equal(V.exact('0.000001'),'0,000001');
});

test('Fuel decimal input refuses exponents, units, thousands, signs and excess precision',()=>{
 for(const value of['1e3','1/2','1 kg','1 000','1,000.00','-1','+1','1.0000001','1000000000000','', 'Infinity','NaN'])assert.throws(()=>V.decimal(value),undefined,value);
 assert.throws(()=>V.decimal('0',{positive:true}));
 assert.equal(V.decimal('0'),'0');
 assert.equal(V.decimal('100.01',{scale:2}),'100.01');
 assert.throws(()=>V.decimal('100.001',{scale:2}));
});

test('Fuel decimal boundary rejects Number even when the number looks small',()=>{
 for(const value of[0,1,1.25,9007199254.740993])assert.throws(()=>V.decimal(value),undefined,String(value));
});

test('Fuel decimal boundary rejects coercible non-text objects instead of inventing a value',()=>{
 assert.throws(()=>V.decimal({toString:()=> '1.25'}));
 assert.throws(()=>V.decimal([],{nullable:true}));
 assert.throws(()=>V.decimal(['1.25']));
});

test('Fuel euro input is exact whole cents and never silently rounds a third decimal',()=>{
 for(const[input,wanted]of[['0','0'],['0.01','1'],['0,10','10'],['12.34','1234'],['0000012,3','1230'],['999999999999.99','99999999999999']])assert.equal(V.majorToMinor(input),wanted,input);
 for(const input of['0.001','12.345','1e2','1,234.00','-0.01'])assert.throws(()=>V.majorToMinor(input),undefined,input);
});

test('Fuel money input refuses already rounded JavaScript Number values',()=>{
 for(const value of[0,12.34,999999999999.99])assert.throws(()=>V.majorToMinor(value),undefined,String(value));
});

test('Fuel aggregate cents display stays exact beyond the safe integer boundary',()=>{
 assert.equal(V.minorToMajor('9007199254740993'),'90071992547409,93');
 assert.equal(V.minorToMajor('90071992547409931234567890'),'900719925474099312345678,90');
 assert.equal(V.minorToMajor('-9007199254740993'),'−90071992547409,93');
 assert.equal(V.minorToMajor('0'),'0,00');
 assert.equal(V.minorToMajor('9'.repeat(64)),'9'.repeat(62)+',99');
 assert.equal(V.minorToMajor('9'.repeat(65)),null);
 for(const value of[1234,Number.MAX_SAFE_INTEGER+1,{},'1e9','1.1'])assert.equal(V.minorToMajor(value),null);
});

test('Fuel unknown values stay distinct from a documented free charge or zero reading',()=>{
 for(const value of[null,undefined,'']){
  assert.equal(V.decimal(value,{nullable:true}),null);
  assert.equal(V.majorToMinor(value,{nullable:true}),null);
  assert.equal(V.minorToMajor(value),null);
  assert.equal(V.exact(value),'Nicht bekannt');
 }
 assert.equal(V.majorToMinor('0'),'0');
 assert.equal(V.exact('0.000000'),'0');
 assert.equal(V.minorToMajor('0'),'0,00');
});

test('Fuel formatted physical display never presents a Number as an exact value',()=>{
 for(const value of[0,1.25,9007199254.740993]){
  let text;try{text=V.exact(value);}catch{continue;}
  assert.equal(text,'Nicht bekannt','a Number must be refused or explicitly unknown');
 }
});

test('Fuel civil dates retain calendar identity across leap days and DST boundaries',()=>{
 for(const day of['1900-01-01','2024-02-29','2026-03-29','2026-10-25','2199-12-31'])assert.equal(V.civil(day),day);
 for(const day of['2025-02-29','2026-02-30','2026-13-01','2026-1-01','1899-12-31','2200-01-01','2026-10-25T00:00:00Z'])assert.throws(()=>V.civil(day),undefined,day);
 assert.equal(V.civil('',{nullable:true}),null);
});

test('Fuel UUID selections normalize casing but never remove corrupt reference predicates',()=>{
 const id='ABCDEF01-2345-4678-9ABC-0123456789AB';
 assert.equal(V.validateUUID(id),id.toLowerCase());
 assert.equal(V.validateUUID('',{nullable:true}),null);
 for(const bad of['',id+'x',' '+id,id.replaceAll('-',''),'not-a-uuid','../../other'])assert.throws(()=>V.validateUUID(bad),undefined,bad);
});

test('Fuel logical CSV row counts and recurrence limits refuse out-of-range fractions',()=>{
 for(const value of['1','1000',1000])assert.equal(V.integer(value,1,1000),String(value));
 for(const value of['0','1001','1.1','1e2','-1','9999999999999999999999999'])assert.throws(()=>V.integer(value,1,1000),undefined,value);
 assert.equal(V.integer('36500',1,36500),'36500');
 assert.throws(()=>V.integer('36501',1,36500));
 assert.equal(V.integer('',1,1000,{nullable:true}),null);
});

test('Fuel dimensions never silently substitute litres, kilograms or electrical energy',()=>{
 for(const[fuel,unit]of[['petrol','l'],['diesel','l'],['lpg','l'],['cng','kg'],['hydrogen','kg'],['electric','kWh']])assert.equal(V.unit(fuel),unit);
 for(const unknown of['unknown','electricity','CNG','kwh','diesel/kg',''])assert.equal(V.unit(unknown),null);
});

test('Fuel text limits count Unicode characters and refuse embedded zero bytes',()=>{
 assert.equal(V.text('  Äpfel 🛻  ',8),'Äpfel 🛻');
 assert.equal(V.text('🛻'.repeat(80),80),'🛻'.repeat(80));
 assert.throws(()=>V.text('🛻'.repeat(81),80));
 assert.throws(()=>V.text('a\0b',80));
 assert.equal(V.text(' ',80,{nullable:true}),null);
});

test('Fuel command byte budgets count actual UTF-8 and JSON escaping, including CSV decisions',()=>{
 const command={action:'import.confirm',generation:2,data:{job_id:'01234567-1234-4234-8234-0123456789ab',job_version:'7',decisions:Array.from({length:100},(_,i)=>({row_number:String(i+1),fingerprint:'ab'.repeat(32),decision:i%2?'skip':'import',duplicate_ack:false})),notes:'Änderung 🛻\r\n"quoted"\tvalue'}};
 const before=JSON.stringify(command);
 assert.equal(V.commandBytes(command),Buffer.byteLength(before,'utf8'));
 assert.ok(V.commandBytes(command)>before.length,'UTF-16 character length is not a byte budget');
 assert.equal(JSON.stringify(command),before,'budget calculation does not mutate command or reorder decisions');
 const twoMiB='a'.repeat(2*1024*1024),csv={action:'import.stage',data:{csv_text:twoMiB}};
 assert.equal(V.commandBytes(csv),twoMiB.length+Buffer.byteLength('{"action":"import.stage","data":{"csv_text":""}}','utf8'));
});

test('Fuel canonical command shape keeps decision order and exact strings',()=>{
 const input={z:'9007199254.740993',data:{notes:'<script>literal</script>',b:2,a:'2'},decisions:[{z:1,a:2},{a:3,z:4}]};
 assert.deepEqual(Object.keys(V.canonical(input)),['data','decisions','z']);
 assert.deepEqual(V.canonical(input).decisions,[{a:2,z:1},{a:3,z:4}]);
 assert.equal(V.canonical(input).z,'9007199254.740993');
 assert.equal(input.data.notes,'<script>literal</script>');
});

test('Fuel chart coordinates quantize only the picture and retain exact source rows',()=>{
 const rows=[{per_100:'90071992547409931234567890.123456'},{per_100:null},{per_100:'0.004',below_precision:true},{per_100:'90071992547409931234567891.123456'}],before=JSON.stringify(rows),points=V.chartPoints(rows,'per_100');
 assert.equal(points[0].value,rows[0].per_100);
 assert.equal(points[3].value,rows[3].per_100);
 assert.equal(points[1],null);
 assert.equal(points[2],null,'a positive below-display bound is never drawn as zero');
 for(const point of points.filter(Boolean)){assert.ok(Number.isInteger(point.x)&&point.x>=0&&point.x<=1000);assert.ok(Number.isInteger(point.y)&&point.y>=0&&point.y<=1000);}
 assert.equal(JSON.stringify(rows),before);
 assert.equal(points[0].row,rows[0]);
 assert.deepEqual(V.chartPoints([{per_100:1.25},{per_100:null}],'per_100'),[]);
 assert.deepEqual(V.chartPoints([{per_100:'0.00',below_precision:true}],'per_100'),[]);
 assert.equal(V.chartPoints([{energy_kwh:'0'}],'energy_kwh')[0].value,'0');
});
