'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/finance-reminders.js'),'utf8');
const {projection}=require('../internal/web/static/finance-reminders.js');
const entry='11111111-1111-4111-8111-111111111111',member='22222222-2222-4222-8222-222222222222';
const snapshot=()=>({entry_id:entry,entry_version:2,generation:1,access_version:'a'.repeat(64),verified_paid:false,needs_reconfirmation:false,can_edit:true,can_confirm:false,members:[{id:member,label:'Taylor'}],members_more:false,reminder:null});
const reminder=()=>({version:1,due_on:'2026-12-10',responsible_member_id:member,responsible_name:'Taylor',state:'draft',confirmed_entry_version:null,confirmed_at:null,responsible_since:null,completed_at:null});
const all=node=>[node,...node.children.flatMap(all)];
class Element{
 constructor(tag,document){this.tag=tag;this.document=document;this.children=[];this.listeners={};this.dataset={};this.attributes={};this.value='';this.disabled=false;this.open=false;this.isConnected=true;this._text='';}
 set textContent(value){this._text=String(value);this.children=[];}get textContent(){return this._text+this.children.map(n=>n.textContent).join('');}
 append(...items){for(const item of items){item.parent=this;this.children.push(item);}}
 replaceChildren(...items){this.children=[];this._text='';this.append(...items);}
 setAttribute(key,value){this.attributes[key]=value;}
 addEventListener(name,fn){(this.listeners[name]??=[]).push(fn);}
 async emit(name,event={}){await Promise.all((this.listeners[name]||[]).map(fn=>fn({target:this,preventDefault(){},...event})));await settle();}
 querySelectorAll(selector){return all(this).slice(1).filter(n=>selector==='[data-reminder-action]'?n.dataset.reminderAction:selector==='[data-reminder-retry]'?n.dataset.reminderRetry: n.tag===selector);}
 querySelector(selector){return this.querySelectorAll(selector)[0]||null;}
 focus(){this.document.activeElement=this;}
 showModal(){this.open=true;}
 close(){this.open=false;for(const fn of this.listeners.close||[])fn();}
 reportValidity(){return true;}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(n=>n!==this);}
 getBoundingClientRect(){return {left:10,right:400,top:10,bottom:400};}
}
async function settle(){for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));}
function harness(initial=snapshot()){
 const document={createElement(tag){return new Element(tag,this);}};document.body=new Element('body',document);const opener=new Element('button',document);
 const events={},writes=[],reads=[];let saved=structuredClone(initial),mode='',seq=0,currentCount=0;
 const sandbox={module:{exports:{}},crypto:{randomUUID:()=>`33333333-3333-4333-8333-${String(++seq).padStart(12,'0')}`},addEventListener:(name,fn)=>{events[name]=fn;}};
 vm.runInNewContext(source,sandbox,{filename:'finance-reminders.js'});
 const controller=sandbox.module.exports.create({document,current:async()=>{currentCount++;},request:async(url,options={})=>{
  reads.push(url);if(options.method!=='POST')return structuredClone(saved);writes.push(structuredClone(options.body));
  if(mode){const error=Error('Request failed');error.status=mode==='conflict'?409:mode==='invalid'?400:0;throw error;}
  const c=options.body;saved.reminder={...reminder(),version:(saved.reminder?.version||0)+1,due_on:c.due_on,responsible_member_id:c.responsible_member_id,state:{save:'draft',confirm:'confirmed',complete:'completed',cancel:'cancelled'}[c.action]};saved.needs_reconfirmation=false;saved.can_confirm=c.action==='save'&&!saved.verified_paid;return structuredClone(saved);
 }});
 const find=fn=>all(document.body).find(fn),action=name=>find(n=>n.dataset.reminderAction===name),dialog=find(n=>n.tag==='dialog');
 return {controller,document,opener,dialog,writes,reads,events,find,action,setMode:value=>{mode=value;},setSaved:value=>{saved=structuredClone(value);},get saved(){return saved;},get currentCount(){return currentCount;},async open(shownVersion=initial.entry_version){await controller.open(entry,opener,shownVersion);},async click(name){await action(name).emit('click');},async save(){await find(n=>n.tag==='form').emit('submit');},async reload(){await find(n=>n.tag==='button'&&n.textContent==='Aktuellen Stand laden').emit('click');}};
}
test('reminder projection rejects foreign entries, malformed versions, duplicate members and invalid dates',()=>{
 const base=snapshot();assert.equal(projection(base,entry),base);
 for(const change of [{entry_id:member},{entry_version:0},{generation:1.5},{access_version:'a'},{verified_paid:'true'},{can_confirm:undefined},{members_more:undefined},{members:[base.members[0],base.members[0]]},{reminder:{...reminder(),due_on:'2026-02-30'}},{reminder:{...reminder(),state:'paid'}}])assert.throws(()=>projection({...base,...change},entry));
});
test('actual controller requires saved draft before explicit confirmation and never creates a payment',async()=>{
 const f=harness();await f.open();assert.equal(f.writes.length,0);assert.equal(f.action('confirm').disabled,true);
 f.find(n=>n.name==='due_on').value='2026-12-10';f.find(n=>n.name==='responsible_member_id').value=member;await f.save();
 assert.equal(f.saved.reminder.state,'draft');assert.equal(f.action('confirm').disabled,false);await f.click('confirm');
 assert.deepEqual(f.writes.map(row=>row.action),['save','confirm']);assert.equal(f.saved.reminder.state,'confirmed');
 assert.deepEqual(Object.keys(f.writes[0]).sort(),['action','version','entry_version','generation','access_version','due_on','responsible_member_id','request_id'].sort());
 assert.ok(f.reads.every(url=>url===`/api/v1/expenses/finance/entries/${entry}/payment-reminder`));assert.equal(f.currentCount,3);
});
test('unsaved due or recipient changes cannot silently ride along with confirmation',async()=>{
 const f=harness({...snapshot(),can_confirm:true,reminder:reminder()});await f.open();const due=f.find(n=>n.name==='due_on');due.value='2026-12-11';await f.find(n=>n.tag==='form').emit('input');
 assert.equal(f.action('confirm').disabled,true);await f.click('confirm');assert.equal(f.writes.length,0);await f.save();await f.click('confirm');assert.equal(f.writes[1].due_on,'2026-12-11');
});
test('source change allows fresh explicit reconfirmation, completion stays distinct from verified payment',async()=>{
 const f=harness({...snapshot(),can_confirm:true,needs_reconfirmation:true,reminder:{...reminder(),state:'confirmed'}});await f.open();assert.match(f.dialog.textContent,/Buchung wurde geändert/);assert.equal(f.action('confirm').disabled,false);
 await f.click('confirm');await f.click('complete');assert.equal(f.saved.reminder.state,'completed');assert.equal(f.saved.verified_paid,false);assert.match(f.dialog.textContent,/keine Zahlungsbestätigung/);
});

test('fresh reminder stamp cannot claim that changed entry contents were reviewed',async()=>{
 const initial={...snapshot(),reminder:{...reminder(),state:'confirmed'}},f=harness(initial);await f.open();
 f.setSaved({...initial,entry_version:3,access_version:'b'.repeat(64),can_confirm:true,needs_reconfirmation:true});await f.reload();
 assert.equal(f.action('confirm').disabled,true);assert.equal(f.find(n=>n.dataset.reminderSourceReview).href,'/m/expenses/workspace/entries?id='+entry);
 await f.click('confirm');assert.equal(f.writes.length,0,'No mutation can confirm unseen source contents');
 f.controller.clear();await f.open(3);assert.equal(f.action('confirm').disabled,false);await f.click('confirm');assert.equal(f.writes[0].entry_version,3);assert.equal(f.writes[0].access_version,'b'.repeat(64));
});
test('verified actual payment disables scheduling but permits explicit metadata completion',async()=>{
 const f=harness({...snapshot(),verified_paid:true,reminder:{...reminder(),state:'confirmed'}});await f.open();assert.equal(f.action('save').disabled,true);assert.equal(f.action('confirm').disabled,true);assert.equal(f.action('complete').disabled,false);
 await f.click('confirm');assert.equal(f.writes.length,0);await f.click('complete');assert.equal(f.saved.reminder.state,'completed');assert.match(f.dialog.textContent,/tatsächliche Zahlung belegt/);
});

test('server confirmation capability blocks reversed sources without pretending the source was not reviewed',async()=>{
 const f=harness({...snapshot(),can_confirm:false,needs_reconfirmation:true,reminder:{...reminder(),state:'confirmed'}});await f.open();
 assert.equal(f.action('confirm').disabled,true);assert.equal(f.find(n=>n.dataset.reminderSourceReview),undefined,'The shown entry version is current');
 await f.click('confirm');assert.equal(f.writes.length,0,'Impossible confirmation is blocked before any POST');assert.equal(f.action('complete').disabled,false,'Explicit reminder closure remains independent');
 await f.click('complete');assert.equal(f.saved.reminder.state,'completed');
});
test('reader sees named responsibility without mutation controls or hidden member options',async()=>{
 const f=harness({...snapshot(),can_edit:false,members:[],reminder:reminder()});await f.open();assert.match(f.dialog.textContent,/Taylor/);assert.equal(f.find(n=>n.tag==='form'),undefined);assert.equal(f.writes.length,0);
});
test('uncertain write freezes edits and explicit retry preserves exact request identity and body',async()=>{
 const f=harness({...snapshot(),can_confirm:true,reminder:reminder()});await f.open();f.setMode('offline');await f.click('confirm');assert.equal(f.action('save').disabled,true);assert.equal(f.find(n=>n.tag==='fieldset').disabled,true);assert.equal(f.writes.length,1);
 f.setMode('');await f.find(n=>n.dataset.reminderRetry).emit('click');assert.equal(f.writes.length,2);assert.deepEqual(f.writes[0],f.writes[1]);assert.equal(f.saved.reminder.state,'confirmed');
});
test('version conflict retains draft and requires explicit current read without replay',async()=>{
 const f=harness({...snapshot(),can_confirm:true,reminder:reminder()});await f.open();f.find(n=>n.name==='due_on').value='2026-12-12';f.setMode('conflict');await f.save();assert.equal(f.find(n=>n.name==='due_on').value,'2026-12-12');assert.equal(f.action('save').disabled,true);assert.match(f.dialog.textContent,/inzwischen geändert/);
 f.setMode('');await f.reload();assert.equal(f.find(n=>n.name==='due_on').value,'2026-12-10');assert.equal(f.writes.length,1);assert.equal(f.action('save').disabled,false);
});
test('known validation failure preserves editable draft and never schedules automatic retry',async()=>{
 const f=harness({...snapshot(),can_confirm:true,reminder:reminder()});await f.open();f.find(n=>n.name==='due_on').value='2026-12-12';f.setMode('invalid');await f.save();assert.equal(f.find(n=>n.name==='due_on').value,'2026-12-12');assert.equal(f.action('save').disabled,false);assert.equal(f.find(n=>n.dataset.reminderRetry),undefined);assert.equal(f.writes.length,1);
});
test('native dismissal returns focus and pagehide removes private form data',async()=>{
 const f=harness({...snapshot(),can_confirm:true,reminder:reminder()});await f.open();assert.equal(f.dialog.open,true);await f.dialog.emit('cancel');assert.equal(f.dialog.open,false);assert.equal(f.document.activeElement,f.opener);assert.doesNotMatch(f.dialog.textContent,/Taylor/);
 await f.open();f.events.pagehide();assert.equal(f.dialog.open,false);assert.equal(f.find(n=>n.name==='due_on'),undefined);
});
test('bounded member list discloses additional candidates and preserves named current assignment',async()=>{
 const f=harness({...snapshot(),members:[],members_more:true,reminder:reminder()});await f.open();assert.match(f.dialog.textContent,/50 berechtigte Mitglieder/);assert.equal(f.find(n=>n.name==='responsible_member_id').value,member);assert.equal(f.find(n=>n.tag==='option'&&n.value===member).textContent,'Taylor');
});
