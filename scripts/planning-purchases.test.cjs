'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const {amount,decisionKey}=require('../internal/web/static/planning-purchases.js');
test('Planning purchases keep exact positive and negative subprecision differences visible',()=>{
 assert.equal(amount({value:'0.000000',rational:'1/3000000',below_precision:true,approximate:true}),'< 0,000001 (exakt 1/3000000)');
 assert.equal(amount({value:'-0.000000',rational:'-1/3000000',below_precision:true,approximate:true}),'> −0,000001 (exakt -1/3000000)');
 assert.equal(amount({value:'0.333333',rational:'1/3',approximate:true}),'≈ 0,333333 (exakt 1/3)');
 assert.equal(amount({value:'0.000000',rational:'0',approximate:false}),'0');
 assert.equal(amount(null),'Offen');
});
test('Reactivation selects a whole meal independently of replaced ingredient identities',()=>{
 assert.equal(decisionKey({action:'reactivate',meal_id:'meal',requirement_id:''}),'meal/meal');
 assert.equal(decisionKey({action:'accept',requirement_id:'ingredient-one'}),'ingredient-one');
 assert.notEqual(decisionKey({action:'renew',requirement_id:'ingredient-two'}),decisionKey({action:'reactivate',meal_id:'ingredient-two'}));
});
