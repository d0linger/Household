'use strict';
const {test}=require('node:test');const assert=require('node:assert/strict');
const {amount,shifted,display,participantCount,portionImpact,comparison}=require('../internal/web/static/planning.js');
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

test('participant preview is bounded, canonical and never a fractional guest count',()=>{
 const a='aaaaaaaa-0000-4000-8000-000000000001',b='bbbbbbbb-0000-4000-8000-000000000002';
 assert.equal(participantCount([a,b],'3'),'5');assert.equal(participantCount([],'0'),'0');assert.equal(participantCount([a],'100'),'101');
 for(const [ids,guests]of [[[a,a],'0'],[[a,a.toUpperCase()],'0'],[['invalid'],'1'],[Array(101).fill(a),'1'],[[],'101'],[[],'-1'],[[],'0.5'],[[],''],[[],null]])assert.equal(participantCount(ids,guests),null);
});

test('conflict comparison identifies participants, guests and component portion modes',()=>{
 const current={title:'Abendessen',planned_on:'2026-10-26',meal_slot:'dinner',kind:'recipe',status:'planned',servings:'2.000000',servings_mode:'participants',guest_count:1,participants:[{name:'Gespeicherte Person',active:false}]};
 const text=comparison(current,[{title:'Beilage',source_revision:1,base_servings:'1',servings:'2',servings_mode:'meal',prep_minutes:null,cook_minutes:10}]);
 assert.ok(text.includes('Portionsmodus: Teilnehmer und Gäste'));assert.ok(text.includes('Gäste: 1'));assert.ok(text.includes('Gespeicherte Person (nicht mehr aktiv)'));assert.ok(text.includes('Mit Mahlzeit verknüpft'));
});

test('portion impact compares exact values without float rounding or formatting-only changes',()=>{
 const old={id:'one',title:'Hauptgericht',servings:'9007199254.740993',servings_mode:'meal'};
 assert.deepEqual(portionImpact([old],[{...old,servings:'9007199254,740993'}]),[]);
 const result=portionImpact([old],[{...old,servings:'9007199254.740992'}]);
 assert.equal(result.length,1);assert.ok(result[0].includes('Gespeichert: 9007199254.740993 Portionen'));assert.ok(result[0].includes('Entwurf: 9007199254.740992 Portionen'));
});

test('portion impact names mode changes, new and removed components and invalid quantities',()=>{
 const old={id:'one',title:'Beilage',servings:'1.000000',servings_mode:'independent'};
 const changed=portionImpact([old],[{...old,servings:'1',servings_mode:'meal'}]);assert.equal(changed.length,1);assert.ok(changed[0].includes('Gespeichert: 1 Portion (eigene Portionen)'));assert.ok(changed[0].includes('Entwurf: 1 Portion (mit Mahlzeit verknüpft)'));
 const replaced=portionImpact([old],[{title:'Neue Beilage',servings:''}]);assert.equal(replaced.length,2);assert.ok(replaced[0].includes('Neu im Entwurf. Entwurf: Menge prüfen'));assert.ok(replaced[1].includes('Im Entwurf entfernt.'));
});
