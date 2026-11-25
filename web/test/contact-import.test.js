// Tests of the review page script of a contact file (ticket 300) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/contact-import.js'), 'utf8');
const sandbox = {};
vm.runInNewContext(source, sandbox);
const { fill, count, state } = sandbox.HejmoImport;

const de = { new: '{n} neu', completed: '{n} ergänzt', skipped: '{n} weggelassen', nothing: 'Nichts zu übernehmen', missing: 'Bei {n} fehlt deine Wahl.' };

test('numbers are put into the texts', () => {
  assert.equal(fill('{n} neu', 8), '8 neu');
  assert.equal(fill('Nichts', 3), 'Nichts');
});

test('the state counts merges as completed, merges of the file as one new contact and an own contact per card', () => {
  const dups = [
    { value: 'merge', cards: 1, infile: false },
    { value: 'merge', cards: 1, infile: false },
    { value: 'merge', cards: 1, infile: false },
    { value: 'merge', cards: 2, infile: true },
    { value: '', cards: 1, infile: false },
  ];
  const c = count(dups, [true, true, true, true, true, true, true]);
  assert.deepEqual({ ...c }, { new: 8, completed: 3, skipped: 0, missing: 1 });
  assert.equal(state(c, de), '8 neu, 3 ergänzt. Bei 1 fehlt deine Wahl.');
});

test('an unticked row and a skipped duplicate are left out, an own contact counts once per card', () => {
  const dups = [{ value: 'skip', cards: 1, infile: false }, { value: 'new', cards: 2, infile: true }];
  const c = count(dups, [true, false, true]);
  assert.deepEqual({ ...c }, { new: 4, completed: 0, skipped: 2, missing: 0 });
  assert.equal(state(c, de), '4 neu, 2 weggelassen.');
});

test('with nothing chosen the bar says there is nothing to take over', () => {
  assert.equal(state(count([{ value: 'skip', cards: 1 }], [false]), de), '2 weggelassen.');
  assert.equal(state(count([], []), de), 'Nichts zu übernehmen.');
  assert.equal(state(count([{ value: '', cards: 1 }], []), de), 'Nichts zu übernehmen. Bei 1 fehlt deine Wahl.');
});
