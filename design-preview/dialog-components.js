// Shared fixture and behavior contract. A design changes presentation, not data.
export function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
export function defaultValues(fixture) { return Object.fromEntries(fixture.fields.map(field=>[field.name,field.value])); }
export function dialogMarkup(design,fixture,prefix='dialog',values={}) {
  const e=escapeHTML, id=`${prefix}-${design.id}-${fixture.id}`;
  const fields=fixture.fields.map((field,index)=>{
    const value=Object.hasOwn(values,field.name)?values[field.name]:field.value;
    const fid=`${id}-${field.name}`.replace(/[^a-zA-Z0-9_-]/g,'-');
    const attrs=`id="${fid}" name="${e(field.name)}" ${field.required?'required aria-required="true"':''} aria-describedby="${fid}-error" ${field.min!==undefined?`min="${field.min}"`:''} ${field.max!==undefined?`max="${field.max}"`:''} ${field.step!==undefined?`step="${field.step}"`:''}`;
    let input;
    if(field.type==='select')input=`<select ${attrs}>${field.options.map(option=>`<option value="${e(option.value)}" ${option.value===value?'selected':''}>${e(option.label)}</option>`).join('')}</select>`;
    else if(field.type==='textarea')input=`<textarea ${attrs} rows="3" maxlength="2000">${e(value)}</textarea>`;
    else if(field.type==='checkbox')input=`<input ${attrs} type="checkbox" ${value?'checked':''}>`;
    else input=`<input ${attrs} type="${field.type}" value="${e(value)}" ${field.type==='text'?'maxlength="200" autocomplete="off"':''}>`;
    return `<div class="world-field ${field.type==='checkbox'?'checkbox-field':''}" data-field="${e(field.name)}"><label for="${fid}"><span class="field-index" aria-hidden="true">${index+1}</span>${e(field.label)}${field.required?'<span class="required-label">Pflichtfeld</span>':''}</label>${input}${field.type==='range'?`<output for="${fid}" data-range-value>${e(value)}</output>`:''}<span class="field-error" id="${fid}-error"></span></div>`;
  }).join('');
  return `<section class="world" data-design="${design.id}" data-layout="${design.layout}" data-treatment="${design.treatment}" data-navigation="${design.navigation}" data-control="${design.control}" data-density="${design.density}" data-align="${design.alignment}" aria-label="${e(design.name)}: ${e(fixture.title)}">
    <header class="world-header"><p class="world-caption">${e(design.name)}</p><h3>${e(fixture.title)}</h3><p>${e(fixture.description)}</p></header>
    <div class="world-layout"><nav class="world-navigation" aria-label="Formularansicht"><button type="button" data-phase="edit" aria-pressed="true">Eingabe</button><button type="button" data-phase="review" aria-pressed="false">Vorschau</button></nav>
    <form class="world-form" novalidate><div class="world-fields">${fields}</div><div class="world-review" hidden><h4>Deine Beispieleingaben</h4><dl data-review-values></dl><button type="button" data-back>Weiter bearbeiten</button></div>
    <div class="world-actions"><button type="submit" class="world-primary">Vorschau prüfen</button><button type="reset">Zurücksetzen</button></div><p class="world-status" role="status" aria-live="polite"></p></form>
    <aside class="world-summary" aria-label="Beispielkontext"><dl>${fixture.summary.map(row=>`<div><dt>${e(row.label)}</dt><dd>${e(row.value)}</dd></div>`).join('')}</dl><p>${e(fixture.note)}</p></aside></div>
  </section>`;
}
// Intentionally self-contained: included unchanged in standalone exports.
export function bindDialog(root,fixture,onChange=()=>{}) {
  const form=root.querySelector('form'), status=root.querySelector('.world-status');
  const controls=Object.fromEntries(fixture.fields.map(field=>[field.name,form.elements.namedItem(field.name)]));
  const values=()=>Object.fromEntries(fixture.fields.map(field=>[field.name,field.type==='checkbox'?controls[field.name].checked:controls[field.name].value]));
  function validate(input,show=false) {
    const valid=input.checkValidity();
    if(show||input.getAttribute('aria-invalid')==='true'){
      input.setAttribute('aria-invalid',String(!valid));
      const error=root.querySelector(`#${input.getAttribute('aria-describedby')}`);
      error.textContent=valid?'':input.validity.valueMissing?'Bitte dieses Pflichtfeld ausfüllen.':input.validity.rangeUnderflow||input.validity.rangeOverflow?`Bitte einen Wert von ${input.min||'0'} bis ${input.max||'zur Obergrenze'} eingeben.`:'Bitte einen gültigen Wert eingeben.';
    }
    return valid;
  }
  function updateReview(){
    const list=root.querySelector('[data-review-values]');list.replaceChildren();
    fixture.fields.forEach(field=>{
      const row=document.createElement('div'),term=document.createElement('dt'),description=document.createElement('dd');
      term.textContent=field.label;
      const value=values()[field.name];description.textContent=field.type==='checkbox'?(value?'Ja':'Nein'):field.type==='select'?(field.options.find(option=>option.value===value)?.label||'Nicht gewählt'):value||'Nicht ausgefüllt';
      row.append(term,description);list.append(row);
      const output=controls[field.name].parentElement.querySelector('[data-range-value]');if(output)output.value=value;
    });
  }
  function phase(name){
    root.querySelector('.world-fields').hidden=name==='review';root.querySelector('.world-review').hidden=name!=='review';
    root.querySelectorAll('[data-phase]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.phase===name)));
    updateReview();
  }
  function setValues(next){
    fixture.fields.forEach(field=>{
      if(!Object.hasOwn(next,field.name))return;
      if(field.type==='checkbox')controls[field.name].checked=!!next[field.name];else controls[field.name].value=String(next[field.name]);
      validate(controls[field.name]);
    });
    status.textContent='';updateReview();
  }
  root.addEventListener('click',event=>{
    const button=event.target.closest('button');
    if(button?.dataset.phase)phase(button.dataset.phase);
    if(button?.hasAttribute('data-back')){phase('edit');controls[fixture.fields[0].name].focus();}
  });
  form.addEventListener('input',event=>{if(!event.target.name)return;validate(event.target);status.textContent='';updateReview();onChange(values());});
  form.addEventListener('change',event=>{if(!event.target.name)return;validate(event.target);status.textContent='';updateReview();onChange(values());});
  // Reveal errors on submit; subsequently refresh them while editing. Inserting
  // an error on blur can move a pointer's submit target before pointerup.
  form.addEventListener('focusout',event=>{if(event.target.name)validate(event.target);});
  form.addEventListener('submit',event=>{
    event.preventDefault();const invalid=Object.values(controls).filter(input=>!validate(input,true));
    if(invalid.length){phase('edit');invalid[0].focus();status.textContent=`${invalid.length} Feld${invalid.length===1?'':'er'} bitte prüfen.`;return;}
    phase('review');status.textContent='Vorschau geprüft. Keine Haushaltsdaten gespeichert.';
  });
  form.addEventListener('reset',event=>{
    event.preventDefault();setValues(Object.fromEntries(fixture.fields.map(field=>[field.name,field.value])));
    Object.values(controls).forEach(input=>{input.removeAttribute('aria-invalid');root.querySelector(`#${input.getAttribute('aria-describedby')}`).textContent='';});
    phase('edit');onChange(values());
  });
  updateReview();return {setValues,values};
}
