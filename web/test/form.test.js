// Tests of the people form script (ticket 76) with node's test runner:
//   cd web && node --test
// form.js is run in a sandbox with a small fake document and form.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/form.js'), 'utf8');

// A radio option with the attributes the template renders.
function radio(form, name, value, takenWith, checked) {
  return {
    form, name, value, checked: !!checked, disabled: false,
    getAttribute(a) { return a === 'data-taken-with' ? takenWith : null; },
    hasAttribute(a) { return a === 'data-taken-with' && takenWith !== undefined; },
    closest() { return null; },
  };
}

// A form of two colours and three markers. Users: A has red with the circle,
// B has blue with the square; the colour values are upper case, as rendered.
function setup(colorTaken = { '#C00': 'circle', '#00C': 'square' }, shapeTaken = { circle: '#C00', square: '#00C', star: '' }) {
  const form = {};
  const inputs = [];
  for (const [v, t] of Object.entries(colorTaken)) inputs.push(radio(form, 'color', v, t));
  for (const [v, t] of Object.entries(shapeTaken)) inputs.push(radio(form, 'shape', v, t));
  const matches = (sel) => {
    const named = /input\[name="(\w+)"\](:checked)?/.exec(sel);
    return inputs.filter((i) => i.name === named[1] && (!named[2] || i.checked));
  };
  form.querySelectorAll = (sel) => matches(sel);
  form.querySelector = (sel) => matches(sel)[0] || null;
  const handlers = [];
  const document = {
    addEventListener(ev, fn) { if (ev === 'change') handlers.push(fn); },
    querySelectorAll() { return []; },
    querySelector() { return null; },
  };
  // The script also wires the meal plan fields and the time picker, which need
  // a window and a navigator; no such elements exist in this form.
  vm.runInNewContext(source, { document, window: {}, navigator: { userAgent: '', platform: '', maxTouchPoints: 0 } });
  const pick = (name, value) => {
    inputs.filter((i) => i.name === name).forEach((i) => { i.checked = i.value === value; });
    const target = inputs.find((i) => i.name === name && i.value === value);
    handlers.forEach((fn) => fn({ target }));
  };
  const disabled = (name) => inputs.filter((i) => i.name === name && i.disabled).map((i) => i.value);
  return { inputs, pick, disabled };
}

test('picking a colour disables the markers taken with it', () => {
  const f = setup();
  f.pick('color', '#C00');
  assert.deepEqual(f.disabled('shape'), ['circle']);
});

test('picking a marker disables the colours taken with it', () => {
  const f = setup();
  f.pick('shape', 'square');
  assert.deepEqual(f.disabled('color'), ['#00C']);
});

test('the checked option is never disabled', () => {
  const f = setup();
  f.pick('color', '#C00');
  f.pick('shape', 'circle');
  assert.deepEqual(f.disabled('shape'), []);
  assert.deepEqual(f.disabled('color'), []);
});

test('changing the other choice enables options again', () => {
  const f = setup();
  f.pick('color', '#C00');
  assert.deepEqual(f.disabled('shape'), ['circle']);
  f.pick('color', '#00C');
  assert.deepEqual(f.disabled('shape'), ['square']);
});

test('colours compare regardless of case', () => {
  // The server writes taken colours in upper case; the radio value may differ.
  const f = setup({ '#c00': 'circle' }, { circle: '#C00', star: '' });
  f.pick('color', '#c00');
  assert.deepEqual(f.disabled('shape'), ['circle']);
});

// The weeks hint of the meal template form (ticket 147): the date after the
// turn of the year follows the household date format.
function hintSetup(dateFormat) {
  const attrs = {
    'data-today': '2026-11-14', 'data-lead': 'Fills week', 'data-this': ' (this week)',
    'data-then': ', then week {week} of {year} ({date})', 'data-tail': ', and so on.',
  };
  if (dateFormat !== undefined) attrs['data-date-format'] = dateFormat;
  const listeners = {};
  const field = (value) => ({ value, addEventListener(ev, fn) { listeners[ev] = fn; } });
  const every = field('2'), start = field('2026-11-16');
  const form = {
    elements: { every, start },
    addEventListener() {},
    querySelector() { return null; },
  };
  const hint = {
    innerHTML: '', textContent: '',
    getAttribute(a) { return a in attrs ? attrs[a] : null; },
    closest() { return form; },
  };
  const document = {
    addEventListener() {},
    querySelectorAll() { return []; },
    querySelector(sel) { return sel === '.tpl-form .weeks-hint' ? hint : null; },
  };
  vm.runInNewContext(source, { document, window: {}, navigator: { userAgent: '', platform: '', maxTouchPoints: 0 } });
  start.value = '2026-11-16';
  listeners.change();
  return hint.innerHTML;
}

test('the weeks hint writes the date after New Year day first', () => {
  assert.equal(hintSetup('dmy'), 'Fills week <b>47</b>, <b>49</b>, <b>51</b>, <b>53</b>, then week <b>2</b> of 2027 (11.01.2027), and so on.');
});

test('the weeks hint writes the date after New Year month first', () => {
  assert.equal(hintSetup('mdy'), 'Fills week <b>47</b>, <b>49</b>, <b>51</b>, <b>53</b>, then week <b>2</b> of 2027 (01/11/2027), and so on.');
});

test('the weeks hint falls back to day first without a format', () => {
  assert.match(hintSetup(undefined), /\(11\.01\.2027\)/);
});
