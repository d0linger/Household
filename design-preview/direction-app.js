import {designDirections,directionGroups,directionThemes} from './designDirections.js';
import {dialogs} from './dialog-fixtures.js';
import {escapeHTML as e,defaultValues,dialogMarkup,bindDialog} from './dialog-components.js';
const $=selector=>document.querySelector(selector);
const designs=new Map(designDirections.map(d=>[d.id,d]));
const fixtures=new Map(dialogs.map(d=>[d.id,d]));
const storageKey='household.design-directions.v2';
const state={theme:'light',fixture:'chores',favorites:[],shortlist:[],final:null,sync:true};
const drafts=new Map();let controllers=[];let opener=null;
function announce(message){$('#announcement').textContent=message;}
function storageWarning(message){$('#storage-warning').hidden=!message;$('#storage-warning').textContent=message;}
function save(){try{localStorage.setItem(storageKey,JSON.stringify({version:2,...state}));storageWarning('');}catch{storageWarning('Browserspeicher gesperrt. Die Auswahl bleibt in dieser Sitzung erhalten. Bitte als JSON exportieren.');}}
try{
  const raw=localStorage.getItem(storageKey);const saved=raw&&raw.length<=50000?JSON.parse(raw):null;
  if(saved?.version===2){
    if(directionThemes.includes(saved.theme))state.theme=saved.theme;
    if(fixtures.has(saved.fixture))state.fixture=saved.fixture;
    for(const [name,max]of [['favorites',100],['shortlist',4]])if(Array.isArray(saved[name]))state[name]=[...new Set(saved[name].filter(id=>designs.has(id)))].slice(0,max);
    if(designs.has(saved.final?.id)&&directionThemes.includes(saved.final?.theme))state.final={id:saved.final.id,theme:saved.final.theme};
    if(typeof saved.sync==='boolean')state.sync=saved.sync;
  }
}catch{storageWarning('Gespeicherte Auswahl nicht lesbar. Bitte nach deiner Auswahl einen JSON-Export sichern.');}
$('#group').insertAdjacentHTML('beforeend',directionGroups.map(group=>`<option value="${e(group)}">${e(group)}</option>`).join(''));
for(const selector of ['#fixture','#compare-fixture'])$(selector).innerHTML=dialogs.map(dialog=>`<option value="${dialog.id}">${e(dialog.label)}: ${e(dialog.title)}</option>`).join('');
function readURL(){
  const params=new URLSearchParams(location.search);
  $('#query').value=(params.get('q')||'').slice(0,100);
  $('#group').value=directionGroups.includes(params.get('group'))?params.get('group'):'all';
  $('#favorites-only').checked=params.get('favorites')==='1';
  if(directionThemes.includes(params.get('theme')))state.theme=params.get('theme');
  if(fixtures.has(params.get('dialog')))state.fixture=params.get('dialog');
}
function syncURL(){
  const params=new URLSearchParams();
  for(const [name,value]of [['q',$('#query').value],['group',$('#group').value==='all'?'':$('#group').value],['favorites',$('#favorites-only').checked?'1':''],['theme',state.theme],['dialog',state.fixture]])if(value)params.set(name,value);
  history.replaceState(null,'',`${location.pathname}?${params}${location.hash}`);
}
function theme(){document.documentElement.dataset.theme=state.theme;$('#theme').value=state.theme;$('#compare-theme').value=state.theme;}
function miniMarkup(design,fixture){
  return dialogMarkup(design,fixture,'mini').replace('class="world"','class="world world--mini" inert aria-hidden="true"');
}
function renderGallery(){
  const query=$('#query').value.trim().toLocaleLowerCase('de'),group=$('#group').value;
  const visible=designDirections.filter(d=>(group==='all'||d.group===group)&&(!$('#favorites-only').checked||state.favorites.includes(d.id))&&`${d.id} ${d.name} ${d.group} ${d.thesis} ${d.signature}`.toLocaleLowerCase('de').includes(query));
  const fixture=fixtures.get(state.fixture);
  $('#gallery').innerHTML=visible.map(d=>`<article class="design-card" data-card="${d.id}" aria-labelledby="card-${d.id}"><div class="design-meta"><div><h3 id="card-${d.id}">${e(d.name)}</h3><p>${d.id} / ${e(d.group)}</p></div><button type="button" class="favorite" data-favorite="${d.id}" aria-label="${e(d.name)} als Favorit" aria-pressed="${state.favorites.includes(d.id)}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h12v18l-6-4-6 4Z"/></svg></button></div>${miniMarkup(d,fixture)}<div class="design-actions"><button type="button" data-preview="${d.id}">Dialog öffnen</button><button type="button" data-shortlist="${d.id}" aria-pressed="${state.shortlist.includes(d.id)}">${state.shortlist.includes(d.id)?'Im Vergleich':'Vergleichen'}</button></div><p class="design-thesis">${e(d.thesis)}</p><p class="design-signature">${e(d.signature)}</p></article>`).join('');
  $('#results-title').textContent=`${visible.length} ${visible.length===1?'Designrichtung':'Designrichtungen'}`;
  $('#result-description').textContent=`Gemeinsamer Beispieldialog: ${fixture.title}. Jedes Design kann alle zehn Dialoge darstellen.`;
  $('#empty').hidden=visible.length!==0;$('#fixture').value=state.fixture;$('#compare-fixture').value=state.fixture;
  updateChoiceControls();syncURL();
}
function updateChoiceControls(){
  document.querySelectorAll('[data-shortlist]').forEach(button=>{
    const picked=state.shortlist.includes(button.dataset.shortlist);
    button.setAttribute('aria-pressed',String(picked));button.disabled=!picked&&state.shortlist.length===4;
    button.textContent=picked?'Im Vergleich':state.shortlist.length===4?'Vergleich voll':'Vergleichen';
  });
  $('#shortlist').innerHTML=state.shortlist.length?state.shortlist.map(id=>`<li><button type="button" data-remove="${id}" aria-label="${e(designs.get(id).name)} aus Vergleich entfernen">${e(designs.get(id).name)} <span aria-hidden="true">×</span></button></li>`).join(''):'<li class="empty-slot">Noch keine Designs ausgewählt.</li>';
  $('#compare').disabled=state.shortlist.length<2;$('#compare').textContent=state.shortlist.length>1?`${state.shortlist.length} Designs vergleichen`:'Dialogvergleich öffnen';
  $('#final-choice').textContent=state.final?`${designs.get(state.final.id).name} (${state.final.id}), gewählt im Modus ${state.final.theme}.`:'Noch keine endgültige Richtung gewählt.';
  $('#clear-final').disabled=!state.final;
  for(const id of ['export-choice','export-final'])$('#'+id).disabled=!state.final&&!state.shortlist.length&&!state.favorites.length;
  document.querySelectorAll('[data-final]').forEach(button=>{const selected=state.final?.id===button.dataset.final;button.setAttribute('aria-pressed',String(selected));button.textContent=selected?'Bevorzugtes Design':'Dieses Design wählen';});
}
let comparisonIDs=[];
function renderComparison(){
  const fixture=fixtures.get(state.fixture);const grid=$('#comparison-grid');grid.dataset.count=String(comparisonIDs.length);
  grid.innerHTML=comparisonIDs.map(id=>{
    const d=designs.get(id);return `<article class="comparison-column" data-column="${id}"><header class="comparison-heading"><div><h3>${e(d.name)}</h3><small>${id} / ${e(d.group)}</small></div></header>${dialogMarkup(d,fixture,'compare',drafts.get(`${id}/${fixture.id}`)||defaultValues(fixture))}<div class="comparison-choose"><button type="button" data-final="${id}" aria-pressed="${state.final?.id===id}">Dieses Design wählen</button><button type="button" data-code="${id}" aria-label="${e(d.name)} als HTML exportieren">Code exportieren</button></div></article>`;
  }).join('');
  controllers=[...grid.querySelectorAll('.world')].map(root=>({id:root.dataset.design,...bindDialog(root,fixture,values=>{
    drafts.set(`${root.dataset.design}/${fixture.id}`,values);
    if(state.sync)controllers.forEach(controller=>{if(controller.id!==root.dataset.design)controller.setValues(values);drafts.set(`${controller.id}/${fixture.id}`,{...values});});
  })}));
  if(state.sync&&controllers.length){const values=controllers[0].values();controllers.slice(1).forEach(controller=>controller.setValues(values));}
  $('#compare-fixture').value=state.fixture;$('#compare-theme').value=state.theme;$('#sync-inputs').checked=state.sync;updateChoiceControls();
}
function openComparison(ids,trigger){comparisonIDs=[...ids];opener=trigger;renderComparison();$('#compare-dialog').showModal();$('#close-compare').focus();}
function closeComparison(){$('#compare-dialog').close();const target=opener?.isConnected?opener:document.querySelector(`[data-preview="${comparisonIDs[0]}"]`)||$('#compare');target.focus();controllers=[];}
function setFixture(value){if(!fixtures.has(value))return;state.fixture=value;renderGallery();if($('#compare-dialog').open)renderComparison();save();}
function setTheme(value){if(!directionThemes.includes(value))return;state.theme=value;theme();save();syncURL();}
function download(name,content,type){const url=URL.createObjectURL(new Blob([content],{type})),link=document.createElement('a');link.href=url;link.download=name;document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
function exportChoice(){
  const selected=new Set([...state.shortlist,...state.favorites,...(state.final?[state.final.id]:[])]);
  download('household-design-direction.json',JSON.stringify({schema:'household-design-direction',version:2,exportedAt:new Date().toISOString(),previewOnly:true,currentTheme:state.theme,dialog:state.fixture,final:state.final,shortlist:state.shortlist,favorites:state.favorites,designs:[...selected].map(id=>designs.get(id))},null,2),'application/json');announce('Designentscheidung als JSON exportiert.');
}
async function exportCode(id,button){
  button.disabled=true;button.setAttribute('aria-busy','true');
  try{
    const files=await Promise.all(['tokens.css','base.css','directions.css','world.css'].map(async name=>{const response=await fetch(new URL(name,import.meta.url));if(!response.ok)throw new Error(name);return response.text();}));
    const directionCSS=files[2].split('\n').filter(line=>line.includes(`[data-design="${id}"]`)||line.includes(`--${id}-`)).join('\n');
    const design=designs.get(id),fixture=fixtures.get(state.fixture);
    const fixtureJSON=JSON.stringify(fixture).replaceAll('<','\\u003c');
    const html=`<!doctype html><html lang="de" data-theme="${state.theme}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${e(design.name)}: ${e(fixture.title)}</title><style>${files[0]}\n${files[1]}\n${directionCSS}\n${files[3]}\nmain{max-width:760px;margin:24px auto;padding:16px;container-type:inline-size}main>header{margin-bottom:24px;display:grid;gap:12px}.world h2{font:inherit}.export-label{display:grid;gap:8px}</style></head><body><main><header><h1>${e(design.name)}</h1><p>Beispieldialog, keine Haushaltsdaten oder Geräteverbindung.</p><label class="export-label">Farbmodus<select id="export-theme"><option value="light">Eigene Palette: Hell</option><option value="dark">Eigene Palette: Dunkel</option><option value="pink-light">Pink Light</option><option value="pink-dark">Pink Dark</option></select></label></header>${dialogMarkup(design,fixture,'export')}</main><script>const fixture=${fixtureJSON};(${bindDialog.toString()})(document.querySelector('.world'),fixture);const mode=document.getElementById('export-theme');mode.value=document.documentElement.dataset.theme;mode.addEventListener('change',()=>document.documentElement.dataset.theme=mode.value);<\/script></body></html>`;
    download(`household-${id}-${fixture.id}.html`,html,'text/html;charset=utf-8');announce('Eigenständiger Beispieldialog mit vier Farbmodi exportiert.');
  }catch{storageWarning('Codeexport fehlgeschlagen. Vorschau-Server prüfen und erneut versuchen.');announce('Codeexport fehlgeschlagen.');}
  finally{button.disabled=false;button.removeAttribute('aria-busy');}
}
document.addEventListener('click',event=>{
  const button=event.target.closest('button');if(!button)return;
  if(button.dataset.favorite){const id=button.dataset.favorite;state.favorites=state.favorites.includes(id)?state.favorites.filter(value=>value!==id):[...state.favorites,id];save();button.setAttribute('aria-pressed',String(state.favorites.includes(id)));if($('#favorites-only').checked){renderGallery();$('#favorites-only').focus();}updateChoiceControls();}
  if(button.dataset.shortlist){const id=button.dataset.shortlist;if(state.shortlist.includes(id))state.shortlist=state.shortlist.filter(value=>value!==id);else if(state.shortlist.length<4)state.shortlist.push(id);save();updateChoiceControls();announce(`${state.shortlist.length} von vier Vergleichsplätzen belegt.`);}
  if(button.dataset.remove){state.shortlist=state.shortlist.filter(value=>value!==button.dataset.remove);save();updateChoiceControls();$('#tray-title').setAttribute('tabindex','-1');$('#tray-title').focus();}
  if(button.dataset.preview)openComparison([button.dataset.preview],button);
  if(button.dataset.final){previousFinal=null;$('#clear-final').textContent='Entscheidung zurücknehmen';state.final={id:button.dataset.final,theme:state.theme};save();updateChoiceControls();announce(`${designs.get(button.dataset.final).name} als bevorzugte Richtung gewählt.`);}
  if(button.dataset.code)exportCode(button.dataset.code,button);
});
$('#compare').addEventListener('click',event=>openComparison(state.shortlist,event.currentTarget));
$('#close-compare').addEventListener('click',closeComparison);
$('#compare-dialog').addEventListener('cancel',event=>{event.preventDefault();closeComparison();});
$('#compare-fixture').addEventListener('change',event=>setFixture(event.target.value));
$('#fixture').addEventListener('change',event=>setFixture(event.target.value));
$('#theme').addEventListener('change',event=>setTheme(event.target.value));
$('#compare-theme').addEventListener('change',event=>setTheme(event.target.value));
$('#sync-inputs').addEventListener('change',event=>{
  state.sync=event.target.checked;save();
  if(state.sync&&controllers.length){const values=controllers[0].values();controllers.forEach(controller=>{controller.setValues(values);drafts.set(`${controller.id}/${state.fixture}`,{...values});});announce('Beispieleingaben anhand des ersten Designs synchronisiert.');}
});
$('#reset-dialog').addEventListener('click',()=>{const fixture=fixtures.get(state.fixture);controllers.forEach(controller=>drafts.delete(`${controller.id}/${state.fixture}`));renderComparison();announce('Alle Vergleichsformulare auf identische Beispieldaten zurückgesetzt.');});
$('#filters').addEventListener('submit',event=>event.preventDefault());let searchTimer;
$('#query').addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(renderGallery,120);});
$('#group').addEventListener('change',renderGallery);$('#favorites-only').addEventListener('change',renderGallery);
function resetFilters(){$('#query').value='';$('#group').value='all';$('#favorites-only').checked=false;renderGallery();}
$('#reset-filters').addEventListener('click',resetFilters);$('#empty-reset').addEventListener('click',()=>{resetFilters();$('#query').focus();});
$('#export-choice').addEventListener('click',exportChoice);$('#export-final').addEventListener('click',exportChoice);
let previousFinal=null;
$('#clear-final').addEventListener('click',()=>{if(previousFinal){state.final=previousFinal;previousFinal=null;}else{previousFinal=state.final;state.final=null;}save();updateChoiceControls();$('#clear-final').disabled=false;$('#clear-final').textContent=previousFinal?'Entscheidung wiederherstellen':'Entscheidung zurücknehmen';});
window.addEventListener('popstate',()=>{readURL();theme();renderGallery();if($('#compare-dialog').open)renderComparison();});
const mobile=matchMedia('(max-width:680px)');const adapt=()=>$('#filter-disclosure').open=!mobile.matches;adapt();mobile.addEventListener('change',adapt);
readURL();theme();renderGallery();save();
