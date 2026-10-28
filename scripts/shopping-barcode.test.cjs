'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const {normalize,camera,formatQuantity}=require('../internal/web/static/shopping-barcode.js');
const settle=async()=>{for(let n=0;n<12;n++)await Promise.resolve();};
function fixture(options={}){
 let stopped=0,requests=0,detects=0,code=[],messages=[],active=[],sequence=0;
 const tasks=new Map(),stream={getTracks:()=>[{stop:()=>{stopped++;}}]},video={srcObject:null,play:async()=>options.play?options.play():undefined,pause:()=>{}};
 class Detector{static async getSupportedFormats(){return['ean_13','code_128'];}async detect(){detects++;return options.detect?options.detect():[];}}
 const control=camera({secure:options.secure??true,media:{getUserMedia:async()=>{requests++;return options.acquire?options.acquire():stream;}},Detector:options.unsupported?undefined:Detector,video,onCode:c=>code.push(c),onStatus:m=>messages.push(m),onActive:a=>active.push(a),later:(fn,delay)=>{const id=++sequence;tasks.set(id,{fn,delay});return id;},cancel:id=>tasks.delete(id)});
 return{control,stream,video,tasks,code,messages,active,stats:()=>({stopped,requests,detects})};
}
test('local identity preserves exact leading zeroes/case and treats URLs as inert text',()=>{
 for(const value of['00073AbC','0012345678905','https://example.invalid/a','<script>inert</script>','A'.repeat(64)])assert.equal(normalize(' '+value+'\n'),value);
 for(const value of['','two codes','A'.repeat(65),'ä','one\0','one\x7f'])assert.throws(()=>normalize(value));
});
test('unsupported/insecure camera requests no media and leaves manual fallback',async()=>{
 for(const options of[{secure:false},{unsupported:true}]){const f=fixture(options);await f.control.start();assert.equal(f.stats().requests,0);assert.match(f.messages.at(-1),/manuell|Handscanner/);}
 const f=fixture({acquire:async()=>{throw Error('Permission denied');}});await f.control.start();assert.equal(f.code.length,0);assert.match(f.messages.at(-1),/Kein Kamerazugriff/);
});
test('closing while permission is pending stops every late stream and never decodes',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve;}),f=fixture({acquire:()=>pending});
 const start=f.control.start();await settle();assert.equal(f.stats().requests,1);f.control.stop();release(f.stream);await start;
 assert.deepEqual(f.stats(),{stopped:1,requests:1,detects:0});assert.equal(f.video.srcObject,null);assert.equal(f.code.length,0);assert.equal(f.tasks.size,0);
});
test('the finite lease also cancels a pending permission request and stops a later stream',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve;}),f=fixture({acquire:()=>pending});
 const starting=f.control.start();await settle();assert.equal(f.stats().requests,1);assert.equal(f.active.at(-1),true);
 const expiry=[...f.tasks.values()].find(t=>t.delay===60000);assert.ok(expiry);expiry.fn();assert.equal(f.active.at(-1),false);assert.equal(f.tasks.size,0);
 release(f.stream);await starting;assert.deepEqual(f.stats(),{stopped:1,requests:1,detects:0});assert.equal(f.code.length,0);
});
test('late detection after close cannot populate a draft or restart capture',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve;}),f=fixture({detect:()=>pending});
 await f.control.start();assert.equal(f.stats().detects,1);f.control.stop();release([{rawValue:'00073AbC'}]);await settle();
 assert.equal(f.code.length,0);assert.equal(f.stats().stopped,1);assert.equal(f.tasks.size,0);assert.equal(f.video.srcObject,null);
});
test('one in-flight native decode fills only an exact draft then stops tracks',async()=>{
 let release;const f=fixture({detect:()=>new Promise(resolve=>{release=resolve;})});await f.control.start();await settle();
 assert.equal(f.stats().detects,1);assert.equal([...f.tasks.values()].filter(t=>t.delay===300).length,0);
 release([{rawValue:' 0012345678905 '}]);await settle();assert.deepEqual(f.code,['0012345678905']);assert.equal(f.stats().stopped,1);assert.equal(f.video.srcObject,null);assert.equal(f.tasks.size,0);
});
test('empty frames schedule bounded serial polling and one minute stops the camera',async()=>{
 const f=fixture();await f.control.start();await settle();assert.equal(f.stats().detects,1);
 const expiry=[...f.tasks.values()].find(t=>t.delay===60000);assert.ok(expiry);expiry.fn();assert.equal(f.stats().stopped,1);assert.equal(f.tasks.size,0);assert.match(f.messages.at(-1),/Minute/);
});
test('a stalled video play promise cannot keep camera tracks beyond the finite lease',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve;}),f=fixture({play:()=>pending});
 const starting=f.control.start();await settle();
 assert.equal(f.stats().requests,1);assert.equal(f.stats().detects,0);assert.equal(f.video.srcObject,f.stream);
 const expiry=[...f.tasks.values()].find(t=>t.delay===60000);assert.ok(expiry);expiry.fn();
 assert.equal(f.stats().stopped,1);assert.equal(f.video.srcObject,null);assert.equal(f.tasks.size,0);
 release();await starting;assert.equal(f.stats().detects,0);assert.equal(f.tasks.size,0);
});
test('pack labels retain exact quantities with local decimals and no redundant zeros',()=>{
 for(const [value,expected] of [['500.000000','500'],['0.000001','0,000001'],['999999999999.999999','999999999999,999999']])assert.equal(formatQuantity(value),expected);
});
