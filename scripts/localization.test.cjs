'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {execFileSync} = require('node:child_process');
const {audit,goAudit,technicalTranslations} = require('./check-localization.cjs');
const root=path.resolve(__dirname,'..');
function runtime(lang){const context=vm.createContext({document:{documentElement:{lang}},Intl,Date,console});for(const file of ['i18n-catalog.js','i18n.js'])vm.runInContext(fs.readFileSync(path.join(root,'internal/web/static',file),'utf8'),context);return context;}
test('catalog covers every explicit authored message and raw source candidates',()=>{const result=audit();assert.equal(result.missing.size,0,'Untranslated application copy: '+[...result.missing.keys()].slice(0,20).join('; '));assert.deepEqual(result.raw,[],'Unwrapped authored source copy remains');});
test('Go descriptors and explicit validation/HTTP message boundaries have reviewed coverage',()=>{assert.deepEqual(goAudit(),[],'Uncataloged authored Go message; translate it or document a precise technical exclusion');});
test('translation does not wrap selector, key-event or browser protocol values',()=>{assert.deepEqual(technicalTranslations(),[]);});
test('generated browser catalog matches the reviewed source catalogs',()=>{execFileSync(process.execPath,['scripts/build-localization.cjs','--check'],{cwd:root});});
test('translation chooses language without transforming parameters or user strings',()=>{const de=runtime('de-AT').HouseholdI18n,en=runtime('en-GB').HouseholdI18n;assert.equal(de.t('Speichern'),'Speichern');assert.equal(en.t('Speichern'),'Save');assert.equal(en.t('%s','<img src=x onerror=alert(1)> Speichern'),'<img src=x onerror=alert(1)> Speichern');assert.equal(en.locale(),'en-GB');assert.equal(de.locale(),'de-AT');});
test('English exact currency and decimal formatting retains arbitrary integer precision',()=>{const en=runtime('en-GB').HouseholdI18n,de=runtime('de-AT').HouseholdI18n;assert.equal(en.moneyExact('900719925474099301'),'€9,007,199,254,740,993.01');assert.equal(de.moneyExact('900719925474099301'),'9.007.199.254.740.993,01\u00a0€');assert.equal(en.moneyExact('-101'),'-€1.01');assert.equal(en.moneyExact('0'),'€0.00');assert.equal(en.exactDecimal('123456789012345678.000001'),'123456789012345678.000001');assert.equal(de.exactDecimal('123456789012345678.000001'),'123456789012345678,000001');assert.throws(()=>en.moneyExact(9007199254740992),/Inexact/);});
test('localized civil dates retain their selected timezone and source day',()=>{const en=runtime('en-GB').HouseholdI18n;assert.equal(en.date('2026-10-25T12:00:00Z',{dateStyle:'medium',timeZone:'UTC'}),'25 Oct 2026');assert.equal(en.date('invalid'), 'Unknown');});
test('English fuel and finance value display does not change canonical command values',()=>{const context=runtime('en-GB');context.module={exports:{}};vm.runInContext(fs.readFileSync(path.join(root,'internal/web/static/fuel-module-values.js'),'utf8'),context);const fuel=context.module.exports;assert.equal(fuel.minorToMajor('900719925474099301'),'9007199254740993.01');assert.equal(fuel.exact('123456789012.000001'),'123456789012.000001');assert.equal(fuel.majorToMinor('12,34'),'1234');assert.equal(fuel.civil('2026-10-25'),'2026-10-25');vm.runInContext(fs.readFileSync(path.join(root,'internal/web/static/finance-values.js'),'utf8'),context);const finance=context.module.exports;assert.equal(finance.money('900719925474099301','EUR',2),'9,007,199,254,740,993.01 EUR');assert.equal(finance.majorToMinor('12,34',2),'1234');});
function actualFunction(context, file, start, end, name) {
  const source=fs.readFileSync(path.join(root,'internal/web/static',file),'utf8');
  const from=source.indexOf(start),through=source.indexOf(end,from);
  assert.ok(from>=0&&through>from,'Actual production helper boundaries');
  const helper=source.split('\n').find(line=>line.startsWith('const _householdText_'));
  return vm.runInContext(helper+'\n'+source.slice(from,through)+';'+name,context);
}
test('typed schedule explanation localizes weekdays/months without changing clock, zone or schedule',()=>{
  const view={explanation:'Unveränderte deutsche Servererklärung',normalized:{schedule:{kind:'weekly',anchor:'calendar',interval:2,weekdays:[1,7],months:[3,10],local_time:'08:30',timezone:'Europe/Vienna'}}};
  const original=JSON.stringify(view);
  const en=actualFunction(runtime('en-GB'),'chores-workspace.js',' function previewExplanation(','\n function ruleText(','previewExplanation');
  const de=actualFunction(runtime('de-AT'),'chores-workspace.js',' function previewExplanation(','\n function ruleText(','previewExplanation');
  assert.equal(en(view),'Every 2 weeks: Mon, Sun · Season: Mar, Oct · 08:30 · Europe/Vienna');
  assert.equal(de(view),view.explanation);assert.equal(JSON.stringify(view),original);
});
test('typed document rule explanation preserves the user pattern exactly',()=>{
  const reason=actualFunction(runtime('en-GB'),'documents-workspace.js',' function ruleReason(','\n function previewResult(','ruleReason');
  const user='Speichern: <script> & "Text enthält: "; %s';
  assert.equal(reason('Text enthält: '+user),'Text contains: '+user);
  assert.equal(reason(user),user);
});
test('typed Recipe import diagnostics preserve numbered positions and leave imported content outside translation',()=>{
  const message=actualFunction(runtime('en-GB'),'recipe-import-review.js',' function importMessage(','\n function losses(','importMessage');
  assert.equal(message('zutat 3: Menge ist ungültig'),'Ingredient 3: invalid quantity');
  assert.equal(message('schritt 2 ist ungültig'),'Step 2 is invalid');
  assert.equal(message('beschreibung: unzulässiger oder zu langer Text'),'Description: Disallowed or excessively long text');
  const raw='User recipe: <script> Speichern';assert.equal(message(raw),raw);
  const de=actualFunction(runtime('de-AT'),'recipe-import-review.js',' function importMessage(','\n function losses(','importMessage');
  assert.equal(de('beschreibung: unzulässiger oder zu langer Text'),'beschreibung: unzulässiger oder zu langer Text');
});
