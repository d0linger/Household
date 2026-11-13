const _householdText_shopping_notifications_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root)=>{
 'use strict';
 const U=typeof module==='object'&&module.exports?require('./shopping-extension-ui.js'):root.HouseholdShoppingUI;
 function list(value){U.keys(value,['id','name','archived','is_template']);if(!U.UUID.test(value.id)||!U.text(value.name)||typeof value.archived!=='boolean'||typeof value.is_template!=='boolean')throw U.error('FORMAT',_householdText_shopping_notifications_js("Listenfreigabe ist unvollständig."));return value;}
 function preference(value){
  if(value===null)return null;
  U.keys(value,['enabled','push_enabled','muted_until','enabled_since','push_enabled_since']);
  if(value.enabled!==true||typeof value.push_enabled!=='boolean'||!Number.isFinite(Date.parse(value.enabled_since)))throw U.error('FORMAT',_householdText_shopping_notifications_js("Persönlicher Hinweisstatus ist unbekannt."));
  for(const field of ['muted_until','push_enabled_since'])if(value[field]!==null&&!Number.isFinite(Date.parse(value[field])))throw U.error('FORMAT',_householdText_shopping_notifications_js("Zeitpunkt der Freigabe ist unbekannt."));
  if(value.push_enabled!==(value.push_enabled_since!==null))throw U.error('FORMAT',_householdText_shopping_notifications_js("Gerätewunsch und Freigabezeitpunkt passen nicht zusammen."));
  return value;
 }
 function view(value){U.keys(value,['context','list','revision','preference']);U.context(value.context);list(value.list);if(!U.version(value.revision))throw U.error('FORMAT',_householdText_shopping_notifications_js("Hinweisrevision fehlt."));preference(value.preference);return value;}
 function overview(value){U.keys(value,['context','revision','lists']);U.context(value.context);if(!U.version(value.revision)||!Array.isArray(value.lists)||value.lists.length>20)throw U.error('FORMAT',_householdText_shopping_notifications_js("Hinweisübersicht ist unvollständig."));const ids=new Set();for(const item of value.lists){U.keys(item,['list','preference']);list(item.list);preference(item.preference);if(ids.has(item.list.id))throw U.error('FORMAT',_householdText_shopping_notifications_js("Liste ist doppelt enthalten."));ids.add(item.list.id);}return value;}
 function muted(value,now){return !!value?.muted_until&&Date.parse(value.muted_until)>now;}
 function input(current,{enabled,push_enabled,mute}){
  view(current);if(typeof enabled!=='boolean'||typeof push_enabled!=='boolean')throw U.error('FORMAT',_householdText_shopping_notifications_js("Hinweiswunsch ausdrücklich wählen."));
  if(enabled&&(current.list.archived||current.list.is_template))throw U.error('INACTIVE',_householdText_shopping_notifications_js("Nur aktive Einkaufslisten können neu beobachtet werden."));
  if(enabled&&!['0','3600','86400','604800'].includes(mute))throw U.error('MUTE',_householdText_shopping_notifications_js("Für die bestehende Pause ausdrücklich eine Änderung wählen. Eine neue Pause beginnt mit dem bestätigten Speichern."));
  return {generation:current.context.generation,revision:current.revision,enabled,push_enabled:enabled&&push_enabled,mute_for_seconds:enabled?Number(mute):0};
 }
 const pure={list,preference,view,overview,muted,input};if(typeof module==='object'&&module.exports){module.exports=pure;return;}if(!U||!root.document)return;
 function watch(panel){
  const $=selector=>panel.querySelector(selector),id=panel.dataset.shoppingWatch,form=$('[data-watch-form]'),fields=$('[data-watch-fields]'),status=$('[data-watch-status]'),alert=$('[data-watch-error]');let current=null,busy=false,unknown=false,client;
  function message(text){status.textContent=text;}
  function lock(){current=null;form.hidden=true;fields.disabled=true;message(_householdText_shopping_notifications_js("Anmeldung oder Freigabe geändert. Aktuellen Hinweisstatus erneut laden."));}
  function fresh(){client?.dispose();client=U.client(panel,{onLock:lock,status:message});}
  fresh();
  function render(value){
   current=view(value);const pref=current.preference,now=Date.parse(current.context.server_time),paused=muted(pref,now);
   $('[data-watch-enabled]').checked=!!pref;$('[data-watch-push]').checked=!!pref?.push_enabled;$('[data-watch-mute]').value=paused?'':'0';
   $('[data-watch-effective]').textContent=current.list.archived||current.list.is_template?_householdText_shopping_notifications_js("Liste archiviert oder Vorlage: keine neuen Inhaltsmitteilungen."):paused?_householdText_shopping_notifications_js("Stumm bis ")+U.date(pref.muted_until)+_householdText_shopping_notifications_js(" (Gerätezeit). Die Pause wird nur durch ausdrückliche Auswahl geändert."):pref?_householdText_shopping_notifications_js("In-App-Hinweise sind aktiv. Gerätewunsch benötigt zusätzlich Serverkonfiguration und diese Browserverbindung."):_householdText_shopping_notifications_js("Keine persönlichen Listenhinweise aktiviert.");
   fields.disabled=false;form.hidden=false;unknown=false;alert.hidden=true;message(_householdText_shopping_notifications_js("Persönlicher Serverstand geprüft. Änderungen gelten erst nach bestätigtem Speichern."));
  }
  async function load(){if(busy)return;busy=true;fields.disabled=true;try{fresh();await client.refresh();const data=await client.request('/api/v1/shopping/lists/'+id+'/notification-preferences?generation='+client.current.generation);if(!U.sameScope(U.scope(data.context),U.scope(client.current)))throw U.error('AUTH',_householdText_shopping_notifications_js("Hinweisantwort gehört zu einer früheren Freigabe."));render(data);}catch(failure){lock();alert.textContent=failure.message;alert.hidden=false;}finally{busy=false;}}
  $('[data-watch-load]').addEventListener('click',()=>void load());
  form.addEventListener('submit',async event=>{
   event.preventDefault();if(busy||unknown||!current)return;
   let body;try{body=input(current,{enabled:$('[data-watch-enabled]').checked,push_enabled:$('[data-watch-push]').checked,mute:$('[data-watch-mute]').value});}catch(failure){alert.textContent=failure.message;alert.hidden=false;alert.focus();return;}
   busy=true;fields.disabled=true;alert.hidden=true;
   try{await client.csrf();if(!U.sameScope(U.scope(current.context),U.scope(client.current)))throw U.error('AUTH',_householdText_shopping_notifications_js("Hinweise mit aktueller Anmeldung neu laden."));const result=await client.request('/api/v1/shopping/lists/'+id+'/notification-preferences',{method:'PUT',body});if(!U.sameScope(U.scope(result.context),U.scope(client.current)))throw U.error('AUTH',_householdText_shopping_notifications_js("Antwort gehört nicht mehr zur aktuellen Anmeldung."));render(result);message(_householdText_shopping_notifications_js("Persönliche Hinweise vom Server bestätigt. Eine Geräteanzeige ist damit nicht bestätigt."));}
   catch(failure){unknown=true;alert.textContent=failure.message+_householdText_shopping_notifications_js(" Aktuellen Hinweisstatus erneut laden, bevor du eine weitere Änderung vornimmst.");alert.hidden=false;alert.focus();message(_householdText_shopping_notifications_js("Speicherergebnis nicht bestätigt. Keine automatische Wiederholung."));}
   finally{busy=false;if(!unknown&&current)fields.disabled=false;}
  });
  root.addEventListener('focus',()=>{if(current&&!busy)void client.refresh().catch(()=>lock());});
 }
 function ownOverview(panel){
  const status=panel.querySelector('[data-watch-overview-status]'),rows=panel.querySelector('[data-watch-overview-rows]');let client,busy=false;
  const lock=()=>{rows.replaceChildren();rows.hidden=true;status.textContent=_householdText_shopping_notifications_js("Hinweisübersicht neu prüfen.");};
  panel.querySelector('[data-watch-overview-load]').addEventListener('click',async()=>{
   if(busy)return;busy=true;lock();client?.dispose();client=U.client(panel,{onLock:lock});
   try{await client.refresh();const result=overview(await client.request('/api/v1/shopping/notification-preferences?generation='+client.current.generation));if(!U.sameScope(U.scope(result.context),U.scope(client.current)))throw U.error('AUTH',_householdText_shopping_notifications_js("Übersicht gehört zu einer früheren Anmeldung."));
    for(const entry of result.lists){const row=U.node('article',undefined,'se-row'),link=U.node('a',entry.list.name);link.href='/m/shopping/lists/'+entry.list.id;row.append(link,U.node('p',muted(entry.preference,Date.parse(result.context.server_time))?_householdText_shopping_notifications_js("Stumm bis ")+U.date(entry.preference.muted_until):entry.preference?.push_enabled?_householdText_shopping_notifications_js("In-App aktiv; zusätzlicher Gerätewunsch gespeichert."):_householdText_shopping_notifications_js("In-App-Hinweise aktiv.")));rows.append(row);}if(!result.lists.length)rows.append(U.node('p',_householdText_shopping_notifications_js("Keine aktiven eigenen Listenhinweise.")));rows.hidden=false;status.textContent=_householdText_shopping_notifications_js("Aktueller persönlicher Serverstand. Geräteverbindungen werden separat verwaltet.");
   }catch(failure){lock();status.textContent=failure.message;}finally{busy=false;}
  });
  root.addEventListener('focus',()=>{if(client?.current&&!busy)void client.refresh().catch(lock);});
 }
 for(const panel of document.querySelectorAll('[data-shopping-watch]'))watch(panel);
 for(const panel of document.querySelectorAll('[data-shopping-watch-overview]'))ownOverview(panel);
 root.HouseholdShoppingNotifications=pure;
})(globalThis);
