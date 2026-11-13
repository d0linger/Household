'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {returnPane,reviewHref,sameDocumentAnchor}=require('../internal/web/static/documents-workspace.js');
test('Document return destinations are a closed page-only set',()=>{
 for(const pane of ['inbox','library','deadlines','processing','duplicates'])assert.equal(returnPane(pane),pane);
 for(const pane of ['https://other.invalid','../settings','rules','integrations','review','',null,undefined])assert.equal(returnPane(pane),'library');
});
test('Document review links retain only explicit record and compiled origin',()=>{
 const id='00000000-0000-4000-8000-000000000001',url=new URL(reviewHref(id,'deadlines'),'https://household.invalid');
 assert.equal(url.pathname,'/m/documents/workspace/review');assert.deepEqual([...url.searchParams],[['from','deadlines'],['id',id]]);
 assert.equal(new URL(reviewHref('','inbox'),'https://household.invalid').searchParams.has('id'),false);
 assert.equal(new URL(reviewHref(id,'//other.invalid'),'https://household.invalid').searchParams.get('from'),'library');
});
test('Only anchors within the exact current document can bypass discard navigation',()=>{
 const current='https://household.invalid/m/documents/workspace/review?id=one&from=inbox';
 assert.equal(sameDocumentAnchor('#doc-tool-details',current),true);
 for(const href of ['https://other.invalid/m/documents/workspace/review?id=one&from=inbox#doc-tool-details','?id=two#doc-tool-details','/settings#doc-tool-details','/m/documents/manage',current,'javascript:alert(1)'])assert.equal(sameDocumentAnchor(href,current),false);
});
test('Actual document click guard preserves both drafts during keyboard tools and cancelled navigation',()=>{
 const source=fs.readFileSync(path.join(__dirname,'../internal/web/static/documents-workspace.js'),'utf8'),line=source.split('\n').find(line=>line.includes("root.addEventListener('click',"));
 let handler,confirmations=0,focused=0,scrolled=0,prevented=0;const current='https://household.invalid/m/documents/workspace/review?id=one&from=inbox';
 const S={dirty:true,notificationDirty:true},environment={S,URL,location:{href:current},sameDocumentAnchor,root:{addEventListener:(type,callback)=>{handler=callback;}},document:{getElementById:id=>id==='doc-tool-details'?{focus(){focused++;},scrollIntoView(){scrolled++;}}:null},confirm:()=>{confirmations++;return false;},_householdText_documents_workspace_js:text=>text};
 vm.runInNewContext(line,environment);
 handler({target:{closest:()=>({href:current+'#doc-tool-details'})},preventDefault(){prevented++;}});
 assert.equal(focused,1);assert.equal(scrolled,1);assert.equal(confirmations,0);assert.equal(prevented,1);assert.equal(S.dirty,true);assert.equal(S.notificationDirty,true);
 handler({target:{closest:()=>({href:'https://household.invalid/m/documents/manage'})},preventDefault(){prevented++;}});
 assert.equal(confirmations,1);assert.equal(prevented,2);assert.equal(S.dirty,true);assert.equal(S.notificationDirty,true);
});
