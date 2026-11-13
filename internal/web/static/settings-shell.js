(() => {
 'use strict';
 const t=(source,...args)=>window.HouseholdI18n.t(source,...args);
 const form=document.querySelector('#appearance-form'),dialog=document.querySelector('#appearance-dialog');
 if(form&&dialog){
  const status=document.querySelector('#appearance-status'),error=document.querySelector('#appearance-error');let saving=false;
  const sync=()=>{const selected=window.HouseholdTheme.get();form.elements.family.value=selected.startsWith('pink-')?'pink':'ocean';form.elements.mode.value=selected.replace('pink-','');};
  sync();document.querySelectorAll('[data-open="appearance-dialog"]').forEach(button=>button.addEventListener('click',sync));
  form.addEventListener('submit',async event=>{
   event.preventDefault();if(saving)return;
   const theme=(form.elements.family.value==='pink'?'pink-':'')+form.elements.mode.value;
   const buttons=Array.from(form.querySelectorAll('button,input')),previous=buttons.map(button=>button.disabled);
   saving=true;buttons.forEach(button=>button.disabled=true);form.setAttribute('aria-busy','true');error.hidden=true;status.hidden=false;status.textContent=t('Darstellung wird gespeichert …');
   const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),15000);
   try{
    if(document.body.dataset.user){
     const response=await fetch('/api/v1/core/preferences',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content},body:JSON.stringify({theme}),signal:controller.signal});
     if(!response.ok){const body=await response.json().catch(()=>({}));throw Error(body.error||t('Darstellung konnte nicht gespeichert werden.'));}
    }
    const result=window.HouseholdTheme.apply(theme,{persist:true});
    const preferences=document.querySelector('#preferences');if(preferences)preferences.elements.theme.value=theme;
    status.textContent=result.persisted?t('Darstellung gespeichert.'):t('Darstellung angewendet. Dieses Gerät konnte sie nicht dauerhaft speichern.');
   }catch(failure){status.hidden=true;error.textContent=failure.name==='AbortError'?t('Speichern hat zu lange gedauert. Bitte erneut versuchen.'):failure.message;error.hidden=false;}
   finally{clearTimeout(timeout);saving=false;form.removeAttribute('aria-busy');buttons.forEach((button,index)=>button.disabled=previous[index]);}
  });
 }
 const tray=document.querySelector('#more-dialog');
 if(tray){
  const triggers=Array.from(document.querySelectorAll('[data-open="more-dialog"]'));
  const expanded=()=>triggers.forEach(button=>button.setAttribute('aria-expanded',String(tray.open)));
  expanded();tray.addEventListener('close',expanded);
  triggers.forEach(button=>button.addEventListener('click',()=>{
   expanded();const content=tray.querySelector('.tray-content'),active=content.querySelector('[aria-current="page"]');
   if(active){const target=active.getBoundingClientRect(),bounds=content.getBoundingClientRect();if(target.bottom>bounds.bottom||target.top<bounds.top)content.scrollTop+=target.top-bounds.top-12;}
  }));
  let startedOutside=false;
  const outside=event=>{const bounds=tray.getBoundingClientRect();return event.clientX<bounds.left||event.clientX>bounds.right||event.clientY<bounds.top||event.clientY>bounds.bottom;};
  tray.addEventListener('pointerdown',event=>{startedOutside=event.target===tray&&outside(event);});
  tray.addEventListener('pointerup',event=>{if(startedOutside&&event.target===tray&&outside(event))tray.close();startedOutside=false;});
  tray.addEventListener('pointercancel',()=>{startedOutside=false;});
 }
})();
