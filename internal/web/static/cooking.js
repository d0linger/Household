'use strict';
const _householdText_cooking_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };

(function(){
  function remaining(timer,now){if(timer.state==='running'&&timer.deadline)return Math.max(0,Date.parse(timer.deadline)-now);if(timer.state==='paused')return Math.max(0,timer.remaining_millis||0);return 0;}
  function duration(ms){const seconds=Math.ceil(Math.max(0,ms)/1000);return[Math.floor(seconds/3600),Math.floor(seconds%3600/60),seconds%60].map(n=>String(n).padStart(2,'0')).join(':');}
  const uuid=/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/,scope=/^[0-9a-f]{64}$/;
  function freshTimer(timer,now){const due=Date.parse(timer?.deadline);return timer?.state==='running'&&Number.isFinite(now)&&Number.isFinite(due)&&now>=due&&now<due+60000;}
  function foregroundReference(data,request,current){
    if(!data||typeof data!=='object'||Array.isArray(data)||Object.keys(data).sort().join(',')!=='auth_scope,deadline,id,revision,server_now,session_id,timer_id'||
       !request||Object.keys(request).sort().join(',')!=='auth_scope,expires_at,id,type'||request.type!=='COOKING_PUSH_FOREGROUND'||!uuid.test(request.id)||!scope.test(request.auth_scope)||!Number.isSafeInteger(request.expires_at)||
       data.id!==request.id||data.auth_scope!==request.auth_scope||!uuid.test(data.session_id)||!uuid.test(data.timer_id)||!Number.isSafeInteger(data.revision)||data.revision<1||
       current?.state!=='active'||data.session_id!==current.id||!Array.isArray(current.timers))return null;
    const now=Date.parse(data.server_now),deadline=Date.parse(data.deadline),timer=current.timers.find(value=>value.id===data.timer_id);
    if(typeof data.server_now!=='string'||typeof data.deadline!=='string'||!Number.isFinite(now)||!Number.isFinite(deadline)||
       !timer||timer.version!==data.revision||Date.parse(timer.deadline)!==deadline||!freshTimer(timer,now)||
       request.expires_at*1000<=now||request.expires_at*1000>deadline+60000)return null;
    return timer;
  }
  function timerSound(AudioContext,visible,changed){
    let context=null,enabled=false,epoch=0,oscillator=null;
    function disable(state='off'){
      ++epoch;enabled=false;const prior=context;context=null;
      if(oscillator){try{oscillator.stop();}catch{}oscillator=null;}
      if(prior){prior.onstatechange=null;void prior.close().catch(()=>{});}changed(state);
    }
    function play(){
      if(!enabled||!visible()||context?.state!=='running')return false;
      if(oscillator)return true;
      try{
        const tone=context.createOscillator(),gain=context.createGain(),at=context.currentTime;oscillator=tone;
        tone.type='sine';tone.frequency.setValueAtTime(660,at);gain.gain.setValueAtTime(0,at);
        gain.gain.linearRampToValueAtTime(.12,at+.02);gain.gain.linearRampToValueAtTime(0,at+.28);
        tone.connect(gain);gain.connect(context.destination);
        tone.onended=()=>{tone.disconnect();gain.disconnect();if(oscillator===tone)oscillator=null;};tone.start(at);tone.stop(at+.3);return true;
      }catch{disable('unavailable');return false;}
    }
    async function enable(){
      if(!AudioContext||!visible()){changed('unavailable');return false;}
      disable();const mine=++epoch;
      try{
        const next=new AudioContext();context=next;await next.resume();
        if(mine!==epoch||!visible()||next.state!=='running'){if(context===next)disable('unavailable');return false;}
        enabled=true;next.onstatechange=()=>{if(context===next&&next.state!=='running')disable('unavailable');};
        changed('active');return play();
      }catch{if(mine===epoch)disable('unavailable');return false;}
    }
    return {enable,disable,play,get enabled(){return enabled;}};
  }
  function foregroundHandler(options){
    let active=null;
    const stop=()=>{active?.abort();active=null;};
    async function receive(event){
      const request=event.data,port=event.ports?.[0],controllerSource=options.controller();
      if(!controllerSource||event.source!==controllerSource||!port||request?.type!=='COOKING_PUSH_FOREGROUND'||Object.keys(request).sort().join(',')!=='auth_scope,expires_at,id,type'||!uuid.test(request.id)||!scope.test(request.auth_scope)||!Number.isSafeInteger(request.expires_at)||!options.visible()||active){port?.close();return;}
      const controller=new AbortController();active=controller;const original=options.session();let timeout;
      port.onmessage=message=>{if(message.data?.cancel===true)controller.abort();};
      try{
        const deadline=new Promise(resolve=>{timeout=setTimeout(()=>{controller.abort();resolve(null);},400);});
        const data=await Promise.race([options.read(request.id,controller.signal),deadline]);
        if(controller.signal.aborted||!options.visible()||options.session()!==original)return;
        const timer=foregroundReference(data,request,original);if(!timer)return;
        const handled=options.handle(timer,Date.parse(data.server_now));
        port.postMessage({handled:handled===true,id:request.id,auth_scope:request.auth_scope});
      }catch{/* Ordinary push remains the fallback for unavailable or uncertain state. */}
      finally{clearTimeout(timeout);controller.abort();if(active===controller)active=null;port.close();}
    }
    return {receive,stop};
  }
  if(typeof module!=='undefined'&&module.exports){module.exports={remaining,duration,freshTimer,foregroundReference,timerSound,foregroundHandler};return;}
  const root=document.querySelector('#cooking');if(!root)return;
  const $=selector=>root.querySelector(selector),kind=root.dataset.kind,rid=root.dataset.id,writable=root.dataset.writable==='true',generation=Number(root.dataset.generation);
  const csrf=()=>document.querySelector('meta[name="csrf-token"]').content;
  let session=null,source=null,busy=false,pending=null,confirmation=null,historyCursor='',finishTouched=false,portionsTouched=false,progressDirty=false,needsRefresh=false,wake=null,clockBase=null,locked=false,pageActive=true,clockTimer=null,loadEpoch=0;
  const announced=new Set(),audible=new Set();
  const foreground=()=>pageActive&&!locked&&!document.hidden&&document.hasFocus()&&session?.state==='active';
  const sound=timerSound(globalThis.AudioContext||globalThis.webkitAudioContext,foreground,state=>{
    $('#cooking-sound').setAttribute('aria-pressed',String(state==='active'));
    $('#cooking-sound').textContent=state==='active'?_householdText_cooking_js('Ton ausschalten'):_householdText_cooking_js('Ton aktivieren und testen');
    $('#cooking-sound-status').textContent=state==='active'?_householdText_cooking_js('Ton ist für diese sichtbare Sitzung aktiviert. Beim Verlassen wird er ausgeschaltet.'):state==='unavailable'?_householdText_cooking_js('Ton ist nicht verfügbar. Prüfe die Browserfreigabe; der sichtbare Timer bleibt nutzbar.'):_householdText_cooking_js('Ton ist aus. Aktiviere ihn nur, wenn du hier einen kurzen Hinweis hören möchtest.');
  });
  const status=message=>{$('#cooking-status').textContent=message;};
  function error(message){$('#cooking-error').textContent=message;$('#cooking-error').hidden=false;$('#cooking-error').focus();if(kind==='session')$('#cooking-refresh').hidden=false;}
  async function api(url,options){const response=await fetch(url,{cache:'no-store',credentials:'same-origin',...options});let data;try{data=await response.json();}catch{throw Error(_householdText_cooking_js("Antwort konnte nicht gelesen werden. Noch nicht bestätigte Eingaben bleiben erhalten."));}if(!response.ok){if(response.status===401||response.status===403)lockAccount();const failure=Error(data.error||_householdText_cooking_js("Anfrage fehlgeschlagen"));failure.status=response.status;throw failure;}return data;}
  function button(text,fn){const node=document.createElement('button');node.type='button';node.textContent=text;node.addEventListener('click',fn);return node;}
  function link(text,url){const a=document.createElement('a');a.textContent=text;a.href=url;return a;}
  function paragraph(text,className){const p=document.createElement('p');p.textContent=text;if(className)p.className=className;return p;}
  function setBusy(value){busy=value;for(const fieldset of root.querySelectorAll('fieldset'))fieldset.disabled=locked||value||needsRefresh||!writable||(fieldset.id==='cooking-controls'&&session?.state!=='active');$('#cooking-refresh').disabled=locked||value;$('#cooking-retry-start').disabled=locked||value;}
  function confirm(title,text,apply){if(busy)return;confirmation=apply;$('#cooking-confirm-title').textContent=title;$('#cooking-confirm-description').textContent=text;$('#cooking-confirm').showModal();$('#cooking-confirm-cancel').focus();}
  $('#cooking-confirm-cancel').addEventListener('click',()=>$('#cooking-confirm').close());
  $('#cooking-confirm').addEventListener('close',()=>{confirmation=null;});
  $('#cooking-confirm-yes').addEventListener('click',()=>{const apply=confirmation;confirmation=null;$('#cooking-confirm').close();if(apply)apply();});
  async function mutate(action,values={},focus){
    if(busy||locked||!writable)return false;
    const body=JSON.stringify({generation,version:session?.version||0,action,...values});
    if(!pending||pending.body!==body)pending={body,key:crypto.randomUUID()};
    setBusy(true);$('#cooking-error').hidden=true;let applied=false;
    try{
      const result=await api('/api/v1/cooking'+(session?'/'+session.id:''),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf(),'Idempotency-Key':pending.key},body});
      progressDirty=false;
      if(action==='start'){location.assign(result.url);return true;}
      if(action==='portions')portionsTouched=false;
      if(result.state!=='active')finishTouched=false;
      await loadSession();
      pending=null;
      applied=true;
      return true;
    }catch(e){if(action==='progress')progressDirty=true;if(!e.status||e.status===409||e.status>=500||progressDirty)needsRefresh=true;const help=kind==='source'?_householdText_cooking_js(" Prüfe den Start erneut oder öffne deine Sitzungen."):_householdText_cooking_js(" Lade den gespeicherten Stand und prüfe ihn: Nicht gespeicherte Schritte und Häkchen werden dabei ersetzt. Deine Portions- und Abschlussentwürfe bleiben erhalten.");error(e.message+(needsRefresh?help:''));if(kind==='source'&&needsRefresh)$('#cooking-start-recovery').hidden=false;return false;}
    finally{setBusy(false);if(applied&&focus)$(focus)?.focus();}
  }
  function sourceContent(bundle){const fragment=document.createDocumentFragment();fragment.append(paragraph(_householdText_cooking_js("Zutatenmengen für das Originalrezept mit ")+bundle.recipe.base_servings+_householdText_cooking_js(" Portionen."),'fine'),paragraph(bundle.recipe.summary||''));const ingredients=document.createElement('ul');for(const row of bundle.ingredients){const li=document.createElement('li');li.textContent=[row.section_name,row.quantity==null?_householdText_cooking_js("Menge offen"):row.quantity,row.unit_code==='piece'?_householdText_cooking_js("Stück"):row.unit_code,row.name,row.quantity_note,row.preparation_note].filter(Boolean).join(' · ');ingredients.append(li);}const steps=document.createElement('ol');for(const step of bundle.steps){const li=document.createElement('li');li.textContent=step.instruction;steps.append(li);}fragment.append(ingredients,steps);return fragment;}
  async function loadHistory(append=false){const data=await api('/api/v1/cooking/history'+(historyCursor?'?before='+encodeURIComponent(historyCursor):''));const list=$('#cooking-history');if(!append)list.replaceChildren();for(const record of data.items){const row=document.createElement('article');row.className='record-row';const a=link(record.title,'/m/recipes/cooking/history/'+record.id);a.className='record-main';a.append(paragraph(record.cooked_on+' · '+record.actual_servings+_householdText_cooking_js(" Portionen · ")+record.author+(record.rating?' · '+record.rating+'/5':''),'fine'));row.append(a);list.append(row);}if(!list.children.length)list.append(paragraph(_householdText_cooking_js("Noch keine Zubereitung bestätigt. Starte über ein Rezept und schließe die Sitzung anschließend bewusst ab.")));historyCursor=data.next;$('#cooking-more').hidden=!historyCursor;}
  async function overview(){const sessions=await api('/api/v1/cooking'),list=$('#cooking-sessions');list.replaceChildren();for(const row of sessions){const article=document.createElement('article');article.className='record-row';const a=link(row.title,'/m/recipes/cooking/'+row.id);a.className='record-main';a.append(paragraph(row.servings+_householdText_cooking_js(" Portionen · Schritt ")+(Number(row.current_step)+1)+_householdText_cooking_js(" von ")+row.steps,'fine'));article.append(a);list.append(article);}if(!sessions.length)list.append(paragraph(_householdText_cooking_js("Keine offene Kochsitzung. Wähle ein Rezept und öffne „Mit diesem Rezept kochen“.")));$('#cooking-overview').hidden=false;await loadHistory();status(_householdText_cooking_js("Deine Sitzungen und der gemeinsame Kochverlauf."));}
  function currentNow(){return clockBase?clockBase.time+performance.now()-clockBase.monotonic:Date.now();}
  function announceTimer(timer,now){
    const key=timer.id+':'+timer.version;
    if(!foreground()||!freshTimer(timer,now))return false;
    if(!announced.has(key)){
      announced.add(key);$('#cooking-announcement').textContent=_householdText_cooking_js('Timer „%s“ ist abgelaufen.',timer.name);
      if(sound.enabled&&sound.play())audible.add(key);
    }
    return audible.has(key);
  }
  function tick(){if(!session||locked||!pageActive)return;const now=currentNow();for(const timer of session.timers){const row=root.querySelector('[data-timer="'+timer.id+'"]');if(!row)continue;const left=remaining(timer,now),elapsed=timer.state==='running'&&left===0;row.querySelector('output').textContent=timer.state==='done'?_householdText_cooking_js("Bestätigt"):elapsed?_householdText_cooking_js("Abgelaufen"):duration(left)+(timer.state==='paused'?_householdText_cooking_js(" · Pausiert"):'');row.querySelector('[data-pause]').hidden=timer.state!=='running'||elapsed;row.querySelector('[data-resume]').hidden=timer.state!=='paused';row.querySelector('[data-acknowledge]').hidden=!elapsed;if(elapsed)announceTimer(timer,now);}}
  function timerRow(timer,savedDuration){
    const row=document.createElement('article');row.className='cooking-timer';row.dataset.timer=timer.id;const title=document.createElement('h3');title.textContent=timer.name;const output=document.createElement('output');output.setAttribute('aria-label',_householdText_cooking_js("Restzeit: ")+timer.name);output.setAttribute('aria-live','off');row.append(title,output);
    if(timer.deadline)row.append(paragraph(_householdText_cooking_js("Ende: ")+new Date(timer.deadline).toLocaleString((globalThis.HouseholdI18n?.locale() || 'de-AT')),'fine'));
    const actions=document.createElement('div');actions.className='actions';
    for(const[action,label]of [['pause',_householdText_cooking_js("Pausieren")],['resume',_householdText_cooking_js("Fortsetzen")],['acknowledge',_householdText_cooking_js("Ablauf bestätigen")]]){const control=button(label,()=>mutate(action,{timer_id:timer.id,timer_version:timer.version},'[data-timer="'+timer.id+"\"] button:not([hidden])"));control.dataset[action]='';actions.append(control);}
    const label=document.createElement('label');label.textContent=_householdText_cooking_js("Neue Dauer · Sekunden");const input=document.createElement('input');input.type='number';input.min='1';input.max='86400';input.value=savedDuration||String(timer.duration_seconds);input.dataset.duration=timer.id;label.append(input);
    const restart=button(_householdText_cooking_js("Mit dieser Dauer neu starten"),()=>{if(!input.reportValidity())return;const seconds=Number(input.value);confirm(_householdText_cooking_js("Timer neu starten?"),'„'+timer.name+_householdText_cooking_js("“ startet ab Bestätigung für ")+seconds+_householdText_cooking_js(" Sekunden. Die bisherige Restzeit wird ersetzt."),()=>mutate('restart',{timer_id:timer.id,timer_version:timer.version,seconds},'[data-timer="'+timer.id+"\"] button:not([hidden])"));});
    const remove=button(_householdText_cooking_js("Timer entfernen"),()=>confirm(_householdText_cooking_js("Timer entfernen?"),'„'+timer.name+_householdText_cooking_js("“ wird aus dieser Sitzung entfernt. Ein laufender Timer endet dadurch."),()=>mutate('timer_remove',{timer_id:timer.id,timer_version:timer.version},'#cooking-timer-form input')));
    row.append(actions,label,restart,remove);return row;
  }
  function checked(){return Array.from(root.querySelectorAll('[data-ingredient]:checked')).map(node=>node.dataset.ingredient);}
  function renderSession(){
    const currentKeys=new Set(session.timers.map(timer=>timer.id+':'+timer.version));for(const history of [announced,audible])for(const key of history)if(!currentKeys.has(key))history.delete(key);
    const active=session.state==='active';$('#cooking-title').textContent=session.source.recipe.title;$('#cooking-session').hidden=false;
    $('#cooking-source-version').textContent=_householdText_cooking_js("Rezeptstand ")+session.source_revision+_householdText_cooking_js(" · Persönliche Kochsitzung · ")+session.servings+_householdText_cooking_js(" Portionen");
    const form=$('#cooking-portions-form');if(!portionsTouched)form.elements.servings.value=session.servings;
    const step=session.source.steps[session.current_step];$('#cooking-step-title').textContent=_householdText_cooking_js("Schritt ")+(session.current_step+1)+_householdText_cooking_js(" von ")+session.source.steps.length;$('#cooking-step-title').tabIndex=-1;$('#cooking-instruction').textContent=step.instruction;
    $('#cooking-progress').max=session.source.steps.length;$('#cooking-progress').value=session.current_step+1;
    $('#cooking-previous').disabled=session.current_step===0;$('#cooking-next').disabled=session.current_step===session.source.steps.length-1;
    $('#cooking-step-timer').hidden=!step.timer_seconds;$('#cooking-step-timer').disabled=step.timer_seconds>86400;$('#cooking-step-timer').textContent=step.timer_seconds>86400?_householdText_cooking_js("Schrittdauer über 24 Stunden: kürzeren Timer unten wählen"):_householdText_cooking_js("Schritttimer starten · ")+(step.timer_seconds||0)+_householdText_cooking_js(" Sekunden");
    const ingredients=$('#cooking-ingredients');ingredients.replaceChildren();session.source.ingredients.forEach((ingredient,index)=>{const li=document.createElement('li'),label=document.createElement('label'),input=document.createElement('input'),text=document.createElement('span');input.type='checkbox';input.dataset.ingredient=ingredient.id;input.checked=session.checked_ids.includes(ingredient.id);const amount=session.quantities[index];text.textContent=[ingredient.section_name,ingredient.name].filter(Boolean).join(' · ');const quantity=amount?(amount.approximate&&!amount.display.startsWith('<')?'≈ ':'')+amount.display:_householdText_cooking_js("Menge offen");const detail=document.createElement('small');detail.textContent=[quantity,ingredient.unit_code==='piece'?_householdText_cooking_js("Stück"):ingredient.unit_code,ingredient.quantity_note,ingredient.preparation_note].filter(Boolean).join(' · ');text.append(detail);label.append(input,text);li.append(label);ingredients.append(li);input.addEventListener('change',()=>mutate('progress',{current_step:session.current_step,checked_ids:checked()},'[data-ingredient="'+ingredient.id+'"]'));});
    const durations=new Map(Array.from(root.querySelectorAll('[data-duration]')).map(input=>[input.dataset.duration,input.value]));$('#cooking-timers').replaceChildren(...session.timers.map(timer=>timerRow(timer,durations.get(timer.id))));if(!session.timers.length)$('#cooking-timers').append(paragraph(_householdText_cooking_js("Noch kein Timer gestartet. Nutze die Schrittdauer oder ergänze einen eigenen Timer.")));
    const finish=$('#cooking-finish-form');if(!finishTouched){finish.elements.actual_servings.value=session.servings;finish.elements.cooked_on.value=session.today;}finish.elements.cooked_on.max=session.today;
    $('#cooking-terminal').hidden=active;if(!active){$('#cooking-terminal').replaceChildren(paragraph(session.state==='completed'?_householdText_cooking_js("Zubereitung bestätigt. Alle Timer dieser Sitzung sind beendet."):_householdText_cooking_js("Sitzung verworfen. Es wurde keine Zubereitung in den Kochverlauf eingetragen.")));if(session.result_id)$('#cooking-terminal').append(link(_householdText_cooking_js("Bestätigten Verlauf ansehen"),'/m/recipes/cooking/history/'+session.result_id));releaseWake();sound.disable();}
    setBusy(busy);tick();status(active?_householdText_cooking_js("Fortschritt und Timer werden nach jeder bestätigten Aktion gespeichert."):_householdText_cooking_js("Diese Sitzung ist beendet."));
  }
  async function loadSession(){const mine=++loadEpoch,data=await api('/api/v1/cooking/'+rid);if(mine!==loadEpoch||locked||!pageActive)return;session=data;needsRefresh=false;clockBase={time:Date.parse(data.server_time),monotonic:performance.now()};renderSession();$('#cooking-refresh').hidden=true;progressDirty=false;}
  function stopAlerts(){sound.disable();foregroundReceiver.stop();clearInterval(clockTimer);clockTimer=null;}
  function startClock(){if(kind==='session'&&!clockTimer&&pageActive&&!locked&&!document.hidden)clockTimer=setInterval(tick,500);}
  function lockAccount(){
    if(locked)return;locked=true;++loadEpoch;stopAlerts();void releaseWake();session=null;clockBase=null;announced.clear();audible.clear();
    for(const selector of ['#cooking-session','#cooking-source','#cooking-overview','#cooking-record'])$(selector).hidden=true;
    $('#cooking-title').textContent=_householdText_cooking_js('Kochen');setBusy(false);
    status(_householdText_cooking_js('Die Anmeldung oder Gerätefreigabe wurde geändert. Öffne die Kochsitzung nach der Anmeldung erneut.'));
  }
  async function cookingContext(id,signal){
    const response=await fetch('/api/v1/core/push/deliveries/'+id+'/cooking-context',{credentials:'same-origin',cache:'no-store',redirect:'error',signal});
    if(response.status===401||response.status===403)lockAccount();
    if(!response.ok||response.redirected){void response.body?.cancel().catch(()=>{});return null;}
    const reader=response.body?.getReader();if(!reader)return null;
    const bytes=new Uint8Array(2048);let length=0;
    try{for(;;){const part=await reader.read();if(part.done)break;if(length+part.value.byteLength>bytes.length){void reader.cancel().catch(()=>{});return null;}bytes.set(part.value,length);length+=part.value.byteLength;}return JSON.parse(new TextDecoder().decode(bytes.subarray(0,length)));}
    finally{reader.releaseLock();}
  }
  const foregroundReceiver=foregroundHandler({controller:()=>navigator.serviceWorker?.controller,visible:foreground,session:()=>session,read:cookingContext,handle(timer,serverNow){
    clockBase={time:serverNow,monotonic:performance.now()};const handled=announceTimer(timer,serverNow);tick();return handled;
  }});
  navigator.serviceWorker?.addEventListener('message',foregroundReceiver.receive);
  async function releaseWake(){if(wake){const held=wake;wake=null;try{await held.release();}catch{}}$('#cooking-wake').textContent=_householdText_cooking_js("Bildschirm wach halten");}
  $('#cooking-sound').addEventListener('click',()=>{if(sound.enabled)sound.disable();else void sound.enable();});
  $('#cooking-wake').addEventListener('click',async()=>{if(wake){await releaseWake();$('#cooking-wake-status').textContent=_householdText_cooking_js("Bildschirm darf wieder ausgehen.");return;}try{if(!navigator.wakeLock)throw Error(_householdText_cooking_js("Dieser Browser unterstützt das Wachhalten nicht."));wake=await navigator.wakeLock.request('screen');$('#cooking-wake').textContent=_householdText_cooking_js("Wachhalten beenden");$('#cooking-wake-status').textContent=_householdText_cooking_js("Bildschirm bleibt während dieser sichtbaren Sitzung wach.");wake.addEventListener('release',()=>{wake=null;$('#cooking-wake').textContent=_householdText_cooking_js("Bildschirm wach halten");$('#cooking-wake-status').textContent=_householdText_cooking_js("Wachhalten wurde beendet. Bei Bedarf erneut einschalten.");});}catch(e){$('#cooking-wake-status').textContent=_householdText_cooking_js("Wachhalten nicht verfügbar: ")+e.message;}});
  $('#cooking-refresh').addEventListener('click',()=>{if(busy)return;loadSession().then(()=>{$('#cooking-error').hidden=true;status(_householdText_cooking_js("Gespeicherter Fortschritt geladen. Deine noch nicht bestätigten Abschlussangaben bleiben erhalten."));}).catch(e=>error(e.message));});
  $('#cooking-previous').addEventListener('click',()=>mutate('progress',{current_step:session.current_step-1,checked_ids:checked()},'#cooking-step-title'));
  $('#cooking-next').addEventListener('click',()=>mutate('progress',{current_step:session.current_step+1,checked_ids:checked()},'#cooking-step-title'));
  $('#cooking-step-timer').addEventListener('click',()=>mutate('timer_add',{timer_name:_householdText_cooking_js("Schritt ")+(session.current_step+1),seconds:session.source.steps[session.current_step].timer_seconds}));
  $('#cooking-portions-form').addEventListener('submit',event=>{event.preventDefault();mutate('portions',{servings:event.target.elements.servings.value});});
  $('#cooking-portions-form').addEventListener('input',()=>{portionsTouched=true;});
  $('#cooking-retry-start').addEventListener('click',()=>{if(pending&&kind==='source')mutate('start',JSON.parse(pending.body));});
  $('#cooking-timer-form').addEventListener('submit',async event=>{event.preventDefault();const form=event.target;if(await mutate('timer_add',{timer_name:form.elements.name.value,seconds:Number(form.elements.seconds.value)}))form.elements.name.value='';});
  $('#cooking-finish-form').addEventListener('input',()=>{finishTouched=true;});
  $('#cooking-finish-form').addEventListener('change',()=>{finishTouched=true;});
  $('#cooking-finish-form').addEventListener('submit',event=>{event.preventDefault();const form=event.target,values={actual_servings:form.elements.actual_servings.value,cooked_on:form.elements.cooked_on.value,rating:form.elements.rating.value?Number(form.elements.rating.value):null,notes:form.elements.notes.value};confirm(_householdText_cooking_js("Zubereitung verbindlich abschließen?"),values.actual_servings+_householdText_cooking_js(" Portionen · ")+values.cooked_on+_householdText_cooking_js("\nBewertung: ")+(values.rating||_householdText_cooking_js("keine Angabe"))+_householdText_cooking_js("\nNotizen: ")+(values.notes||(_householdText_cooking_js("keine")))+_householdText_cooking_js("\n\nDiese Angaben erscheinen im gemeinsamen Verlauf. Laufende Timer enden; Vorrat wird nicht abgebucht."),()=>mutate('finish',values));});
  $('#cooking-abandon').addEventListener('click',()=>confirm(_householdText_cooking_js("Kochsitzung verwerfen?"),_householdText_cooking_js("Fortschritt und Timer dieser Sitzung werden beendet. Nicht bestätigte Abschlussnotizen werden verworfen; es entsteht kein Eintrag im Kochverlauf."),()=>mutate('abandon')));
  $('#cooking-start-form').addEventListener('submit',event=>{event.preventDefault();mutate('start',{source_version_id:source.source_version_id,servings:event.target.elements.servings.value});});
  $('#cooking-more').addEventListener('click',async()=>{if(busy)return;busy=true;$('#cooking-more').disabled=true;try{await loadHistory(true);}catch(e){error(e.message);}finally{busy=false;$('#cooking-more').disabled=false;}});
  document.addEventListener('visibilitychange',()=>{if(document.hidden){stopAlerts();void releaseWake();return;}if(locked)return;startClock();if(kind==='session'&&!busy&&!progressDirty&&!$('#cooking-confirm').open)loadSession().catch(e=>error(e.message));});
  window.addEventListener('household-push-lock',lockAccount);
  window.addEventListener('offline',()=>{$('#cooking-connection').textContent=_householdText_cooking_js('Offline: Bestätigte Timer laufen anhand ihres gespeicherten Endes weiter. Neue oder geänderte Timer sind erst nach Serverbestätigung gespeichert.');});
  window.addEventListener('online',()=>{$('#cooking-connection').textContent=_householdText_cooking_js('Verbindung wieder verfügbar. Lade den gespeicherten Stand, bevor du unbestätigte Änderungen wiederholst.');if(kind==='session')$('#cooking-refresh').hidden=false;});
  window.addEventListener('pagehide',()=>{pageActive=false;++loadEpoch;stopAlerts();void releaseWake();root.hidden=true;});
  window.addEventListener('pageshow',event=>{if(event.persisted)location.reload();});
  window.addEventListener('beforeunload',event=>{if((finishTouched||portionsTouched||progressDirty)&&session?.state==='active'){event.preventDefault();event.returnValue='';}});
  startClock();
  (async()=>{try{if(kind==='overview')await overview();else if(kind==='source'){source=await api('/api/v1/cooking/source/'+rid);$('#cooking-title').textContent=source.source.recipe.title;$('#cooking-source-note').textContent=_householdText_cooking_js("Stand ")+source.source_revision+' · '+source.source.ingredients.length+_householdText_cooking_js(" Zutaten · ")+source.source.steps.length+_householdText_cooking_js(" Schritte");$('#cooking-source-content').replaceChildren(sourceContent(source.source));$('#cooking-edit-source').href='/m/recipes/recipes/'+rid+'/workspace';$('#cooking-start-form').elements.servings.value=source.source.recipe.base_servings;$('#cooking-source').hidden=false;if(!source.source.steps.length){$('#cooking-start-form fieldset').disabled=true;status(_householdText_cooking_js("Ergänze zuerst einen Zubereitungsschritt im Rezepteditor."));}else status(_householdText_cooking_js("Prüfe den Rezeptstand und wähle deine Portionen."));}else if(kind==='history'){const record=await api('/api/v1/cooking/history/'+rid);$('#cooking-title').textContent=record.source.recipe.title;$('#cooking-record-meta').textContent=record.cooked_on+' · '+record.actual_servings+_householdText_cooking_js(" Portionen · ")+record.author+_householdText_cooking_js(" · Rezeptstand ")+record.source_revision+_householdText_cooking_js(" · Bewertung: ")+(record.rating?record.rating+'/5':_householdText_cooking_js("keine Angabe"));$('#cooking-record-notes').textContent=record.notes||_householdText_cooking_js("Keine Kochnotizen gespeichert.");$('#cooking-record-source').replaceChildren(sourceContent(record.source));$('#cooking-record').hidden=false;status(_householdText_cooking_js("Bestätigte Zubereitung mit unverändertem Rezeptstand."));}else await loadSession();}catch(e){error(e.message);status(_householdText_cooking_js("Kochdaten konnten nicht vollständig geladen werden."));}})();
})();
