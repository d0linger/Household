'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const {amount,reasons}=require('../internal/web/static/ingredient-pantry.js');
test('Comparison display keeps unknown, approximate and positive sub-micro values distinct',()=>{assert.equal(amount(null),'unbekannt');assert.equal(amount({value:'0.000000',rational:'0'}),'0');assert.equal(amount({value:'0.333333',rational:'1/3',approximate:true}),'≈ 0,333333');assert.equal(amount({value:'0.000000',rational:'1/1000000000000000',approximate:true,below_precision:true}),'kleiner als 0,000001 (positiv)');for(const code of ['catalog_unassigned','catalog_missing','pantry_disabled','product_unassigned','binding_stale','product_archived','quantity_unknown','unit_incompatible'])assert.ok(reasons[code]);});
