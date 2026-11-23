// Tests of the name proposal on the pairing confirmation (#203, Q148) with node's test runner:
// choosing a person puts the proposed name in the name field, unless the parent typed one.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/name-proposal.js'), 'utf8');

function setup(proposals = { mia: 'Mia’s phone', max: 'Max’s phone' }) {
  const attrs = {};
  const field = {
    value: '',
    getAttribute(a) { return a in attrs ? attrs[a] : null; },
    setAttribute(a, v) { attrs[a] = v; },
  };
  const form = {
    elements: { name: field },
    getAttribute(a) { return a === 'data-name-proposals' && proposals ? JSON.stringify(proposals) : null; },
  };
  let listener;
  vm.runInNewContext(source, { document: { addEventListener(ev, fn) { if (ev === 'change') listener = fn; } } });
  const choose = (id) => listener({ target: { name: 'person', value: id, checked: true, form } });
  return { field, choose, listener: () => listener };
}

test('choosing a person proposes the name', () => {
  const s = setup();
  s.choose('mia');
  assert.equal(s.field.value, 'Mia’s phone');
});

test('choosing another person changes a name that was only proposed', () => {
  const s = setup();
  s.choose('mia');
  s.choose('max');
  assert.equal(s.field.value, 'Max’s phone');
});

test('a name the parent typed stays', () => {
  const s = setup();
  s.choose('mia');
  s.field.value = 'Mias Handy im Flur';
  s.choose('max');
  assert.equal(s.field.value, 'Mias Handy im Flur');
});

test('a name the parent typed before choosing stays', () => {
  const s = setup();
  s.field.value = 'Flur';
  s.choose('mia');
  assert.equal(s.field.value, 'Flur');
});

test('a form without proposals, and a person without one, change nothing', () => {
  const none = setup(null);
  none.choose('mia');
  assert.equal(none.field.value, '');
  const unknown = setup();
  unknown.choose('nobody');
  assert.equal(unknown.field.value, '');
});

test('other fields do not trigger it', () => {
  const s = setup();
  s.listener()({ target: { name: 'name', value: 'x', checked: false, form: {} } });
  assert.equal(s.field.value, '');
});
