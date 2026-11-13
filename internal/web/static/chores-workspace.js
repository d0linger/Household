const _householdText_chores_workspace_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
/* Private, online-only Chores workspace. All assets and requests stay local. */
function choresWorkspaceHref(pane,area=''){
 if(!['today','areas','series','plan','history','settings'].includes(pane))throw Error('chores_workspace_invalid');
 if(area&&(!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(area)||area==='00000000-0000-0000-0000-000000000000'))throw Error('chores_area_invalid');
 return '/m/chores/workspace/'+pane+(area&&['today','history'].includes(pane)?'?'+new URLSearchParams({area}):'');
}
(() => {
 'use strict';
 const root=document.getElementById('chores-workspace');if(!root)return;
 const $=name=>document.getElementById('chores-'+name),api='/api/v1/chores',content=$('content'),filters=$('filters');
 const S={generation:Number(root.dataset.generation),canWrite:root.dataset.canWrite==='true',pane:root.dataset.pane,id:root.dataset.resource,
  csrf:'',identity:null,busy:false,denied:false,pending:null,epoch:0,next:'',query:{},view:null};
 let sequence=0;
 const node=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
 const button=(title,action,primary=false)=>{const e=node('button',title,primary?'primary':'');e.type='button';e.addEventListener('click',()=>run(action));return e;};
 const link=(title,href,cls='button')=>{const e=node('a',title,cls);e.href=href;return e;};
 const status=text=>{$('status').textContent=text;};
 const labels={once:_householdText_chores_workspace_js("Einmalig"),days:_householdText_chores_workspace_js("Tagesabstand"),weekly:_householdText_chores_workspace_js("Ausgewählte Wochentage"),monthly:_householdText_chores_workspace_js("Monatsabstand"),scheduled:_householdText_chores_workspace_js("Fester Kalenderplan"),completion:_householdText_chores_workspace_js("Nach tatsächlicher Erledigung"),
  open:_householdText_chores_workspace_js("Offen"),completed:_householdText_chores_workspace_js("Erledigt"),skipped:_householdText_chores_workspace_js("Ausgelassen"),fixed:_householdText_chores_workspace_js("Feste Person"),rotation:_householdText_chores_workspace_js("Rotation"),unassigned:_householdText_chores_workspace_js("Ohne Zuweisung")};
 const known=value=>value===null||value===undefined||value===''?_householdText_chores_workspace_js("Nicht bekannt"):String(value);
 const format=instant=>{if(!instant)return _householdText_chores_workspace_js("Nicht bekannt");const date=new Date(instant);return Number.isFinite(date.getTime())?new Intl.DateTimeFormat((globalThis.HouseholdI18n?.locale() || 'de-AT'),{dateStyle:'medium',timeStyle:'short',timeZone:S.view?.timezone||'Europe/Vienna'}).format(date):_householdText_chores_workspace_js("Nicht bekannt");};
 function localDay(instant=new Date(),timezone=S.view?.timezone||'Europe/Vienna'){
  const parts=new Intl.DateTimeFormat('en-CA',{year:'numeric',month:'2-digit',day:'2-digit',timeZone:timezone}).formatToParts(instant);
  const p=Object.fromEntries(parts.map(x=>[x.type,x.value]));return p.year+'-'+p.month+'-'+p.day;
 }
 const facts=entries=>{const dl=node('dl',undefined,'chores-facts');for(const [title,value] of entries){dl.append(node('dt',title),node('dd',known(value)));}return dl;};
 function field(parent,title,name,value='',type='text',options={}){
  const label=node('label',title);const input=node(type==='textarea'?'textarea':'input');if(type!=='textarea')input.type=type;
  input.id='chore-field-'+(++sequence);input.name=name;input.value=value??'';if(options.required)input.required=true;
  for(const key of ['min','max','maxLength','step'])if(options[key]!==undefined)input[key]=options[key];label.append(input);
  if(options.help)label.append(node('small',options.help));parent.append(label);return input;
 }
 function select(parent,title,name,choices,value=''){
  const label=node('label',title),input=node('select');input.name=name;input.id='chore-field-'+(++sequence);
  for(const [key,text]of choices){const option=node('option',text);option.value=key;input.append(option);}input.value=value??'';label.append(input);parent.append(label);return input;
 }
 function check(parent,title,name,value=false){const label=node('label',undefined,'chores-check'),input=node('input');input.type='checkbox';input.name=name;input.checked=value===true;label.append(input,node('span',title));parent.append(label);return input;}
 function section(parent,title){const e=node('fieldset'),legend=node('legend',title);e.append(legend);parent.append(e);return e;}
 function empty(title,detail){const e=node('section',undefined,'chores-empty');e.append(node('h2',title),node('p',detail));content.append(e);}
 function clearError(){$('error').hidden=true;$('conflict').replaceChildren();}
 function deny(){S.denied=true;S.epoch++;S.csrf='';S.pending=null;content.replaceChildren();filters.replaceChildren();$('paging').replaceChildren();status(_householdText_chores_workspace_js("Berechtigung wurde geändert. Diese Ansicht mit aktueller Anmeldung neu öffnen."));}
 function error(err){$('error-text').textContent=err.message||_householdText_chores_workspace_js("Vorgang konnte nicht bestätigt werden.");$('retry').hidden=!S.pending;$('error').hidden=false;$('error').focus();
  if(err.status===409){const box=$('conflict');box.replaceChildren(node('p',_householdText_chores_workspace_js("Deine Eingaben bleiben erhalten. Ein anderer Vorgang oder die Modulgeneration hat den Stand geändert. Aktuellen Stand vergleichen und neu entscheiden.")));
   box.append(button(_householdText_chores_workspace_js("Aktuellen Stand daneben anzeigen"),async()=>{const target=S.pane==='work'?'/occurrences/'+S.id:S.pane==='series-edit'&&S.id!=='new'?'/series/'+S.id:'/preferences';const current=await request(api+target);
    const v=current.item?.occurrence||current.template||current.preferences||{};box.append(facts([[_householdText_chores_workspace_js("Aktueller Titel"),current.item?.title||v.title],[_householdText_chores_workspace_js("Aktuelle Version"),v.version],[_householdText_chores_workspace_js("Status"),labels[v.state]||v.state],[_householdText_chores_workspace_js("Modulgeneration"),current.generation]]));}));
   box.append(link(_householdText_chores_workspace_js("Mit aktuellem Stand neu beginnen"),location.pathname));
  }
 }
 async function run(action){if(S.busy||S.denied)return;clearError();S.busy=true;root.setAttribute('aria-busy','true');
  try{await action();}catch(err){error(err);}finally{S.busy=false;root.setAttribute('aria-busy','false');}}
 async function request(url,{body,key}={}){
  if(S.denied)throw Error(_householdText_chores_workspace_js("Berechtigung zuerst neu prüfen."));if(body!==undefined&&!navigator.onLine)throw Error(_householdText_chores_workspace_js("Offline. Eingaben bleiben erhalten; nach Wiederherstellen der Verbindung erneut prüfen."));
  const epoch=S.epoch,controller=new AbortController(),timer=setTimeout(()=>controller.abort(),20000);
  try{const headers={Accept:'application/json'};if(body!==undefined){headers['Content-Type']='application/json';headers['X-CSRF-Token']=S.csrf;}if(key)headers['Idempotency-Key']=key;
   const response=await fetch(url,{method:body===undefined?'GET':'POST',headers,credentials:'same-origin',cache:'no-store',signal:controller.signal,...(body===undefined?{}:{body:JSON.stringify(body)})});
   if([401,403].includes(response.status)){deny();if(response.body)await response.body.cancel();const err=Error(_householdText_chores_workspace_js("Aktuelle Berechtigung fehlt. Mit aktueller Anmeldung neu öffnen."));err.status=response.status;throw err;}
   const reader=response.body?.getReader();if(!reader)throw Error(_householdText_chores_workspace_js("Serverantwort fehlt."));let length=0;const chunks=[];
   try{for(;;){const result=await reader.read();if(result.done)break;length+=result.value.byteLength;if(length>3*1024*1024)throw Error(_householdText_chores_workspace_js("Antwort ist zu groß; Auswahl eingrenzen."));chunks.push(result.value);}}catch(err){await reader.cancel().catch(()=>{});throw err;}finally{reader.releaseLock();}
   const raw=new Uint8Array(length);let offset=0;for(const part of chunks){raw.set(part,offset);offset+=part.byteLength;}
   const value=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));if(epoch!==S.epoch||S.denied)throw Error(_householdText_chores_workspace_js("Ansicht wurde inzwischen ungültig."));
   if(!response.ok){const err=Error(value.error||_householdText_chores_workspace_js("Aufgabenentscheidung wurde nicht übernommen."));err.status=response.status;throw err;}return value;
  }catch(err){if(err.status===undefined)err.status=0;throw err;}finally{clearTimeout(timer);}
 }
 async function current(){const data=await request('/api/v1/core/bootstrap');const user=data.user;
  if(!user||user.id!==document.body.dataset.user||user.household_id!==document.body.dataset.household||S.identity&&(user.membership_id!==S.identity.membership_id||user.role!==S.identity.role)){deny();throw Error(_householdText_chores_workspace_js("Anmeldung oder Mitgliedschaft wurde geändert. Ansicht neu öffnen."));}
  S.identity=user;S.csrf=data.csrf;S.canWrite=['admin','editor'].includes(user.role);return data;
 }
 async function mutate(path,payload,after){
  if(S.pending)throw Error(_householdText_chores_workspace_js("Zuerst den noch unklaren Vorgang mit unveränderten Angaben prüfen."));await current();
  const operation={url:api+path,body:structuredClone(payload),key:crypto.randomUUID(),after};S.pending=operation;
  try{const result=await request(operation.url,operation);S.pending=null;await after(result);status(_householdText_chores_workspace_js("Änderung übernommen."));}
  catch(err){if(err.status!==0)S.pending=null;else err.message=_householdText_chores_workspace_js("Ergebnis unklar. Nicht als neuen Vorgang senden; denselben Vorgang unverändert erneut prüfen. ")+err.message;throw err;}
 }
 $('retry').addEventListener('click',()=>run(async()=>{const pending=S.pending;if(!pending)return;await current();const result=await request(pending.url,pending);S.pending=null;$('retry').hidden=true;await pending.after(result);status(_householdText_chores_workspace_js("Vorgang bestätigt."));}));
 function formSubmit(form,action){form.addEventListener('submit',event=>{event.preventDefault();if(!form.reportValidity())return;run(action);});}
 function submit(parent,title){const e=node('button',title,'primary');e.type='submit';parent.append(e);return e;}
 function base(version=0){return {generation:S.generation,version};}
 function updateView(view){S.view=view;S.generation=view.generation??S.generation;if(typeof view.can_write==='boolean')S.canWrite=view.can_write;}
 function heading(title,description){$('heading').textContent=title;$('context').textContent=description;}
 function page(view,loadMore){$('paging').replaceChildren();S.next=view.next_cursor||'';if(S.next)$('paging').append(button(_householdText_chores_workspace_js("Weitere Einträge laden"),()=>loadMore(S.next)));}
 async function reference(parent,title,name,referenceType,selected='',selectedLabel=''){
  const wrap=node('div'),input=select(wrap,title,name,[['',_householdText_chores_workspace_js("Bitte auswählen")]],''),tools=node('div',undefined,'chores-reference-tools');
  const search=node('input');search.type='search';search.maxLength=100;search.setAttribute('aria-label',title+_householdText_chores_workspace_js(" suchen"));const hint=node('small');hint.setAttribute('role','status');
  let cursor='',loading=false;const load=async(next='')=>{if(loading)return;loading=true;input.disabled=true;hint.textContent=_householdText_chores_workspace_js("Auswahl laden …");try{
   const q=new URLSearchParams({q:search.value.trim(),cursor:next}),result=await request('/api/v1/core/reference-options/'+referenceType+'?'+q);
   const keep=input.value;for(const option of [...input.options])if(option.value&&option.value!==keep)option.remove();
   for(const record of result.records||[]){if([...input.options].some(x=>x.value===record.id))continue;const option=node('option',record.label);option.value=record.id;input.append(option);}
   input.value=keep;cursor=result.next_cursor||'';more.hidden=!cursor;hint.textContent=(result.records?.length||0)+_householdText_chores_workspace_js(" Treffer auf dieser Seite. Gewählte Person bleibt erhalten.");
  }finally{loading=false;input.disabled=false;}};
  const find=button(_householdText_chores_workspace_js("Suchen"),()=>load()),more=button(_householdText_chores_workspace_js("Weitere Treffer"),()=>load(cursor));more.hidden=true;tools.append(search,find,more);wrap.append(tools,hint);parent.append(wrap);
  if(selected){const option=node('option',selectedLabel||_householdText_chores_workspace_js("Gewählter Eintrag"));option.value=selected;input.append(option);input.value=selected;}
  search.addEventListener('keydown',event=>{if(event.key==="Enter"){event.preventDefault();run(()=>load());}});
  await load();return input;
 }
 function row(title,detail){const e=node('article',undefined,'chores-row'),main=node('div',undefined,'chores-row-main'),actions=node('div',undefined,'actions');main.append(node('h3',title),node('p',detail));e.append(main,actions);return {element:e,main,actions};}
 async function today(cursor=''){
  const q=new URLSearchParams({...S.query,...(cursor?{cursor}:{})}),view=await request(api+'/tasks?'+q);updateView(view);
  if(!cursor)content.replaceChildren();heading(_householdText_chores_workspace_js("Heute für mich"),_householdText_chores_workspace_js("Fällige Aufgaben · ")+view.timezone);
  if(!view.items.length&&!cursor)empty(_householdText_chores_workspace_js("Keine fälligen Aufgaben"),_householdText_chores_workspace_js("Passe den Zeitraum an oder sieh dir nicht zugewiesene Aufgaben an."));
  for(const item of view.items){const o=item.occurrence,d=item.detail;const held=!item.series_active||item.series_paused&&new Date(o.due_at)>new Date(item.paused_at);
   const r=row(item.title,(labels[o.state]||o.state)+' · '+(item.area_name||_householdText_chores_workspace_js("Ohne Bereich"))+' · '+(item.estimated_minutes==null?_householdText_chores_workspace_js("Dauer offen"):item.estimated_minutes+_householdText_chores_workspace_js(" min")));
   r.main.append(node('p',_householdText_chores_workspace_js("Fällig: ")+format(o.due_at)+' · '+(item.assignee_name||_householdText_chores_workspace_js("Nicht zugewiesen"))));
   if(d.original_due_at!==o.due_at)r.main.append(node('p',_householdText_chores_workspace_js("Ursprünglich: ")+format(d.original_due_at)));
   if(item.claim_active)r.main.append(node('p',_householdText_chores_workspace_js("Übernommen von ")+item.claim_name+_householdText_chores_workspace_js(" bis ")+format(d.claim_expires_at)));
   if(held)r.main.append(node('p',item.series_active?_householdText_chores_workspace_js("Während der Serienpause ausgesetzt"):_householdText_chores_workspace_js("Serie archiviert"),'chores-alert'));
   if(item.required_remaining||!item.prerequisites_met)r.main.append(node('small',item.required_remaining+_householdText_chores_workspace_js(" Pflichtpunkte offen")+(!item.prerequisites_met?_householdText_chores_workspace_js(" · Voraussetzung fehlt"):'')));
   r.actions.append(link(_householdText_chores_workspace_js("Arbeitsansicht"),'/m/chores/workspace/work/'+o.id));content.append(r.element);
  }page(view,today);
 }
 async function todayFilters(){
  const form=node('form'),grid=node('div',undefined,'chores-filter-grid');form.append(grid);
  select(grid,_householdText_chores_workspace_js("Aufgaben für"),'scope',[['mine',_householdText_chores_workspace_js("Mich")],['household',_householdText_chores_workspace_js("Den Haushalt")],['unassigned',_householdText_chores_workspace_js("Nicht zugewiesen")]],S.query.scope||'mine');
  field(grid,_householdText_chores_workspace_js("Bis einschließlich"),'through',S.query.through||localDay(),'date',{required:true});field(grid,_householdText_chores_workspace_js("Ab Datum · leer schließt Überfälliges ein"),'from',S.query.from||'','date');
  field(grid,_householdText_chores_workspace_js("Titel suchen"),'search',S.query.search||'','search',{maxLength:120});select(grid,_householdText_chores_workspace_js("Status"),'state',[['open',_householdText_chores_workspace_js("Offen")],['all',_householdText_chores_workspace_js("Alle")],['completed',_householdText_chores_workspace_js("Erledigt")],['skipped',_householdText_chores_workspace_js("Ausgelassen")]],S.query.state||'open');
  check(grid,_householdText_chores_workspace_js("Pausierte und archivierte Serien einbeziehen"),'held',S.query.include_paused==='true');await areaSelect(grid,_householdText_chores_workspace_js("Bereich"),'area',S.query.area||'');submit(form,_householdText_chores_workspace_js("Filter anwenden"));filters.replaceChildren(form);
  formSubmit(form,async()=>{const data=new FormData(form);S.query=Object.fromEntries([...data].filter(([name])=>name!=='held'));S.query.include_paused=S.query.include_archived=form.elements.held.checked?'true':'false';navigationArea();await today();});
 }
 async function areaSelect(parent,title,name,selected='',selectedName=''){
  const wrap=node('div'),input=select(wrap,title,name,[['',_householdText_chores_workspace_js("Ohne Bereich / alle")]],selected),tools=node('div',undefined,'chores-reference-tools'),search=node('input');search.type='search';search.maxLength=100;search.setAttribute('aria-label',_householdText_chores_workspace_js("Bereich suchen"));
  let cursor='';const load=async(next='')=>{const view=await request(api+'/areas?'+new URLSearchParams({search:search.value,cursor:next}));const keep=input.value;
   for(const option of [...input.options])if(option.value&&option.value!==keep)option.remove();for(const item of view.items){if([...input.options].some(x=>x.value===item.id))continue;const option=node('option',item.name);option.value=item.id;input.append(option);}input.value=keep;cursor=view.next_cursor;more.hidden=!cursor;};
  const more=button(_householdText_chores_workspace_js("Weitere Bereiche"),()=>load(cursor));more.hidden=true;tools.append(search,button(_householdText_chores_workspace_js("Suchen"),()=>load()),more);wrap.append(tools);parent.append(wrap);
  if(selected){const option=node('option',selectedName||_householdText_chores_workspace_js("Gewählter Bereich"));option.value=selected;input.append(option);input.value=selected;}await load();return input;
 }
 async function areas(cursor=''){
  const view=await request(api+'/areas?'+new URLSearchParams({...S.query,...(cursor?{cursor}:{})}));updateView(view);if(!cursor)content.replaceChildren();heading(_householdText_chores_workspace_js("Bereiche"),_householdText_chores_workspace_js("Aufgaben nach Räumen und Lebensbereichen organisieren."));
  if(!view.items.length&&!cursor)empty(_householdText_chores_workspace_js("Noch keine Bereiche"),_householdText_chores_workspace_js("Lege einen Bereich an und ordne ihn einer Aufgabenserie zu."));
  for(const area of view.items){const r=row(area.name,area.open_count+_householdText_chores_workspace_js(" offen · ")+area.overdue_count+_householdText_chores_workspace_js(" überfällig")+(area.archived_at?_householdText_chores_workspace_js(" · Archiviert"):''));
   r.actions.append(link(_householdText_chores_workspace_js("Aufgaben anzeigen"),'/m/chores/workspace/today?area='+area.id));if(S.canWrite)r.actions.append(button(_householdText_chores_workspace_js("Bereich bearbeiten"),()=>areaEditor(area)));content.append(r.element);}page(view,areas);
 }
 function areaEditor(area={}){
  filters.replaceChildren();const form=node('form');form.append(node('h2',area.id?_householdText_chores_workspace_js("Bereich bearbeiten"):_householdText_chores_workspace_js("Bereich anlegen")));
  field(form,_householdText_chores_workspace_js("Name"),'name',area.name||'','text',{required:true,maxLength:100});field(form,_householdText_chores_workspace_js("Reihenfolge"),'position',area.position||0,'number',{min:0,max:10000,required:true});check(form,_householdText_chores_workspace_js("Archiviert"),'archived',!!area.archived_at);submit(form,_householdText_chores_workspace_js("Bereich speichern"));filters.append(form);
  formSubmit(form,()=>mutate('/areas'+(area.id?'/'+area.id:''),{...base(area.version||0),name:form.elements.name.value,position:Number(form.elements.position.value),archived:form.elements.archived.checked},async()=>{filters.replaceChildren();await areas();}));form.elements.name.focus();
 }
 function previewExplanation(view){
  const schedule=view.normalized.schedule;if(globalThis.HouseholdI18n?.language()!=='en')return view.explanation;
  if(schedule.anchor==='completion')return _householdText_chores_workspace_js("%d Tage nach der tatsächlichen Erledigung · %s · %s",schedule.interval,schedule.local_time,schedule.timezone);
  let label=_householdText_chores_workspace_js("Einmaliger Termin");
  if(schedule.kind==='days')label=_householdText_chores_workspace_js("Alle %d Kalendertage",schedule.interval);
  if(schedule.kind==='weekly'){const days=schedule.weekdays.map(day=>new Intl.DateTimeFormat('en-GB',{weekday:'short',timeZone:'UTC'}).format(new Date(Date.UTC(2024,0,day))));label=_householdText_chores_workspace_js("Alle %d Wochen: %s",schedule.interval,days.join(', '));}
  if(schedule.kind==='monthly')label=_householdText_chores_workspace_js("Alle %d Monate am %d.; kurze Monate bis zum Monatsende",schedule.interval,schedule.month_day);
  if(schedule.months?.length){const months=schedule.months.map(month=>new Intl.DateTimeFormat('en-GB',{month:'short',timeZone:'UTC'}).format(new Date(Date.UTC(2024,month-1,1))));label+=_householdText_chores_workspace_js(" · Saison: %s",months.join(', '));}
  return label+' · '+schedule.local_time+' · '+schedule.timezone;
 }
 function ruleText(v){return (labels[v.rule_kind]||v.rule_kind)+(v.rule_kind==='once'?'':_householdText_chores_workspace_js(" · Abstand ")+v.interval_value)+' · '+(labels[v.anchor_kind]||v.anchor_kind)+' · '+String(v.local_time||'').slice(0,5)+' '+v.timezone;}
 async function series(cursor=''){
  const view=await request(api+'/series?'+new URLSearchParams({...S.query,...(cursor?{cursor}:{})}));updateView(view);if(!cursor)content.replaceChildren();heading(_householdText_chores_workspace_js("Aufgabenserien"),_householdText_chores_workspace_js("Regeln und Zuständigkeit ändern; abgeschlossene Termine behalten ihren Stand."));
  if(!view.items.length&&!cursor)empty(_householdText_chores_workspace_js("Noch keine Serien"),_householdText_chores_workspace_js("Lege eine einmalige Aufgabe oder eine wiederkehrende Serie an."));
  for(const item of view.items){const t=item.template,p=item.profile,v=item.snapshot,r=row(v.title,ruleText(v));
   r.main.append(node('p',(t.active?(p.paused?_householdText_chores_workspace_js("Pausiert"):_householdText_chores_workspace_js("Aktiv")):_householdText_chores_workspace_js("Archiviert"))+_householdText_chores_workspace_js(" · Letzte Erledigung: ")+format(item.last_completed_at)));
   if(p.review_required)r.main.append(node('p',_householdText_chores_workspace_js("Alte Regel muss vor rollierender Planung ausdrücklich geprüft werden."),'chores-alert'));
   if(item.schedule_state?.backlog)r.main.append(node('p',_householdText_chores_workspace_js("Terminrückstand wird in begrenzten Läufen weiter aufgearbeitet.")));
   r.actions.append(link(S.canWrite?_householdText_chores_workspace_js("Zukünftige Serie bearbeiten"):_householdText_chores_workspace_js("Vorlagenstand ansehen"),'/m/chores/workspace/series/'+t.id));
   if(S.canWrite){r.actions.append(button(t.active?(p.paused?_householdText_chores_workspace_js("Fortsetzen"):_householdText_chores_workspace_js("Pausieren")):_householdText_chores_workspace_js("Aktivieren"),()=>seriesControl(item,t.active?(p.paused?'resume':'pause'):'activate')));if(t.active)r.actions.append(button(_householdText_chores_workspace_js("Archivieren"),()=>seriesControl(item,'archive')));}
   content.append(r.element);
  }page(view,series);
 }
 function seriesControl(item,action){
  const form=node('form');filters.replaceChildren(form);const titles={pause:_householdText_chores_workspace_js("Serie pausieren"),resume:_householdText_chores_workspace_js("Serie fortsetzen"),archive:_householdText_chores_workspace_js("Serie archivieren"),activate:_householdText_chores_workspace_js("Serie aktivieren")};
  form.append(node('h2',titles[action]),node('p',action==='pause'?_householdText_chores_workspace_js("Künftige offene Termine werden ausgesetzt. Beim Fortsetzen werden während der Pause fällig gewordene Termine als ausgelassen dokumentiert. Bei Erledigungsregeln beginnt der neue Abstand am Ende der Pause."):action==='archive'?_householdText_chores_workspace_js("Die Serie bleibt mit ihrer Historie erhalten. Offene Termine erscheinen als abgesagt und können nicht abgeschlossen werden."):_householdText_chores_workspace_js("Bestehende Terminidentitäten bleiben erhalten. Neue Termine werden begrenzt geplant; bei Erledigungsregeln beginnt der Abstand am Ende der Pause.")));
  if(action==='pause')field(form,_householdText_chores_workspace_js("Automatisch fortsetzen am · optional"),'pause_until','','date');submit(form,titles[action]);form.append(button(_householdText_chores_workspace_js("Abbrechen"),async()=>filters.replaceChildren()));
  formSubmit(form,()=>mutate('/series/'+item.template.id+'/control',{...base(item.profile.version),action,...(action==='pause'?{pause_until:form.elements.pause_until.value}:{})},async()=>{filters.replaceChildren();await series();}));form.querySelector('input,button').focus();
 }

 // Editors and focused workflows are defined below before initial dispatch.
 function navigationArea(){for(const anchor of root.querySelectorAll('a[data-chores-pane]')){try{anchor.href=choresWorkspaceHref(anchor.dataset.choresPane,S.query.area||'');anchor.removeAttribute('aria-disabled');}catch{anchor.removeAttribute('href');anchor.setAttribute('aria-disabled','true');}}}
 for(const anchor of root.querySelectorAll('a[data-chores-pane]'))anchor.addEventListener('click',event=>{try{anchor.href=choresWorkspaceHref(anchor.dataset.choresPane,S.query.area||'');}catch(err){event.preventDefault();error(err);}});
 async function start(){
  await current();const initial=await request(api+'/preferences');updateView(initial);S.view.timezone=initial.preferences.timezone;
  for(const a of root.querySelectorAll('[data-chores-pane]'))if(a.dataset.choresPane===S.pane||S.pane==='series-edit'&&a.dataset.choresPane==='series')a.setAttribute('aria-current','page');
  const q=new URLSearchParams(location.search);for(const name of ['area','scope','from','through','state','search','include_archived','include_paused'])if(q.has(name))S.query[name]=q.get(name);
  navigationArea();
  switch(S.pane){case'today':await todayFilters();await today();break;case'areas':listTools('areas');await areas();break;case'series':listTools('series');await series();break;
   case'series-edit':await seriesEditor();break;case'work':await work();break;case'plan':await plan();break;case'history':await historyPage();break;case'settings':await settings();break;default:throw Error(_householdText_chores_workspace_js("Aufgabenbereich fehlt."));}
  status(S.canWrite?_householdText_chores_workspace_js("Aktueller Stand geladen."):_householdText_chores_workspace_js("Lesezugriff. Persönliche Einstellungen und Kalenderfreigaben bleiben verfügbar."));
 }
 function listTools(kind){const form=node('form'),grid=node('div',undefined,'chores-filter-grid');form.append(grid);field(grid,_householdText_chores_workspace_js("Suchen"),'search',S.query.search||'','search',{maxLength:120});check(grid,_householdText_chores_workspace_js("Archivierte Einträge einbeziehen"),'archived',S.query.include_archived==='true');submit(form,_householdText_chores_workspace_js("Anzeigen"));
  if(kind==='areas'&&S.canWrite)form.append(button(_householdText_chores_workspace_js("Bereich anlegen"),async()=>areaEditor()));filters.replaceChildren(form);formSubmit(form,async()=>{S.query={search:form.elements.search.value,include_archived:String(form.elements.archived.checked)};await(kind==='areas'?areas():series());});}

 async function seriesEditor(){
  let data=null;if(S.id!=='new'){data=await request(api+'/series/'+S.id);updateView(data);}content.replaceChildren();filters.replaceChildren();
  const v=data?.snapshot||{},p=data?.profile||{},t=data?.template||{};
  heading(data?_householdText_chores_workspace_js("Zukünftige Serie ändern"):_householdText_chores_workspace_js("Neue Aufgabenserie"),_householdText_chores_workspace_js("Einzeltermine werden in ihrer Arbeitsansicht geändert. Historische Vorlagen bleiben erhalten."));
  const form=node('form'),about=section(form,_householdText_chores_workspace_js("Aufgabe und Aufwand")),grid=node('div',undefined,'chores-form-grid');about.append(grid);let invalidatePreview=()=>{};
  field(grid,_householdText_chores_workspace_js("Titel"),'title',v.title||'','text',{required:true,maxLength:200});await areaSelect(grid,_householdText_chores_workspace_js("Bereich"),'area_id',v.area_id||'',v.area_name);
  const desc=field(grid,_householdText_chores_workspace_js("Anleitung"),'description',v.description||'','textarea',{maxLength:10000});desc.parentElement.classList.add('chores-full');
  field(grid,_householdText_chores_workspace_js("Material · eine Zeile je Eintrag"),'materials',(v.materials||[]).join('\n'),'textarea',{help:_householdText_chores_workspace_js("Bis zu 20 Materialzeilen. Keine automatische Bestandsbuchung.")});
  field(grid,_householdText_chores_workspace_js("Geschätzte Minuten · optional"),'estimated_minutes',v.estimated_minutes??'','number',{min:1,max:1440});
  select(grid,_householdText_chores_workspace_js("Schwierigkeit"),'difficulty',[['',_householdText_chores_workspace_js("Noch nicht eingeschätzt")],['1',_householdText_chores_workspace_js("1 · Leicht")],['2','2'],['3','3'],['4','4'],['5',_householdText_chores_workspace_js("5 · Anspruchsvoll")]],v.difficulty??'');
  select(grid,_householdText_chores_workspace_js("Priorität"),'priority',[['0',_householdText_chores_workspace_js("Niedrig")],['1',_householdText_chores_workspace_js("Normal")],['2',_householdText_chores_workspace_js("Hoch")],['3',_householdText_chores_workspace_js("Dringend")]],v.priority??1);
  const rule=section(form,_householdText_chores_workspace_js("Kalenderregel")),rg=node('div',undefined,'chores-form-grid');rule.append(rg);
  const kind=select(rg,_householdText_chores_workspace_js("Wiederholung"),'kind',[['once',_householdText_chores_workspace_js("Einmalig")],['days',_householdText_chores_workspace_js("Tagesabstand")],['weekly',_householdText_chores_workspace_js("Wochentage")],['monthly',_householdText_chores_workspace_js("Monatsabstand")]],v.rule_kind||'once');
  const interval=field(rg,_householdText_chores_workspace_js("Abstand · Tage / Wochen / Monate"),'interval',v.interval_value||1,'number',{min:1,max:366,required:true});
  const anchor=select(rg,_householdText_chores_workspace_js("Folgetermin zählt ab"),'anchor',[['scheduled',_householdText_chores_workspace_js("Festem Kalenderplan")],['completion',_householdText_chores_workspace_js("Tatsächlicher Erledigung")]],v.anchor_kind||'scheduled');
  field(rg,_householdText_chores_workspace_js("Erster lokaler Termin"),'starts_on',v.starts_on||localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});
  field(rg,_householdText_chores_workspace_js("Lokale Uhrzeit"),'local_time',String(v.local_time||'09:00').slice(0,5),'time',{required:true});field(rg,(_householdText_chores_workspace_js('IANA-Zeitzone')),'timezone',v.timezone||S.view?.timezone||'Europe/Vienna','text',{required:true,maxLength:100});
  const monthday=field(rg,_householdText_chores_workspace_js("Ursprünglicher Tag im Monat"),'month_day',v.month_day||Number(localDay().slice(8)),'number',{min:1,max:31,required:true});
  field(rg,_householdText_chores_workspace_js("Letztes Datum · optional"),'ends_on',v.ends_on||'','date',{min:'1900-01-01',max:'2200-12-31'});
  const weekdayGroup=node('div'),weekdays=node('div',undefined,'chores-checks');weekdayGroup.append(node('p',_householdText_chores_workspace_js("Wochentage")),weekdays);rule.append(weekdayGroup);
  [_householdText_chores_workspace_js("Montag"),_householdText_chores_workspace_js("Dienstag"),_householdText_chores_workspace_js("Mittwoch"),_householdText_chores_workspace_js("Donnerstag"),_householdText_chores_workspace_js("Freitag"),_householdText_chores_workspace_js("Samstag"),_householdText_chores_workspace_js("Sonntag")].forEach((day,i)=>{const input=check(weekdays,day,'weekday',v.weekdays?.includes(i+1)||false);input.value=String(i+1);});
  const monthGroup=node('div'),months=node('div',undefined,'chores-checks');monthGroup.append(node('p',_householdText_chores_workspace_js("Saisonmonate · keine Auswahl bedeutet ganzjährig")),months);rule.append(monthGroup);
  [_householdText_chores_workspace_js("Jänner"),_householdText_chores_workspace_js("Februar"),_householdText_chores_workspace_js("März"),_householdText_chores_workspace_js("April"),_householdText_chores_workspace_js("Mai"),_householdText_chores_workspace_js("Juni"),_householdText_chores_workspace_js("Juli"),_householdText_chores_workspace_js("August"),_householdText_chores_workspace_js("September"),_householdText_chores_workspace_js("Oktober"),_householdText_chores_workspace_js("November"),_householdText_chores_workspace_js("Dezember")].forEach((month,i)=>{const input=check(months,month,'month',v.months?.includes(i+1)||false);input.value=String(i+1);});
  rule.append(node('p',_householdText_chores_workspace_js("Zeitumstellung: Bei doppelter Uhrzeit gilt die frühere Instanz; eine fehlende Uhrzeit wird um die Zeitlücke verschoben. Ein Monatsanker 31 bleibt auch nach Februar 31."),'muted'));
  function ruleFields(){const weekly=kind.value==='weekly',monthly=kind.value==='monthly',daily=kind.value==='days',once=kind.value==='once',completion=daily&&anchor.value==='completion';
   weekdayGroup.hidden=!weekly;weekdays.querySelectorAll('input').forEach(x=>x.disabled=!weekly);monthday.parentElement.hidden=!monthly;monthday.disabled=!monthly;
   interval.parentElement.hidden=once;interval.disabled=once;anchor.parentElement.hidden=!daily;anchor.disabled=!daily;
   monthGroup.hidden=completion||once;months.querySelectorAll('input').forEach(x=>x.disabled=completion||once);
  }kind.addEventListener('change',ruleFields);anchor.addEventListener('change',ruleFields);ruleFields();
  const assignment=section(form,_householdText_chores_workspace_js("Zuständigkeit und Eignung"));const assignmentKind=select(assignment,_householdText_chores_workspace_js("Zuweisung"),'assignment_kind',[['unassigned',_householdText_chores_workspace_js("Ohne Zuweisung")],['fixed',_householdText_chores_workspace_js("Feste Person")],['rotation',_householdText_chores_workspace_js("Geordnete Rotation")]],v.assignment_kind||'unassigned');
  const fixedAssignment=node('div');assignment.append(fixedAssignment);const assignee=await reference(fixedAssignment,_householdText_chores_workspace_js("Feste Person"),'assignee_id','core/memberships',v.assignee_id||'');
  const roster=node('div');assignment.append(node('p',_householdText_chores_workspace_js("Geeignete Personen · Reihenfolge entspricht der Rotation. Vertretungen werden in den Abwesenheiten ausdrücklich gewählt.")),roster);
  async function addMember(member={eligible:true}){if(roster.children.length>=32)throw Error(_householdText_chores_workspace_js("Höchstens 32 Personen."));const r=node('div',undefined,'chores-repeat-row');roster.append(r);invalidatePreview();
   const input=await reference(r,_householdText_chores_workspace_js("Geeignete Person"),'rotation_member','core/memberships',member.membership_id||'',member.name);check(r,_householdText_chores_workspace_js("Geeignet"),'eligible',member.eligible);r.append(button(_householdText_chores_workspace_js("Person entfernen"),async()=>{r.remove();invalidatePreview();}));r.choreMember=input;
  }for(const member of data?.members||[])await addMember(member);assignment.append(button(_householdText_chores_workspace_js("Person zur Eignungsliste hinzufügen"),()=>addMember()));
  function assignmentFields(){const fixed=assignmentKind.value==='fixed';fixedAssignment.hidden=!fixed;fixedAssignment.querySelectorAll('input,select,button').forEach(input=>input.disabled=!fixed);}
  assignmentKind.addEventListener('change',assignmentFields);assignmentFields();
  const parts=section(form,_householdText_chores_workspace_js("Unteraufgaben und Voraussetzungen")),checklist=node('div');parts.append(checklist);
  function addCheck(item={required:true}){if(checklist.children.length>=50)throw Error(_householdText_chores_workspace_js("Höchstens 50 Unteraufgaben."));const r=node('div',undefined,'chores-repeat-row');r.dataset.key=item.key||'';
   field(r,_householdText_chores_workspace_js("Unteraufgabe"),'check_title',item.title||'','text',{required:true,maxLength:300});check(r,_householdText_chores_workspace_js("Pflicht"),'required',item.required);r.append(button(_householdText_chores_workspace_js("Unteraufgabe entfernen"),async()=>{r.remove();invalidatePreview();}));checklist.append(r);invalidatePreview();
  }for(const item of data?.checklist||[])addCheck(item);parts.append(button(_householdText_chores_workspace_js("Unteraufgabe hinzufügen"),async()=>addCheck()));
  const prerequisites=node('div');parts.append(node('p',_householdText_chores_workspace_js("Voraussetzungen müssen für den jeweiligen Termin tatsächlich erledigt sein. Selbstbezug und Zyklen werden abgewiesen.")),prerequisites);
  async function addPrerequisite(item={}){if(prerequisites.children.length>=16)throw Error(_householdText_chores_workspace_js("Höchstens 16 Voraussetzungen."));const r=node('div',undefined,'chores-repeat-row');prerequisites.append(r);invalidatePreview();
   const input=await reference(r,_householdText_chores_workspace_js("Voraussetzungsserie"),'prerequisite','chores/task_templates',item.id||'',item.title);r.append(button(_householdText_chores_workspace_js("Voraussetzung entfernen"),async()=>{r.remove();invalidatePreview();}));r.chorePrerequisite=input;
  }for(const item of data?.prerequisites||[])await addPrerequisite(item);parts.append(button(_householdText_chores_workspace_js("Voraussetzung hinzufügen"),()=>addPrerequisite()));
  const validity=section(form,_householdText_chores_workspace_js("Gültigkeit und vorhandene Termine"));
  field(validity,_householdText_chores_workspace_js("Neue Regel gilt ab"),'effective_on',localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});
  field(validity,_householdText_chores_workspace_js("Termine erzeugen ab · für neue oder bisher ungeprüfte Serien"),'catch_up_from','','date',{min:'1900-01-01',max:'2200-12-31'});
  check(validity,_householdText_chores_workspace_js("Ich bestätige den in der Vorschau gewählten Beginn und die Behandlung älterer Rückstände"),'acknowledge_past',false);
  const review=node('section',undefined,'chores-preview');review.hidden=true;form.append(review);const previewButton=submit(form,_householdText_chores_workspace_js("Vollständige Vorschau prüfen")),apply=button(_householdText_chores_workspace_js("Geprüfte zukünftige Serie speichern"),async()=>{
   if(!review.choreCommand)throw Error(_householdText_chores_workspace_js("Zuerst Vorschau prüfen."));const submittedDraft=draftSnapshot();await mutate('/series',review.choreCommand,async result=>{const id=result.template?.id||result.profile?.template_id||result.id;
    if(!id)throw Error(_householdText_chores_workspace_js("Gespeicherte Serienkennung fehlt."));initialDraft=submittedDraft;if(draftSnapshot()===submittedDraft)window.removeEventListener('beforeunload',protectDraft);location.assign('/m/chores/workspace/series/'+id);});},true);apply.hidden=true;form.append(apply);content.append(form);
  if(!S.canWrite){for(const fieldset of form.querySelectorAll('fieldset'))fieldset.disabled=true;previewButton.hidden=true;return;}
  function draftSnapshot(){return JSON.stringify({fields:[...form.querySelectorAll('input[name],select[name],textarea[name]')].map(input=>[input.name,input.value,input.type==='checkbox'?input.checked:null]),checklistKeys:[...checklist.children].map(item=>item.dataset.key)});}
  let initialDraft=draftSnapshot();
  function protectDraft(event){if(form.isConnected&&!S.denied&&draftSnapshot()!==initialDraft){event.preventDefault();event.returnValue='';}}
  window.addEventListener('beforeunload',protectDraft);
  function command(){const elements=form.elements,kindValue=kind.value,anchorValue=kindValue==='days'?anchor.value:'scheduled';const number=name=>elements[name].value===''?null:Number(elements[name].value);
   return {...base(p.version||0),id:data?t.id:undefined,base_version:t.version||0,title:elements.title.value,description:elements.description.value,
    materials:elements.materials.value.split('\n').map(x=>x.trim()).filter(Boolean),area_id:elements.area_id.value,estimated_minutes:number('estimated_minutes'),difficulty:number('difficulty'),priority:Number(elements.priority.value),
    schedule:{kind:kindValue,interval:kindValue==='once'?1:Number(interval.value),anchor:anchorValue,starts_on:elements.starts_on.value,local_time:elements.local_time.value,timezone:elements.timezone.value,
     weekdays:kindValue==='weekly'?[...weekdays.querySelectorAll('input:checked')].map(x=>Number(x.value)):[],months:anchorValue==='completion'||kindValue==='once'?[]:[...months.querySelectorAll('input:checked')].map(x=>Number(x.value)),
     month_day:kindValue==='monthly'?Number(monthday.value):0,ends_on:elements.ends_on.value,dst_policy:'civil-v1/forward-gap/early-fold'},
    assignment_kind:assignmentKind.value,assignee_id:assignmentKind.value==='fixed'?assignee.value:'',members:[...roster.children].map(r=>({membership_id:r.choreMember.value,eligible:r.querySelector('[name=eligible]').checked})),
    checklist:[...checklist.children].map(r=>({key:r.dataset.key,title:r.querySelector('[name=check_title]').value,required:r.querySelector('[name=required]').checked})),
    prerequisite_ids:[...prerequisites.children].map(r=>r.chorePrerequisite.value),effective_on:elements.effective_on.value,catch_up_from:elements.catch_up_from.value,acknowledge_past:elements.acknowledge_past.checked};
  }
  invalidatePreview=()=>{review.choreCommand=null;apply.hidden=true;review.hidden=true;};
  form.addEventListener('input',event=>{if(event.target.name)invalidatePreview();});form.addEventListener('change',event=>{if(event.target.name)invalidatePreview();});
  formSubmit(form,async()=>{const previewDraft=draftSnapshot(),view=await request(api+'/series/preview',{body:command()});updateView(view);if(draftSnapshot()!==previewDraft){status(_householdText_chores_workspace_js("Eingaben geändert. Vorschau erneut prüfen."));return;}review.replaceChildren(node('h2',_householdText_chores_workspace_js("Vorschau")),node('p',previewExplanation(view)),facts([[_householdText_chores_workspace_js("Gültig ab"),format(view.effective_at)],[_householdText_chores_workspace_js("Betroffene offene Termine"),view.affected_open],[_householdText_chores_workspace_js("Zeitzone"),view.normalized.schedule.timezone]]));
   const list=node('ol');for(const slot of view.projection.slots)list.append(node('li',slot.day+' · '+slot.local_time+' '+slot.timezone+' · '+format(slot.due_at)+(slot.resolution==='exact'?'':_householdText_chores_workspace_js(" · Zeitumstellung: ")+slot.resolution)));review.append(list,node('p',_householdText_chores_workspace_js(view.note)));
   if(view.scheduling_paused)review.append(node('p',_householdText_chores_workspace_js("Die Serie bleibt pausiert. Jetzt entstehen keine neuen Termine. Beim Fortsetzen wird der nächste Termin mit dem tatsächlichen Pausenende neu berechnet."),'notice'));
   if(view.projection.more)review.append(node('p',_householdText_chores_workspace_js("Weitere Termine folgen außerhalb dieser begrenzten Vorschau.")));
   if(view.needs_past_confirmation&&!view.normalized.acknowledge_past){review.append(node('p',_householdText_chores_workspace_js("Beginn für ältere Rückstände ausdrücklich bestätigen und Vorschau erneut prüfen."),'chores-alert'));apply.hidden=true;}
   else{review.choreCommand=view.normalized;apply.hidden=false;}review.hidden=false;review.tabIndex=-1;review.focus();});
 }

 async function work(){
  const data=await request(api+'/occurrences/'+S.id);updateView(data);content.replaceChildren();filters.replaceChildren();const item=data.item,o=item.occurrence,d=item.detail,v=data.snapshot;
  S.query.area=v.area_id||'';navigationArea();
  heading(item.title,_householdText_chores_workspace_js("Arbeitsansicht · ")+(labels[o.state]||o.state));content.append(link(_householdText_chores_workspace_js("Zur Heute-Ansicht"),choresWorkspaceHref('today',S.query.area)));
  content.append(facts([[_householdText_chores_workspace_js("Fällig"),format(o.due_at)],[_householdText_chores_workspace_js("Ursprünglicher Termin"),format(d.original_due_at)],[_householdText_chores_workspace_js("Bereich"),v.area_name],[_householdText_chores_workspace_js("Zugewiesen"),item.assignee_name||_householdText_chores_workspace_js("Nicht zugewiesen")],
   [_householdText_chores_workspace_js("Zuständigkeitsgrund"),_householdText_chores_workspace_js(d.assignment_reason)],[_householdText_chores_workspace_js("Regel"),ruleText(v)],[_householdText_chores_workspace_js("Vorlagenstand"),v.revision],[_householdText_chores_workspace_js("Übernommen"),item.claim_active?item.claim_name+_householdText_chores_workspace_js(" bis ")+format(d.claim_expires_at):_householdText_chores_workspace_js("Keine aktuelle Übernahme")]]));
  if(v.legacy_capture)content.append(node('p',_householdText_chores_workspace_js("Historischer Altstand: ursprüngliche Person, Dauer oder damalige Anleitung können unbekannt sein.")));
  if(data.facts){content.append(facts([[_householdText_chores_workspace_js("Letzte tatsächliche Erledigung"),format(data.facts.last_completed_at)],[_householdText_chores_workspace_js("Nächster bereits geplanter Termin"),data.facts.next_open?format(data.facts.next_open.due_at):_householdText_chores_workspace_js("Noch kein weiterer offener Termin")]]));
   if(data.facts.completion)content.append(facts([[_householdText_chores_workspace_js("Tatsächlich erledigt am"),format(data.facts.completion.completed_at)],[_householdText_chores_workspace_js("Tatsächlich erledigt von"),data.facts.completion.snapshot.actor_display_name||_householdText_chores_workspace_js("Historische Person unbekannt")],[_householdText_chores_workspace_js("Tatsächliche Minuten"),data.facts.completion.actual_minutes]]));}
  if(data.origins?.length){content.append(node('h2',_householdText_chores_workspace_js("Herkunft")));for(const origin of data.origins)content.append(link(origin.label,origin.path));}
  content.append(node('h2',_householdText_chores_workspace_js("Anleitung")),node('p',d.overrides.description??v.description??_householdText_chores_workspace_js("Keine Anleitung hinterlegt."),'chores-instructions'));
  const material=node('ul');for(const text of v.materials||[])material.append(node('li',text));if(material.children.length)content.append(node('h2',_householdText_chores_workspace_js("Material")),material);
  const held=!item.series_active||item.series_paused&&new Date(o.due_at)>new Date(item.paused_at),mutable=S.canWrite&&o.state==='open'&&!held;
  if(held)content.append(node('p',item.series_active?_householdText_chores_workspace_js("Termin ist durch eine Serienpause ausgesetzt."):_householdText_chores_workspace_js("Serie ist archiviert."),'notice'));
  const checks=section(content,_householdText_chores_workspace_js("Unteraufgaben"));if(!data.checklist.length)checks.append(node('p',_householdText_chores_workspace_js("Keine Unteraufgaben hinterlegt.")));
  for(const part of data.checklist){const input=check(checks,part.title+(part.required?_householdText_chores_workspace_js(" · Pflicht"):''),'part-'+part.item_key,part.done);input.disabled=!mutable;
   input.addEventListener('change',()=>{const done=input.checked;if(S.busy||S.denied){input.checked=part.done;return;}run(async()=>{input.disabled=true;try{await mutate('/occurrences/'+S.id,{...base(o.version),action:'check',checklist_key:part.item_key,done},work);}catch(err){input.checked=part.done;throw err;}finally{if(input.isConnected)input.disabled=!mutable;}});});}
  content.append(node('h2',_householdText_chores_workspace_js("Voraussetzungen")));if(!data.dependencies.length)content.append(node('p',_householdText_chores_workspace_js("Keine Voraussetzungen.")));
  for(const dependency of data.dependencies){const r=row(dependency.title,dependency.occurrence_id?(labels[dependency.state]||dependency.state)+' · '+format(dependency.due_at):_householdText_chores_workspace_js("Noch kein passender Termin vorhanden"));
   if(dependency.occurrence_id)r.actions.append(link(_householdText_chores_workspace_js("Voraussetzung öffnen"),'/m/chores/workspace/work/'+dependency.occurrence_id));content.append(r.element);}
  content.append(link(_householdText_chores_workspace_js("Verlauf und tatsächlichen Aufwand ansehen"),choresWorkspaceHref('history',S.query.area)));
  if(!mutable)return;
  const actions=node('div',undefined,'actions');actions.append(button(item.claim_active?_householdText_chores_workspace_js("Übernahme freigeben"):_householdText_chores_workspace_js("Für vier Stunden übernehmen"),()=>mutate('/occurrences/'+S.id,{...base(o.version),action:item.claim_active?'release':'claim'},work)));
  actions.append(link(_householdText_chores_workspace_js("Zukünftige Serie bearbeiten"),'/m/chores/workspace/series/'+o.template_id));content.append(actions);
  const completion=node('form');completion.append(node('h2',_householdText_chores_workspace_js("Tatsächliche Erledigung")));field(completion,_householdText_chores_workspace_js("Tatsächliche Minuten · leer bedeutet unbekannt"),'actual_minutes','','number',{min:0,max:1440});
  const confirmed=check(completion,_householdText_chores_workspace_js("Die Aufgabe ist tatsächlich erledigt"),'confirmed');confirmed.required=true;const finish=submit(completion,_householdText_chores_workspace_js("Erledigung bestätigen"));finish.disabled=item.required_remaining>0||!item.prerequisites_met;
  if(finish.disabled)completion.append(node('p',_householdText_chores_workspace_js("Pflichtpunkte und Voraussetzungen zuerst erledigen."),'chores-alert'));content.append(completion);
  formSubmit(completion,()=>mutate('/occurrences/'+S.id,{...base(o.version),action:'complete',actual_minutes:completion.elements.actual_minutes.value===''?null:Number(completion.elements.actual_minutes.value)},work));
  const change=node('details'),summary=node('summary',_householdText_chores_workspace_js("Einzeltermin verschieben, ändern oder auslassen"));change.append(summary);content.append(change);
  const edit=node('form'),eg=node('div',undefined,'chores-form-grid');edit.append(eg);const local=new Date(o.due_at);let tz=d.timezone||data.timezone;
  try{new Intl.DateTimeFormat((globalThis.HouseholdI18n?.locale() || 'de-AT'),{timeZone:tz}).format(local);}catch{tz=data.timezone;change.append(node('p',_householdText_chores_workspace_js("Alte Zeitzone ist ungültig. Für eine Änderung ausdrücklich eine gültige Zeitzone prüfen."),'notice'));}
  const clock=new Intl.DateTimeFormat('en-GB',{hour:'2-digit',minute:'2-digit',hourCycle:'h23',timeZone:tz}).format(local);
  const snooze=node('form');snooze.append(node('h3',_householdText_chores_workspace_js("Nur den Termin verschieben")));
  field(snooze,_householdText_chores_workspace_js("Neues lokales Datum"),'due_on',localDay(local,tz),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});field(snooze,_householdText_chores_workspace_js("Lokale Uhrzeit"),'local_time',clock,'time',{required:true});field(snooze,(_householdText_chores_workspace_js('IANA-Zeitzone')),'timezone',tz,'text',{required:true,maxLength:100});submit(snooze,_householdText_chores_workspace_js("Verschiebung prüfen"));change.append(snooze);
  formSubmit(snooze,()=>previewDecision(snooze,{...base(o.version),action:'snooze',due_on:snooze.elements.due_on.value,local_time:snooze.elements.local_time.value,timezone:snooze.elements.timezone.value},_householdText_chores_workspace_js("Geprüften Termin verschieben")));
  field(eg,_householdText_chores_workspace_js("Nur diesen Titel ändern"),'title',item.title,'text',{required:true,maxLength:200});field(eg,_householdText_chores_workspace_js("Nur diese Anleitung ändern"),'description',d.overrides.description??v.description,'textarea',{maxLength:10000});
  field(eg,_householdText_chores_workspace_js("Neues lokales Datum"),'due_on',localDay(local,tz),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});field(eg,_householdText_chores_workspace_js("Uhrzeit"),'local_time',clock,'time',{required:true});field(eg,_householdText_chores_workspace_js("Zeitzone"),'timezone',tz,'text',{required:true,maxLength:100});
  const assignee=await reference(eg,_householdText_chores_workspace_js("Zuweisung für diesen Termin"),'assignee_id','core/memberships',o.assignee_id||'',item.assignee_name);submit(edit,_householdText_chores_workspace_js("Einzeländerung prüfen"));change.append(edit);
  formSubmit(edit,()=>previewDecision(edit,{...base(o.version),action:'edit_once',title:edit.elements.title.value,description:edit.elements.description.value,due_on:edit.elements.due_on.value,
   local_time:edit.elements.local_time.value,timezone:edit.elements.timezone.value,assignee_id:assignee.value},_householdText_chores_workspace_js("Geprüfte Einzeländerung speichern")));
  const skip=node('form');skip.append(node('p',_householdText_chores_workspace_js("Auslassen betrifft genau diesen Termin. Es zählt nicht als tatsächliche Erledigung. Ein Folgetermin bleibt nachvollziehbar.")));
  const skipConfirmed=check(skip,_householdText_chores_workspace_js("Genau diesen Termin auslassen"),'confirmed');skipConfirmed.required=true;submit(skip,_householdText_chores_workspace_js("Auslassen prüfen"));change.append(skip);formSubmit(skip,()=>previewDecision(skip,{...base(o.version),action:'skip'},_householdText_chores_workspace_js("Geprüften Termin auslassen")));
 }

 async function previewDecision(form,body,title){
  const preview=await request(api+'/occurrences/'+S.id+'/preview',{body});let panel=form.querySelector('.chores-preview');if(!panel){panel=node('section',undefined,'chores-preview');form.append(panel);form.addEventListener('input',()=>{panel.replaceChildren();});form.addEventListener('change',()=>{panel.replaceChildren();});}
  panel.replaceChildren(node('h3',_householdText_chores_workspace_js("Einzeltermin-Vorschau")),facts([[_householdText_chores_workspace_js("Bisher fällig"),format(preview.old_due_at)],[_householdText_chores_workspace_js("Ursprünglich fällig"),format(preview.original_due_at)],[_householdText_chores_workspace_js("Neuer Termin"),preview.new_due_at?format(preview.new_due_at):_householdText_chores_workspace_js("Unverändert / ausgelassen")],[_householdText_chores_workspace_js("Tatsächliche Erledigung"),_householdText_chores_workspace_js("Nein")]]),node('p',_householdText_chores_workspace_js(preview.note)));
  if(preview.resolution!=='unchanged'&&preview.resolution!=='exact')panel.append(node('p',_householdText_chores_workspace_js("Zeitumstellung: ")+preview.resolution));panel.append(button(title,()=>mutate('/occurrences/'+S.id,preview.normalized,work),true));panel.tabIndex=-1;panel.focus();
 }

 async function plan(){
  heading(_householdText_chores_workspace_js("Dein Tagesvorschlag"),_householdText_chores_workspace_js("Nach Priorität und Fälligkeit ausgewählt. Ohne automatische Zuweisung oder Erledigung."));
  const prefs=await request(api+'/preferences');updateView(prefs);const form=node('form'),grid=node('div',undefined,'chores-filter-grid');form.append(grid);
  field(grid,_householdText_chores_workspace_js("Tag"),'day',localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});field(grid,_householdText_chores_workspace_js("Verfügbares Minutenbudget"),'budget',prefs.preferences.daily_minutes,'number',{required:true,min:0,max:1440});submit(form,_householdText_chores_workspace_js("Tagesvorschlag berechnen"));filters.replaceChildren(form);
  const load=async()=>{const data=await request(api+'/plan?'+new URLSearchParams({day:form.elements.day.value,budget:form.elements.budget.value}));updateView(data);content.replaceChildren();const p=data.plan;
   content.append(facts([[_householdText_chores_workspace_js("Budget"),p.budget_minutes+_householdText_chores_workspace_js(" min")],[_householdText_chores_workspace_js("Vorgeschlagen"),p.selected_minutes+_householdText_chores_workspace_js(" min")],[_householdText_chores_workspace_js("Übrig"),p.remaining_minutes+_householdText_chores_workspace_js(" min")]]));
   const progress=node('progress');progress.max=Math.max(1,p.budget_minutes);progress.value=p.selected_minutes;progress.setAttribute('aria-label',_householdText_chores_workspace_js("Vorgeschlagenes Zeitbudget"));content.append(progress);
   for(const [title,items]of [[_householdText_chores_workspace_js("Vorgeschlagene Aufgaben"),p.selected],[_householdText_chores_workspace_js("Dauer noch unbekannt"),p.unestimated],[_householdText_chores_workspace_js("Nicht in diesem Vorschlag"),p.excluded]]){
    content.append(node('h2',title));if(!items.length)content.append(node('p',_householdText_chores_workspace_js("Keine Einträge.")));for(const choice of items){const r=row(choice.item.title,_householdText_chores_workspace_js(choice.reason));
     r.main.append(node('p',(choice.item.estimated_minutes==null?_householdText_chores_workspace_js("Dauer unbekannt"):choice.item.estimated_minutes+_householdText_chores_workspace_js(" min"))+_householdText_chores_workspace_js(" · Fällig ")+format(choice.item.due_at)));r.actions.append(link(_householdText_chores_workspace_js("Arbeitsansicht"),'/m/chores/workspace/work/'+choice.item.id));content.append(r.element);}
   }
  };formSubmit(form,load);await load();
 }

 async function historyPage(){
  heading(_householdText_chores_workspace_js("Erledigungsverlauf"),_householdText_chores_workspace_js("Tatsächliche Personen und Zeiten; unbekannte Altangaben bleiben unbekannt."));const form=node('form'),grid=node('div',undefined,'chores-filter-grid');form.append(grid);
  field(grid,_householdText_chores_workspace_js("Von · optional, sonst 30 Tage"),'from','','date',{min:'1900-01-01',max:'2200-12-31'});field(grid,_householdText_chores_workspace_js("Bis einschließlich"),'through',localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});
  const person=await reference(grid,_householdText_chores_workspace_js("Tatsächlich erledigt von"),'person','core/memberships');await areaSelect(grid,_householdText_chores_workspace_js("Bereich"),'area',S.query.area||'');submit(form,_householdText_chores_workspace_js("Verlauf anzeigen"));filters.replaceChildren(form);
  let query={through:form.elements.through.value,area:S.query.area||''};
  async function load(cursor=''){const data=await request(api+'/history?'+new URLSearchParams({...query,...(cursor?{cursor}:{})}));updateView(data);
   if(!cursor){content.replaceChildren();content.append(facts([[_householdText_chores_workspace_js("Erledigungen"),data.totals.completions],[_householdText_chores_workspace_js("Bekannte tatsächliche Minuten"),data.totals.known_minutes],[_householdText_chores_workspace_js("Dauer unbekannt"),data.totals.unknown_duration],[_householdText_chores_workspace_js("Tatsächliche Person unbekannt"),data.totals.unknown_actor]]));}
   if(!data.items.length&&!cursor)empty(_householdText_chores_workspace_js("Keine Erledigungen im Zeitraum"),_householdText_chores_workspace_js("Wähle andere Filter oder einen Zeitraum mit tatsächlichen Abschlüssen."));
   for(const entry of data.items){const r=row(entry.title,format(entry.completed_at)+' · '+(entry.actor_name||_householdText_chores_workspace_js("Historische Person unbekannt")));
    r.main.append(node('p',(entry.actual_minutes==null?_householdText_chores_workspace_js("Tatsächliche Dauer unbekannt"):entry.actual_minutes+_householdText_chores_workspace_js(" tatsächliche min"))+' · '+(entry.area_name||_householdText_chores_workspace_js("Ohne Bereich"))));
    r.actions.append(link(_householdText_chores_workspace_js("Eingefrorenen Arbeitsstand ansehen"),'/m/chores/workspace/work/'+entry.occurrence_id));content.append(r.element);}
   page(data,load);
  }formSubmit(form,async()=>{query={from:form.elements.from.value,through:form.elements.through.value,person:person.value,area:form.elements.area.value};S.query.area=query.area;navigationArea();await load();});await load();
 }

 async function settings(){
  const data=await request(api+'/preferences');updateView(data);const p=data.preferences;content.replaceChildren();filters.replaceChildren();heading(_householdText_chores_workspace_js("Aufgabeneinstellungen"),_householdText_chores_workspace_js("Persönliche Planung, ausdrückliche Benachrichtigungen und widerrufbare Kalenderfreigaben."));
  const form=node('form'),personal=section(form,_householdText_chores_workspace_js("Persönlicher Tagesplan")),pg=node('div',undefined,'chores-form-grid');personal.append(pg);
  field(pg,_householdText_chores_workspace_js("Tägliches Minutenbudget"),'daily_minutes',p.daily_minutes,'number',{required:true,min:0,max:1440});select(pg,_householdText_chores_workspace_js("Höchste Schwierigkeit im Vorschlag"),'max_difficulty',[['',_householdText_chores_workspace_js("Keine Grenze")],['1','1'],['2','2'],['3','3'],['4','4'],['5','5']],p.max_difficulty??'');
  const notifications=section(form,_householdText_chores_workspace_js("Benachrichtigungen")),ng=node('div',undefined,'chores-form-grid');notifications.append(ng);
  select(ng,_householdText_chores_workspace_js("Benachrichtigungsmodus"),'notification_mode',[['off',_householdText_chores_workspace_js("Aus")],['legacy',_householdText_chores_workspace_js("Bestehende Einzel-Erinnerungen")],['digest',_householdText_chores_workspace_js("Persönliche Zusammenfassung")]],p.notification_mode);
  field(ng,_householdText_chores_workspace_js("Zusammenfassung um"),'digest_clock',minuteClock(p.digest_minute),'time',{required:true});field(ng,(_householdText_chores_workspace_js('IANA-Zeitzone')),'timezone',p.timezone,'text',{required:true,maxLength:100});
  field(ng,_householdText_chores_workspace_js("Ruhezeit ab · optional"),'quiet_start',minuteClock(p.quiet_start),'time');field(ng,_householdText_chores_workspace_js("Ruhezeit bis · optional"),'quiet_end',minuteClock(p.quiet_end),'time');
  notifications.append(node('p',_householdText_chores_workspace_js("Neue verwaltete Serien verwenden Zusammenfassungen. Bestehende Einzel-Erinnerungen bleiben im Altmodus erhalten. Zusätzliche Gerätehinweise aktivierst du getrennt für dein Konto und deine Geräte.")));
  notifications.append(node('p',_householdText_chores_workspace_js("Diese Zeitzone und Ruhezeiten gelten auch für Gerätehinweise. Die Tagesübersicht folgt bei Zeitumstellungen der lokalen Uhrzeit. Im Modus Aus werden keine Aufgabenhinweise gesendet.")));
  notifications.append(link(_householdText_chores_workspace_js("Gerätehinweise für Aufgaben verwalten"),'/settings/notifications'));
  check(notifications,_householdText_chores_workspace_js("Ich erlaube ausdrücklich Eskalationen anderer Personen an mich"),'allow_escalations',p.allow_escalations);
  check(notifications,_householdText_chores_workspace_js("Länger überfällige eigene Aufgaben ausdrücklich eskalieren"),'escalation_enabled',p.escalation_enabled);
  field(notifications,_householdText_chores_workspace_js("Eskalation nach überfälligen Tagen"),'escalation_after_days',p.escalation_after_days,'number',{required:true,min:1,max:30});
  const recipient=await reference(notifications,_householdText_chores_workspace_js("Eskalation an · Empfänger muss zuvor zustimmen"),'escalation_recipient_id','core/memberships',p.escalation_recipient_id||'');
  submit(form,_householdText_chores_workspace_js("Persönliche Einstellungen speichern"));content.append(form);
  formSubmit(form,()=>{const e=form.elements;return mutate('/preferences',{...base(p.version),daily_minutes:Number(e.daily_minutes.value),max_difficulty:e.max_difficulty.value===''?null:Number(e.max_difficulty.value),
   notification_mode:e.notification_mode.value,digest_minute:clockMinute(e.digest_clock.value),quiet_start:clockMinute(e.quiet_start.value),quiet_end:clockMinute(e.quiet_end.value),timezone:e.timezone.value,
   escalation_enabled:e.escalation_enabled.checked,escalation_after_days:Number(e.escalation_after_days.value),escalation_recipient_id:recipient.value,allow_escalations:e.allow_escalations.checked},settings);});
  await absencesPanel();await calendarPanel();
 }
 function minuteClock(minute){return minute==null?'':String(Math.floor(minute/60)).padStart(2,'0')+':'+String(minute%60).padStart(2,'0');}
 function clockMinute(clock){if(!clock)return null;const [hour,minute]=clock.split(':').map(Number);return hour*60+minute;}
 async function absencesPanel(){
  const area=node('section');area.append(node('h2',_householdText_chores_workspace_js("Abwesenheiten und Vertretung")));content.append(area);
  area.append(node('p',_householdText_chores_workspace_js("Neue Termine berücksichtigen Abwesenheiten. Bereits zugewiesene oder übernommene Aufgaben wechseln nicht automatisch die Person.")));
  const rows=node('div'),paging=node('div',undefined,'actions'),editor=node('div');area.append(rows,paging,editor);
  async function load(cursor=''){const data=await request(api+'/absences?'+new URLSearchParams({cursor}));if(!cursor)rows.replaceChildren();
   for(const item of data.items){const r=row(item.member_name,item.starts_on+_householdText_chores_workspace_js(" bis ")+item.ends_on+(item.substitute_name?_householdText_chores_workspace_js(" · Vertretung: ")+item.substitute_name:_householdText_chores_workspace_js(" · Keine Vertretung")));
    if(S.canWrite)r.actions.append(button(_householdText_chores_workspace_js("Abwesenheit bearbeiten"),()=>edit(item)));rows.append(r.element);}
   if(!rows.children.length)rows.append(node('p',_householdText_chores_workspace_js("Keine aktuellen Abwesenheiten.")));paging.replaceChildren();if(data.next_cursor)paging.append(button(_householdText_chores_workspace_js("Weitere Abwesenheiten"),()=>load(data.next_cursor)));
  }
  async function edit(item={}){editor.replaceChildren();const form=node('form');editor.append(form);form.append(node('h3',item.id?_householdText_chores_workspace_js("Abwesenheit bearbeiten"):_householdText_chores_workspace_js("Abwesenheit erfassen")));
   const member=await reference(form,_householdText_chores_workspace_js("Abwesende Person"),'membership_id','core/memberships',item.membership_id||'',item.member_name);
   field(form,_householdText_chores_workspace_js("Von einschließlich"),'starts_on',item.starts_on||localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});field(form,_householdText_chores_workspace_js("Bis einschließlich"),'ends_on',item.ends_on||localDay(),'date',{required:true,min:'1900-01-01',max:'2200-12-31'});
   const substitute=await reference(form,_householdText_chores_workspace_js("Ausdrückliche Vertretung · optional"),'substitute_id','core/memberships',item.substitute_id||'',item.substitute_name);submit(form,_householdText_chores_workspace_js("Abwesenheit speichern"));form.append(button(_householdText_chores_workspace_js("Abbrechen"),async()=>editor.replaceChildren()));
   if(item.id){const confirm=check(form,_householdText_chores_workspace_js("Diese Abwesenheit ausdrücklich entfernen"),'remove');form.append(button(_householdText_chores_workspace_js("Bestätigte Abwesenheit entfernen"),async()=>{if(!confirm.checked)throw Error(_householdText_chores_workspace_js("Entfernen ausdrücklich bestätigen."));await mutate('/absences/'+item.id,{...base(item.version),delete:true},async()=>{editor.replaceChildren();await load();});}));}
   formSubmit(form,()=>mutate('/absences'+(item.id?'/'+item.id:''),{...base(item.version||0),membership_id:member.value,starts_on:form.elements.starts_on.value,ends_on:form.elements.ends_on.value,substitute_id:substitute.value,delete:false},async()=>{editor.replaceChildren();await load();}));
  }
  if(S.canWrite)area.prepend(button(_householdText_chores_workspace_js("Abwesenheit erfassen"),()=>edit()));await load();
 }
 async function calendarPanel(){
  const panel=node('section');panel.append(node('h2',_householdText_chores_workspace_js("Kalender")),node('p',_householdText_chores_workspace_js("Exportiert werden Titel und Termine. Anleitung, Material und Namen anderer Personen bleiben außerhalb der Kalenderdatei. Der geheime Feed-Link wird nur einmal angezeigt.")));
  const downloads=node('div',undefined,'actions');downloads.append(link(_householdText_chores_workspace_js("Meinen Kalender herunterladen"),api+'/calendar.ics?scope=mine&days_back=7&days_ahead=90'),link(_householdText_chores_workspace_js("Haushaltskalender herunterladen"),api+'/calendar.ics?scope=household&days_back=7&days_ahead=90'));panel.append(downloads);
  const form=node('form');select(form,_householdText_chores_workspace_js("Freigegebener Umfang"),'scope',[['mine',_householdText_chores_workspace_js("Nur mir zugewiesene und von mir übernommene Termine")],['household',_householdText_chores_workspace_js("Haushalt")]], 'mine');field(form,_householdText_chores_workspace_js("Vergangene Tage"),'days_back',7,'number',{required:true,min:0,max:31});field(form,_householdText_chores_workspace_js("Künftige Tage"),'days_ahead',90,'number',{required:true,min:1,max:90});
  const confirm=check(form,_householdText_chores_workspace_js("Ich behandle diesen Link wie einen privaten Zugangsschlüssel"),'confirmed');confirm.required=true;submit(form,_householdText_chores_workspace_js("Kalenderfreigabe erzeugen"));panel.append(form);const secret=node('div'),list=node('div');panel.append(secret,list);content.append(panel);
  async function load(){const data=await request(api+'/calendar-feeds');list.replaceChildren();for(const feed of data.items){const r=row(feed.scope==='mine'?_householdText_chores_workspace_js("Mein Kalender"):_householdText_chores_workspace_js("Haushaltskalender"),feed.active?_householdText_chores_workspace_js("Gültig bis ")+format(feed.expires_at):_householdText_chores_workspace_js("Abgelaufen oder widerrufen"));
    if(feed.active)r.actions.append(button(_householdText_chores_workspace_js("Freigabe widerrufen"),()=>mutate('/calendar-feeds/'+feed.id+'/revoke',{...base(feed.version),action:'revoke',scope:feed.scope,days_back:feed.days_back,days_ahead:feed.days_ahead},async()=>{secret.replaceChildren();await load();})));list.append(r.element);}
   if(data.more)list.append(node('p',_householdText_chores_workspace_js("Es werden die 100 neuesten Freigaben angezeigt. Alle aktiven Freigaben stehen zuerst.")));
  }
  formSubmit(form,()=>mutate('/calendar-feeds',{...base(),action:'create',scope:form.elements.scope.value,days_back:Number(form.elements.days_back.value),days_ahead:Number(form.elements.days_ahead.value)},async result=>{
   secret.replaceChildren();if(result.secret_unavailable)secret.append(node('p',_householdText_chores_workspace_js("Der erste Link ist nicht mehr verfügbar. Freigabe widerrufen und neu erzeugen."),'notice'));
   else{secret.append(node('h3',_householdText_chores_workspace_js("Privaten Kalender-Link jetzt übernehmen")));const input=field(secret,_householdText_chores_workspace_js("Nur einmal angezeigter Kalender-Link"),'secret',new URL(result.url,location.origin).href);input.readOnly=true;input.classList.add('chores-secret');secret.append(button(_householdText_chores_workspace_js("Link kopieren"),async()=>{await navigator.clipboard.writeText(input.value);status(_householdText_chores_workspace_js("Privater Link kopiert."));}));}
   await load();}));await load();
 }

 run(start);
})();
