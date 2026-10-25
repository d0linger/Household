'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {remaining,duration}=require('../internal/web/static/cooking.js');
test('deadline is an absolute instant through a daylight saving transition',()=>{
 const timer={state:'running',deadline:'2026-10-25T01:30:00Z'};
 assert.equal(remaining(timer,Date.parse('2026-10-25T02:29:59+01:00')),1000);
 assert.equal(remaining(timer,Date.parse('2026-10-25T03:30:00+01:00')),0);
});
test('paused timer preserves milliseconds regardless of device time',()=>{
 const timer={state:'paused',remaining_millis:1234};
 for(const now of [0,Date.parse('2099-01-01')])assert.equal(remaining(timer,now),1234);
 assert.equal(remaining({...timer,state:'done'},0),0);
});
test('countdown rounds remaining fractions upwards and never becomes negative',()=>{
 for(const [input,want] of [[-100,'00:00:00'],[0,'00:00:00'],[1,'00:00:01'],[59999,'00:01:00'],[3600000,'01:00:00'],[86400000,'24:00:00']])assert.equal(duration(input),want);
});
