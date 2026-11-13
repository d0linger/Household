((root) => {
 'use strict';
 const tr = (source, ...args) => root.HouseholdI18n ? root.HouseholdI18n.t(source, ...args) : source.replace(/%s/g, () => String(args.shift() ?? ''));
 const categories = Object.freeze(['chores_due','chores_digest','chores_escalation','cooking_timer','shopping','pantry','fuel','expenses','meters','documents','mealplan','security','test']);
 const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
 const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
 const version = value => Number.isSafeInteger(value) && value >= 0;
 const stamp = value => value === null || typeof value === 'string' && Number.isFinite(Date.parse(value));
 const invalid = () => Error(tr('Die Einstellungen konnten nicht sicher gelesen werden. Bitte erneut laden.'));
 function minute(value) {
  if (value === '') return null;
  if (typeof value !== 'string' || !/^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/.test(value)) throw Error(tr('Gib eine gültige Uhrzeit ein.'));
  return Number(value.slice(0,2))*60+Number(value.slice(3));
 }
 function clock(value) {
  if (value === null) return '';
  if (!Number.isInteger(value) || value < 0 || value > 1439) throw invalid();
  return String(Math.floor(value/60)).padStart(2,'0')+':'+String(value%60).padStart(2,'0');
 }
 function preference(value) {
  if (!object(value) || !categories.includes(value.category) || typeof value.enabled !== 'boolean' || !version(value.version) ||
      typeof value.timezone !== 'string' || value.timezone.length < 1 || value.timezone.length > 100 || !stamp(value.enabled_since)) throw invalid();
  clock(value.quiet_start); clock(value.quiet_end);
  if ((value.quiet_start === null) !== (value.quiet_end === null) || value.quiet_start !== null && value.quiet_start === value.quiet_end) throw invalid();
  return value;
 }
 function preferences(value) {
  if (!object(value) || !Array.isArray(value.preferences) || !Array.isArray(value.categories) || value.preferences.length > 13 || value.categories.length > 13) throw invalid();
  const seen = new Set(), flags = new Set();
  for (const p of value.preferences) { preference(p); if (seen.has(p.category)) throw invalid(); seen.add(p.category); }
  for (const c of value.categories) {
   if (!object(c) || !categories.includes(c.category) || typeof c.available !== 'boolean' || flags.has(c.category) || !seen.has(c.category)) throw invalid();
   flags.add(c.category);
  }
  if (seen.size !== flags.size) throw invalid();
  return value;
 }
 function delivery(value) {
  if (!object(value) || !uuid.test(value.id) || !['pending','accepted','cancelled','expired','failed'].includes(value.state) ||
      typeof value.safe_code !== 'string' || !/^[a-z0-9_]{0,64}$/.test(value.safe_code) || !stamp(value.accepted_at) ||
      typeof value.updated_at !== 'string' || !stamp(value.updated_at) || value.seen_at !== undefined && !stamp(value.seen_at)) throw invalid();
  if (value.state === 'accepted' && value.accepted_at === null || ['display_requested','user_confirmed'].includes(value.safe_code) && value.state !== 'accepted') throw invalid();
  return value;
 }
 function devices(value) {
  if (!object(value) || !Array.isArray(value.devices) || value.devices.length > 128) throw invalid();
  const seen = new Set();
  for (const d of value.devices) {
   if (!object(d) || !uuid.test(d.id) || !uuid.test(d.device_id) || seen.has(d.id) ||
       !['registered','disabled','renewal_required','expired'].includes(d.state) || typeof d.name !== 'string' || Array.from(d.name).length > 80 ||
       typeof d.registered_at !== 'string' || !stamp(d.registered_at) || !stamp(d.expires_at) || !Array.isArray(d.categories) || d.categories.length > 13) throw invalid();
   seen.add(d.id); const names = new Set();
   for (const p of d.categories) {
    if (!object(p) || !categories.includes(p.category) || names.has(p.category) || typeof p.enabled !== 'boolean' ||
        typeof p.paused !== 'boolean' || typeof p.name !== 'string' || Array.from(p.name).length > 80 || !version(p.version) || !stamp(p.enabled_since)) throw invalid();
    names.add(p.category);
   }
   if (d.last_test !== null && d.last_test !== undefined) delivery(d.last_test);
  }
  return value.devices;
 }
 function deliveryText(value) {
  delivery(value);
  if (value.safe_code === 'user_confirmed') return tr('Von dir als gesehen bestätigt.');
  if (value.safe_code === 'display_requested') return tr('Der Browser hat die Anzeige angefordert. Sichtbarkeit und Lesen sind damit noch nicht bestätigt.');
  switch (value.state) {
   case 'accepted': return tr('Vom Push-Dienst angenommen. Die Anzeige auf deinem Gerät ist noch nicht bestätigt.');
   case 'cancelled': return tr('Testnachricht aufgehoben. Prüfe die aktuelle Gerätefreigabe.');
   case 'expired': return tr('Das Zeitfenster der Testnachricht ist abgelaufen. Du kannst bewusst einen neuen Test senden.');
   case 'failed': return tr('Der Versand wurde nicht bestätigt. Prüfe den Gerätestatus, bevor du erneut testest.');
   default: return tr('Testnachricht angefordert. Der Server prüft die Zustellung.');
  }
 }
 const api = {minute,clock,preference,preferences,devices,delivery,deliveryText};
 if (typeof module === 'object' && module.exports) { module.exports = api; return; }
 const panel = document.querySelector('[data-notification-settings]'), U = root.HouseholdShoppingUI;
 if (!panel || !U) return;
 const labels = {
  chores_due:tr('Fällige Aufgaben'),chores_digest:tr('Aufgaben-Tagesübersicht'),chores_escalation:tr('Überfällige Aufgaben'),
  cooking_timer:tr('Kochtimer'),shopping:tr('Einkauf'),pantry:tr('Vorrat'),fuel:tr('Fahrzeuge'),expenses:tr('Finanzen'),
  meters:tr('Zähler'),documents:tr('Dokumente'),mealplan:tr('Essensplan'),security:tr('Kontosicherheit'),test:tr('Testnachrichten')
 };
 const $ = selector => panel.querySelector(selector), node = (tag,text,className) => { const e=document.createElement(tag); if(text!==undefined)e.textContent=text; if(className)e.className=className; return e; };
 const button = (text, action) => { const b=node('button',text);b.type='button';if(action)b.addEventListener('click',action);return b; };
 let snapshot = null, stopped = false, loading = false, epoch = 0, pending = 0, enabledModules = new Set();
 const polls = new Map(), timers = new Set(), globalStatus=$('[data-notification-status]');
 function message(element, text, error=false) { element.textContent=text;element.dataset.error=String(error); }
 function announce(text, error=false) { message(globalStatus,text,error); }
 function status() { const p=node('p');p.setAttribute('role','status');p.setAttribute('aria-live','polite');return p; }
 function stopPolls() { for(const timer of timers)clearTimeout(timer);timers.clear();polls.clear(); }
 function lock() {
  stopped=true;++epoch;stopPolls();$('[data-notification-preferences]').replaceChildren();$('[data-notification-devices]').replaceChildren();
  for(const control of panel.querySelectorAll('button,input,select'))control.disabled=true;
  announce(tr('Die Anmeldung wurde geändert. Öffne diese Seite nach der Anmeldung erneut.'),true);
 }
 function refreshLock() { $('[data-notification-refresh]').disabled=stopped||loading||pending>0; }
 async function read(url, options) {
  try { return await U.raw(url,{timeout:10000,...options}); }
  catch(error) { if(error.status===401||error.status===403)root.dispatchEvent(new Event('household-push-lock'));throw error; }
 }
 async function account() {
  const current=await read('/api/v1/core/bootstrap'),expected=document.body.dataset;
  if(current.user?.id!==expected.user||current.user?.household_id!==expected.household) { root.dispatchEvent(new Event('household-push-lock'));throw invalid(); }
  enabledModules=new Set(Object.entries(current.states||{}).filter(([,state])=>state.state==='enabled').map(([name])=>name));
 }
 function failure(error) {
  if(stopped)return tr('Die Anmeldung wurde geändert. Öffne diese Seite nach der Anmeldung erneut.');
  if(error.status===429)return tr('Zu viele Anfragen. Warte kurz und versuche es erneut.');
  return error.code==='HTTP'?error.message:tr('Die Anfrage wurde nicht bestätigt. Dein Entwurf bleibt erhalten; prüfe die Verbindung und versuche es erneut.');
 }
 function enable(form,busy,available=true) {
  form.setAttribute('aria-busy',String(busy));
  for(const field of form.querySelectorAll('input,button,select'))field.disabled=busy||stopped||!available;
 }
 function check(text,checked) {
  const label=node('label',undefined,'notification-setting-check'),input=node('input');input.type='checkbox';input.checked=checked;label.append(input,document.createTextNode(text));return {label,input};
 }
 function field(text,type,value) {
  const label=node('label',text),input=node('input');input.type=type;input.value=value;label.append(input);return {label,input};
 }
 function date(raw) { if(!raw)return tr('Unbekannt');return new Intl.DateTimeFormat(root.HouseholdI18n?.locale()||document.documentElement.lang,{dateStyle:'medium',timeStyle:'short'}).format(new Date(raw)); }
 function preferenceRow(initial,availability) {
  let current=initial,busy=false,shoppingControl=null;
  const article=node('article',undefined,'notification-preference'),heading=node('div'),form=node('form',undefined,'notification-form'),result=status();
  const title=node('h3',labels[initial.category]);title.id='notification-category-'+initial.category;form.setAttribute('aria-labelledby',title.id);heading.append(title);
  const chores=initial.category.startsWith('chores_');
  if(chores) {
   heading.append(node('p',tr('Dein Aufgabenmodus, deine Zeitzone, Ruhezeiten und Eskalationsfreigaben bleiben maßgeblich. Im Modus Aus werden keine Aufgabenhinweise gesendet.'),'notification-description'));
   if(initial.category==='chores_due')heading.append(node('p',tr('Einzelne Fälligkeiten kannst du hier zusätzlich zur Tagesübersicht aktivieren. Gemeldet werden deine aktuell zugewiesenen oder übernommenen Aufgaben.'),'notification-description'));
   if(initial.category==='chores_digest')heading.append(node('p',tr('Wähle dafür in den Aufgabeneinstellungen den Modus Persönliche Zusammenfassung.'),'notification-description'));
   if(initial.category==='chores_escalation')heading.append(node('p',tr('Du erhältst nur ausdrücklich an dich gerichtete Eskalationen, denen du in den Aufgabeneinstellungen zugestimmt hast.'),'notification-description'));
   if(enabledModules.has('chores')){const link=node('a',tr('Aufgabeneinstellungen öffnen'));link.href='/m/chores/workspace/settings';heading.append(link);}
   else heading.append(node('p',tr('Für Gerätehinweise muss das Aufgabenmodul aktiv sein.'),'notification-description'));
  } else if(initial.category==='cooking_timer') {
   heading.append(node('p',tr('Erlaube Kochtimer hier und bei den gewünschten Geräten vor dem Timerstart. Spätere Freigaben holen laufende Timer nicht nach; starte sie bei Bedarf ausdrücklich neu.'),'notification-description'));
   heading.append(node('p',tr('Nur deine eigenen laufenden Kochtimer werden gemeldet. Pause, Neustart, Bestätigung oder Sitzungsende entwerten alte Hinweise. Ein Push ist kein garantierter Wecker.'),'notification-description'));
  } else if(initial.category==='shopping') {
   heading.append(node('p',tr('Beobachtete Listen und Stummschaltungen bleiben maßgeblich. Diese Einstellung ersetzt keine Listenfreigabe.'),'notification-description'));
   shoppingControl=node('p',undefined,'notification-description');shoppingControl.dataset.shoppingControl='';heading.append(shoppingControl);
   heading.append(node('p',tr('Bestehende Geräte werden nicht automatisch für Einkauf freigegeben. Erlaube die Kategorie unten bei jedem gewünschten Gerät ausdrücklich; die Push-Freigabe der jeweiligen Liste bleibt zusätzlich erforderlich.'),'notification-description'));
   if(enabledModules.has('shopping')){const link=node('a',tr('Listenfreigaben und Stummschaltungen prüfen'));link.href='/settings#settings-notifications';heading.append(link);}
   else heading.append(node('p',tr('Für Gerätehinweise muss das Einkaufsmodul aktiv sein.'),'notification-description'));
  } else if(initial.category==='pantry') {
   heading.append(node('p',tr('Erlaube Vorrat hier und unten bei jedem gewünschten Gerät ausdrücklich, bevor ein neuer Hinweis entsteht. Bestehende Geräte werden nicht automatisch freigegeben.'),'notification-description'));
   heading.append(node('p',tr('Neue Vorratshinweise werden gebündelt: höchstens eine allgemeine Nachricht pro Tag in der hier gewählten Zeitzone. Verspätete Hinweise bleiben nur bis zu ihrem ursprünglichen Ablauf gültig.'),'notification-description'));
   heading.append(node('p',tr('Eine Tageszusammenfassung verfällt spätestens um Mitternacht in dieser Zeitzone. Ruhezeiten verschieben sie nicht in den nächsten Tag.'),'notification-description'));
   const details=node('details'),summary=node('summary',tr('Wann du Vorratshinweise erhältst'));
   details.append(summary,node('p',tr('Mindestbestand: Nach deiner Freigabe muss zuerst ein ausreichender Bestand erkannt werden. Erst ein danach erkannter niedriger Bestand löst einen Hinweis aus. Bereits bestehende Engpässe werden nicht nachgemeldet.'),'notification-description'));
   details.append(node('p',tr('Die Bestände werden regelmäßig geprüft. Ein kurzer Engpass zwischen zwei Prüfungen kann unbemerkt bleiben.'),'notification-description'));
   details.append(node('p',tr('Ablaufdaten werden ab 09:00 Uhr in der Haushaltszeitzone berücksichtigt. Ein Verbrauchsdatum hat Vorrang vor dem Mindesthaltbarkeitsdatum. Verbrauchte oder entfernte Chargen werden nicht gemeldet; Hinweise nach dem Öffnen sind keine Ablaufalarme.'),'notification-description'));
   heading.append(details);
   if(enabledModules.has('pantry')){const link=node('a',tr('Vorrat und Ablaufdaten prüfen'));link.href='/m/pantry/stock';heading.append(link);}
   else heading.append(node('p',tr('Für Gerätehinweise muss das Vorratsmodul aktiv sein.'),'notification-description'));
  } else if(initial.category==='fuel') {
   heading.append(node('p',tr('Erlaube Fahrzeuge hier und unten bei jedem gewünschten Gerät ausdrücklich vor der Frist. Die Zuständigkeit allein aktiviert keine Gerätehinweise.'),'notification-description'));
   heading.append(node('p',tr('Fahrzeugtermine gehen nur an das dafür gewählte Haushaltsmitglied. Wartungspläne verwenden ausschließlich ihre eigene Zuständigkeit; ohne Zuordnung wird niemand benachrichtigt.'),'notification-description'));
   const details=node('details'),summary=node('summary',tr('Regeln für Termine und Kilometerziele'));
   details.append(summary,node('p',tr('Datumsfristen werden sieben Tage vorher und am Fälligkeitstag ab 09:00 Uhr in der Haushaltszeitzone berücksichtigt. Jeder Hinweis bleibt 24 Stunden gültig. Frühere Friststufen werden nicht nachgeholt.'),'notification-description'));
   details.append(node('p',tr('Änderst du Termin oder Zuständigkeit nach Beginn einer Friststufe, wird diese Stufe nicht mehr versendet.'),'notification-description'));
   details.append(node('p',tr('Kilometerhinweise benötigen einen eindeutigen Stand aus der passenden Zählerreihe, der höchstens 30 Kalendertage alt ist. Zuerst muss nach deiner Freigabe ein Stand unter dem Ziel erkannt werden, danach ein Stand am oder über dem Ziel. Ein bereits erreichtes Ziel beim ersten Prüfen bleibt ohne Hinweis.'),'notification-description'));
   details.append(node('p',tr('Auch der dokumentierte Anfangsstand der passenden Zählerreihe kann gelten, wenn er ausreichend aktuell ist.'),'notification-description'));
   details.append(node('p',tr('Künftige, zu alte oder widersprüchliche Zählerstände lösen keine Kilometerhinweise aus. Änderungen an Ziel oder Zuständigkeit beginnen diese Prüfung neu. Archivierte Fahrzeuge und inaktive Pläne erhalten keine neuen Hinweise; erledigte oder geänderte Fristen entwerten alte Hinweise.'),'notification-description'));
   details.append(node('p',tr('Eine verknüpfte Aufgabe hat eigene Benachrichtigungen. Wenn du beide Kategorien erlaubst, können beide Hinweise erscheinen.'),'notification-description'));
   heading.append(details);
   if(enabledModules.has('fuel')){
    const links=node('div',undefined,'notification-actions'),overview=node('a',tr('Fahrzeugtermine und Zuständigkeit prüfen')),maintenance=node('a',tr('Wartungspläne prüfen'));
    overview.href='/m/fuel/workspace/overview';maintenance.href='/m/fuel/workspace/maintenance';links.append(overview,maintenance);heading.append(links);
   }else heading.append(node('p',tr('Für Gerätehinweise muss das Fahrzeugmodul aktiv sein.'),'notification-description'));
  } else if(initial.category==='expenses') {
   heading.append(node('p',tr('Erlaube Finanzhinweise hier und unten bei jedem gewünschten Gerät ausdrücklich. Eine bestätigte Fälligkeit oder Zuständigkeit allein aktiviert keine Gerätehinweise.'),'notification-description'));
   heading.append(node('p',tr('Nachrichten auf dem Sperrbildschirm enthalten keine Beträge, Kontonamen oder zahlenden Personen. Private Angaben bleiben in der aktuell berechtigten Finanzansicht.'),'notification-description'));
   const details=node('details'),summary=node('summary',tr('Zahlungsfristen und Budgetschwellen'));
   details.append(summary,node('p',tr('Zahlungserinnerungen gehören zu einer bestehenden Buchung. Speichere und bestätige Fälligkeit und Zuständigkeit ausdrücklich. Hinweise gehen nur an die zuständige Person, solange sie die Buchung und ihre geschützten Quellen sehen darf.'),'notification-description'));
   details.append(node('p',tr('Datumsfristen werden sieben Tage vorher und am Fälligkeitstag ab 09:00 Uhr in der Haushaltszeitzone berücksichtigt. Jeder Hinweis bleibt 24 Stunden gültig. Frühere Friststufen werden nicht nachgeholt.'),'notification-description'));
   details.append(node('p',tr('Erledigte oder aufgehobene Erinnerungen und belegte tatsächliche Zahlungen beenden offene Hinweise. Eine gebuchte Ausgabe allein ist noch keine belegte Zahlung. Änderungen an Buchung, Frist oder Zuständigkeit können eine erneute Bestätigung erfordern.'),'notification-description'));
   details.append(node('p',tr('Budgets melden neue Überschreitungen bei 80 % und 100 %. Nach deiner Freigabe wird zunächst still ein Ausgangsstand ermittelt. Eine bereits überschrittene Schwelle wird nicht nachgemeldet; sie wird erst nach einem Rückgang unter 75 % beziehungsweise 95 % wieder bereit.'),'notification-description'));
   details.append(node('p',tr('Es zählt derselbe aktuell sichtbare vollständige Monat wie in der Budgetansicht, einschließlich Übertrag. Unvollständige Berechnungen und ein verfügbares Monatsbudget von null oder weniger lösen keine Prozenthinweise aus. Änderungen an Sichtbarkeit, Budget oder Monat beginnen mit einem neuen stillen Ausgangsstand.'),'notification-description'));
   heading.append(details);
   if(enabledModules.has('expenses')){const links=node('div',undefined,'notification-actions'),entries=node('a',tr('Buchungen und Zahlungserinnerungen prüfen')),budgets=node('a',tr('Aktuelle Budgets prüfen'));entries.href='/m/expenses/workspace/entries';budgets.href='/m/expenses/workspace/planning?section=budgets';links.append(entries,budgets);heading.append(links);}
   else heading.append(node('p',tr('Für Gerätehinweise muss das Finanzmodul aktiv sein.'),'notification-description'));
  } else if(initial.category==='meters') {
   heading.append(node('p',tr('Zählerhinweise benötigen eine ausdrücklich gewählte Zuständigkeit und die Freigabe im Konto sowie auf diesem Gerät.'),'notification-description'));
   heading.append(node('p',tr('Messlücken sind keine Verbrauchsspitzen. Hinweise enthalten keine Messwerte, Standorte oder Gerätenamen.'),'notification-description'));
   if(enabledModules.has('meters')){const link=node('a',tr('Zählerhinweise einrichten'));link.href='/m/meters/workspace/settings';heading.append(link);}
  } else if(initial.category==='documents') {   const details=node('details');details.append(node('summary',tr('Fristen und persönliche Importprobleme')));   details.append(node('p',tr('Fristhinweise gehen nur an die ausdrücklich zugewiesene Person. Bestätigte Erinnerung und Haushaltszeitzone bleiben maßgeblich; verstrichene Hinweise werden nicht nachgeholt.')));   details.append(node('p',tr('Verarbeitungsfehler gehen nur an die anfragende Person, Importfehler nur an den weiterhin berechtigten Administrator des Eingangs. Wiederholte Fehler desselben Vorgangs erzeugen keine neue Meldung.')));   details.append(node('p',tr('Dokumenttitel, Dateinamen, Postfächer und Inhalte werden nicht übertragen. Konto und jedes Gerät brauchen eine eigene Freigabe.')));heading.append(details);   if(enabledModules.has('documents')){const link=node('a',tr('Bestätigte Dokumentfristen prüfen'));link.href='/m/documents/workspace/deadlines';heading.append(link);}  } else if(initial.category==='mealplan') {   const details=node('details');details.append(node('summary',tr('Bestätigte Zubereitung und Vorbereitung')));   details.append(node('p',tr('Nur ausdrücklich zugewiesene Personen erhalten Hinweise zu bestätigten Terminen. Teilnehmer und Ersteller werden nicht automatisch benachrichtigt.')));   details.append(node('p',tr('Termine werden im gespeicherten Mahlzeiteneditor bestätigt. Änderungen an der Mahlzeit erfordern eine erneute Bestätigung; Vorlagen, offene, gekochte und abgesagte Mahlzeiten senden keine Hinweise.')));   details.append(node('p',tr('Hinweise bleiben höchstens vier Stunden aktuell. Rezeptdauer und Tagesabschnitt erzeugen keine Uhrzeit; vergangene Termine werden nicht nachgeholt. Konto und jedes Gerät brauchen eine eigene Freigabe.')));heading.append(details);   if(enabledModules.has('mealplan')){const link=node('a',tr('Mahlzeiten und bestätigte Termine prüfen'));link.href='/m/mealplan/plans';heading.append(link);}  } else if(initial.category==='security') {   const details=node('details');details.append(node('summary',tr('Persönliche Änderungen an deinem Konto')));   details.append(node('p',tr('Hinweise betreffen nur dein eigenes Konto: Passwort, Anmeldefaktoren, Passkeys, Sitzungen und neu autorisierte Geräte. Andere Haushaltsmitglieder werden nicht benachrichtigt.')));   details.append(node('p',tr('Ein Gerätehinweis ist höchstens eine Stunde aktuell. Konto und Gerät müssen bereits vor dem Ereignis zugestimmt haben; spätere Freigaben holen keine Ereignisse nach.')));   details.append(node('p',tr('Die persönliche Meldung bleibt in deinem Posteingang, auch wenn eine Sicherheitsänderung alle Sitzungen beendet. Auf dem Sperrbildschirm erscheinen keine Namen, Kennwörter oder Gerätedetails.')));heading.append(details);   const link=node('a',tr('Kontosicherheit prüfen'));link.href='/settings/security';heading.append(link);  } else if(initial.category==='test') {
   heading.append(node('p',tr('Eine Testnachricht wird ausschließlich über deinen ausdrücklichen Testklick ausgelöst.'),'notification-description'));
  }
  if(!availability.available){heading.append(node('p',tr('Gerätehinweise für diese Kategorie sind noch nicht verfügbar.'),'notification-description'));article.classList.add('notification-unavailable');article.append(heading);return article;}
  const allowed=check(tr('Gerätehinweise erlauben'),current.enabled);form.append(allowed.label);
  let zone,start,end;
  if(!chores) {
   const more=node('details'),summary=node('summary',tr('Zeitzone und Ruhezeiten')),fields=node('div',undefined,'notification-quiet');
   zone=field(tr('Zeitzone'),'text',current.timezone);zone.input.maxLength=100;zone.input.required=true;zone.input.placeholder='Europe/Vienna';zone.input.autocomplete='off';
   start=field(tr('Ruhezeit von'),'time',clock(current.quiet_start));end=field(tr('Ruhezeit bis'),'time',clock(current.quiet_end));
   fields.append(zone.label,start.label,end.label);more.append(summary,fields,node('p',tr('Beide Uhrzeiten leer lassen, um keine zusätzliche Ruhezeit festzulegen.'),'fine'));form.append(more);
  }
  const actions=node('div',undefined,'notification-actions'),save=node('button',tr('Diese Kategorie speichern'));save.type='submit';actions.append(save);form.append(actions,result);
  function shoppingGuidance(){
   if(!shoppingControl)return;
   shoppingControl.textContent=current.version>0?tr('Diese Auswahl steuert deine Einkaufs-Gerätehinweise jetzt dauerhaft. Beim Ausschalten bleibt der frühere Listenversand aus; In-App-Hinweise und Listenfreigaben bleiben unabhängig davon erhalten.'):tr('Mit dem ersten Speichern ersetzt diese Auswahl dauerhaft die bisherigen Gerätehinweise für Einkaufslisten. Das gilt auch, wenn du die Kategorie ausgeschaltet speicherst.');
   shoppingControl.dataset.shoppingControl=current.version>0?'central':'legacy';
  }
  shoppingGuidance();
  function apply(next) { current=preference(next);allowed.input.checked=current.enabled;if(zone){zone.input.value=current.timezone;start.input.value=clock(current.quiet_start);end.input.value=clock(current.quiet_end);}shoppingGuidance(); }
  form.addEventListener('submit',async event=>{
   event.preventDefault();if(busy||stopped||!availability.available||!form.reportValidity())return;
   let next;
   try {
    next={category:current.category,enabled:allowed.input.checked,version:current.version,timezone:zone?zone.input.value.trim():current.timezone,
     quiet_start:start?minute(start.input.value):current.quiet_start,quiet_end:end?minute(end.input.value):current.quiet_end};
    if((next.quiet_start===null)!==(next.quiet_end===null)||next.quiet_start!==null&&next.quiet_start===next.quiet_end)throw Error(tr('Fülle beide Ruhezeiten aus oder lasse beide leer. Beginn und Ende müssen verschieden sein.'));
   }catch(error){message(result,error.message,true);return;}
   busy=true;++pending;enable(form,true);refreshLock();message(result,tr('Wird gespeichert …'));const turn=epoch;
   try {
    const saved=preference(await read('/api/v1/core/notification-preferences',{method:'POST',body:next}));
    if(stopped||turn!==epoch)return;if(saved.category!==current.category)throw invalid();apply(saved);
    snapshot.preferences=snapshot.preferences.map(p=>p.category===saved.category?saved:p);message(result,tr('Diese Kategorie ist gespeichert.'));
   }catch(error){
    if(!stopped&&turn===epoch&&error.status===409){
     try { const latest=preferences(await read('/api/v1/core/notification-preferences')),saved=latest.preferences.find(p=>p.category===current.category);if(stopped||turn!==epoch)return;if(!saved)throw invalid();apply(saved);message(result,tr('Diese Kategorie wurde inzwischen geändert. Der aktuelle Stand ist geladen; prüfe ihn vor dem Speichern.'),true); }
     catch(refreshError){if(!stopped&&turn===epoch)message(result,failure(refreshError),true);}
    }else if(!stopped&&turn===epoch)message(result,failure(error),true);
   }finally{busy=false;--pending;enable(form,false,availability.available);refreshLock();}
  });
  enable(form,false,availability.available);article.append(heading,form);return article;
 }
 function deviceCategory(device,initial,category) {
  let current=initial,busy=false;
  const form=node('form',undefined,'notification-device-category'),fields=node('fieldset'),legend=node('legend',labels[category.category]),controls=node('div',undefined,'notification-device-fields');
  form.dataset.notificationCategory=category.category;
  const name=field(tr('Gerätename für diese Kategorie'),'text',current.name),allowed=check(tr('Erlauben'),current.enabled),paused=check(tr('Pausieren'),current.paused),result=status();
  name.input.maxLength=80;name.input.autocomplete='off';name.input.placeholder=tr('Zum Beispiel: Küche');
  controls.append(name.label,allowed.label,paused.label);fields.append(legend,controls);const save=node('button',tr('Gerätefreigabe speichern'));save.type='submit';form.append(fields,save,result);
  const available=category.available&&device.state==='registered';
  function apply(saved){current=saved;name.input.value=saved.name;allowed.input.checked=saved.enabled;paused.input.checked=saved.paused;}
  form.addEventListener('submit',async event=>{
   event.preventDefault();if(busy||stopped||!available||!form.reportValidity())return;
   const body={category:category.category,enabled:allowed.input.checked,paused:paused.input.checked,name:name.input.value.trim(),version:current.version};
   busy=true;++pending;enable(form,true);refreshLock();message(result,tr('Wird gespeichert …'));const turn=epoch;
   try {
    const saved=await read('/api/v1/core/push/subscriptions/'+device.id+'/preferences',{method:'POST',body});
    devices({devices:[{...device,categories:[saved],last_test:null}]});
    if(stopped||turn!==epoch)return;if(saved.category!==category.category)throw invalid();apply(saved);message(result,tr('Gerätefreigabe gespeichert. Die persönliche Kategorie muss ebenfalls erlaubt sein.'));
   }catch(error){
    if(!stopped&&turn===epoch&&error.status===409){
     try {const latest=devices(await read('/api/v1/core/push/device-preferences')),saved=latest.find(d=>d.id===device.id)?.categories.find(p=>p.category===category.category);if(stopped||turn!==epoch)return;if(!saved)throw invalid();apply(saved);message(result,tr('Die Gerätefreigabe wurde inzwischen geändert. Prüfe den neu geladenen Stand.'),true);}
     catch(refreshError){if(!stopped&&turn===epoch)message(result,failure(refreshError),true);}
    }else if(!stopped&&turn===epoch)message(result,failure(error),true);
   }finally{busy=false;--pending;enable(form,false,available);refreshLock();}
  });
  enable(form,false,available);return form;
 }
 function schedule(key,callback) {
  const timer=setTimeout(()=>{timers.delete(timer);if(!stopped&&polls.has(key))void callback();},5000);timers.add(timer);
 }
 function deviceRow(device,index,localID) {
  const article=node('article',undefined,'notification-device'),header=node('div',undefined,'notification-device-header'),intro=node('div');
  const title=device.name||(device.device_id===localID?tr('Dieses Gerät'):tr('Gerät %s',index+1));intro.append(node('h3',title));
  const states={registered:tr('Am Server registriert'),disabled:tr('Deaktiviert'),renewal_required:tr('Erneut verbinden erforderlich'),expired:tr('Freigabe abgelaufen')};
  intro.append(node('p',states[device.state]),node('p',tr('Eingerichtet: %s',date(device.registered_at))));if(device.expires_at)intro.append(node('p',tr('Freigabe bis: %s',date(device.expires_at))));
  const send=button(tr('Testnachricht senden')),result=status(),seen=button(tr('Ich habe diese Testnachricht gesehen')),testArea=node('div',undefined,'notification-test-result');
  let active=device.last_test||null,sending=false,confirming=false,uncertain=false,pollCount=0,pollStart=performance.now();
  const testAvailable=!!snapshot.categories.find(c=>c.category==='test'&&c.available)&&device.state==='registered';
  seen.hidden=true;send.disabled=!testAvailable;testArea.append(result,seen);header.append(intro,send);article.append(header,testArea);
  function display(value) { active=delivery(value);message(result,deliveryText(active));seen.hidden=active.state!=='accepted'||active.safe_code==='user_confirmed';send.disabled=!testAvailable||sending||active.state==='pending'; }
  async function poll() {
   if(stopped||!active||active.state!=='pending'||!polls.has(device.id))return;
   if(++pollCount>12||performance.now()-pollStart>=60000){polls.delete(device.id);send.disabled=!testAvailable;message(result,tr('Die Anzeige ist nicht bestätigt. Prüfe das Gerät und lade den Status bei Bedarf erneut.'));return;}
   const turn=epoch,id=active.id;
   try {const current=delivery(await read('/api/v1/core/push/deliveries/'+id));if(stopped||turn!==epoch||active?.id!==id)return;display(current);if(active.state==='pending')schedule(device.id,poll);else polls.delete(device.id);}
   catch(error){polls.delete(device.id);if(!stopped&&turn===epoch){send.disabled=true;message(result,tr('Der Teststatus ist ungewiss. Aktualisiere die Übersicht, bevor du erneut sendest.'),true);}}
  }
  send.addEventListener('click',async()=>{
   if(sending||stopped||!testAvailable)return;sending=true;uncertain=false;++pending;send.disabled=true;refreshLock();message(result,tr('Testnachricht wird angefordert …'));const turn=epoch;
   try {
    const created=await read('/api/v1/core/push/subscriptions/'+device.id+'/test',{method:'POST',body:{}});
    if(stopped||turn!==epoch)return;if(!object(created)||!uuid.test(created.id)||created.state!=='pending')throw invalid();
    active={id:created.id,state:'pending',safe_code:'',accepted_at:null,updated_at:new Date().toISOString()};display(active);pollCount=0;pollStart=performance.now();polls.set(device.id,active.id);schedule(device.id,poll);
   }catch(error){uncertain=true;if(!stopped&&turn===epoch)message(result,tr('Der Teststatus ist ungewiss. Aktualisiere die Übersicht, bevor du erneut sendest.'),true);}
   finally{sending=false;--pending;refreshLock();if(!stopped)send.disabled=!testAvailable||uncertain||active?.state==='pending';}
  });
  seen.addEventListener('click',async()=>{
   if(confirming||stopped||active?.state!=='accepted'||active.safe_code==='user_confirmed')return;confirming=true;++pending;seen.disabled=true;refreshLock();const turn=epoch,id=active.id;
   try {const saved=delivery(await read('/api/v1/core/push/deliveries/'+id+'/seen',{method:'POST',body:{}}));if(stopped||turn!==epoch||active?.id!==id)return;display(saved);}
   catch(error){if(!stopped&&turn===epoch)message(result,failure(error),true);}
   finally{confirming=false;--pending;seen.disabled=stopped;refreshLock();}
  });
  if(active){display(active);if(active.state==='pending'){polls.set(device.id,active.id);schedule(device.id,poll);}}
  const details=node('details'),summary=node('summary',tr('Kategorien für dieses Gerät'));details.append(summary);
  const availableCategories=snapshot.categories.filter(c=>c.available);
  if(!availableCategories.length)details.append(node('p',tr('Für dieses Gerät sind derzeit keine Kategorien verfügbar.')));
  for(const c of availableCategories){const saved=device.categories.find(p=>p.category===c.category)||{category:c.category,enabled:false,paused:false,name:device.name,version:0,enabled_since:null};details.append(deviceCategory(device,saved,c));}
  article.append(details);return article;
 }
 async function load() {
  if(stopped||loading||pending)return;loading=true;refreshLock();const turn=++epoch;stopPolls();announce(tr('Benachrichtigungseinstellungen werden geladen …'));
  try {
   await account();if(stopped||turn!==epoch)return;
   const [rawPreferences,rawDevices]=await Promise.all([read('/api/v1/core/notification-preferences'),read('/api/v1/core/push/device-preferences')]);
   const next=preferences(rawPreferences),owned=devices(rawDevices);let local=null;try{local=await root.HouseholdPushDevice?.inspect();}catch{}
   if(stopped||turn!==epoch)return;snapshot=next;
   const categoryRows=c=>preferenceRow(snapshot.preferences.find(p=>p.category===c.category),c);
   const available=snapshot.categories.filter(c=>c.available),unavailable=snapshot.categories.filter(c=>!c.available);
   const categoryHost=$('[data-notification-preferences]');categoryHost.replaceChildren(...available.map(categoryRows));
   if(unavailable.length){
    const details=node('details',undefined,'notification-help');details.classList.add('notification-upcoming');
    details.append(node('summary',tr('Weitere Kategorien · %s noch nicht verfügbar',unavailable.length)),...unavailable.map(categoryRows));categoryHost.append(details);
   }
   $('[data-notification-devices]').replaceChildren(...owned.map((d,i)=>deviceRow(d,i,local?.device_id)));
   if(!owned.length)$('[data-notification-devices]').append(node('p',tr('Noch keine eigenen Geräte registriert. Verbinde dieses Gerät oben und aktualisiere anschließend die Übersicht.'),'notification-empty'));
   announce(tr('Aktueller Stand geladen. Änderungen werden pro Zeile gespeichert.'));
  }catch(error){if(!stopped&&turn===epoch)announce(failure(error),true);}
  finally{loading=false;refreshLock();}
 }
 $('[data-notification-refresh]').addEventListener('click',()=>void load());
 root.addEventListener('household-push-lock',lock);
 root.addEventListener('pagehide',()=>{stopped=true;++epoch;stopPolls();panel.hidden=true;});
 root.addEventListener('pageshow',event=>{if(event.persisted)root.location.reload();});
 void load();
})(globalThis);
