'use strict';
const _householdText_planning_notifications_js=(source,...args)=>{const i18n=globalThis.HouseholdI18n;if(i18n)return i18n.t(source,...args);let index=0;return source.replace(/%s/g,()=>String(args[index++]??''));};
(function(){
 const tr=_householdText_planning_notifications_js;
 function create(root,{writable,ask,lock,sourceChanged}){
  const host=root.querySelector('#planning-notifications');if(!host)return null;
  let meal='',renderedMealVersion=0,basis=null,dirty=false,pending=null,busy=false,locked=false,epoch=0;
  const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text)n.textContent=text;if(cls)n.className=cls;return n;};
  const heading=node('h3',tr('Zubereitungshinweise')),status=node('p','', 'fine'),error=node('p','', 'notice error'),open=node('button',tr('Zubereitungshinweise einrichten'));
  heading.tabIndex=-1;error.hidden=true;error.tabIndex=-1;error.setAttribute('role','alert');status.setAttribute('role','status');open.type='button';
  const form=node('form'),fields=node('fieldset'),actions=node('div',null,'actions'),save=node('button',tr('Termine ausdrücklich bestätigen'),'primary'),discard=node('button',tr('Eingaben verwerfen')),retry=node('button',tr('Unklare Speicherung erneut prüfen'));
  discard.type=retry.type='button';retry.hidden=true;form.hidden=true;actions.append(save,discard,retry);form.append(fields,actions);host.append(heading,status,open,form,error);
  function paint(){host.setAttribute('aria-busy',String(busy));open.disabled=busy||locked;fields.disabled=busy||locked||!writable||!!pending||!basis?.eligible;save.disabled=busy||locked||!writable||!!pending||!basis?.eligible;discard.disabled=busy||!!pending;retry.disabled=busy||locked;retry.hidden=!pending;open.hidden=!form.hidden;}
  function fail(message){error.textContent=message;error.hidden=false;error.focus();}
  function setBusy(value){busy=value;lock(value||!!pending);paint();}
  async function request(method,body,key){const response=await fetch('/api/v1/planning/'+root.dataset.id+'/meals/'+meal+'/notifications',{method,cache:'no-store',signal:AbortSignal.timeout(20000),headers:method==='POST'?{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':key}:{Accept:'application/json'},...(body?{body}:{})});let data;try{data=await response.json();}catch{throw Error(tr('Antwort unklar. Die Anfrage mit derselben Kennung erneut prüfen.'));}if(!response.ok){const e=Error(data.error||tr('Hinweise konnten nicht geladen oder gespeichert werden.'));e.status=response.status;throw e;}return data;}
  function field(label,input){const wrapper=node('label',label);wrapper.append(input);return wrapper;}
  function select(options,value){const input=node('select');for(const [id,label]of options){const option=node('option',label);option.value=id;input.append(option);}input.value=value||'';return input;}
  function addStep(step={id:crypto.randomUUID(),kind:'step',label:'',local_date:'',local_time:''}){
   const list=fields.querySelector('[data-reminder-steps]');if(list.children.length>=8)return;
   const row=node('fieldset');row.dataset.reminderStep=step.id;row.append(node('legend',tr('Bestätigter Termin')));
   const grid=node('div',null,'form-grid'),kind=select([['start',tr('Zubereitung beginnen')],['step',tr('Vorbereitungsschritt')]],step.kind),label=node('input'),date=node('input'),time=node('input');
   kind.name='kind';label.name='label';label.maxLength=120;label.value=step.label;label.required=true;date.type='date';date.name='local_date';date.required=true;date.value=step.local_date;time.type='time';time.name='local_time';time.step='60';time.required=true;time.value=step.local_time;
   grid.append(field(tr('Terminart'),kind),field(tr('Schritt benennen'),label),field(tr('Bestätigtes Datum'),date),field(tr('Bestätigte Uhrzeit'),time));row.append(grid);
   if(step.scheduled_at){const value=new Date(step.scheduled_at);if(Number.isFinite(value.getTime()))row.append(node('p',tr('Gespeicherter Zeitpunkt: %s',new Intl.DateTimeFormat(globalThis.HouseholdI18n?.locale()||'de-AT',{dateStyle:'medium',timeStyle:'short',timeZone:basis.timezone}).format(value)+' · '+basis.timezone),'fine'));}
   const remove=node('button',tr('Termin entfernen'));remove.type='button';remove.addEventListener('click',()=>{row.remove();dirty=true;fields.querySelector('[data-add-reminder]').disabled=false;fields.querySelector('[data-add-reminder]').focus();});row.append(remove);list.append(row);fields.querySelector('[data-add-reminder]').disabled=list.children.length>=8;
  }
  function render(data){
   if(!data||!data.settings||!Array.isArray(data.settings.steps)||data.settings.steps.length>8||!Array.isArray(data.members)||typeof data.timezone!=='string')throw Error(tr('Hinweise konnten nicht geladen oder gespeichert werden.'));
   if(!Number.isSafeInteger(renderedMealVersion)||renderedMealVersion<1||data.meal_version!==renderedMealVersion){sourceChanged?.();throw Error(tr('Die angezeigte Mahlzeit ist nicht mehr aktuell. Gespeicherten Plan laden, deinen Mahlzeitenentwurf vergleichen und die geprüfte Mahlzeit erneut öffnen. Deine Eingaben bleiben erhalten.'));}
   basis=data;fields.replaceChildren();fields.append(node('legend',tr('Verantwortung und Termine')));
   fields.append(node('p',tr('Termine gelten in der Haushaltszeitzone %s. Datum und Uhrzeit werden nicht aus Mahlzeit, Rezeptdauer oder Teilnehmern abgeleitet.',data.timezone),'fine'));
   fields.append(node('p',tr('Bei der Zeitumstellung gilt die erste Ausführung einer doppelt vorkommenden Uhrzeit. Nicht existierende Uhrzeiten werden abgelehnt.'),'fine'));
   const options=[['',tr('Zuständige Person auswählen')],...data.members.map(m=>[m.id,m.name+(m.username?' · '+m.username:'')])];
   if(data.settings.responsible_member_id&&!data.members.some(m=>m.id===data.settings.responsible_member_id))options.push([data.settings.responsible_member_id,tr('Bisherige Person nicht mehr verfügbar')]);
   const person=select(options,data.settings.responsible_member_id);person.name='responsible_member_id';fields.append(field(tr('Zuständig für diese Hinweise'),person));
   const enabled=node('input');enabled.type='checkbox';enabled.name='enabled';enabled.checked=data.settings.enabled;const enableLabel=field(tr('Diese bestätigten Termine melden'),enabled);enableLabel.className='check';fields.append(enableLabel);
   const list=node('div');list.dataset.reminderSteps='';const add=node('button',tr('Termin ergänzen'));add.type='button';add.dataset.addReminder='';add.addEventListener('click',()=>{addStep();dirty=true;list.lastElementChild?.querySelector('input')?.focus();});fields.append(list,add);
   for(const step of data.settings.steps)addStep(step);
   fields.append(node('p',tr('Höchstens acht Termine, davon ein Zubereitungsstart. Beim Aktivieren müssen alle Termine in der Zukunft liegen. Hinweise bleiben höchstens vier Stunden aktuell; vergangene Termine werden nicht nachgeholt.'),'fine'));
   const link=node('a',tr('Essensplanhinweise auf Geräten verwalten'));link.href='/settings/notifications';fields.append(node('p',tr('Die zuständige Person muss Essensplanhinweise im Konto und auf jedem gewünschten Gerät erlauben. Namen, Gerichte und Schritttexte bleiben vom Sperrbildschirm fern.'),'fine'),link);
   status.textContent=!data.eligible?tr('Hinweise benötigen eine gespeicherte, geplante oder eingekaufte Mahlzeit außerhalb einer Vorlage.'):data.needs_confirmation?tr('Die Mahlzeit wurde geändert. Zuständigkeit und zukünftige Termine erneut prüfen und ausdrücklich bestätigen.'):tr('Hinweise sind unabhängig von Teilnehmern und Mahlzeitenentwürfen.');
   dirty=false;form.hidden=false;error.hidden=true;paint();
  }
  async function load(){const token=epoch;let loaded=false;setBusy(true);status.textContent=tr('Zubereitungshinweise werden geladen …');try{const data=await request('GET');if(token!==epoch)return;render(data);loaded=true;}catch(e){if(token===epoch)fail(e.message);}finally{setBusy(false);if(loaded)fields.querySelector('select')?.focus();}}
  async function send(repeat=false){
   if(busy||locked||!writable||!basis?.eligible)return;if(!repeat){if(pending||!form.reportValidity())return;const steps=Array.from(fields.querySelectorAll('[data-reminder-step]'),row=>({id:row.dataset.reminderStep,kind:row.querySelector('[name=kind]').value,label:row.querySelector('[name=label]').value,local_date:row.querySelector('[name=local_date]').value,local_time:row.querySelector('[name=local_time]').value}));pending={key:crypto.randomUUID(),body:JSON.stringify({generation:basis.generation,plan_version:basis.plan_version,meal_version:basis.meal_version,settings_version:basis.settings.version,timezone:basis.timezone,responsible_member_id:fields.querySelector('[name=responsible_member_id]').value,enabled:fields.querySelector('[name=enabled]').checked,confirmed:true,steps})};}
   let saved=false;setBusy(true);error.hidden=true;
   try{await request('POST',pending.body,pending.key);pending=null;dirty=false;form.hidden=true;saved=true;status.textContent=tr('Zubereitungshinweise gespeichert. Der Mahlzeitenentwurf bleibt unverändert.');}
   catch(e){if(e.status&&e.status<500)pending=null;fail(e.message+(pending?' '+tr('Antwort unklar. Die Anfrage mit derselben Kennung erneut prüfen.'):' '+tr('Deine Eingaben bleiben erhalten. Zum Laden des aktuellen Stands die Eingaben ausdrücklich verwerfen.')));}
   finally{setBusy(false);if(saved)open.focus();}
  }
  form.addEventListener('input',()=>{dirty=true;});form.addEventListener('change',()=>{dirty=true;});form.addEventListener('submit',event=>{event.preventDefault();send();});open.addEventListener('click',load);retry.addEventListener('click',()=>send(true));
  discard.addEventListener('click',async()=>{if(busy||pending)return;if(dirty&&!await ask(tr('Hinweise verwerfen?'),tr('Nur die ungespeicherten Hinweise werden verworfen. Der Mahlzeitenentwurf bleibt erhalten.')))return;dirty=false;basis=null;form.hidden=true;error.hidden=true;paint();open.focus();});
  return {
   bind(id,version=0){epoch++;meal=id||'';renderedMealVersion=Number(version);basis=null;dirty=false;pending=null;form.hidden=true;error.hidden=true;host.hidden=!meal;status.textContent=tr('Hinweise sind unabhängig von Teilnehmern und Mahlzeitenentwürfen.');paint();},
   hasWork:()=>dirty||!!pending||busy,
   async canLeave(){if(busy||pending){fail(tr('Zuerst die unklare Speicherung mit derselben Kennung prüfen.'));return false;}return !dirty||await ask(tr('Hinweise verwerfen?'),tr('Nur die ungespeicherten Hinweise werden verworfen. Der Mahlzeitenentwurf bleibt erhalten.'));},
   setLocked(value){locked=value;paint();}
  };
 }
 window.HouseholdPlanningNotifications={create};
})();
