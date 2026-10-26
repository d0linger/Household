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
  ...(active.some(item => item.id === 'EX19') ? ['Der aktuelle Stand der Finanz-Unteraufgaben und Tests steht in der [laufenden Prüfliste](financial-visibility-verification.md#current-producer-checklist).', ''] : []),
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
