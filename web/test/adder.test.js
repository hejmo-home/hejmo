// Tests of the arithmetic of the add card's script (ticket 165):
//   node --test web/test
// The rules are the ones of internal/unit; the script repeats only what the
// steppers and the unit chips need.
const test = require('node:test');
const assert = require('node:assert/strict');
const a = require('../static/adder.js');

test('an amount takes a comma or a point and must be between 0 and 9999', () => {
  assert.equal(a.parse('2'), 2);
  assert.equal(a.parse('0,5'), 0.5);
  assert.equal(a.parse(' 0.5 '), 0.5);
  assert.equal(a.parse('9999'), 9999);
  for (const bad of ['', '0', 'abc', '10000', '-1', '1,2,3', '1e3', ',5']) assert.ok(Number.isNaN(a.parse(bad)), bad);
});

test('- and + go to the next whole step, 50 for g and ml', () => {
  assert.equal(a.step('g'), 50);
  assert.equal(a.step('ml'), 50);
  assert.equal(a.step('kg'), 1);
  assert.equal(a.stepUp(0.5, 1), 1);
  assert.equal(a.stepUp(1, 1), 2);
  assert.equal(a.stepUp(500, 50), 550);
  assert.equal(a.stepUp(525, 50), 550);
  assert.equal(a.stepUp(9999, 1), 9999);
  assert.equal(a.stepDown(1.5, 1), 1);
  assert.equal(a.stepDown(500, 50), 450);
  assert.equal(a.stepDown(525, 50), 500);
});

test('- is not possible below one step: nothing is zero', () => {
  assert.equal(a.stepDown(1, 1), null);
  assert.equal(a.stepDown(0.5, 1), null);
  assert.equal(a.stepDown(50, 50), null);
  assert.equal(a.stepDown(30, 50), null);
});

test('numbers are written with a comma in German and a point in English, without trailing zeros', () => {
  assert.equal(a.show(1.5, 'de'), '1,5');
  assert.equal(a.show(1.5, 'en'), '1.5');
  assert.equal(a.show(2, 'de'), '2');
  assert.equal(a.show(0.333333, 'en'), '0.3333');
});

test('a unit of the same kind keeps the amount: 500 g are 0.5 kg', () => {
  assert.equal(a.convert(500, 1, 1000), 0.5);
  assert.equal(a.convert(0.5, 1000, 1), 500);
  assert.equal(a.convert(1, 12, 1), 12);
});

test('the editor tells the box that the page converted the number', () => {
  const field = { value: '' };
  const form = { querySelector: (sel) => (sel === 'input[name="converted"]' ? field : null) };
  a.markConverted(form);
  assert.equal(field.value, '1');
  a.markConverted({ querySelector: () => null }); // no field, nothing to do
});
