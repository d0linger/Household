(() => {
 'use strict';
 const quantity=value=>String(value??'').replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',','),unit=value=>value==='piece'?'Stück':value;
 function amount(value){if(!value)return'Unbekannt';const text=value.below_precision?'< 0,000001':(value.approximate?'≈ ':'')+quantity(value.value);return value.approximate?text+' (exakt '+value.rational+')':text;}
 const outcomes={sufficient:'Durch freien Vorrat gedeckt',shortage:'Bekannte Fehlmengen',incomplete:'Zuordnungen oder Mengen offen',empty:'Keine auswertbaren Zutaten'};
 const reasons={catalog_unassigned:'Zutat ist keinem Katalogeintrag zugeordnet',catalog_missing:'Zutatenkatalogeintrag fehlt',catalog_archived:'Zutatenkatalogeintrag ist archiviert',pantry_disabled:'Vorrat ist deaktiviert',product_unassigned:'Produktzuordnung fehlt',binding_source_changed:'Zutatenstand wurde geändert',binding_stale:'Zuordnung stammt aus einem früheren Datenstand',product_missing:'Produkt fehlt',product_archived:'Produkt ist archiviert',product_unit_changed:'Basiseinheit wurde geändert – Zuordnung erneut bestätigen',quantity_unknown:'Menge ist unbekannt',unit_incompatible:'Einheiten sind nicht umrechenbar',source_missing:'Kein bestätigter Rezeptstand',source_size_limit:'Rezeptstand überschreitet die Planungsgrenze',source_ingredient_limit:'Zu viele Zutaten für die Planung'};
 const dateKinds={use_by:'Verbrauchsdatum',best_before:'Mindesthaltbarkeit',opening_hint:'Hinweis nach dem Öffnen'};
 function dateLabel(reason){return(dateKinds[reason.kind]||'Datum')+' '+reason.on+(reason.excluded_from_coverage?' · nicht zur Deckung gezählt':reason.requires_review?' · prüfen':reason.past?' · bereits vergangen':'');}
 const helpers={amount,dateLabel,outcomes};
 if(typeof module!=='undefined'&&module.exports){module.exports=helpers;return;}
 const el=(tag,text,className)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(className)node.className=className;return node;};
 function create(root,options){
  if(!root)return null;
  const $=selector=>root.querySelector(selector),form=$('#suggestion-form'),list=$('#suggestion-candidates');
  let current=null,query=null,epoch=0,controller=null,loading=false,locked=false,pageNumber=0,returnTo=null;
  function freeze(){form.elements.q.disabled=loading||locked;form.elements.servings.disabled=loading||locked;form.elements.on.disabled=loading||locked;form.querySelector('button').disabled=loading||locked;$('#suggestion-next').disabled=loading||locked;for(const action of list.querySelectorAll('[data-suggestion-select]'))action.disabled=loading||locked;root.setAttribute('aria-busy',String(loading));}
  function invalidate(message='Auswahl geändert. Erneut bewerten, um aktuelle Vorschläge zu sehen.'){
   epoch++;controller?.abort();controller=null;loading=false;current=null;query=null;pageNumber=0;list.replaceChildren();$('#suggestion-result').hidden=true;$('#suggestion-error').hidden=true;$('#suggestion-status').textContent=message;freeze();
  }
  function reset(context){invalidate('Portionen und Tag prüfen, dann die Auswahl bewerten.');if(context){form.elements.servings.value=context.servings;form.elements.on.value=context.on;}root.open=false;returnTo=null;}
  function contextChanged(context){if(!context)return;if(form.elements.servings.value!==context.servings||form.elements.on.value!==context.on){form.elements.servings.value=context.servings;form.elements.on.value=context.on;invalidate('Mahlzeitenmenge oder Tag geändert. Die Vorschläge neu bewerten.');}}
  function fail(message){current=null;query=null;list.replaceChildren();$('#suggestion-result').hidden=true;const error=$('#suggestion-error');error.textContent=message+' Auswahl neu bewerten.';error.hidden=false;error.focus();$('#suggestion-status').textContent='Keine aktuelle Bewertung verfügbar.';}
  function render(){
   $('#suggestion-result').hidden=false;$('#suggestion-result-title').textContent='Bewertete Rezeptauswahl · Seite '+pageNumber;
   const observed=new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',timeStyle:'short',timeZone:current.timezone}).format(new Date(current.read_at));
   $('#suggestion-read-at').textContent='Beobachtet: '+observed+' · '+current.timezone+' · Vergleich für '+quantity(current.servings)+' Portionen am '+current.planned_on+'.';
   $('#suggestion-scope').textContent='Bis zu 20 Rezepte dieser Auswahlseite werden nach Deckung und passenden Datumsgründen geordnet. Weitere Seiten werden unabhängig neu bewertet.';
   if(!current.recipes_enabled)$('#suggestion-status').textContent='Rezepte sind deaktiviert. Nach Aktivierung die Auswahl neu bewerten.';
   else if(!current.candidates.length)$('#suggestion-status').textContent='Keine passenden aktiven Rezepte. Suchbegriff kürzen oder ein Rezept anlegen.';
   else $('#suggestion-status').textContent=current.candidates.length+' Alternativen einzeln bewertet.'+(!current.pantry_enabled?' Vorrat ist deaktiviert; Verfügbarkeit bleibt offen.':'');
   list.replaceChildren();
   for(const candidate of current.candidates){
    const row=el('li'),heading=el('h4',candidate.title),state=el('p',outcomes[candidate.outcome]||'Prüfung offen');
    heading.tabIndex=-1;row.append(heading,el('p','Rezeptstand '+(candidate.revision||'unbekannt')+' · Originalbasis '+quantity(candidate.base_servings)+' Portionen','fine'),state);
    if(candidate.source_issue)row.append(el('p',reasons[candidate.source_issue]||'Rezeptstand vor Verwendung prüfen.','notice'));
    for(const product of candidate.products){row.append(el('p',product.name+': '+(product.complete?'Bedarf ':'Bekannter Teilbedarf ')+amount(product.needed)+' '+unit(product.unit)+' · frei '+amount(product.free)+' '+unit(product.unit)+' · fehlt '+amount(product.shortage)+' '+unit(product.unit)+(product.complete?'':' · Gesamtbedarf offen')));}
    if(candidate.date_review)row.append(el('p','Datumsprüfung erforderlich. Dieser Vorschlag wird nicht als dringende Verbrauchsempfehlung hervorgehoben.','notice'));
    else if(candidate.urgent_on)row.append(el('p','Passender Datumsgrund bis '+candidate.urgent_on+'.'));
    if(candidate.dates.length){const details=el('details'),dates=el('ul');details.append(el('summary','Datumsgründe und relevante Chargenmengen ('+candidate.dates.length+' von '+candidate.date_reason_count+')'),el('p','Die Gesamtbewertung berücksichtigt alle relevanten Chargen. Hier erscheinen höchstens 50 Datumsgründe, Prüfhinweise zuerst.','fine'));for(const reason of candidate.dates){const product=candidate.products.find(p=>p.product_id===reason.product_id);dates.append(el('li',(product?.name||'Produkt')+' · '+dateLabel(reason)+' · '+amount(reason.free)+' '+unit(product?.unit||'')+' · Charge '+reason.lot_id.slice(0,8)));}details.append(dates);row.append(details);}
    const details=el('details'),lines=el('ul');details.append(el('summary','Zutaten und Zuordnungen prüfen'));
    for(const line of candidate.lines){const item=el('li',line.name+': '+(line.reason?(reasons[line.reason]||'Zuordnung prüfen'):amount(line.demand)+' '+unit(candidate.products.find(p=>p.product_id===line.product_id)?.unit||'')));if(line.reason){const mapping=line.catalog_id&&!['quantity_unknown','unit_incompatible','catalog_missing','catalog_archived','pantry_disabled'].includes(line.reason),link=el('a',mapping?'Zutatenzuordnung prüfen':'Rezeptangaben prüfen');link.href=mapping?'/m/recipes/catalog/'+line.catalog_id:'/m/recipes/recipes/'+candidate.recipe_id+'/workspace';item.append(document.createTextNode(' · '),link);}lines.append(item);}
    if(!candidate.lines.length)lines.append(el('li','Keine auswertbaren Zutaten in diesem Rezeptstand.'));details.append(lines);row.append(details);
    if(options.writable&&!candidate.source_issue){const action=el('button','Rezeptstand prüfen');action.type='button';action.dataset.suggestionSelect='';action.setAttribute('aria-label',candidate.title+' · Rezeptstand prüfen');action.addEventListener('click',async()=>{if(locked||loading||!current)return;returnTo=action;const selectedPage=current;loading=true;freeze();try{await options.onSelect(candidate,selectedPage);}catch(error){fail(error.message);}finally{loading=false;freeze();}});row.append(action);}
    list.append(row);
   }
   $('#suggestion-next').hidden=!current.next_cursor;freeze();$('#suggestion-result-title').focus();
  }
  async function evaluate(next=false){
   if(loading||locked||!form.reportValidity())return;
   const previous=current,params=next&&query?new URLSearchParams(query):new URLSearchParams({q:form.elements.q.value,servings:form.elements.servings.value,on:form.elements.on.value});
   if(next){if(!previous?.next_cursor)return;params.set('cursor',previous.next_cursor);}
   const active=++epoch;controller?.abort();controller=new AbortController();loading=true;current=null;list.replaceChildren();$('#suggestion-result').hidden=true;$('#suggestion-error').hidden=true;$('#suggestion-status').textContent='Freier Vorrat und Rezeptstände werden geprüft …';freeze();
   try{const response=await fetch('/api/v1/planning/suggestions?'+params,{signal:controller.signal,cache:'no-store'});const data=await response.json();if(active!==epoch)return;if(!response.ok)throw Error(data.error||'Bewertung fehlgeschlagen.');current=data;query=new URLSearchParams({q:data.search,servings:data.servings,on:data.planned_on});pageNumber=next?pageNumber+1:1;form.elements.servings.value=data.servings;form.elements.on.value=data.planned_on;form.elements.on.min=data.today;render();}
   catch(error){if(active===epoch&&error.name!=='AbortError')fail(error.message);}
   finally{if(active===epoch){loading=false;controller=null;freeze();}}
  }
  form.addEventListener('submit',event=>{event.preventDefault();evaluate();});form.addEventListener('input',()=>invalidate());$('#suggestion-next').addEventListener('click',()=>evaluate(true));
  return{reset,contextChanged,setLocked(value){locked=value;freeze();},returnFocus(){if(returnTo?.isConnected)returnTo.focus();},invalidate};
 }
 window.HouseholdRecipeSuggestions={...helpers,create};
})();
