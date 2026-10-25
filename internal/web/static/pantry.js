(() => {
  'use strict';
  const board=document.querySelector('#pantry-board');if(!board)return;
  const $=selector=>board.querySelector(selector),form=$('#pantry-filter'),container=$('#pantry-products'),status=$('#pantry-status'),summary=$('#pantry-summary');
  const previous=$('#pantry-previous'),next=$('#pantry-next'),refresh=$('#pantry-refresh');
  const node=(tag,text)=>{const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;};
  const qty=value=>String(value??'0').replace(/(\.\d*?)0+$/,'$1').replace(/\.$/,'').replace('.',',');
  const unit=code=>code==='piece'?'Stück':code;
  const date=value=>value?String(value).slice(0,10).split('-').reverse().join('.'):'';
  const moment=(value,zone)=>new Intl.DateTimeFormat('de-AT',{dateStyle:'medium',timeStyle:'short',timeZone:zone}).format(new Date(value))+' ('+zone+')';
  let query='',day='',cursor='',nextCursor='',history=[],loading=false,sequence=0;
  const initial=new URLSearchParams(location.search);form.elements.q.value=initial.get('q')||'';form.elements.at.value=initial.get('at')||'';
  async function get(url){const response=await fetch(url,{headers:{Accept:'application/json'}});const data=await response.json();if(!response.ok)throw Error(data.error||'Bestand konnte nicht geladen werden.');return data;}
  function link(label,url){const a=node('a',label);a.href=url;return a;}
  function lotsFor(product,at,search){
    const details=node('details'),heading=node('summary','Chargen anzeigen · '+product.lot_count),list=node('div'),message=node('p'),more=node('button','Weitere Chargen');
    details.className='pantry-lots';details.append(heading,list,message,more);more.type='button';more.hidden=true;message.setAttribute('role','status');
    let loaded=false,busy=false,lotCursor='';
    async function load(){if(busy)return;busy=true;more.disabled=true;message.textContent='Chargen werden geladen …';
      try{const params=new URLSearchParams({at,q:search,cursor:lotCursor}),page=await get('/api/v1/pantry/products/'+product.id+'/lots?'+params);
        for(const row of page.records){const article=node('article');article.className='pantry-lot';const label=String(row.name||'').trim()||'Charge vom '+date(row.received_on)+' · '+row.location;article.append(link(label,'/m/pantry/lots/'+row.id),node('p',qty(row.stock)+' '+unit(product.unit)+' · '+row.location));
          const dates=[['Erhalten',row.received_on],['Mindestens haltbar bis',row.best_before_on],['Zu verbrauchen bis',row.use_by_on],['Geöffnet',row.opened_on]].filter(([,value])=>value).map(([label,value])=>label+': '+date(value));
          article.append(node('p',dates.join(' · ')));if(Number(row.later_corrections)>0)article.append(node('p',row.later_corrections+' spätere Korrekturbuchung(en). Letzte: '+moment(row.latest_correction_at,page.timezone)),link('Korrekturen im Chargenjournal prüfen','/m/pantry/lots/'+row.id));
          if(!at)article.append(link('Verbrauch prüfen','/m/pantry/consume/'+row.id));list.append(article);
        }
        loaded=true;lotCursor=page.next_cursor;more.hidden=!lotCursor;more.textContent='Weitere Chargen';message.textContent=list.children.length?list.children.length+' Chargen geladen.':'Noch keine Chargen an diesem Stichtag.';
      }catch(error){message.textContent=error.message+' Erneut laden ist möglich.';more.textContent='Chargen erneut laden';more.hidden=false;}
      finally{busy=false;more.disabled=false;}
    }
    details.addEventListener('toggle',()=>{if(details.open&&!loaded)load();});more.addEventListener('click',load);return details;
  }
  async function load(target,trail,focus=false){if(loading)return;loading=true;previous.disabled=next.disabled=refresh.disabled=true;form.querySelector('button').disabled=true;
    const request=++sequence;status.textContent='Bestand wird geladen …';container.setAttribute('aria-busy','true');
    try{const data=await get('/api/v1/pantry/stock?'+new URLSearchParams({q:query,at:day,cursor:target}));if(request!==sequence)return;
      cursor=target;history=trail;nextCursor=data.next_cursor;container.replaceChildren();summary.replaceChildren();
      summary.append(node('h2',data.total+' Produkte'+(day?' · '+date(day):' · Aktuell')));
      const totals=node('p');totals.className='pantry-totals';totals.textContent=data.totals.length?'Gesamter gefilterter Bestand: '+data.totals.map(t=>qty(t.quantity)+' '+unit(t.unit)).join(' · '):'Keine passenden Produkte.';summary.append(totals);
      if(day){const note=node('p','Bis zum Tagesende in '+data.timezone+'.');note.className='fine';summary.append(note);}$('#pantry-history-note').hidden=!day;
      for(const product of data.products){const article=node('article');article.className='pantry-product';article.dataset.pantryProduct=product.id;
        const top=node('div');top.className='section-heading';const title=node('h3');title.append(link(product.name,'/m/pantry/products/'+product.id));top.append(title,node('strong',qty(product.stock)+' '+unit(product.unit)));article.append(top);
        const locations=product.locations.join(', ')+(Number(product.location_count)>product.locations.length?' · '+product.location_count+' Orte insgesamt':'');
        const dates=[['Mindestens haltbar bis',product.next_best_before],['Zu verbrauchen bis',product.next_use_by]].filter(([,v])=>v).map(([label,v])=>label+': '+date(v));
        article.append(node('p',locations||'Kein belegter Lagerort'));if(dates.length)article.append(node('p',dates.join(' · ')));if(product.minimum!==null)article.append(node('p','Mindestbestand: '+qty(product.minimum)+' '+unit(product.unit)));
        if(product.target!==null&&product.target!==undefined)article.append(node('p',(day?'Aktueller Zielbestand: ':'Zielbestand: ')+qty(product.target)+' '+unit(product.unit)));if(!day)article.append(link('Nachkauf prüfen','/m/pantry/replenish/'+product.id));
        if(product.archived)article.append(node('p','Produkt archiviert'));if(Number(product.later_corrections)>0)article.append(node('p',product.later_corrections+' spätere Korrekturbuchung(en); zuletzt '+moment(product.latest_correction_at,data.timezone)+'. Mengen hier bleiben am Stichtag.'));
        article.append(lotsFor(product,day,query));container.append(article);
      }
      if(!data.products.length)container.append(node('p','Keine Produkte gefunden. Suchbegriff oder Stichtag anpassen.'));
      previous.hidden=!history.length;next.hidden=!nextCursor;status.textContent=data.products.length+' von '+data.total+' Produkten auf dieser Seite. Mengen werden nicht zwischen unterschiedlichen Einheiten addiert.';
      const params=new URLSearchParams();if(query)params.set('q',query);if(day)params.set('at',day);window.history.replaceState(null,'',location.pathname+(params.size?'?'+params:''));if(focus)container.focus();
    }catch(error){previous.hidden=next.hidden=true;status.textContent=error.message+' Vorherige Anzeige bleibt erhalten. Mit Aktualisieren erneut versuchen.';}
    finally{loading=false;container.removeAttribute('aria-busy');previous.disabled=next.disabled=refresh.disabled=false;form.querySelector('button').disabled=false;}
  }
  form.addEventListener('submit',event=>{event.preventDefault();query=form.elements.q.value.trim();day=form.elements.at.value;load('',[],true);});
  previous.addEventListener('click',()=>load(history.at(-1)||'',history.slice(0,-1),true));next.addEventListener('click',()=>load(nextCursor,[...history,cursor],true));refresh.addEventListener('click',()=>{query=form.elements.q.value.trim();day=form.elements.at.value;load('',[],true);});
  query=form.elements.q.value.trim();day=form.elements.at.value;load('',[]);
})();
