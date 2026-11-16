// Tests of the live sample under the free-text allergy field (ticket 163):
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/food.js'), 'utf8');

function setup() {
  const sample = {
    textContent: '',
    getAttribute(a) { return { 'data-template': 'Nicht automatisch geprüft, nicht filterbar: {text}', 'data-empty': 'Nichts eingetragen' }[a] || null; },
  };
  const input = { value: 'Kiwi' };
  const listeners = [];
  const document = {
    addEventListener(ev, fn) { if (ev === 'input') listeners.push(fn); },
    querySelector(sel) { return sel === '[data-food-sample]' ? sample : sel === '[data-food-other]' ? input : null; },
  };
  vm.runInNewContext(source, { document });
  const type = (v) => { input.value = v; listeners.forEach((fn) => fn({ target: input })); };
  return { sample, input, listeners, type };
}

test('the sample shows the entries as the page will show them', () => {
  const { sample, type } = setup();
  assert.equal(sample.textContent, 'Nicht automatisch geprüft, nicht filterbar: Kiwi', 'it is filled when the page loads');
  type(' Erdbeeren ,, Tomaten  ,erdbeeren');
  assert.equal(sample.textContent, 'Nicht automatisch geprüft, nicht filterbar: Erdbeeren, Tomaten');
});

test('an empty field says that nothing is entered', () => {
  const { sample, type } = setup();
  type(' , ');
  assert.equal(sample.textContent, 'Nichts eingetragen');
});

test('typing in another field does not touch the sample', () => {
  const { sample, input, listeners } = setup();
  input.value = 'changed without an event for it';
  listeners.forEach((fn) => fn({ target: { value: 'x' } }));
  assert.equal(sample.textContent, 'Nicht automatisch geprüft, nicht filterbar: Kiwi');
});
