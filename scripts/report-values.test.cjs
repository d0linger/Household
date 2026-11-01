'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const {euroMinor} = require('../internal/web/static/report-values.js');

test('Report totals preserve exact signed cents beyond JavaScript integer precision', () => {
  assert.equal(euroMinor('9007199254740993'), '90.071.992.547.409,93\u00a0€');
  assert.equal(euroMinor('-9007199254740993'), '-90.071.992.547.409,93\u00a0€');
  assert.equal(euroMinor('10000000000000000'), '100.000.000.000.000,00\u00a0€');
});

test('Report known zero and unknown remain distinct with safe legacy integer compatibility', () => {
  assert.equal(euroMinor('0'), '0,00\u00a0€');
  assert.equal(euroMinor(-1), '-0,01\u00a0€');
  assert.equal(euroMinor(1250), '12,50\u00a0€');
  assert.equal(euroMinor(null), 'Nicht bekannt');
  assert.equal(euroMinor(undefined), 'Nicht bekannt');
});

test('Report formatting rejects coercion, fractions and already imprecise numeric totals', () => {
  for (const value of [9007199254740993, 1.1, NaN, Infinity, true, {}, [], '1.0', '01', '-0', '+1', '1e3', '']) {
    assert.throws(() => euroMinor(value));
  }
});
