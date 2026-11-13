(() => {
 'use strict';
 const M = window.HouseholdMeters;
 if (!M) return;
 const {S, node, content} = M;
 const tr = source => globalThis.HouseholdI18n ? globalThis.HouseholdI18n.t(source) : source;
 const existingSettings = S.views.settings;

 async function readSettings() {
  await M.current();
  const register = M.register();
  if (!register) throw Error(tr('Zuerst ein Register auswählen.'));
  const response = await M.request(M.api + 'notification-settings?register=' + encodeURIComponent(register.id));
  if (response.generation !== S.generation || response.settings?.id !== register.id ||
      response.settings.register_version !== register.version || !Array.isArray(response.members)) {
   throw Error(tr('Modulgeneration geändert. Aktuellen Stand neu öffnen.'));
  }
  return response;
 }

 async function edit() {
  const response = await readSettings(), settings = response.settings;
  const choices = [['', tr('Keine Zuständigkeit')], ...response.members.map(member => [member.id, member.name + ' · ' + member.username])];
  const built = M.build(tr('Zählerbenachrichtigungen einrichten'), [
   {name:'responsible_member_id',label:tr('Zuständige Person'),choices,value:settings.responsible_member_id || ''},
   {name:'reading_enabled',label:tr('An fällige Ablesungen erinnern'),type:'checkbox',value:settings.reading_enabled},
   {name:'anomaly_enabled',label:tr('Konfigurierte Verbrauchsabweichungen melden'),type:'checkbox',value:settings.anomaly_enabled},
   {name:'stale_enabled',label:tr('Fehlende Adaptermessungen melden'),type:'checkbox',value:settings.stale_enabled},
   {name:'stale_after_seconds',label:tr('Adapterdaten fehlen seit (Sekunden)'),type:'number',value:String(settings.stale_after_seconds),
    options:{required:true,min:'300',max:'31622400',step:'1',help:tr('86400 Sekunden entsprechen einem Tag. Verbindungsaufbau allein zählt nicht als Messung.')}}
  ], tr('Benachrichtigungen speichern'), data => M.mutate('notification.save', settings.version ? settings.id : '', settings.version,
   {...data, register_id:settings.id}));
  built.form.insertBefore(node('p', tr('Die Person muss Zählerhinweise zusätzlich im Konto und auf jedem gewünschten Gerät erlauben. Ohne Zuständigkeit bleiben alle Hinweise aus.')), built.grid);
  built.form.insertBefore(node('p', tr('Verbrauchshinweise verwenden die im Register gesetzten Grenzen. Lücken und Schätzungen lösen keine Verbrauchswarnung aus.')), built.grid);
 }

 S.views.settings = async () => {
  await existingSettings();
  if (!M.register()) return;
  const response = await readSettings(), settings = response.settings;
  const person = response.members.find(member => member.id === settings.responsible_member_id);
  const section = node('section');
  section.id = 'meter-notifications';
  section.append(node('h2',tr('Zählerbenachrichtigungen')), node('p',tr('Ablesetermine, Verbrauchshinweise und fehlende Adapterdaten werden getrennt freigegeben.')));
  section.append(M.facts([
   [tr('Zuständige Person'),person ? person.name + ' · ' + person.username : tr('Keine Zuständigkeit')],
   [tr('Ableseerinnerungen'),settings.reading_enabled ? tr('Aktiv') : tr('Aus')],
   [tr('Verbrauchshinweise'),settings.anomaly_enabled ? tr('Aktiv') : tr('Aus')],
   [tr('Adapterdaten'),settings.stale_enabled ? tr('Aktiv') : tr('Aus')]
  ]));
  section.append(node('p',tr('Bereits bestehende Abweichungen bleiben beim Aktivieren still. Neue Warnungen folgen erst nach einem gesunden Ausgangszustand; erneute Warnungen sind mindestens 24 Stunden getrennt.')));
  const actions = node('div',undefined,'actions');
  if (S.canWrite) actions.append(M.button(tr('Zählerhinweise einrichten'),edit));
  actions.append(M.link(tr('Konto und Geräte freigeben'),'/settings/notifications'));
  section.append(actions);
  content.append(section);
 };
})();
