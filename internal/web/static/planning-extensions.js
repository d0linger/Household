(() => {
 'use strict';
 const ruleLabels={stock:'Freier Vorrat',urgency:'Passende Datumsgründe',effort:'Aufwand',cost:'Bekannte Kosten',repetition:'Abwechslung'};
 const slotLabels={breakfast:'Frühstück',lunch:'Mittagessen',dinner:'Abendessen',snack:'Zwischenmahlzeit'};
 function decimal(raw){if(typeof raw!=='string')return null;const text=raw.trim().replace(',','.');if(!/^\d{1,12}(?:\.\d{1,6})?$/.test(text))return null;const[a,b='']=text.split('.');return BigInt(a)*1000000n+BigInt(b.padEnd(6,'0'));}
 function portions(raw){const n=decimal(raw);if(n===null||n<=0n)throw Error('Eine positive Portionsmenge mit höchstens sechs Nachkommastellen eingeben.');return(n/1000000n).toString()+'.'+(n%1000000n).toString().padStart(6,'0');}
 function majorToMinor(raw){if(typeof raw!=='string')throw Error('Betrag als Text eingeben.');const text=raw.trim().replace(',','.');if(text==='')return null;if(!/^\d{1,11}(?:\.\d{1,2})?$/.test(text))throw Error('EUR-Betrag mit höchstens zwei Nachkommastellen eingeben.');const[a,b='']=text.split('.'),n=BigInt(a)*100n+BigInt(b.padEnd(2,'0'));if(n>1000000000000n)throw Error('Betrag ist größer als die zulässige Grenze.');return n.toString();}
 function minorInput(raw){if(raw===null||raw===undefined)return'';if(typeof raw!=='string'||!/^\d{1,16}$/.test(raw))return'';const n=BigInt(raw);return(n/100n).toString()+','+(n%100n).toString().padStart(2,'0');}
 function money(raw){const text=minorInput(raw);return text===''?'Unbekannt':text+' €';}
 function quantity(raw){if(typeof raw!=='string'||decimal(raw)===null)return'Unbekannt';return raw.replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');}
 function version(raw){if(!Number.isSafeInteger(raw)||raw<0)throw Error('Der gespeicherte Stand ist ungültig. Neu laden.');return raw;}
 const uuid=raw=>typeof raw==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(raw);
 const same=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
 function rules(fields){const result={};for(const key of Object.keys(ruleLabels)){const value=fields.querySelector('[data-rule="'+key+'"]').value;if(!/^\d{1,3}$/.test(value)||Number(value)>100)throw Error('Alle fünf Gewichtungen als ganze Werte von 0 bis 100 angeben.');result[key]=Number(value);}return result;}
 function el(tag,text,className){const node=document.createElement(tag);if(text!==undefined&&text!==null)node.textContent=text;if(className)node.className=className;return node;}
 function button(text,handler){const node=el('button',text);node.type='button';node.addEventListener('click',handler);return node;}
 function option(select,value,label){const node=el('option',label);node.value=value;select.append(node);return node;}
 function ruleInputs(root,values={}){root.replaceChildren();for(const[key,label]of Object.entries(ruleLabels)){const wrapper=el('label',label),input=el('input');input.type='number';input.min='0';input.max='100';input.step='1';input.required=true;input.dataset.rule=key;input.value=String(values[key]??50);wrapper.append(input);root.append(wrapper);}}
 const helpers={decimal,portions,majorToMinor,minorInput,money,quantity,version,uuid,same,rules,ruleLabels,slotLabels,el,button,option,ruleInputs};
 if(typeof module!=='undefined'&&module.exports){module.exports=helpers;return;}
 function create(root,host){
  const scope={week:null,epoch:0},controllers=[],note=root.querySelector('#planning-extension-notice'),retry=root.querySelector('#planning-extension-retry');let parentLocked=false,working=false,pending=null,sequence=0;
  function lock(){host.lock(working||!!pending);for(const c of controllers)c.lock(parentLocked||working||!!pending);retry.hidden=!pending;retry.disabled=working;}
  function notice(text){note.textContent=text;note.hidden=!text;}
  function current(){return scope.week;}
  function base(){const w=current();if(!w)throw Error('Die Woche zuerst vollständig laden.');return{generation:version(Number(root.dataset.generation)),plan_version:version(Number(w.plan.version)),week:w.days[0]};}
  async function request(path,options={}){
   const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),20000);let response;
   try{const url='/api/v1/planning'+(path.startsWith('/source/')?'':'/'+encodeURIComponent(root.dataset.id))+path;response=await fetch(url,{cache:'no-store',...options,signal:controller.signal});const text=await response.text();if(new TextEncoder().encode(text).length>16*1024*1024)throw Error('Antwort überschreitet die Anzeigegrenze.');let data;try{data=JSON.parse(text);}catch{throw Error('Die Serverantwort ist nicht lesbar.');}if(!response.ok){const error=Error(typeof data.error==='string'?data.error:'Anfrage abgelehnt.');error.status=response.status;throw error;}return data;}finally{clearTimeout(timer);}
  }
  async function read(path){return request(path);}
  async function send(requestState){
   working=true;lock();notice('Änderung wird geprüft …');
   try{const result=await request(requestState.path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':requestState.key},body:requestState.body});pending=null;notice('Änderung bestätigt.');try{requestState.onSuccess?.(result);}catch{notice('Änderung bestätigt, aber die Detailanzeige konnte nicht aktualisiert werden. Gespeicherten Stand prüfen; keine zweite Übernahme starten.');}try{await host.refresh();}catch{notice('Änderung bestätigt. Aktuelle Woche konnte nicht geladen werden; gespeicherten Plan erneut laden.');}return result;}
   catch(error){if(!error.status||error.status>=500){pending=requestState;notice('Ausgang der Anfrage unklar. Alle Änderungen bleiben gesperrt. Nur diese Anfrage mit derselben Kennung erneut prüfen; keine neue Übernahme starten.');}else{pending=null;notice(error.status===409?'Gespeicherter Stand hat sich geändert. Entwurf bleibt erhalten. Aktuellen Stand neu laden und ausdrücklich prüfen.':error.message);}throw error;}
   finally{working=false;lock();}
  }
  async function mutate(path,input,onSuccess){if(parentLocked||working||pending)throw Error('Zuerst den noch offenen Vorgang abschließen.');const state={path,body:JSON.stringify(input),key:crypto.randomUUID(),onSuccess};return send(state);}
  async function review(path,input){if(parentLocked||working||pending)throw Error('Zuerst den offenen Vorgang abschließen.');return request(path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},body:JSON.stringify(input)});}
  const api={...helpers,current,base,read,review,mutate,refresh:()=>host.refresh(),ask:host.ask,writable:root.dataset.writable==='true',get locked(){return parentLocked||working||!!pending;},stamp:()=>scope.epoch,notice};
  for(const name of ['Leftovers','Budget','Calendar','Autoplan']){const controller=window['HouseholdPlanning'+name]?.create(root,api);if(controller)controllers.push(controller);}
  retry.addEventListener('click',async()=>{if(!pending||working)return;try{await send(pending);}catch{notice(pending?'Die gleiche Anfrage bleibt ungeklärt. Später erneut mit derselben Kennung prüfen.':'Die Anfrage wurde abgelehnt. Gespeicherten Stand und Entwurf prüfen.');}});
  return{
   onWeek(week){scope.week=week;scope.epoch=++sequence;for(const c of controllers)c.update(week,scope.epoch);lock();},
   setLocked(value){parentLocked=value;lock();},
   hasWork(){return!!pending||working||controllers.some(c=>c.dirty());},
   isPending(){return!!pending||working;},
   async canChangeWeek(){if(pending||working){notice('Die ungeklärte Anfrage zuerst mit derselben Kennung prüfen.');return false;}if(!controllers.some(c=>c.dirty()))return true;if(!await host.ask('Wochenbezogene Entwürfe verwerfen?','Nicht gespeicherte Reste-, Budget- und Vorschlagsangaben gehören zu dieser Woche. Abbrechen erhält sie vollständig.'))return false;for(const c of controllers)c.discard();return true;}
  };
 }
 window.HouseholdPlanningExtensions={...helpers,create};
})();
