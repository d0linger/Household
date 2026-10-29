(() => {
  'use strict';
  const board=document.querySelector('#replenish-board');if(!board)return;
  const $=selector=>board.querySelector(selector),node=(tag,text)=>{const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;};
  const qty=v=>String(v??'0').replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',','),unit=v=>v==='piece'?'Stück':v==='portion'?'Portionen':v;
  const product=board.dataset.product,owner=document.body.dataset.user,household=document.body.dataset.household;
  const pack=$('#replenish-pack'),form=$('#replenish-form'),shopping=$('#replenish-shopping'),review=$('#replenish-review-section');
  let current=null,boot=null,pending=null,busy=false,uncertain=false;
  function error(selector,message){const el=$(selector);el.textContent=message;el.hidden=false;el.focus();}
  function link(text,path){const el=node('a',text);el.href=path;return el;}
  async function request(url,options={}){let response;try{response=await fetch(url,{...options,headers:{Accept:'application/json',...options.headers}});}catch(e){throw Error('Verbindung unterbrochen. '+e.message);}
    let data;try{data=await response.json();}catch{const e=Error('Antwort konnte nicht gelesen werden.');e.status=response.ok?0:response.status;throw e;}
    if(!response.ok){const e=Error(data.error||'Anfrage konnte nicht abgeschlossen werden.');e.status=response.status;throw e;}return data;
  }
  function freeze(value){pack.disabled=value;$('#replenish-refresh').disabled=value;if(shopping)$('#replenish-shopping-fields').disabled=value||!current?.suggestion.suggested||boot?.states.shopping?.state!=='enabled';}
  function row(parent,label,value){const p=node('p');p.append(node('strong',label+': '),document.createTextNode(value));parent.append(p);}
  function fill(){const basis=$('#replenish-basis'),amounts=$('#replenish-amounts'),p=current.product,s=current.suggestion,u=unit(p.unit_code);basis.replaceChildren();amounts.replaceChildren();
    row(basis,'Physischer Bestand',qty(current.balance)+' '+u);row(basis,'Mindestbestand',p.low_stock_threshold===null?'Nicht festgelegt':qty(p.low_stock_threshold)+' '+u);
    row(basis,'Zielbestand',s.target?qty(s.target)+' '+u+(s.target_source==='minimum'?' · bisheriger Mindestbestand als Ziel':''):'Nicht festgelegt');
    const chosen=pack.value;pack.replaceChildren(new Option('Ohne Packung · Menge in der Basiseinheit',''));
    for(const option of current.packs){const el=new Option(option.label+' · '+qty(option.quantity)+' '+unit(option.unit_code)+(option.disabled?' · deaktiviert':''),option.id);el.disabled=option.disabled;pack.append(el);}pack.value=current.pack_id||chosen||'';
    const reasons={minimum_unknown:'Für einen automatischen Vorschlag zuerst einen Mindestbestand festlegen.',minimum_met:'Der Mindestbestand ist erreicht. Aktuell wird kein Nachkauf vorgeschlagen.',product_archived:'Das Produkt ist archiviert. Es wird kein neuer Nachkauf vorgeschlagen.',below_minimum:'Der physische Bestand liegt unter dem Mindestbestand.'};
    amounts.append(node('p',reasons[s.reason]||'Stand bitte prüfen.'));if(s.suggested){row(amounts,'Fehlmenge bis zum Ziel',qty(s.shortage)+' '+u);if(s.pack_count){const chosenPack=current.packs.find(x=>x.id===current.pack_id);row(amounts,'Ganze Packungen',s.pack_count+' × '+chosenPack.label+' · je '+qty(chosenPack.quantity)+' '+unit(chosenPack.unit_code));}row(amounts,'Einkaufsmenge',qty(s.quantity)+' '+u);row(amounts,'Bestand nach dem Einkauf',qty(current.balance)+' → '+qty(s.after)+' '+u);row(amounts,'Über dem Ziel durch Packungsrundung',qty(s.surplus)+' '+u);}
    $('#replenish-calculation').hidden=false;if(shopping){shopping.hidden=!s.suggested;$('#replenish-shopping-note').textContent=boot.states.shopping?.state==='enabled'?'Die Übernahme erzeugt einen offenen Artikel. Sie bucht keinen Vorratseingang.':'Einkaufen ist deaktiviert. Die Vorratsvorschau bleibt verfügbar.';}
    $('#replenish-status').textContent='Aktueller Stand geladen. Änderungen vor der Bestätigung erfordern eine neue Prüfung.';
  }
  async function load(focus=false){if(busy||uncertain)return;busy=true;freeze(true);$('#replenish-error').hidden=true;$('#replenish-load-retry').hidden=true;$('#replenish-status').textContent='Aktueller Stand wird geladen …';
    try{const [view,fresh]=await Promise.all([request('/api/v1/pantry/products/'+product+'/replenish?'+new URLSearchParams({pack:pack.value})),request('/api/v1/core/bootstrap')]);
      if(fresh.user.id!==owner||fresh.user.household_id!==household||fresh.states.pantry?.state!=='enabled'||fresh.states.pantry.generation!==view.generation)throw Error('Konto oder Modulstand hat sich geändert. Seite erneut öffnen und Auswahl prüfen.');
      current=view;boot=fresh;pending=null;review.hidden=true;$('#replenish-result').hidden=true;fill();if(focus)$('#replenish-calculation-title').focus();
    }catch(e){error('#replenish-error',e.message+' Deine Auswahl bleibt erhalten.');$('#replenish-load-retry').hidden=false;$('#replenish-status').textContent='Stand konnte nicht geladen werden.';current=null;}
    finally{busy=false;freeze(false);}
  }
  function leaveReview(){if(busy||uncertain)return;review.hidden=true;pending=null;freeze(false);$('#replenish-review')?.focus();}
  async function post(){if(!pending||busy)return;busy=true;freeze(true);$('#replenish-confirm').disabled=true;$('#replenish-exact-retry').disabled=true;$('#replenish-cancel').disabled=true;
    try{const result=await request(pending.url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':pending.key},body:pending.body});
      pending=null;uncertain=false;review.hidden=true;const content=$('#replenish-result-content');content.replaceChildren(node('p',qty(result.quantity)+' '+unit(result.unit_code)+' wurden als offener Artikel übernommen.'),link('Artikel auf der Einkaufsliste ansehen','/m/shopping/items/'+result.item_id));$('#replenish-result').hidden=false;$('#replenish-result-title').focus();
    }catch(e){uncertain=!e.status||e.status>=500;error('#replenish-post-error',e.message+(uncertain?' Der Ausgang ist ungewiss. Frage genau dieselbe Übernahme erneut ab.':' Es wurde keine neue Übernahme bestätigt. Auswahl und Mengen bleiben zur Prüfung erhalten.'));
      $('#replenish-confirm').hidden=true;$('#replenish-exact-retry').hidden=!uncertain;$('#replenish-cancel').hidden=uncertain;$('#replenish-adopt').hidden=uncertain||e.status!==409;if(!uncertain)pending=null;
    }finally{busy=false;$('#replenish-confirm').disabled=false;$('#replenish-exact-retry').disabled=false;$('#replenish-cancel').disabled=false;}
  }
  form.addEventListener('submit',e=>{e.preventDefault();load(true);});$('#replenish-load-retry').addEventListener('click',()=>load(true));
  shopping?.addEventListener('submit',e=>{e.preventDefault();if(!current||busy||uncertain)return;const list=$('#replenish-list');if(list.dataset.referenceState!=='ready'||!list.value){error('#replenish-error','Einkaufsliste zuerst vollständig laden und ausdrücklich auswählen.');return;}
    if(pack.value!==current.pack_id){error('#replenish-error','Die Packungsauswahl wurde geändert. Vorschau zuerst aktualisieren.');return;}
    const s=current.suggestion;pending={url:'/api/v1/m/pantry/products/'+product+'/actions/shopping',key:crypto.randomUUID(),body:JSON.stringify({generation:current.generation,version:current.product.version,target_generations:{shopping:boot.states.shopping.generation},data:{reviewed:true,list_id:list.value,balance:current.balance,movement_count:current.movement_count,pack_id:current.pack_id,pack_version:current.pack_version,quantity:s.quantity}})};
    const description=$('#replenish-review-description');description.replaceChildren(node('p',current.product.name));row(description,'Einkaufsliste',list.selectedOptions[0].textContent);row(description,'Einkaufsmenge',qty(s.quantity)+' '+unit(current.product.unit_code));if(s.pack_count)row(description,'Packungen',s.pack_count+' × '+current.packs.find(x=>x.id===current.pack_id).label);row(description,'Geprüfter Bestand → nach Einkauf',qty(current.balance)+' → '+qty(s.after)+' '+unit(current.product.unit_code));row(description,'Über dem Ziel',qty(s.surplus)+' '+unit(current.product.unit_code));
    $('#replenish-post-error').hidden=true;$('#replenish-confirm').hidden=false;$('#replenish-cancel').hidden=false;$('#replenish-exact-retry').hidden=true;$('#replenish-adopt').hidden=true;review.hidden=false;freeze(true);$('#replenish-review-title').focus();
  });
  $('#replenish-confirm').addEventListener('click',post);$('#replenish-exact-retry').addEventListener('click',post);$('#replenish-cancel').addEventListener('click',leaveReview);$('#replenish-adopt').addEventListener('click',()=>load(true));$('#replenish-new').addEventListener('click',()=>load(true));
  window.addEventListener('beforeunload',event=>{if(uncertain){event.preventDefault();event.returnValue='';}});
  load();
})();
