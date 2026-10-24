/* Exact review arithmetic is shared with Node regression tests. */
const HouseholdCheckoutValues = (() => {
  function scaled(value, digits) {
    const match=String(value).trim().replace(',','.').match(/^(\d{1,12})(?:\.(\d{1,6}))?$/);
    if(!match||(match[2]||'').length>digits)throw Error('Menge oder Betrag hat zu viele Nachkommastellen.');
    return BigInt(match[1])*10n**BigInt(digits)+BigInt((match[2]||'').padEnd(digits,'0'));
  }
  function decimal(value,digits=6){const factor=10n**BigInt(digits);return (value/factor).toString()+'.'+(value%factor).toString().padStart(digits,'0');}
  function convert(quantity,from,to,integer=false){
    const units={piece:['count',1n],g:['mass',1n],kg:['mass',1000n],ml:['volume',1n],l:['volume',1000n]},a=units[from],b=units[to];
    if(!a||!b||a[0]!==b[0])throw Error('Tatsächliche Einheit passt nicht zum Vorratsprodukt.');
    const q=scaled(quantity,6),n=q*a[1];
    if(q<=0n||n%b[1]!==0n)throw Error('Menge muss positiv und mit sechs Nachkommastellen exakt darstellbar sein.');
    const result=n/b[1];
    if(result>=10n**18n||integer&&result%1000000n!==0n)throw Error('Basismenge ist zu groß oder muss ganzzahlig sein.');
    return decimal(result);
  }
  function minor(value){if(value==='')return null;const n=scaled(value,2);if(n>1000000000000n)throw Error('Betrag ist zu groß.');return n;}
  function money(value){if(value===null||value===undefined||value==='')return 'unbekannt';let n=BigInt(value),sign='';if(n<0n){sign='−';n=-n;}return sign+(n/100n).toLocaleString('de-AT')+','+(n%100n).toString().padStart(2,'0')+' €';}
  return {convert,minor,money};
})();
if(typeof module!=='undefined')module.exports=HouseholdCheckoutValues;
if(typeof document!=='undefined')(() => {
  'use strict';
  const root=document.querySelector('#checkout');if(!root)return;
  const $=s=>root.querySelector(s),form=$('#checkout-line'),confirmForm=$('#checkout-confirm-form'),fields=form.elements;
  const {convert,minor,money}=HouseholdCheckoutValues,unit=v=>v==='piece'?'Stück':v;
  const url='/api/v1/shopping/lists/'+root.dataset.list+'/checkout',draft=new Map();
  let source,position=0,busy=false,pending,receiptRows=[];
  const node=(tag,text,className)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(className)n.className=className;return n;};
  function say(text,focus=false){$('#checkout-message').textContent=text;if(focus)$('#checkout-message').focus();}
  async function request(path,options={}){const response=await fetch(path,{...options,headers:{Accept:'application/json',...options.headers}});const data=await response.json();if(!response.ok)throw Error(data.error||'Vorgang konnte nicht bestätigt werden.');return data;}
  function current(){return source.items[position];}
  function values(){return Object.fromEntries(new FormData(form));}
  function retain(){if(source?.items[position])draft.set(current().id,{...draft.get(current().id),...values()});}
  function decision(){
    const purchased=['store','no_stock'].includes(fields.decision.value),stock=fields.decision.value==='store';
    $('#checkout-purchase').hidden=!purchased;$('#checkout-stock').hidden=!stock;
    fields.quantity.required=purchased;fields.product_id.required=stock;fields.location_id.required=stock;
  }
  function ensureChoice(select,id,label){if(id&&![...select.options].some(o=>o.value===id))select.add(new Option(label||id,id));select.value=id||'';}
  function show(){
    $('#checkout-editor').hidden=false;$('#checkout-review').hidden=true;$('#checkout-line-error').hidden=true;
    const item=current(),saved=draft.get(item.id)||{},purchased=Boolean(item.checked_at)&&item.availability!=='unavailable';
    $('#checkout-progress').textContent='Position '+(position+1)+' von '+source.items.length;
    $('#checkout-name').textContent=item.name;
    const plan=$('#checkout-plan');plan.replaceChildren(node('p','Geplant: '+(item.quantity||'ohne Menge')+' '+unit(item.unit_code||'')+' · Schätzung '+money(item.estimate_minor)));
    if(item.product)plan.append(node('p',(item.availability==='substituted'?'Gewählter Ersatz: ':'Produkt: ')+item.product.name+' · '+(item.product.brand||'')+' · '+item.product.pack_quantity+' '+unit(item.product.pack_unit)+' pro Packung.'));
    if(item.price_observation)plan.append(node('p','Preisquelle: '+item.price_observation.observed_on+' · '+money(item.price_observation.price_minor)+' für '+item.price_observation.quantity+' '+unit(item.price_observation.unit_code)+'.','fine'));
    plan.append(node('p',purchased?'Als gekauft markiert. Tatsächliche Menge und Verwendung prüfen.':'Offen oder nicht verfügbar. Diese Position ausdrücklich auslassen; zum Ändern zuerst zur Liste zurückkehren.'));
    fields.decision.replaceChildren(new Option('Bitte ausdrücklich wählen',''));
    if(purchased){if(root.dataset.pantryEnabled==='true')fields.decision.add(new Option('In den Vorrat einlagern','store'));fields.decision.add(new Option('Gekauft, ohne Vorrat abschließen','no_stock'));}else fields.decision.add(new Option('Nicht gekauft · aus diesem Abschluss auslassen','skip'));
    fields.decision.value=saved.decision||'';
    for(const name of ['quantity','price','best_before','use_by'])fields[name].value=saved[name]||'';
    fields.unit.value=saved.unit||'piece';
    ensureChoice(fields.product_id,saved.product_id,saved.product?.name);ensureChoice(fields.location_id,saved.location_id,saved.location?.name);
    $('#checkout-base').textContent=saved.base?'Geprüfte Basismenge: '+saved.base+' '+unit(saved.product.unit_code)+'.':'Produkt und tatsächliche Menge prüfen. Die Basismenge wird vor dem Abschluss angezeigt.';
    $('#checkout-back').disabled=position===0;
    $('#checkout-next').textContent=position===source.items.length-1?'Position prüfen und Abschluss ansehen':'Position prüfen und weiter';
    decision();$('#checkout-name').focus();
  }
  async function validateLine(){
    if(!form.reportValidity())return false;
    const data=values(),saved={...data,item_id:current().id};
    if(data.decision==='skip'){
      for(const name of ['quantity','unit','price','product_id','location_id','best_before','use_by'])saved[name]='';
    }else{
      convert(data.quantity,data.unit,data.unit);minor(data.price);
      if(data.decision==='store'){
        for(const name of ['product_id','location_id'])if(fields[name].dataset.referenceState!=='ready')throw Error('Vorratsauswahl zuerst vollständig laden oder erneut versuchen.');
        const signature=JSON.stringify(data);
        const [product,location]=await Promise.all([request('/api/v1/m/pantry/products/'+data.product_id),request('/api/v1/m/pantry/locations/'+data.location_id)]);
        if(JSON.stringify(values())!==signature)throw Error('Deine Eingabe wurde während der Prüfung geändert. Erneut prüfen.');
        if(product.archived_at||location.archived_at)throw Error('Produkt oder Lagerort ist archiviert. Aktive Auswahl treffen.');
        saved.product=product;saved.location=location;saved.product_version=product.version;saved.location_version=location.version;
        saved.base=convert(data.quantity,data.unit,product.unit_code,product.integer_quantities);
      }else for(const name of ['product_id','location_id','best_before','use_by'])saved[name]='';
    }
    draft.set(current().id,saved);return true;
  }
  function renderLine(target,line,sourceItem,stockLinks=true){
    const article=node('article',undefined,'checkout-summary-row');article.append(node('h3',line.name||sourceItem.name));
    article.append(node('p','Plan: '+(sourceItem.quantity||'ohne Menge')+' '+unit(sourceItem.unit_code||'')+' · '+money(sourceItem.estimate_minor)));
    if(line.decision==='skip')article.append(node('p','Nicht gekauft · ausdrücklich ausgelassen.'));
    else{
      article.append(node('p','Ist: '+line.quantity+' '+unit(line.unit)+' · '+money(line.actual_minor)));
      if(line.stock){const stock=line.stock;article.append(node('p','Einlagerung: '+stock.quantity+' '+unit(stock.unit)+' · '+stock.product_name+' · '+stock.location_name));
        if(stock.best_before)article.append(node('p','Mindesthaltbar bis: '+stock.best_before));if(stock.use_by)article.append(node('p','Zu verbrauchen bis: '+stock.use_by));
        if(stock.lot_id&&stockLinks){const link=node('a','Charge und spätere Bestandskorrekturen öffnen');link.href='/m/pantry/lots/'+stock.lot_id;article.append(link);}
        else if(stock.lot_id)article.append(node('p','Der ursprüngliche Vorratsstand ist zurzeit nicht verfügbar. Die Einlagerungsangaben dieses Einkaufs bleiben als Historie erhalten.','fine'));
      }else article.append(node('p','Gekauft, ohne Einlagerung.'));
    }
    target.append(article);
  }
  function summaryData(){
    let planned=0n,actual=0n,plannedUnknown=0,actualUnknown=0;
    for(const item of source.items){const line=draft.get(item.id);if(item.availability!=='unavailable'){if(item.estimate_minor===null)plannedUnknown++;else planned+=BigInt(item.estimate_minor);}if(line.decision!=='skip'){const price=minor(line.price);if(price===null)actualUnknown++;else actual+=price;}}
    return {planned_known_minor:planned.toString(),planned_unknown:plannedUnknown,actual_known_minor:actual.toString(),actual_unknown:actualUnknown};
  }
  function totals(target,data,total){
    target.replaceChildren(node('p','Bekannte Planung: '+money(data.planned_known_minor)+' · '+data.planned_unknown+' Positionen ohne Schätzung.'),node('p','Bekannte tatsächliche Preise: '+money(data.actual_known_minor)+' · '+data.actual_unknown+' gekaufte Positionen ohne Preis.'));
    target.append(node('p','Belegsumme: '+money(total)));
    if(!data.planned_unknown&&!data.actual_unknown)target.append(node('p','Ist minus Plan: '+money(BigInt(data.actual_known_minor)-BigInt(data.planned_known_minor))));
    if(total!==null&&total!==undefined&&!data.actual_unknown)target.append(node('p','Nicht zugeordnete Belegdifferenz: '+money(BigInt(total)-BigInt(data.actual_known_minor))+'. Wird nicht auf Artikel verteilt.'));
    else target.append(node('p','Ein vollständiger Abgleich braucht alle tatsächlichen Positionspreise und die Belegsumme.'));
  }
  function review(){
    $('#checkout-editor').hidden=true;$('#checkout-review').hidden=false;confirmForm.elements.reviewed.checked=false;
    const target=$('#checkout-lines');target.replaceChildren();
    for(const item of source.items){const d=draft.get(item.id),price=minor(d.price);renderLine(target,{...d,actual_minor:price===null?null:price.toString(),stock:d.decision==='store'?{quantity:d.base,unit:d.product.unit_code,product_name:d.product.name,location_name:d.location.name,best_before:d.best_before,use_by:d.use_by}:null},item);}
    updateTotals();$('#checkout-review').focus();
  }
  function updateTotals(){try{totals($('#checkout-totals'),summaryData(),minor(confirmForm.elements.receipt_total.value));}catch(error){$('#checkout-totals').textContent=error.message;}}
  function completed(result){
    $('#checkout-editor').hidden=true;$('#checkout-review').hidden=true;$('#checkout-changes').hidden=true;$('#checkout-completed').hidden=false;
    const data=result.snapshot,target=$('#checkout-result');target.replaceChildren(node('p','Eingekauft am '+data.received_on+'. Dieser Abschluss bleibt unverändert; erneutes Bestätigen legt keinen zweiten Bestand an.'));
    const totalBox=node('div',undefined,'notice');totals(totalBox,data,data.receipt_total_minor);target.append(totalBox);
    if(data.receipt){const link=node('a','Beleg öffnen: '+data.receipt.name);link.href='/attachments/'+data.receipt.id;target.append(link);}else target.append(node('p','Kein Beleg verknüpft.'));
    const stockLinks=root.dataset.pantryEnabled==='true'&&Number(root.dataset.pantryGeneration)===data.pantry_generation;
    for(const line of data.lines)renderLine(target,line,data.source.items.find(item=>item.id===line.item_id),stockLinks);
    say('Abschluss gespeichert. Für einen weiteren Einkauf die Liste kopieren.');$('#checkout-completed').focus();
  }
  function changes(before,after){
    const target=$('#checkout-change-list');target.replaceChildren();
    if(!before){$('#checkout-changes').hidden=true;return;}
    const oldItems=new Map(before.items.map(item=>[item.id,item])),newItems=new Map(after.items.map(item=>[item.id,item]));
    const product=item=>item.product?[item.product.name,item.product.brand,item.product.pack_quantity,unit(item.product.pack_unit)].filter(Boolean).join(' · '):'Freier Artikel';
    const availability=item=>({available:'Verfügbar',unavailable:'Nicht verfügbar',substituted:'Ersatz gewählt'})[item.availability];
    const price=item=>item.price_observation?item.price_observation.observed_on+' · '+money(item.price_observation.price_minor)+' / '+item.price_observation.quantity+' '+unit(item.price_observation.unit_code):'Keine Preisquelle';
    const fields=[['Artikel',i=>i.name],['Geplante Menge',i=>(i.quantity||'ohne Menge')+' '+unit(i.unit_code||'')],['Produkt oder Ersatz',product],['Verfügbarkeit',availability],['Einkaufsstatus',i=>i.checked_at?'Gekauft':'Offen'],['Schätzung',i=>money(i.estimate_minor)],['Preisquelle',price],['Notiz',i=>i.note||'Keine'],['Gruppe',i=>i.group_name||'Keine']];
    if(before.list.name!==after.list.name)target.append(node('p','Listenname: '+before.list.name+' → '+after.list.name));
    if(before.list.store_id!==after.list.store_id)target.append(node('p','Die Geschäftszuteilung der Liste wurde geändert. Aktuelle Preise prüfen.'));
    for(const old of before.items)if(!newItems.has(old.id))target.append(node('p','Entfernt: '+old.name+'. Diese Position wird nicht abgeschlossen.'));
    for(const item of after.items){const old=oldItems.get(item.id);if(!old){target.append(node('p','Neu hinzugefügt: '+item.name));continue;}
      const rows=[];for(const [label,format]of fields){const a=format(old),b=format(item);if(a!==b)rows.push(label+': '+a+' → '+b);}
      if(old.assignee_id!==item.assignee_id)rows.push('Zuständigkeit geändert. Bei Bedarf die Person in den Listendetails prüfen.');
      if(!rows.length&&old.version!==item.version)rows.push('Zwischenzeitlich erneut gespeichert. Aktuellen Stand prüfen.');
      if(rows.length){const article=node('article',undefined,'checkout-summary-row');article.append(node('h3',item.name));for(const text of rows)article.append(node('p',text));target.append(article);}
    }
    $('#checkout-changes').hidden=!target.childElementCount;
  }
  async function load(){
    if(busy)return;retain();busy=true;$('#checkout-refresh').disabled=true;
    try{
      const result=await request(url);if(result.completed){completed(result.completed);return;}
      if(root.dataset.canWrite!=='true'){say('Zum Abschließen ist eine Schreibberechtigung erforderlich.');return;}
      if(!result.items.length){retain();source=result;position=0;$('#checkout-editor').hidden=true;$('#checkout-review').hidden=true;$('#checkout-completed').hidden=true;$('#checkout-changes').hidden=true;say('Die Liste enthält keine Positionen mehr. Über „Zur Liste“ neue Artikel ergänzen.',true);return;}
      receiptRows=await request('/api/v1/m/shopping/lists/'+root.dataset.list+'/attachments');
      const receipt=confirmForm.elements.receipt_id,selected=receipt.value;receipt.replaceChildren(new Option('Beleg oder „Kein Beleg“ auswählen',''),new Option('Kein Beleg vorhanden','none'));
      for(const row of receiptRows)receipt.add(new Option(row.name,row.id));receipt.value=selected;
      retain();changes(source,result);source=result;position=0;
      if(!confirmForm.elements.received_on.value)confirmForm.elements.received_on.value=result.today;
      confirmForm.elements.received_on.max=result.today;
      say(draft.size?'Aktueller Vergleich geladen. Dein Entwurf bleibt erhalten; jede Position erneut prüfen.':'Für jede Position eine Entscheidung treffen.');show();
    }catch(error){say(error.message+' Deine Eingaben bleiben erhalten.',true);}finally{busy=false;$('#checkout-refresh').disabled=false;}
  }
  fields.decision.addEventListener('change',decision);
  form.addEventListener('submit',event=>{event.preventDefault();$('#checkout-next').click();});
  $('#checkout-next').addEventListener('click',async()=>{
    if(busy)return;busy=true;$('#checkout-next').disabled=true;$('#checkout-back').disabled=true;$('#checkout-line-error').hidden=true;
    try{if(await validateLine()){if(position<source.items.length-1){position++;show();}else review();}}
    catch(error){$('#checkout-line-error').textContent=error.message;$('#checkout-line-error').hidden=false;$('#checkout-line-error').focus();}
    finally{busy=false;$('#checkout-next').disabled=false;$('#checkout-back').disabled=position===0;}
  });
  $('#checkout-back').addEventListener('click',()=>{if(busy||position===0)return;retain();position--;show();});
  $('#checkout-edit').addEventListener('click',()=>{if(busy)return;position=0;show();});
  $('#checkout-refresh').addEventListener('click',()=>void load());
  confirmForm.elements.receipt_total.addEventListener('input',()=>{confirmForm.elements.reviewed.checked=false;updateTotals();});
  for(const name of ['receipt_id','received_on'])confirmForm.elements[name].addEventListener('change',()=>{confirmForm.elements.reviewed.checked=false;});
  confirmForm.addEventListener('submit',async event=>{
    event.preventDefault();if(busy||!confirmForm.reportValidity())return;
    let body;
    try{
      minor(confirmForm.elements.receipt_total.value);
      body={generation:Number(root.dataset.generation),pantry_generation:Number(root.dataset.pantryGeneration),fingerprint:source.fingerprint,
        receipt_id:confirmForm.elements.receipt_id.value==='none'?'':confirmForm.elements.receipt_id.value,receipt_total:confirmForm.elements.receipt_total.value,
        received_on:confirmForm.elements.received_on.value,lines:source.items.map(item=>{const d=draft.get(item.id),line={};for(const key of ['item_id','decision','quantity','unit','price','product_id','location_id','best_before','use_by'])line[key]=d[key]||'';for(const key of ['product_version','location_version'])line[key]=d.decision==='store'?d[key]:0;return line;})};
    }catch(error){say(error.message,true);return;}
    const signature=JSON.stringify(body);if(!pending||pending.signature!==signature)pending={signature,key:crypto.randomUUID()};
    busy=true;for(const element of confirmForm.elements)element.disabled=true;$('#checkout-refresh').disabled=true;
    try{const result=await request(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':pending.key},body:signature});completed(result);pending=null;}
    catch(error){say(error.message+' Entwurf bleibt erhalten. Bei unklarer Antwort den aktuellen Abschluss laden; keinen neuen Einkauf anlegen.',true);}
    finally{busy=false;for(const element of confirmForm.elements)element.disabled=false;$('#checkout-refresh').disabled=false;}
  });
  void load();
})();
