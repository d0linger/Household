(() => {
 'use strict';
 const form=document.querySelector('#module-order-form');if(!form)return;
 const t=(source,...args)=>window.HouseholdI18n.t(source,...args);
 const list=form.querySelector('#module-order-list'),status=form.querySelector('#module-order-status'),error=form.querySelector('#module-order-error');
 const rows=()=>Array.from(list.children),order=()=>rows().map(row=>row.dataset.moduleId);
 const controls=Array.from(form.querySelectorAll('button'));
 let saved=order().join(','),saving=false,dragged=null;
 function sync(){
  const items=rows();items.forEach((row,index)=>{row.querySelector('[data-order-up]').disabled=saving||index===0;row.querySelector('[data-order-down]').disabled=saving||index===items.length-1;row.querySelector('[data-order-drag]').draggable=!saving;});
  form.querySelector('button.primary').disabled=saving||order().join(',')===saved;
 }
 function changed(){sync();error.hidden=true;status.hidden=false;status.textContent=t('Reihenfolge geändert, noch nicht gespeichert.');}
 function move(row,direction,focus){
  if(saving)return;
  const sibling=direction<0?row.previousElementSibling:row.nextElementSibling;if(!sibling)return;
  if(direction<0)list.insertBefore(row,sibling);else list.insertBefore(sibling,row);
  changed();const target=focus?.disabled?row.querySelector('[data-order-drag]'):focus;target?.focus({preventScroll:true});
  status.textContent=t('%s: Position %s von %s. Noch nicht gespeichert.',row.dataset.moduleLabel,rows().indexOf(row)+1,rows().length);
 }
 rows().forEach(row=>{
  row.querySelector('[data-order-up]').addEventListener('click',event=>move(row,-1,event.currentTarget));
  row.querySelector('[data-order-down]').addEventListener('click',event=>move(row,1,event.currentTarget));
  row.querySelector('[data-order-drag]').addEventListener('keydown',event=>{if(event.key==='ArrowUp'||event.key==='ArrowDown'){event.preventDefault();move(row,event.key==='ArrowUp'?-1:1,event.currentTarget);}});
  row.querySelector('[data-order-drag]').addEventListener('dragstart',event=>{if(saving){event.preventDefault();return;}dragged=row;event.dataTransfer.effectAllowed='move';event.dataTransfer.setData('text/plain',row.dataset.moduleId);row.classList.add('order-dragging');});
  row.addEventListener('dragover',event=>{if(dragged&&!saving){event.preventDefault();event.dataTransfer.dropEffect='move';}});
  row.addEventListener('drop',event=>{if(!dragged||saving)return;event.preventDefault();if(dragged!==row){const bounds=row.getBoundingClientRect();list.insertBefore(dragged,event.clientY<bounds.top+bounds.height/2?row:row.nextElementSibling);changed();dragged.querySelector('[data-order-drag]').focus({preventScroll:true});}});
  row.addEventListener('dragend',()=>{dragged?.classList.remove('order-dragging');dragged=null;});
 });
 function applyOrder(ids){
  const items=rows();if(!Array.isArray(ids)||ids.length!==items.length||new Set(ids).size!==ids.length||ids.some(id=>!items.some(row=>row.dataset.moduleId===id)))return false;
  const ordered=ids.map(id=>items.find(row=>row.dataset.moduleId===id));list.append(...ordered);
  for(const navigation of document.querySelectorAll('.sidebar nav,.mobile-nav')){
   const links=Array.from(navigation.querySelectorAll('a[data-module-id]'));
   navigation.append(...ids.map(id=>links.find(link=>link.dataset.moduleId===id)).filter(Boolean));navigation.scrollLeft=0;
  }
  saved=order().join(',');return true;
 }
 async function save(reset){
  if(saving)return;const submitted=reset?[]:order(),focusOwner=document.activeElement;let succeeded=false;saving=true;controls.forEach(button=>button.disabled=true);sync();form.setAttribute('aria-busy','true');error.hidden=true;status.hidden=false;status.textContent=t('Reihenfolge wird gespeichert …');
  const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),15000);
  try{
   const response=await fetch('/api/v1/core/preferences',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},body:JSON.stringify({module_order:submitted}),signal:controller.signal});
   const body=await response.json().catch(()=>({}));
   if(!response.ok)throw Error(body.error||t('Reihenfolge konnte nicht gespeichert werden. Bitte erneut versuchen.'));
   succeeded=true;
   if(!applyOrder(body.effective_module_order)){status.textContent=t('Reihenfolge gespeichert. Lade die Seite neu, um sie anzuzeigen.');return;}
   status.textContent=reset?t('Standardreihenfolge wiederhergestellt.'):t('Deine Modulreihenfolge wurde gespeichert.');
  }catch(failure){status.hidden=true;error.hidden=false;error.textContent=failure.name==='AbortError'?t('Speichern hat zu lange gedauert. Bitte erneut versuchen.'):failure.message;}
  finally{clearTimeout(timeout);saving=false;form.removeAttribute('aria-busy');controls.forEach(button=>button.disabled=false);sync();if(form.contains(focusOwner)&&(document.activeElement===document.body||document.activeElement===focusOwner))(succeeded&&!reset?status:focusOwner).focus({preventScroll:true});}
 }
 form.addEventListener('submit',event=>{event.preventDefault();void save(false);});
 form.querySelector('[data-order-reset]').addEventListener('click',()=>void save(true));sync();
})();
