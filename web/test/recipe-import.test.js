// Tests of the script of the recipe import page (ticket 289) with node's test runner.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/recipe-import.js'), 'utf8');
const ctx = vm.createContext({ document: { querySelector: () => null } });
vm.runInContext(source, ctx);

test('the host of a typed link is what the waiting line names', () => {
  assert.equal(ctx.rimpHost('https://www.beispielrezepte.de/rezepte/1187/x.html'), 'www.beispielrezepte.de');
  assert.equal(ctx.rimpHost('  http://Example.COM:8080/a?b=1 '), 'example.com');
  assert.equal(ctx.rimpHost('kein link'), '');
  assert.equal(ctx.rimpHost(''), '');
});

test('the waiting text puts the host into its format', () => {
  assert.equal(ctx.rimpWaitText('Hejmo liest die Seite {host} …', 'www.x.de'), 'Hejmo liest die Seite www.x.de …');
  assert.equal(ctx.rimpWaitText('Hejmo liest die Seite {host} …', ''), 'Hejmo liest die Seite …');
});
