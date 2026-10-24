'use strict';
const fs = require('node:fs');
const path = require('node:path');

function summarize(text) {
  const lines = text.trim().split(/\r?\n/);
  if (!/^mode: (set|count|atomic)$/.test(lines.shift())) throw Error('Invalid Go coverage header');
  const blocks = new Map();
  for (const line of lines) {
    const match = /^(\S+):(\d+\.\d+,\d+\.\d+) (\d+) (\d+)$/.exec(line);
    if (!match) throw Error('Invalid Go coverage block');
    const [, file, range, rawStatements, rawCount] = match;
    const key = file + ':' + range, statements = Number(rawStatements), covered = Number(rawCount) > 0;
    if (!Number.isSafeInteger(statements) || !Number.isSafeInteger(Number(rawCount))) throw Error('Invalid coverage counts');
    const previous = blocks.get(key);
    if (previous && previous.statements !== statements) throw Error('Inconsistent coverage block');
    blocks.set(key, {file, statements, covered: covered || previous?.covered});
  }
  const packages = {};
  let statements = 0, covered = 0;
  for (const block of blocks.values()) {
    const name = path.posix.dirname(block.file);
    const pkg = packages[name] ||= {statements: 0, covered: 0};
    pkg.statements += block.statements;
    statements += block.statements;
    if (!Number.isSafeInteger(statements)) throw Error('Coverage statement total exceeds safe range');
    if (block.covered) {pkg.covered += block.statements; covered += block.statements;}
  }
  if (!statements) throw Error('Empty coverage cannot pass');
  for (const pkg of Object.values(packages)) pkg.percent = pkg.statements ? pkg.covered / pkg.statements * 100 : 0;
  return {statements, covered, percent: covered / statements * 100, packages};
}
module.exports = {summarize};
if (require.main === module) {
  try {
    const [file, rawFloor = '0', output] = process.argv.slice(2), floor = Number(rawFloor);
    if (!file || !Number.isFinite(floor) || floor < 0 || floor > 100) throw Error('Usage: check-coverage.cjs PROFILE MIN_PERCENT [REPORT.json]');
    const report = summarize(fs.readFileSync(file, 'utf8'));
    if (output) fs.writeFileSync(output, JSON.stringify(report, null, 2) + '\n');
    for (const [pkg, result] of Object.entries(report.packages)) console.log(`${result.percent.toFixed(1).padStart(5)}% ${pkg}`);
    console.log(`Total ${report.percent.toFixed(1)}% (${report.covered}/${report.statements} statements); floor ${floor}%`);
    if (report.percent < floor) throw Error('Coverage below the recorded floor');
  } catch (error) {console.error(error.message); process.exitCode = 1;}
}
