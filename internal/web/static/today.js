'use strict';
const _householdText_today_js=(source,...args)=>{
  if(globalThis.HouseholdI18n)return globalThis.HouseholdI18n.t(source,...args);
  let i=0;return source.replace(/%%|%[sdv]/g,token=>token==='%%'?'%':String(args[i++]??''));
};
(function(){
  const tr=_householdText_today_js;
  function safeURL(value){
    if(typeof value!=='string'||!value.startsWith('/')||value.startsWith('//')||/[\\\u0000-\u001f\u007f]/.test(value))return '/notifications';
    try{const url=new URL(value,'https://household.invalid');return url.origin==='https://household.invalid'?url.pathname+url.search+url.hash:'/notifications';}catch{return '/notifications';}
  }
  function sectionModel(value,limit){
    if(!value||!['ready','disabled','error'].includes(value.state))return {state:'error',items:[],more:false};
    if(value.state!=='ready')return {state:value.state,items:[],more:false};
    if(!Array.isArray(value.items))return {state:'error',items:[],more:false};
    const items=value.items.slice(0,limit).filter(item=>item&&typeof item.title==='string'&&typeof item.url==='string').map(item=>({title:item.title.slice(0,1000),url:safeURL(item.url),meta:typeof item.meta==='string'?item.meta.slice(0,300):'',read:item.read===true}));
    return {state:'ready',items,more:value.more===true||value.items.length>limit};
  }
  function dayLabel(day,locale){
    if(typeof day!=='string'||!/^\d{4}-\d{2}-\d{2}$/.test(day))return '';
    const date=new Date(day+'T12:00:00Z');
    if(!Number.isFinite(date.getTime())||date.toISOString().slice(0,10)!==day)return '';
    return new Intl.DateTimeFormat(locale,{weekday:'long',day:'numeric',month:'long',timeZone:'UTC'}).format(date);
  }
  if(typeof module!=='undefined'&&module.exports){module.exports={safeURL,sectionModel,dayLabel};return;}
  const root=document.querySelector('#today');if(!root)return;
  const $=id=>document.getElementById(id),status=$('today-status'),refresh=$('today-refresh'),error=$('today-error');
  const descriptions={
    tasks:{limit:6,empty:()=>tr('Für dich sind heute keine offenen Aufgaben fällig.'),disabled:()=>tr('Die Aufgabenverwaltung ist in diesem Haushalt nicht aktiviert.')},
    notifications:{limit:6,empty:()=>tr('Keine aktuellen Mitteilungen für dich.'),disabled:()=>tr('Mitteilungen sind derzeit nicht verfügbar.')},
    cooking:{limit:3,empty:()=>tr('Keine offene Kochsitzung. Starte in einem Rezept, wenn du kochen möchtest.'),disabled:()=>tr('Das Kochbuch ist in diesem Haushalt nicht aktiviert.')},
    meals:{limit:6,empty:()=>tr('Für heute ist noch keine Mahlzeit geplant.'),disabled:()=>tr('Die Essensplanung ist in diesem Haushalt nicht aktiviert.')}
  };
  let active=null,sequence=0;
  function paragraph(text,className){const p=document.createElement('p');p.textContent=text;p.className=className;return p;}
  function clearSections(message){for(const name of Object.keys(descriptions)){const target=$('today-'+name);target.replaceChildren(paragraph(message,'today-empty'));target.setAttribute('aria-busy','false');}}
  function renderSection(name,value){
    const settings=descriptions[name],model=sectionModel(value,settings.limit),target=$('today-'+name);target.replaceChildren();target.setAttribute('aria-busy','false');
    if(model.state==='error'){const message=paragraph(tr('Dieser Bereich konnte nicht geladen werden. Aktualisiere die Übersicht, um es erneut zu versuchen.'),'today-empty');message.classList.add('today-partial');target.append(message);return false;}
    if(model.state==='disabled'){target.append(paragraph(settings.disabled(),'today-empty'));return true;}
    if(!model.items.length){target.append(paragraph(settings.empty(),'today-empty'));return true;}
    const list=document.createElement('ul');list.className='today-list';
    for(const item of model.items){const li=document.createElement('li'),a=document.createElement('a'),text=document.createElement('span'),title=document.createElement('span');a.href=item.url;text.className='today-item-text';title.className='today-item-title';title.textContent=item.title;text.append(title);
      if(name==='notifications'&&!item.read){const dot=document.createElement('span');dot.className='today-unread';dot.setAttribute('aria-hidden','true');a.append(dot);const unread=document.createElement('span');unread.className='sr-only';unread.textContent=tr('Ungelesen')+' · ';title.prepend(unread);}
      if(item.meta){const meta=document.createElement('span');meta.className='today-item-meta';meta.textContent=item.meta;text.append(meta);}
      const arrow=document.createElement('span');arrow.textContent='→';arrow.setAttribute('aria-hidden','true');a.append(text,arrow);li.append(a);list.append(li);}
    target.append(list);if(model.more)target.append(paragraph(tr('Weitere Einträge findest du im jeweiligen Bereich.'),'today-more'));return true;
  }
  async function load(){
    if(active)active.abort();const controller=new AbortController();active=controller;const request=++sequence;
    refresh.disabled=true;error.hidden=true;$('today-login').hidden=true;clearSections(tr('Wird geladen …'));for(const name of Object.keys(descriptions))$('today-'+name).setAttribute('aria-busy','true');status.textContent=tr('Deine Übersicht wird geladen …');
    $('today-date').textContent=tr('Der Haushaltstag wird geladen …');
    const timer=setTimeout(()=>controller.abort(),12000);
    try{const response=await fetch('/api/v1/core/today',{credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'},signal:controller.signal});
      if(request!==sequence)return;
      if(!response.ok||response.redirected){$('today-login').hidden=!(response.status===401||response.status===403||response.redirected);throw new Error('request');}
      const data=await response.json();if(request!==sequence)return;
      const label=dayLabel(data.day,document.documentElement.lang||'de');$('today-date').textContent=label?label+' · '+String(data.timezone||''):tr('Der Haushaltstag ist gerade nicht verfügbar.');
      let complete=Boolean(label);for(const name of Object.keys(descriptions))if(!renderSection(name,data[name]))complete=false;
      status.textContent=complete?tr('Aktueller Stand geladen.'):tr('Ein Teil der Übersicht ist nicht verfügbar. Die übrigen Bereiche sind aktuell.');
    }catch{if(request!==sequence)return;clearSections(tr('Keine aktuellen Daten geladen.'));$('today-date').textContent=tr('Der Haushaltstag ist gerade nicht verfügbar.');error.hidden=false;status.textContent=tr('Die Übersicht konnte nicht aktualisiert werden.');}
    finally{clearTimeout(timer);if(request===sequence){refresh.disabled=false;active=null;}}
  }
  function pause(){sequence++;if(active)active.abort();active=null;refresh.disabled=false;clearSections(tr('Die Übersicht wird beim Zurückkehren neu geladen.'));status.textContent='';$('today-date').textContent='';error.hidden=true;}
  refresh.addEventListener('click',load);
  window.addEventListener('pagehide',pause);
  window.addEventListener('pageshow',event=>{if(event.persisted)load();});
  document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='hidden')pause();else load();});
  load();
})();
