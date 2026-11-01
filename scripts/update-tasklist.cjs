const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
const args = process.argv.slice(2);
if (args.some(arg => arg !== '--check')) throw Error('Usage: node scripts/update-tasklist.cjs [--check]');
const sourceFile = path.join(root, 'docs/improvements-180.md');
const source = fs.readFileSync(sourceFile, 'utf8');
const groups = [];
const items = [];
let heading = '';
for (const line of source.split(/\r?\n/)) {
  if (line.startsWith('## ')) heading = line.slice(3);
  const match = line.match(/^\| ([A-Z]{2}\d{2}) \| (.*?) \| (.*?) \| (.*?) \|/);
  if (!match) continue;
  const item = {id: match[1], title: match[2], criterion: match[3], status: match[4]};
  if (!['Verifiziert', 'Implementiert', 'In Arbeit', 'Offen', 'Extern offen'].includes(item.status)) {
    throw Error('Unknown checklist status: ' + item.id);
  }
  let group = groups.find(group => group.title === heading);
  if (!group) groups.push(group = {title: heading, items: []});
  group.items.push(item);
  items.push(item);
}
if (items.length !== 180 || new Set(items.map(item => item.id)).size !== 180) {
  throw Error('The checklist must retain all 180 unique identities');
}
const count = status => items.filter(item => item.status === status).length;
const sourceSummary = `180 Punkte: **${count('Verifiziert')} verifiziert, ${count('Implementiert')} implementiert, ${count('In Arbeit')} in Arbeit, ${count('Offen')} offen${count('Extern offen') ? `, ${count('Extern offen')} extern offen` : ''}**.`;
const summaryPattern = /^180 Punkte: \*\*.*?\*\*\./m;
if (!summaryPattern.test(source)) throw Error('Acceptance summary is missing');
if (args.includes('--check')) {
  if (!source.includes(sourceSummary)) throw Error('Acceptance summary is stale; run node scripts/update-tasklist.cjs');
} else if (!source.includes(sourceSummary)) {
  fs.writeFileSync(sourceFile, source.replace(summaryPattern, sourceSummary));
}
const active = items.filter(item => item.status === 'In Arbeit');
let chorePhaseProgress = '';
if (active.some(item => item.id.startsWith('CH'))) {
  const plan = fs.readFileSync(path.join(root, 'docs/chores-module-plan.md'), 'utf8');
  const phases = [...plan.matchAll(/^- \[([ x])\] (\d{3})\./gm)];
  if (phases.length !== 120 || new Set(phases.map(row => row[2])).size !== 120) throw Error('Chores must retain all 120 unique phase steps');
  chorePhaseProgress = phases.filter(row => row[1] === 'x').length + '/120 abgearbeitete Phasenunterpunkte. ';
}

const extraStatuses = ['Implementiert', 'Extern offen'].filter(status => count(status) > 0)
  .map(status => `${count(status)} ${status.toLowerCase()}`);
const summary = [`${count('Verifiziert')} / 180 verifiziert und abgehakt`, `${count('In Arbeit')} in Arbeit`, `${count('Offen')} offen`, ...extraStatuses];
const date = new Intl.DateTimeFormat('de-AT', {
  dateStyle: 'medium', timeStyle: 'short', timeZone: 'Europe/Vienna'
}).format(new Date());
const lines = [
  '# Household – Aufgabenliste', '',
  `Stand: ${date} (Europe/Vienna).`, '',
  `**${summary.join(' · ')}.**`, '',
  'Ein Haken bedeutet: umgesetzt und durch ausgeführte Prüfungen belegt. Vorbereitete Änderungen und einzelne bestandene Zwischentests schließen einen Verbesserungspunkt noch nicht ab.', '',
  active.length ? `Aktuell in Arbeit: ${active.map(item => `**${item.id} – ${item.title}**`).join(', ')}.` : 'Derzeit ist kein Punkt als „In Arbeit“ markiert.', '',
  ...(active.some(item => item.id.startsWith('MP')) ? ['**Aktueller Modulblock: Essensplanung.** Geplante Reste, Wochenbudget, Kalender und Autoplan sind umgesetzt und funktional abgenommen. Der genaue Git-Index-Geheimnisscan und der lokale Checkpoint werden abgeschlossen. Der [Modulplan mit116Checkboxen](mealplan-module-plan.md) zeigt die abgehakten Unteraufgaben.', ''] : []),
  ...(source.includes('Abgenommener Modulblock: **Essensplanung (') ? ['**Essensplanung vollständig lokal abgenommen: 20 / 20 Punkte.** Geplante Reste, Wochenbudget, Kalender und Autoplan sind nach nativen Tests, Browserprüfung und verschlüsseltem Restore/Purge gemeinsam abgehakt. [Modulplan](mealplan-module-plan.md) · [Abnahmenachweise und Grenzen](mealplan-module-validation.md).', ''] : []),
  ...(source.includes('Aktueller Modulblock: **Fahrzeuge & Tanken (FU01–FU20)**') ? ['**Aktueller Modulblock: Fahrzeuge & Tanken (FU01–FU20).** Erst vollständige Implementierung, danach gemeinsame Tests und Fehlerbehebung. Der nächste Modulblock beginnt nach bestandener Gesamtabnahme. [Modulplan und Phasencheckliste](fuel-module-plan.md).', ''] : []),
  ...(source.includes('Aktueller Modulblock: **Vorrat (PA10/PA13/PA19)**') ? ['**Aktueller Modulblock: Vorrat (PA10/PA13/PA19).** Die vollständige Implementierung und unabhängige Quellenprüfung sind abgeschlossen. Gemeinsame Tests und Fehlerbehebung laufen; die drei Punkte werden erst nach bestandener Gesamtabnahme abgehakt. [Modulplan und Phasencheckliste](pantry-module-plan.md) · [ausgeführte Prüfungen und offene Abnahme](pantry-module-validation.md).', ''] : []),
  ...(source.includes('Abgenommener Modulblock: **Vorrat (PA10/PA13/PA19)**') ? ['**Vorrat vollständig lokal abgenommen: 20 / 20 Punkte.** PA10, PA13 und PA19 sind nach nativen Tests, eigenständigem Browserlauf, echtem Restore/Purge und vollständiger 28-Komponenten-Regression abgehakt. [Modulplan](pantry-module-plan.md) · [Abnahmenachweise](pantry-module-validation.md).', ''] : []),
  ...(source.includes('Aktueller Modulblock: **Einkaufen (SH15/SH17)**') ? ['**Aktueller Modulblock: Einkaufen (SH15/SH17).** Persönliche Listenhinweise mit optionalem Web Push und ausdrücklich gespeicherte Offline-Lesekopien. Der vollständige Umfang ist vor Umsetzung beschrieben; erst nach gesamter Implementierung beginnen gemeinsame Tests. [Modulplan und128Schritte](shopping-module-plan.md).', ''] : []),
  ...(active.some(item => item.id.startsWith('RE')) ? ['**Aktueller Modulblock: Rezepte.** Die zehn Erweiterungen sind implementiert; gemeinsame Tests und Fehlerbehebung laufen. Abgehakte Unteraufgaben und ausgeführte Prüfungen stehen in der [Rezept-Phasencheckliste](recipes-module-plan.md#phase-3--erst-nach-vollst%C3%A4ndiger-phase-2-gemeinsam-testen-und-fehler-beheben). Die zehn Verbesserungspunkte werden nach gemeinsamer Modulabnahme abgehakt.', ''] : []),
  ...(source.includes('Abgenommener Modulblock: **Rezepte (') ? ['**Rezepte vollständig lokal abgenommen: 20 / 20 Punkte.** Alle zehn Erweiterungen sind nach nativer Komposition, Linux-Race, eigener und vollständiger Browserprüfung sowie echtem Restore/Purge gemeinsam abgehakt. [Modulplan](recipes-module-plan.md) · [Abnahmenachweise und Grenzen](recipes-module-validation.md).', ''] : []),
  ...(source.includes('Abgenommener Modulblock: **Finanzen (') ? ['**Finanzen vollständig lokal abgenommen: 20 / 20 Punkte.** Vollständige 717-Test-Linux-Race-Kampagne plus zwei separat quellenqualifizierte Race-Tests, 70,86 % Statement-Abdeckung, 31 Browserkomponenten und tatsächlicher verschlüsselter Restore/Purge bestanden. Exakter Index-Geheimnisscan ohne Funde; lokaler Code-Checkpoint 0102380. [Modulplan mit Haken](finance-module-plan.md) · [Kriterien und Nachweise](finance-module-validation.md).', ''] : []),
  ...(active.some(item => item.id.startsWith('CH')) ? ["**Aktueller Modulblock: Aufgaben (CH01–CH20).** " + chorePhaseProgress + "Die Implementierung ist vollständig; die gemeinsame Modulabnahme bleibt offen. Nach den in der breiten Regression gefundenen Pantry-Versionsgrenzen werden die Nachweise auf dem korrigierten gemeinsamen Quellstand erneuert. Frühere Prüfungen bleiben mit ihren tatsächlichen Quellen und Fehlern dokumentiert; Unterpunkt 114 verfolgt die notwendigen Wiederholungen. [120-Schritte-Plan mit Haken](chores-module-plan.md) · [Aktuelle Prüfnachweise](chores-module-validation.md). Alle 20 CH-Kriterien werden erst nach vollständigen Tests, exakter Indexprüfung und lokalem Checkpoint abgehakt.", ''] : []),
  ...(active.some(item => item.id.startsWith('EX')) ? ["**Aktueller Modulblock: Finanzen (19 Erweiterungen in Arbeit, EX02 bleibt erhalten).**\n\n- [x] Finaler Stand a6e871cc: zwölf Qualitätsprüfungen, 307 Go-Unit-Haupttests und 297 Node-Tests bestanden; Produktionscode unverändert.\n- [x] Geprüfte Datenbankkorrekturen gemeinsam übertragen; Finanzquellen, echte Zeilenlöschung, Pantry14 und sichere Auditprojektion nachgeprüft.\n- [x] Aktuelle Runtime-, OCR- und Backup-Images auf 515c3433 als nicht privilegierter Benutzer gebaut.\n- [x] Aktuelle lokale OCI-/SBOM-/Provenienzprüfung auf 515c3433 bestanden.\n- [x] Finanz-Browser auf 515c3433: 208 Prüfpunkte, 16 manuell geprüfte Screenshots und 16 saubere Axe-Scans.\n- [x] Verschlüsselter Restore/Purge auf 515c3433: 15 tatsächliche Stufen und vollständig verifizierte Bereinigung.\n- [x] Historische Fuel-Testvorbereitung korrigiert; alle vier beschädigten Altbestände getrennt bestanden.\n- [x] Git-sichtbaren Stand 515c3433 mit 892 Dateien gescannt: keine Funde; private Prüfkopie entfernt.\n- [x] Alle 716 Haupttests tatsächlich ausgeführt: 696 bestanden, 20 fehlgeschlagen, keine übersprungen; Originalbefunde und Bereinigung dokumentiert.\n- [x] Vollständige Linux-Race-Regression bestanden: 717 Haupttests, sechs strenge Shards, alle 20 früheren Fehlerfälle erfolgreich; 15 Bereinigungen verifiziert.\n- [x] Vollständige Browserkette auf 515c3433: 31 Komponenten, 1.273 Prüfpunkte, 179 Screenshots und 167 saubere Axe-Scans.\n- [x] Mobile Korrektur getrennt geprüft: 208 Prüfpunkte, überlappungsfreie Werte und 16 saubere Axe-Scans; verschlüsselter Restore/Purge bestanden.\n- [x] Acht korrigierte Finanzverträge und sechs Capture-/Aufräumfälle vollständig nachgeprüft.\n- [x] Sparziel-Gegenbuchung separat korrigiert: reguläre Rückbuchung und beschädigter Nachbarbestand beide nachgeprüft.\n- [x] Dreizehn geprüfte Korrekturdateien auf Stand 515c3433 übertragen; alle 85 freigegebenen SQL-Dateien unverändert, 92 aktuelle Pins bestanden.\n- [x] Liquiditätsvorschau mit zwei zusätzlichen nativen und Linux-Race-Fällen bestanden; keine Fehler/Skips/Races, Produktionscode unverändert.\n- [x] Ergänzter Teststand a6e871cc: Formatierung, vet und vollständiger getaggter Linter auf tatsächlicher Quellkopie bestanden.\n- [x] Zwei zusätzliche Liquiditätstests quellenqualifiziert übertragen; 719 eindeutige Race-Haupttests belegt, 70,86 % beobachtete Statement-Abdeckung.\n- [ ] Abschließende Indexprüfung, gemeinsame Modulabnahme und lokaler Checkpoint.\n\nFehlversuche bleiben als solche erhalten. Die 19 Finanzverbesserungen werden nach der gemeinsamen Modulabnahme abgehakt. [Modulplan mit Haken](finance-module-plan.md) · [Prüfnachweise und Quellenstände](finance-module-validation.md).", ''] : []),
  'Diese Ansicht wird aus der [verbindlichen Liste mit Abnahmekriterien und Nachweisen](improvements-180.md) erzeugt. Aktualisieren: `node scripts/update-tasklist.cjs`.', '',
  '| Modul | Abgehakt | In Arbeit | Offen |', '|---|---:|---:|---:|'
];
for (const group of groups) {
  const n = status => group.items.filter(item => item.status === status).length;
  lines.push(`| ${group.title} | ${n('Verifiziert')} / ${group.items.length} | ${n('In Arbeit')} | ${n('Offen')} |`);
}
for (const group of groups) {
  lines.push('', `## ${group.title}`, '');
  for (const item of group.items) {
    const checked = item.status === 'Verifiziert';
    const suffix = checked || item.status === 'Offen' ? '' : ` — **${item.status}**`;
    lines.push(`- [${checked ? 'x' : ' '}] **${item.id} ${item.title}**${suffix} — ${item.criterion}`);
  }
}
lines.push('');
const output = path.join(root, 'docs/taskliste.md');
const text = lines.join('\n');
if (args.includes('--check')) {
  const normalize = value => value.replaceAll('\r\n', '\n').replace(/^Stand: .*$/m, 'Stand:');
  if (!fs.existsSync(output) || normalize(fs.readFileSync(output, 'utf8')) !== normalize(text)) {
    throw Error('Task list is stale; run node scripts/update-tasklist.cjs');
  }
} else {
  fs.writeFileSync(output, text);
}
console.log(JSON.stringify({total: items.length, checked: count('Verifiziert'), inProgress: count('In Arbeit'), open: count('Offen'), file: 'docs/taskliste.md', checkedOnly: args.includes('--check')}, null, 2));
