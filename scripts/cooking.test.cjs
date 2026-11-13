'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {remaining,duration,freshTimer,foregroundReference,timerSound,foregroundHandler}=require('../internal/web/static/cooking.js');
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

const uuid=n=>'00000000-0000-4000-8000-'+String(n).padStart(12,'0'),scope='a'.repeat(64),due=Date.parse('2026-11-03T12:00:00Z');
const timer=()=>({id:uuid(1),version:2,state:'running',deadline:new Date(due).toISOString()});
const session=()=>({id:uuid(2),state:'active',timers:[timer()]});
const request=()=>({type:'COOKING_PUSH_FOREGROUND',id:uuid(3),auth_scope:scope,expires_at:due/1000+60});
const reference=()=>({id:uuid(3),auth_scope:scope,timer_id:uuid(1),session_id:uuid(2),revision:2,deadline:new Date(due).toISOString(),server_now:new Date(due+1000).toISOString()});
test('timer alert freshness excludes early, paused, done and one-minute-old deadlines',()=>{
 for(const now of [due,due+1,due+59999])assert.equal(freshTimer(timer(),now),true);
 for(const now of [due-1,due+60000,NaN])assert.equal(freshTimer(timer(),now),false);
 for(const changed of [{state:'paused'},{state:'done'},{deadline:null},{deadline:'invalid'}])assert.equal(freshTimer({...timer(),...changed},due),false);
});
test('authenticated foreground reference binds exact account scope, session, timer revision and deadline',()=>{
 assert.equal(foregroundReference(reference(),request(),session()).id,uuid(1));
 for(const change of [{id:uuid(4)},{auth_scope:'b'.repeat(64)},{session_id:uuid(4)},{timer_id:uuid(4)},{revision:3},{revision:1.5},{deadline:new Date(due-1000).toISOString()},{server_now:new Date(due-1).toISOString()},{server_now:new Date(due+60000).toISOString()},{url:'/outside'}])assert.equal(foregroundReference({...reference(),...change},request(),session()),null,JSON.stringify(change));
 for(const state of ['paused','done'])assert.equal(foregroundReference(reference(),request(),{...session(),timers:[{...timer(),state}]}),null);
 assert.equal(foregroundReference(reference(),request(),{...session(),state:'completed'}),null);
 assert.equal(foregroundReference(reference(),{...request(),expires_at:due/1000},session()),null);
 assert.equal(foregroundReference(reference(),{...request(),expires_at:due/1000+61},session()),null);
});
test('foreground reference uses server time despite an arbitrary browser wall-clock jump',()=>{
 const original=Date.now;try{Date.now=()=>Date.parse('2099-01-01');assert.ok(foregroundReference(reference(),request(),session()));Date.now=()=>0;assert.ok(foregroundReference(reference(),request(),session()));}finally{Date.now=original;}
});
function soundFixture(options={}){
 const states=[],contexts=[];let visible=true,played=0,closed=0,stopped=0;
 class Audio {
  constructor(){this.state='suspended';this.currentTime=0;contexts.push(this);}
  async resume(){if(options.resume)await options.resume();if(options.denied)throw Error('permission denied');this.state='running';}
  async close(){closed++;this.state='closed';}
  createGain(){return {gain:{setValueAtTime(){},linearRampToValueAtTime(){}},connect(){},disconnect(){}};}
  createOscillator(){return {frequency:{setValueAtTime(){}},connect(){},disconnect(){},start(){played++;},stop(){stopped++;}};}
 }
 const sound=timerSound(options.missing?null:Audio,()=>visible,state=>states.push(state));
 return {sound,states,contexts,setVisible(value){visible=value;},get played(){return played;},get closed(){return closed;},get stopped(){return stopped;}};
}
test('foreground audio requires an explicit activation and closes rather than silently resuming',async()=>{
 const f=soundFixture();assert.equal(f.sound.play(),false);assert.equal(f.played,0);
 assert.equal(await f.sound.enable(),true);assert.equal(f.sound.enabled,true);assert.equal(f.played,1);
 f.setVisible(false);assert.equal(f.sound.play(),false);f.sound.disable();assert.equal(f.closed,1);assert.equal(f.sound.enabled,false);
 f.setVisible(true);assert.equal(f.sound.play(),false);assert.equal(f.played,1);
 assert.equal(await f.sound.enable(),true);assert.equal(f.played,2);f.sound.disable();
});
test('unavailable or denied audio never reports a played alert',async()=>{
 for(const options of [{missing:true},{denied:true}]){const f=soundFixture(options);assert.equal(await f.sound.enable(),false);assert.equal(f.sound.enabled,false);assert.equal(f.played,0);assert.equal(f.states.at(-1),'unavailable');}
});
test('late audio activation after page hiding cannot revive its disposed context',async()=>{
 let release;const f=soundFixture({resume:()=>new Promise(resolve=>{release=resolve;})}),pending=f.sound.enable();
 f.setVisible(false);f.sound.disable();release();assert.equal(await pending,false);assert.equal(f.played,0);assert.equal(f.sound.enabled,false);assert.equal(f.closed,1);
});
function foregroundFixture(options={}){
 const source={},responses=[],closes=[],reads=[];let current=session(),visible=true,handled=0;
 const handler=foregroundHandler({controller:()=>source,visible:()=>visible,session:()=>current,read:async(id,signal)=>{reads.push({id,signal});return options.read?options.read(id,signal):reference();},handle(){handled++;return options.audible===true;}});
 const event=()=>({source,data:request(),ports:[{postMessage(value){responses.push(value);},close(){closes.push(true);}}]});
 return {handler,event,responses,closes,reads,get handled(){return handled;},setVisible(value){visible=value;},setSession(value){current=value;}};
}
test('foreground handler requests silence only after actual audible handling',async()=>{
 for(const audible of [false,true]){const f=foregroundFixture({audible});await f.handler.receive(f.event());assert.equal(f.handled,1);assert.equal(f.responses[0].handled,audible);assert.equal(f.responses[0].id,uuid(3));assert.equal(f.reads.length,1);assert.equal(f.reads[0].signal.aborted,true);assert.equal(f.closes.length,1);}
});
test('forged worker source and hidden page do not fetch delivery context',async()=>{
 const forged=foregroundFixture();await forged.handler.receive({...forged.event(),source:{}});assert.equal(forged.reads.length,0);
 const hidden=foregroundFixture();hidden.setVisible(false);await hidden.handler.receive(hidden.event());assert.equal(hidden.reads.length,0);
 const invalid=foregroundFixture();await invalid.handler.receive({...invalid.event(),data:{...request(),id:'../../session'}});assert.equal(invalid.reads.length,0);
});
test('foreground request abort, late reply, session replacement and visibility loss cannot emit a tone',async()=>{
 for(const action of ['abort','hidden','session','worker-cancel']){
  let release;const f=foregroundFixture({audible:true,read:()=>new Promise(resolve=>{release=resolve;})}),event=f.event(),pending=f.handler.receive(event);
  if(action==='abort')f.handler.stop();else if(action==='hidden')f.setVisible(false);else if(action==='session')f.setSession(session());else event.ports[0].onmessage({data:{cancel:true}});
  release(reference());await pending;assert.equal(f.handled,0,action);assert.deepEqual(f.responses,[]);assert.equal(f.reads[0].signal.aborted,true);
 }
});
test('foreground lookup has a bounded deadline and one in-flight request',async()=>{
 let release;const f=foregroundFixture({read:()=>new Promise(resolve=>{release=resolve;})}),pending=f.handler.receive(f.event());
 await f.handler.receive(f.event());assert.equal(f.reads.length,1);await pending;assert.equal(f.handled,0);assert.equal(f.reads[0].signal.aborted,true);
 release(reference());await Promise.resolve();assert.deepEqual(f.responses,[]);
});
