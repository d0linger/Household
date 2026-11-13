const _householdText_shopping_barcode_js = (source, ...args) => { const i18n = globalThis.HouseholdI18n; if (i18n) return i18n.t(source, ...args); let index = 0; return args.length ? source.replace(/%%|%[sdv]/g, token => token === '%%' ? '%' : String(args[index++] ?? '')) : source; };
((root) => {
  'use strict';
  function normalize(raw) {
    if (typeof raw !== 'string') throw Error(_householdText_shopping_barcode_js("Barcode erneut eingeben."));
    const code = String(raw).trim();
    if (String(raw).length > 256 || !/^[\x21-\x7e]{1,64}$/.test(code)) throw Error(_householdText_shopping_barcode_js("1 bis 64 sichtbare ASCII-Zeichen ohne Leerzeichen eingeben."));
    return code;
  }
  function formatQuantity(value) {
    const [whole,fraction=''] = String(value).split('.');
    const significant = fraction.replace(/0+$/,'');
    return significant ? `${whole},${significant}` : whole;
  }
  function camera({secure,media,Detector,video,onCode,onStatus,onActive,later=setTimeout,cancel=clearTimeout}) {
    let revision = 0, stream, timer, expiry;
    function stop() {
      ++revision; if (timer !== undefined) cancel(timer); if (expiry !== undefined) cancel(expiry);
      timer = expiry = undefined;
      if (stream) for (const track of stream.getTracks()) track.stop();
      stream = undefined; video.pause(); video.srcObject = null; onActive(false);
    }
    async function start() {
      stop(); const current = revision;
      if (!secure || !media?.getUserMedia || !Detector?.getSupportedFormats) { onStatus(_householdText_shopping_barcode_js("Kamera hier nicht verfügbar. Barcode manuell oder per Handscanner eingeben.")); return; }
      try {
        const supported = await Detector.getSupportedFormats();
        if (current !== revision) return;
        const formats = supported.filter(f=>['ean_13','ean_8','upc_a','upc_e','code_128','code_39','itf','qr_code'].includes(f));
        if (!formats.length) { onStatus(_householdText_shopping_barcode_js("Keine lokale Barcode-Erkennung verfügbar. Manuelle Eingabe verwenden.")); return; }
        const detector = new Detector({formats});
        onActive(true);onStatus(_householdText_shopping_barcode_js("Kamerazugriff wird angefragt. Abbrechen oder weiterhin manuell eingeben ist möglich."));
        expiry = later(()=>{ if (current === revision) { stop(); onStatus(_householdText_shopping_barcode_js("Kamera nach einer Minute gestoppt. Bei Bedarf ausdrücklich erneut starten.")); } },60000);
        const acquired = await media.getUserMedia({audio:false,video:{facingMode:{ideal:'environment'},width:{ideal:1280,max:1280},height:{ideal:720,max:720}}});
        if (current !== revision) { for (const track of acquired.getTracks()) track.stop(); return; }
        stream = acquired; video.srcObject = stream;
        await video.play();
        if (current !== revision) return;
        onStatus(_householdText_shopping_barcode_js("Kamera aktiv. Ein Treffer füllt nur das Eingabefeld; danach lokal nachschlagen."));
        async function detect() {
          if (current !== revision) return;
          try {
            const matches = await detector.detect(video);
            if (current !== revision) return;
            if (matches.length) {
              const code = normalize(matches[0].rawValue); stop(); onCode(code);
              onStatus(_householdText_shopping_barcode_js("Code übernommen, Kamera gestoppt. Jetzt lokal nachschlagen und prüfen.")); return;
            }
            timer = later(()=>void detect(),300);
          } catch (_) { if (current === revision) { stop(); onStatus(_householdText_shopping_barcode_js("Lokale Erkennung nicht möglich. Barcode manuell oder per Handscanner eingeben.")); } }
        }
        void detect();
      } catch (_) { if (current === revision) { stop(); onStatus(_householdText_shopping_barcode_js("Kein Kamerazugriff. Berechtigung bei Bedarf prüfen oder manuell eingeben.")); } }
    }
    return {start,stop};
  }
  if (typeof module === 'object' && module.exports) { module.exports = {normalize,camera,formatQuantity}; return; }
  const panel = document.querySelector('#shopping-barcode'); if (!panel) return;
  const $ = selector => panel.querySelector(selector), lookupForm = $('#barcode-lookup'), codeInput = lookupForm.elements.code;
  const status = $('#barcode-status'), result = $('#barcode-result'), mapForm = $('#barcode-map'), select = mapForm?.elements.product_id;
  const mapButton = $('#barcode-map-save');
  const endpoint = `/api/v1/shopping/lists/${panel.dataset.list}/barcode`;
  let view, selectedProduct, busy = false, revision = 0, pending;
  const node = (tag,text) => { const n = document.createElement(tag); n.textContent = text; return n; };
  const description = product => _householdText_shopping_barcode_js("%s%s · %s %s pro Packung", product.name, product.brand ? ' · '+product.brand : '', formatQuantity(product.pack_quantity), product.pack_unit);
  async function api(url,body,key) {
    const response = await fetch(url,{method:body ? 'POST':'GET',headers:{Accept:'application/json',...(body ? {'Content-Type':'application/json','X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content,'Idempotency-Key':key}: {})},body:body ? JSON.stringify(body):undefined});
    const value = await response.json(); if (!response.ok) throw Error(value.error || _householdText_shopping_barcode_js("Daten erneut prüfen. Deine Eingabe bleibt erhalten.")); return value;
  }
  function activity(active) { busy = active; codeInput.disabled = active; lookupForm.querySelector('button').disabled = active; if (select) select.disabled = active || select.dataset.referenceState !== 'ready'; }
  const capture = camera({secure:root.isSecureContext,media:navigator.mediaDevices,Detector:root.BarcodeDetector,video:$('#barcode-video'),onCode:code=>{codeInput.value=code;invalidate();codeInput.focus();},onStatus:message=>{$('#barcode-camera-status').textContent=message;},onActive:active=>{$('#barcode-video').hidden=!active;$('#barcode-start').disabled=active;$('#barcode-stop').disabled=!active;}});
  function invalidate() { ++revision; view = selectedProduct = pending = undefined; result.hidden = true; result.replaceChildren(); if(mapForm)mapForm.hidden=true; }
  codeInput.addEventListener('input',invalidate);
  $('#barcode-start').addEventListener('click',()=>{if(!busy)void capture.start();});
  $('#barcode-stop').addEventListener('click',()=>{capture.stop();$('#barcode-camera-status').textContent=_householdText_shopping_barcode_js("Kamera gestoppt. Manuelle Eingabe bleibt verfügbar.");});
  $('#barcode-camera').addEventListener('toggle',event=>{if(!event.currentTarget.open)capture.stop();});
  root.addEventListener('pagehide',()=>{++revision;capture.stop();});
  document.addEventListener('visibilitychange',()=>{if(document.hidden)capture.stop();});
  async function lookup() {
    if (busy) return; capture.stop(); let code;
    try { code = normalize(codeInput.value); } catch(error) { status.textContent=error.message;codeInput.focus();return; }
    invalidate(); const current = revision; activity(true); status.textContent=_householdText_shopping_barcode_js("Lokale Zuordnung wird gesucht …");
    try {
      const loaded = await api(endpoint+'?'+new URLSearchParams({code})); if(current !== revision)return;
      view=loaded;codeInput.value=view.code;result.hidden=false;result.append(node('h2',view.product ? _householdText_shopping_barcode_js("Produkt gefunden"):_householdText_shopping_barcode_js("Noch keine lokale Zuordnung")),node('p',_householdText_shopping_barcode_js("Barcode: ")+view.code));
      status.textContent=_householdText_shopping_barcode_js("Nur lokale Daten geprüft. Es wurde nichts ergänzt oder gekauft.");
      if(view.product){
        result.append(node('p',description(view.product)));
        if(panel.dataset.canWrite==='true')renderActions();
      }else if(mapForm){mapForm.hidden=false;mapButton.disabled=true;select.value='';$('#barcode-map-product').textContent=_householdText_shopping_barcode_js("Bestehendes Produkt ausdrücklich auswählen.");}
    }catch(error){if(current===revision)status.textContent=error.message;}
    finally{activity(false);}
  }
  lookupForm.addEventListener('submit',event=>{event.preventDefault();void lookup();});
  async function mutate(intent,product,quantity='') {
    capture.stop();
    const body={generation:view.generation,list_version:view.list_version,code:view.code,alias_id:intent==='map'?'':view.alias_id,alias_version:intent==='map'?0:view.alias_version,product_id:product.id,product_version:product.version,quantity};
    const encoded=JSON.stringify(body);if(!pending||pending.intent!==intent||pending.body!==encoded)pending={intent,body:encoded,key:crypto.randomUUID()};
    return api(endpoint+'/'+intent,body,pending.key);
  }
  function renderActions(){
    const form=node('form',''),label=node('label',_householdText_shopping_barcode_js("Anzahl Packungen selbst festlegen")),quantity=node('input',''),button=node('button',_householdText_shopping_barcode_js("Geprüft zur Liste hinzufügen"));
    quantity.name='quantity';quantity.inputMode='decimal';quantity.maxLength=40;quantity.required=true;label.append(quantity);form.append(label,button);result.append(form);
    const remove=node('button',_householdText_shopping_barcode_js("Diese lokale Zuordnung entfernen"));remove.type='button';remove.className='subtle';result.append(remove);
    form.addEventListener('submit',async event=>{event.preventDefault();if(busy||!form.reportValidity())return;activity(true);button.disabled=remove.disabled=true;
      try{await mutate('add',view.product,quantity.value.trim().replace(',','.'));form.remove();remove.remove();status.textContent=_householdText_shopping_barcode_js("Ein ungeprüfter Einkaufsartikel wurde ergänzt. Kein Einkauf wurde bestätigt.");result.append(node('p',_householdText_shopping_barcode_js("Menge wurde ausdrücklich übernommen. Weitere Artikel über die Liste prüfen.")));}
      catch(error){status.textContent=error.message;quantity.focus();}finally{activity(false);button.disabled=remove.disabled=false;}});
    remove.addEventListener('click',async()=>{if(busy)return;activity(true);button.disabled=remove.disabled=true;
      try{await mutate('remove',view.product);activity(false);await lookup();status.textContent=_householdText_shopping_barcode_js("Lokale Zuordnung entfernt. Artikel und bisherige Einkäufe bleiben erhalten.");}
      catch(error){status.textContent=error.message;remove.focus();}finally{activity(false);button.disabled=remove.disabled=false;}});
  }
  select?.addEventListener('change',async()=>{
    selectedProduct=undefined;pending=undefined;const current=revision,productID=select.value;mapButton.disabled=true;
    if(!productID){$('#barcode-map-product').textContent=_householdText_shopping_barcode_js("Bestehendes Produkt auswählen.");return;}
    try{const product=await api('/api/v1/m/shopping/products/'+encodeURIComponent(productID));if(current!==revision||select.value!==productID)return;selectedProduct=product;$('#barcode-map-product').textContent=description(product);mapButton.disabled=false;}
    catch(error){$('#barcode-map-product').textContent=error.message;}
  });
  mapForm?.addEventListener('submit',async event=>{event.preventDefault();if(busy||!view||!selectedProduct)return;activity(true);mapButton.disabled=true;
    try{await mutate('map',selectedProduct);activity(false);await lookup();status.textContent=_householdText_shopping_barcode_js("Zuordnung gespeichert. Erst eine ausdrücklich geprüfte Menge ergänzt einen Artikel.");}
    catch(error){status.textContent=error.message;select.focus();}finally{activity(false);mapButton.disabled=!selectedProduct;}});
})(globalThis);
