const _householdText_pantry_extension_ui_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root)=>{
 'use strict';
 const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
 const link=(text,url)=>{const a=node('a',text);a.href=url;return a;};
 async function raw(url,{method='GET',body,key,timeout=15000}={}){if(!url.startsWith('/api/v1/')||url.startsWith('//'))throw Error(_householdText_pantry_extension_ui_js("Nicht unterstützte Anfrage."));const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),timeout);try{const response=await fetch(url,{method,signal:controller.signal,cache:'no-store',headers:{Accept:'application/json',...(body!==undefined?{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]')?.content||'','Idempotency-Key':key||''}:{})},body:body===undefined?undefined:typeof body==='string'?body:JSON.stringify(body)});let data;try{data=await response.json();}catch{const e=Error(_householdText_pantry_extension_ui_js("Serverantwort konnte nicht gelesen werden. Ergebnis gegebenenfalls unter derselben Kennung prüfen."));e.status=response.status;throw e;}if(!response.ok){const e=Error(data.error||_householdText_pantry_extension_ui_js("Anfrage konnte nicht abgeschlossen werden."));e.status=response.status;e.current=data.current;e.retryAfter=response.headers.get(_householdText_pantry_extension_ui_js("Retry-After"));throw e;}return data;}finally{clearTimeout(timer);}}
 const sameAuthority=(previous,next)=>!!previous&&!!next&&previous.auth_scope===next.auth_scope&&previous.generation===next.generation;
 function client(panel,{onLock=()=>{},status=()=>{},read=raw,pageDocument=root.document,events=root,dispatchLock=()=>root.dispatchEvent(new CustomEvent('household-pantry-lock',{detail:{reason:'visibility'}}))}={}){let current=null,revision=0,confirmed=false,changing=false;
  function hide(){confirmed=false;++revision;for(const n of panel.querySelectorAll('[data-private]'))n.hidden=true;dispatchLock();}
  async function context(){
   const ticket=revision;let result;
   try{result=await read('/api/v1/pantry/extensions/context');}
   catch(error){if(error.status===401||error.status===403){changing=true;hide();onLock();}throw error;}
   if(ticket!==revision)throw Error(_householdText_pantry_extension_ui_js("Ansicht hat sich geändert. Aktuellen Kontext erneut laden."));
   const value=result.context;
   if(!value?.auth_scope||!value.user_id||!value.household_id||!Number.isSafeInteger(value.generation)||value.generation<1||!value.server_time)throw Error(_householdText_pantry_extension_ui_js("Aktueller Vorratskontext ist unvollständig."));
   if(current&&!sameAuthority(current,value)){changing=true;hide();onLock();status(_householdText_pantry_extension_ui_js("Anmeldung, Berechtigung oder Vorratsgeneration wurde geändert. Oberfläche mit aktuellem Konto neu öffnen."));throw Error(_householdText_pantry_extension_ui_js("Kontokontext geändert. Bisherige Formulare wurden verborgen."));}
   current=value;confirmed=true;return value;
  }
  function assert(){if(!confirmed||changing||!current)throw Error(_householdText_pantry_extension_ui_js("Aktuelle Anmeldung zuerst erneut prüfen."));}
  async function request(url,options){assert();const ticket=revision,scope=current;const data=await read(url,options);if(ticket!==revision||!sameAuthority(scope,current)||!confirmed)throw Error(_householdText_pantry_extension_ui_js("Verspätete Antwort gehört nicht mehr zur bestätigten Ansicht. Unter derselben Kennung erneut prüfen."));return data;}
  async function focus(){hide();if(pageDocument.hidden||changing)return;try{await context();for(const n of panel.querySelectorAll('[data-private][data-open="true"]'))n.hidden=false;status(_householdText_pantry_extension_ui_js("Aktuelle Anmeldung erneut geprüft. Entwürfe und Vorschläge müssen vor dem Buchen aktuell geprüft werden."));}catch(error){status(error.message);}}
  pageDocument.addEventListener('visibilitychange',()=>void focus());events.addEventListener('focus',()=>{if(!pageDocument.hidden)void focus();});events.addEventListener('pageshow',event=>{if(event.persisted)void focus();});
  events.addEventListener('household-pantry-device-change',event=>{if(event.detail?.kind==='reset'){hide();onLock();}});
  return {context,request,assert,revalidate:focus,get current(){return current;},get revision(){return revision;},open:element=>{element.dataset.open='true';element.hidden=false;},close:element=>{element.dataset.open='false';element.hidden=true;},hide};
 }
 function picker(parent,label,source,{onSelect=()=>{}}={}){const box=node('div',undefined,'pe-picker'),searchLabel=node('label',label+_householdText_pantry_extension_ui_js(" suchen")),input=node('input');input.type='search';input.maxLength=300;searchLabel.append(input);const search=node('button',_householdText_pantry_extension_ui_js("Suchen")),more=node('button',_householdText_pantry_extension_ui_js("Weitere Treffer")),selectedLabel=node('label',label),select=node('select'),notice=node('p',_householdText_pantry_extension_ui_js("Noch keine aktuelle Auswahl."),'fine');search.type=more.type='button';more.hidden=true;select.append(new Option(_householdText_pantry_extension_ui_js("Bitte ausdrücklich wählen"),''));selectedLabel.append(select);box.append(searchLabel,search,selectedLabel,more,notice);parent.append(box);let cursor='',items=new Map(),ready=false,sequence=0,query='',busy=false;
  function lock(value){busy=value;search.disabled=more.disabled=select.disabled=value;}
  async function load(append=false){const ticket=++sequence;ready=false;lock(true);try{if(!append){query=input.value.trim();cursor='';items.clear();select.replaceChildren(new Option(_householdText_pantry_extension_ui_js("Bitte ausdrücklich wählen"),''));onSelect(null);}const data=await raw('/api/v1/core/reference-options/'+source+'?'+new URLSearchParams({q:query,cursor:append?cursor:''}));if(ticket!==sequence)return;for(const item of data.records||[]){if(!items.has(item.id))select.append(new Option(item.label,item.id));items.set(item.id,item);}cursor=data.next_cursor||'';more.hidden=!cursor;ready=true;notice.textContent=items.size?_householdText_pantry_extension_ui_js("Aktuelle Auswahl geladen. Eintrag ausdrücklich auswählen."):_householdText_pantry_extension_ui_js("Keine passenden aktuellen Einträge. Suchbegriff ändern.");}catch(error){if(ticket===sequence)notice.textContent=error.message+_householdText_pantry_extension_ui_js(" Auswahl erneut laden.");}finally{if(ticket===sequence)lock(false);}}
  search.addEventListener('click',()=>void load());more.addEventListener('click',()=>void load(true));input.addEventListener('keydown',event=>{if(event.key==="Enter"){event.preventDefault();void load();}});select.addEventListener('change',()=>{if(ready)onSelect(items.get(select.value)||null);});
  return {load,clear(){++sequence;ready=false;items.clear();input.value='';select.replaceChildren(new Option(_householdText_pantry_extension_ui_js("Bitte ausdrücklich wählen"),''));more.hidden=true;notice.textContent=_householdText_pantry_extension_ui_js("Auswahl erneut laden.");},value(){if(!ready||busy||!items.has(select.value))throw Error(label+_householdText_pantry_extension_ui_js(" aktuell laden und ausdrücklich auswählen."));return items.get(select.value);},get element(){return box;}};
 }
 function operation({kind,base,client,status,onReceipt,onRefused,onReview,onUncertain,storage,random=()=>crypto.randomUUID(),now=Date.now}){
  let pending=null,terminal=false;
  if(storage===undefined){try{storage=root.sessionStorage;}catch{storage=null;}}
  const storageKey='household-pantry-pending:'+kind;
  function preserve(){
   if(!pending)return;
   try{storage.setItem(storageKey,JSON.stringify({key:pending.key,generation:pending.generation,user:client.current.user_id,household:client.current.household_id}));}catch{}
  }
  function clear(){try{storage.removeItem(storageKey);}catch{}}
  function receipt(value){onReceipt(value);pending=null;terminal=true;clear();}
  async function restore(){
   let saved;try{saved=JSON.parse(storage.getItem(storageKey)||'null');}catch{}
   if(saved&&/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(saved.key)&&Number.isSafeInteger(saved.generation)&&saved.generation>0&&saved.user===client.current.user_id&&saved.household===client.current.household_id){
    pending={key:saved.key,generation:saved.generation,review:null,expires:0,attempted:true};onUncertain();
    status(saved.generation===client.current.generation?_householdText_pantry_extension_ui_js("Frühere Vorgangskennung geladen. Gespeicherten Ausgang ausdrücklich prüfen."):_householdText_pantry_extension_ui_js("Frühere Vorgangskennung gehört zu einer anderen Vorratsgeneration und wird nicht neu ausgeführt. Einen neuen unabhängigen Vorgang ausdrücklich beginnen."));return true;
   }
   if(saved)clear();return false;
  }
  async function review(input){
   if(pending?.attempted)throw Error(_householdText_pantry_extension_ui_js("Ungewissen Vorgang zuerst unter derselben Kennung klären oder verbindlich abbrechen."));
   if(terminal)throw Error(_householdText_pantry_extension_ui_js("Zuerst ausdrücklich einen neuen unabhängigen Vorgang beginnen."));
   pending=pending||{key:random(),generation:input.generation,attempted:false};
   const result=await client.request(base+'/preview',{method:'POST',body:input,key:pending.key,timeout:25000});
   if(!result.review||!result.expires)throw Error(_householdText_pantry_extension_ui_js("Signierte Vorschau fehlt. Aktuell neu prüfen."));
   pending.review=result.review;pending.expires=result.expires;onReview(result);return result;
  }
  async function execute(){
   if(!pending?.review)throw Error(_householdText_pantry_extension_ui_js("Zuerst aktuell prüfen."));
   if(now()>=pending.expires*1000)throw Error(_householdText_pantry_extension_ui_js("Prüfvorschlag abgelaufen. Vor einer ersten Ausführung neu prüfen."));
   pending.attempted=true;preserve();onUncertain();
   try{receipt(await client.request(base+'/execute',{method:'POST',body:{review:pending.review},key:pending.key,timeout:25000}));}
   catch(error){status(_householdText_pantry_extension_ui_js("Ergebnis ungewiss. Kennung bleibt erhalten; Status prüfen, nicht erneut anlegen."));throw error;}
  }
  async function check(abandon=false){
   if(!pending)throw Error(_householdText_pantry_extension_ui_js("Keine ungewisse Vorgangskennung."));
   const result=await client.request(base+'/operations/'+pending.key+(abandon?'/abandon':'?generation='+pending.generation),abandon?{method:'POST',body:{generation:pending.generation},key:pending.key,timeout:25000}:undefined);
   if(result.state==='complete'){receipt(result.receipt||result.result);return;}
   if(result.state==='refused'){pending=null;terminal=true;clear();onRefused(result);return;}
   onUncertain();status(result.message||_householdText_pantry_extension_ui_js("Vorgang noch nicht eindeutig geklärt. Kennung und Eingaben bleiben gesperrt."));
  }
  return {review,execute,status:()=>check(),abandon:()=>check(true),restore,get blocked(){return !!pending?.attempted;},get retired(){return !!pending&&pending.generation!==client.current?.generation;},get terminal(){return terminal;},edit(){if(pending?.attempted)throw Error(_householdText_pantry_extension_ui_js("Ungewissen Vorgang zuerst klären."));if(pending)pending.review=null;},fresh(){if(pending?.attempted&&pending.generation===client.current?.generation)throw Error(_householdText_pantry_extension_ui_js("Vorgang zuerst klären."));pending=null;terminal=false;clear();}};
 }
 if(typeof module==='object'&&module.exports){module.exports={operation,client,sameAuthority};return;}
 if(root.document)root.HouseholdPantryUI={node,link,raw,client,picker,operation};
})(globalThis);
