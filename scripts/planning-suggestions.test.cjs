'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const {amount,dateLabel,outcomes}=require('../internal/web/static/planning-suggestions.js');
test('suggestion quantities retain unknown, exact large and unpostable fractions',()=>{
 assert.equal(amount(null),'Unbekannt');
 assert.equal(amount({value:'9007199254.740993',rational:'9007199254740993/1000000',approximate:false}),'9007199254,740993');
 assert.equal(amount({value:'0.333333',rational:'1/3',approximate:true}),'≈ 0,333333 (exakt 1/3)');
 assert.equal(amount({value:'0.000000',rational:'1/1000000000000',approximate:true,below_precision:true}),'< 0,000001 (exakt 1/1000000000000)');
 assert.equal(amount({value:'0.000000',rational:'0',approximate:false}),'0');
});
test('date reasons distinguish use-by exclusion, opening guidance and best-before review',()=>{
 assert.equal(dateLabel({kind:'use_by',on:'2026-10-25',past:true,requires_review:true,excluded_from_coverage:true}),'Verbrauchsdatum 2026-10-25 · nicht zur Deckung gezählt');
 assert.equal(dateLabel({kind:'opening_hint',on:'2026-10-26',requires_review:true}),'Hinweis nach dem Öffnen 2026-10-26 · prüfen');
 assert.equal(dateLabel({kind:'best_before',on:'2026-10-30'}),'Mindesthaltbarkeit 2026-10-30');
});
test('unknown and empty candidates never receive a covered label',()=>{
 assert.match(outcomes.sufficient,/gedeckt/);assert.match(outcomes.incomplete,/offen/);assert.match(outcomes.empty,/Keine auswertbaren/);assert.match(outcomes.shortage,/Fehlmengen/);
});
