// Tests of the language switcher script (ticket 134) with node's test runner:
//   cd web && node --test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/form.js'), 'utf8');

// An open language menu with a summary button and one option inside it.
function setup() {
  const summary = { focused: false, focus() { this.focused = true; } };
  const inside = {};
  const menu = {
    open: true,
    removeAttribute(a) { if (a === 'open') this.open = false; },
    querySelector(sel) { return sel === 'summary' ? summary : null; },
    contains(n) { return n === inside || n === summary; },
  };
  const handlers = {};
  const document = {
    addEventListener(ev, fn) { handlers[ev] = fn; },
    querySelector(sel) { return sel === 'details.lang[open]' && menu.open ? menu : null; },
  };
  vm.runInNewContext(source, { document });
  return { menu, summary, inside, handlers };
}

test('Escape closes the open menu and returns focus to its button', () => {
  const f = setup();
  f.handlers.keydown({ key: 'Escape' });
  assert.equal(f.menu.open, false);
  assert.equal(f.summary.focused, true);
});

test('other keys leave the menu open', () => {
  const f = setup();
  f.handlers.keydown({ key: 'Tab' });
  assert.equal(f.menu.open, true);
});

test('a click outside closes the menu, a click inside does not', () => {
  const f = setup();
  f.handlers.click({ target: f.inside });
  assert.equal(f.menu.open, true);
  f.handlers.click({ target: {} });
  assert.equal(f.menu.open, false);
});
