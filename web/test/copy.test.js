// Tests of the "Copy" button (ticket 133) with node's test runner:
//   cd web && node --test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/form.js'), 'utf8');

// A copy button with the attributes the template renders, and the script run
// against a fake document and clipboard. Every click handler the script
// registers is called, as the browser would.
function setup({ clipboard = true, execCommand = true } = {}) {
  const attrs = { 'data-copy': 'mia.dedyn.io', 'data-copied-text': 'Kopiert' };
  const button = {
    textContent: 'Kopieren',
    getAttribute(a) { return a in attrs ? attrs[a] : null; },
    setAttribute(a, v) { attrs[a] = v; },
    closest(sel) { return sel === '[data-copy]' ? button : null; },
  };
  const written = [];
  const timers = [];
  const clicks = [];
  const field = { style: {}, setAttribute() {}, select() {} };
  const document = {
    addEventListener(ev, fn) { if (ev === 'click') clicks.push(fn); },
    querySelectorAll() { return []; },
    querySelector() { return null; },
    createElement() { return field; },
    body: { appendChild() {}, removeChild() {} },
    execCommand(cmd) { written.push('exec:' + cmd + ':' + field.value); return execCommand; },
  };
  const navigator = { userAgent: '', platform: '', maxTouchPoints: 0 };
  if (clipboard) navigator.clipboard = { writeText(t) { written.push(t); return Promise.resolve(); } };
  vm.runInNewContext(source, { document, window: { addEventListener() {} }, navigator, setTimeout(fn) { timers.push(fn); } });
  const click = () => clicks.forEach((fn) => fn({ target: button }));
  return { button, written, timers, click };
}

test('the button copies its text and says Kopiert, then goes back', async () => {
  const f = setup();
  f.click();
  await Promise.resolve();
  assert.deepEqual(f.written, ['mia.dedyn.io']);
  assert.equal(f.button.textContent, 'Kopiert');
  f.timers.forEach((fn) => fn());
  assert.equal(f.button.textContent, 'Kopieren');
});

test('without a clipboard (plain http) the old copy command is used', () => {
  const f = setup({ clipboard: false });
  f.click();
  assert.equal(f.written.length, 1);
  assert.match(f.written[0], /^exec:copy:mia\.dedyn\.io$/);
  assert.equal(f.button.textContent, 'Kopiert');
});

test('a failing copy command leaves the button as it was', () => {
  const f = setup({ clipboard: false, execCommand: false });
  f.click();
  assert.equal(f.button.textContent, 'Kopieren');
});
