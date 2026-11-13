((root) => {
 'use strict';
 const tr=(source,...args)=>root.HouseholdI18n?root.HouseholdI18n.t(source,...args):source.replace(/%s/g,()=>String(args.shift()??''));
 const uuid=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
 const object=value=>value!==null&&typeof value==='object'&&!Array.isArray(value);
 const positive=value=>Number.isSafeInteger(value)&&value>0;
 const date=value=>typeof value==='string'&&/^\d{4}-\d{2}-\d{2}$/.test(value)&&Number.isFinite(Date.parse(value+'T00:00:00Z'))&&new Date(value+'T00:00:00Z').toISOString().slice(0,10)===value;
 function projection(value,id){
  if(!object(value)||!uuid.test(id)||value.entry_id!==id||!positive(value.entry_version)||!positive(value.generation)||
   !/^[0-9a-f]{64}$/.test(value.access_version)||typeof value.verified_paid!=='boolean'||typeof value.needs_reconfirmation!=='boolean'||
   typeof value.can_edit!=='boolean'||typeof value.can_confirm!=='boolean'||typeof value.members_more!=='boolean'||!Array.isArray(value.members)||value.members.length>50)throw Error(tr('Die Zahlungserinnerung konnte nicht sicher gelesen werden. Bitte erneut laden.'));
  const seen=new Set();
  for(const member of value.members){if(!object(member)||!uuid.test(member.id)||seen.has(member.id)||typeof member.label!=='string'||!member.label||member.label.length>256)throw Error(tr('Die Zahlungserinnerung konnte nicht sicher gelesen werden. Bitte erneut laden.'));seen.add(member.id);}
  const reminder=value.reminder;
  if(reminder!==null&&(!object(reminder)||!positive(reminder.version)||!date(reminder.due_on)||!uuid.test(reminder.responsible_member_id)||
   typeof reminder.responsible_name!=='string'||!['draft','confirmed','completed','cancelled'].includes(reminder.state)))throw Error(tr('Die Zahlungserinnerung konnte nicht sicher gelesen werden. Bitte erneut laden.'));
  return value;
 }
 function create({document,request,current}){
  const el=(tag,text,className)=>{const item=document.createElement(tag);if(text!==undefined)item.textContent=text;if(className)item.className=className;return item;};
  const dialog=el('dialog',undefined,'finance-reminder-dialog');dialog.id='finance-reminder-dialog';dialog.setAttribute('aria-labelledby','finance-reminder-title');
  const header=el('div',undefined,'finance-section-heading'),title=el('h2',tr('Zahlungserinnerung')),close=el('button',tr('Schließen'));title.id='finance-reminder-title';close.type='button';header.append(title,close);
  const intro=el('p',tr('Eine Erinnerung gehört zu dieser Buchung. Sie bucht und überweist kein Geld.'),'fine'),body=el('div'),status=el('p'),footer=el('div',undefined,'actions');status.setAttribute('role','status');status.setAttribute('aria-live','polite');
  const reload=el('button',tr('Aktuellen Stand laden')),link=el('a',tr('Finanzhinweise auf Geräten verwalten'));reload.type='button';link.href='/settings/notifications';footer.append(reload,link);
  dialog.append(header,intro,body,status,footer);document.body.append(dialog);
  let entry='',view=null,busy=false,turn=0,opener=null,form=null,due=null,person=null,pending=null,conflicted=false,shownVersion=0;
  const endpoint=()=>'/api/v1/expenses/finance/entries/'+entry+'/payment-reminder';
  const message=(text,error=false)=>{status.textContent=text;status.dataset.error=String(error);};
  const changed=()=>!!view?.reminder&&(due.value!==view.reminder.due_on||person.value!==view.reminder.responsible_member_id);
  function locks(){
   dialog.setAttribute('aria-busy',String(busy));close.disabled=busy;reload.disabled=busy;
   if(form){form.querySelector('fieldset').disabled=busy||!view.can_edit||!!pending||conflicted;
    due.disabled=person.disabled=view.verified_paid;
    for(const action of form.querySelectorAll('[data-reminder-action]')){
     const kind=action.dataset.reminderAction;
     action.disabled=busy||!view.can_edit||conflicted||view.verified_paid&&['save','confirm'].includes(kind)||!!pending||kind!=='save'&&(changed()||!view.reminder)||
      kind==='confirm'&&view.reminder?.state==='confirmed'&&!view.needs_reconfirmation||
      kind==='confirm'&&(!view.can_confirm||view.entry_version!==shownVersion)||
      kind==='confirm'&&['completed','cancelled'].includes(view.reminder?.state)||kind==='complete'&&view.reminder?.state!=='confirmed'||
      ['complete','cancel'].includes(kind)&&['completed','cancelled'].includes(view.reminder?.state);
    }
   }
  }
  function clear(){turn++;pending=null;view=null;entry='';busy=false;conflicted=false;shownVersion=0;form=null;due=null;person=null;body.replaceChildren();message('');if(dialog.open)dialog.close();}
  function render(){
   body.replaceChildren();form=null;due=null;person=null;
   const r=view.reminder,states={draft:tr('Entwurf · noch nicht bestätigt'),confirmed:tr('Fälligkeit bestätigt'),completed:tr('Erinnerung erledigt · keine Zahlungsbestätigung'),cancelled:tr('Erinnerung aufgehoben')};
   body.append(el('p',view.verified_paid?tr('Passende tatsächliche Zahlung belegt. Für diese Buchung werden keine Zahlungserinnerungen mehr versendet.'):r?states[r.state]:tr('Für diese Buchung ist noch keine Zahlungserinnerung gespeichert.')));
   if(view.needs_reconfirmation)body.append(el('p',tr('Die Buchung wurde geändert. Prüfe den aktuellen Originalstand und bestätige die Fälligkeit erneut.'),'notice'));
   if(view.entry_version!==shownVersion){
    const review=el('a',tr('Aktuellen Buchungsstand prüfen'));review.href='/m/expenses/workspace/entries?id='+entry;review.dataset.reminderSourceReview='true';
    body.append(el('p',tr('Die angezeigte Buchung ist nicht mehr aktuell. Öffne den aktuellen Buchungsstand, prüfe ihn und öffne danach die Erinnerung erneut. Erst dann kannst du die Fälligkeit bestätigen.'),'notice'),review);
   }
   if(r){const facts=el('dl',undefined,'finance-kv');facts.append(el('dt',tr('Fällig am')),el('dd',r.due_on),el('dt',tr('Zuständig')),el('dd',r.responsible_name||tr('Zuständigkeit aktuell prüfen')));body.append(facts);}
   if(!view.can_edit){body.append(el('p',tr('Du kannst diese Erinnerung ansehen. Änderungen benötigen die aktuelle Bearbeitungsberechtigung der Buchung.'),'fine'));locks();return;}
   form=el('form');const fields=el('fieldset'),legend=el('legend',tr('Fälligkeit und Zuständigkeit')),grid=el('div',undefined,'form-grid');fields.append(legend,grid);
   const dueLabel=el('label',tr('Fällig am'));due=el('input');due.type='date';due.name='due_on';due.required=true;due.value=r?.due_on||'';dueLabel.append(due);
   const personLabel=el('label',tr('Zuständiges Haushaltsmitglied'));person=el('select');person.name='responsible_member_id';person.required=true;
   const empty=el('option',tr('Person ausdrücklich auswählen'));empty.value='';person.append(empty);
   for(const member of view.members){const option=el('option',member.label);option.value=member.id;person.append(option);}
   if(r&&!view.members.some(member=>member.id===r.responsible_member_id)){const selected=el('option',r.responsible_name||tr('Zuständigkeit aktuell prüfen'));selected.value=r.responsible_member_id;selected.disabled=true;person.append(selected);}
   person.value=r?.responsible_member_id||'';personLabel.append(person);grid.append(dueLabel,personLabel);
   if(view.members_more)fields.append(el('p',tr('Die Personenauswahl ist auf 50 berechtigte Mitglieder begrenzt. Eine fehlende Person wird nicht automatisch ersetzt.'),'notice'));
   fields.append(el('p',tr('Speichere zuerst den Entwurf. Bestätige danach ausdrücklich die Fälligkeit. Eine Zuständigkeit allein aktiviert keine Gerätehinweise.'),'fine'));
   const actions=el('div',undefined,'actions');
   for(const[action,text]of[['save',tr('Erinnerungsentwurf speichern')],['confirm',tr('Fälligkeit ausdrücklich bestätigen')],['complete',tr('Erinnerung als erledigt markieren')],['cancel',tr('Erinnerung aufheben')]]){
    const button=el('button',text);button.type=action==='save'?'submit':'button';button.dataset.reminderAction=action;if(action==='confirm')button.className='primary';if(action!=='save')button.addEventListener('click',()=>submit(action));actions.append(button);
   }
   fields.append(actions,el('p',tr('„Erledigt“ beendet nur diese Erinnerung. Eine tatsächliche Zahlung wird ausschließlich aus passenden vorhandenen Zahlungsdaten erkannt.'),'fine'));form.append(fields);body.append(form);
   form.addEventListener('input',locks);form.addEventListener('change',locks);form.addEventListener('submit',event=>{event.preventDefault();void submit('save');});locks();
  }
  async function load(){
   if(busy||!entry)return;busy=true;locks();message(tr('Zahlungserinnerung wird geladen …'));const epoch=turn,id=entry;
   try{await current();if(epoch!==turn)return;const next=projection(await request(endpoint(),{privateObject:true}),id);if(epoch!==turn)return;view=next;pending=null;conflicted=false;render();message('');}
   catch(error){if(epoch!==turn)return;message(error.message,true);}
   finally{if(epoch===turn){busy=false;locks();}}
  }
  async function submit(action){
   if(busy||!view||!view.can_edit||view.verified_paid&&['save','confirm'].includes(action)||conflicted||!form||!form.reportValidity())return;
   if(action!=='save'&&(!view.reminder||changed()))return;
   if(action==='confirm'&&(!view.can_confirm||view.entry_version!==shownVersion))return;
   if(action==='complete'&&view.reminder.state!=='confirmed'||action==='confirm'&&['completed','cancelled'].includes(view.reminder.state)||action==='cancel'&&['completed','cancelled'].includes(view.reminder.state))return;
   const r=view.reminder;
   const command=pending||{action,version:r?.version||0,entry_version:view.entry_version,generation:view.generation,access_version:view.access_version,
    due_on:action==='save'?due.value:r.due_on,responsible_member_id:action==='save'?person.value:r.responsible_member_id,request_id:root.crypto.randomUUID()};
   if(!date(command.due_on)||!uuid.test(command.responsible_member_id)){message(tr('Wähle ein gültiges Fälligkeitsdatum und eine zuständige Person.'),true);return;}
   await send(command);
  }
  async function send(command){
   if(busy||!entry)return;busy=true;pending=command;locks();message(tr('Zahlungserinnerung wird gespeichert …'));const epoch=turn,id=entry;
   try{await current();if(epoch!==turn)return;const next=projection(await request(endpoint(),{method:'POST',body:command,privateObject:true}),id);if(epoch!==turn)return;view=next;pending=null;conflicted=false;render();message(tr('Zahlungserinnerung gespeichert.'));
   }catch(error){if(epoch!==turn)return;
    if(error.status===409){pending=null;conflicted=true;message(tr('Der Stand wurde inzwischen geändert. Lade die aktuelle Buchung und Erinnerung erneut; deine Eingaben werden nicht automatisch übernommen.'),true);}
    else if(error.status&&error.status<500){pending=null;message(error.message,true);}
    else {message(tr('Die Speicherung ist noch nicht bestätigt. Prüfe denselben Vorgang erneut oder lade den aktuellen Stand; es erfolgt keine automatische Wiederholung.'),true);const retry=el('button',tr('Denselben Erinnerungsvorgang erneut prüfen'));retry.type='button';retry.dataset.reminderRetry='true';retry.addEventListener('click',()=>{if(!busy&&pending)void send(pending);});body.querySelector('[data-reminder-retry]')?.remove();body.append(retry);}
   }finally{if(epoch===turn){busy=false;locks();}}
  }
  async function open(id,trigger,entryVersion){if(busy||!uuid.test(id)||!positive(entryVersion))return;clear();entry=id;shownVersion=entryVersion;opener=trigger;dialog.showModal();close.focus();await load();}
  close.addEventListener('click',()=>{if(!busy)clear();});dialog.addEventListener('cancel',event=>{event.preventDefault();if(!busy)clear();});dialog.addEventListener('close',()=>{if(opener?.isConnected)opener.focus();opener=null;});
  dialog.addEventListener('click',event=>{if(event.target!==dialog||busy)return;const rect=dialog.getBoundingClientRect();if(event.clientX<rect.left||event.clientX>rect.right||event.clientY<rect.top||event.clientY>rect.bottom)clear();});
  reload.addEventListener('click',()=>{if(!busy)void load();});root.addEventListener?.('pagehide',clear);
  return {open,clear};
 }
 const api={projection,create};if(typeof module==='object'&&module.exports)module.exports=api;else root.HouseholdFinanceReminders=api;
})(globalThis);
