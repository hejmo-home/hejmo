// Tests of the theme chooser script (ticket 125) with node's test runner:
//   cd web && node --test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/theme.js'), 'utf8');

// A tiny element tree: just what the script touches.
class El {
  constructor(tag, cls, attrs) {
    this.tag = tag; this.className = cls || ''; this.attrs = Object.assign({}, attrs); this.children = []; this.parentNode = null;
    this.handlers = {}; this.nodeValue = null; this.innerHTML = ''; this.checked = false;
  }
  get value() { return this.attrs.value; }
  get disabled() { return 'disabled' in this.attrs; }
  get lastChild() { return this.children[this.children.length - 1] || null; }
  get firstChild() { return this.children[0] || null; }
  get textContent() { return this.nodeValue !== null ? this.nodeValue : this.children.map((c) => c.textContent).join(''); }
  set textContent(v) { this.children = [text(v)]; this.children[0].parentNode = this; }
  appendChild(c) { c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) { c.parentNode = this; this.children.splice(this.children.indexOf(ref), 0, c); return c; }
  removeChild(c) { this.children.splice(this.children.indexOf(c), 1); c.parentNode = null; return c; }
  replaceChild(n, o) { n.parentNode = this; this.children[this.children.indexOf(o)] = n; o.parentNode = null; return o; }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  setAttribute(n, v) { this.attrs[n] = v; }
  removeAttribute(n) { delete this.attrs[n]; }
  addEventListener(ev, fn) { this.handlers[ev] = fn; }
  cloneNode() {
    const c = new El(this.tag, this.className, this.attrs); c.nodeValue = this.nodeValue;
    this.children.forEach((k) => c.appendChild(k.cloneNode()));
    return c;
  }
  matches(list) {
    return list.split(',').some((sel) => this.matchesOne(sel.trim()));
  }
  matchesOne(sel) {
    if (sel[0] === '.') return this.className.split(' ').includes(sel.slice(1));
    if (sel[0] === '[') return sel.slice(1, -1) in this.attrs;
    return this.tag === sel;
  }
  all(sel, out) {
    this.children.forEach((c) => { if (c.matches(sel)) out.push(c); c.all(sel, out); });
    return out;
  }
  querySelectorAll(sel) { return this.all(sel, []); }
  querySelector(sel) { return this.all(sel, [])[0] || null; }
}
const text = (s) => { const t = new El('#text'); t.nodeValue = s; return t; };
const tag = (t, cls, attrs, ...kids) => { const e = new El(t, cls, attrs); kids.forEach((k) => e.appendChild(typeof k === 'string' ? text(k) : k)); return e; };

// The chooser as the box renders it for a browser: the chip starts as "follow the device".
function chooser({ auto = true } = {}) {
  const opt = (value, name, swatch, extra) => tag('label', 'theme-opt', {},
    tag('input', '', Object.assign({ type: 'radio', value, 'data-theme-choice': '' }, extra)),
    tag('span', 'swatch' + swatch, {}), tag('span', 'opt-text', {}, tag('span', 'opt-name', {}, name, tag('span', 'opt-state', {}, 'Selected'))));
  const where = tag('p', 'hint where', {}, 'Applies to this browser only.');
  const menu = tag('fieldset', 'theme-menu', { 'data-theme-choices': '', 'data-saved': 'Saved: {name}.', 'data-error': 'Could not be saved.' },
    tag('legend', '', {}, 'Theme'),
    opt('light', 'Light', ''), opt('dark', 'Dark', ''), opt('auto', 'Automatic', ' half', auto ? {} : { disabled: '' }), opt('device', 'Like your device', ' phone half'), where);
  const chipName = tag('span', 'theme-name', {}, tag('span', 'vh', {}, 'Theme: '), 'Like your device');
  const summary = tag('summary', '', {}, tag('span', 'swatch sm phone half', {}), chipName);
  const details = tag('details', 'theme-pick', {}, summary, menu);
  const radio = (v) => menu.querySelectorAll('[data-theme-choice]').find((r) => r.value === v);
  return { details, menu, summary, chipName, where, radio };
}

// Runs theme.js on a page; `store` is the localStorage content, `fail` makes writing throw (private mode).
function load({ store = {}, fail = false, auto = true, sun = null } = {}) {
  const c = chooser({ auto });
  const rootAttrs = {};
  if (sun) { rootAttrs['data-sun-rise'] = String(sun[0]); rootAttrs['data-sun-set'] = String(sun[1]); }
  const root = new El('html', '', rootAttrs);
  const handlers = {};
  const document = {
    documentElement: root,
    addEventListener(ev, fn) { handlers[ev] = fn; },
    querySelector: (s) => c.details.querySelector(s),
    createElement: (t) => new El(t),
    createTextNode: text,
  };
  const localStorage = {
    getItem: (k) => (k in store ? store[k] : null),
    setItem(k, v) { if (fail) throw new Error('denied'); store[k] = v; },
  };
  vm.runInNewContext(source, { document, localStorage, setInterval() {}, Date });
  handlers.DOMContentLoaded();
  const pick = (v) => { const r = c.radio(v); r.handlers.change({ target: r }); };
  return Object.assign(c, { root, store, pick });
}

const marked = (p) => p.menu.querySelectorAll('[data-theme-choice]').filter((r) => r.checked === true).map((r) => r.value);

test('choosing dark applies it at once, stores it, updates the chip and says so', () => {
  const p = load();
  p.pick('dark');
  assert.equal(p.root.getAttribute('data-theme'), 'dark');
  assert.equal(p.store['hejmo-theme'], 'dark');
  assert.deepEqual(marked(p), ['dark']);
  assert.equal(p.chipName.lastChild.nodeValue, 'Dark');
  assert.equal(p.chipName.querySelector('.vh').textContent, 'Theme: ');
  assert.match(p.summary.querySelector('.swatch').className, /\bsm\b/);
  const msg = p.menu.querySelector('.msg');
  assert.equal(msg.getAttribute('role'), 'status');
  assert.equal(msg.textContent, 'Saved: Dark.');
  assert.equal(p.menu.children[p.menu.children.indexOf(msg) + 1], p.where, 'the line sits above "where it applies"');
});

test('a second choice replaces the line instead of stacking another', () => {
  const p = load();
  p.pick('dark'); p.pick('light');
  assert.equal(p.menu.querySelectorAll('.msg').length, 1);
  assert.equal(p.menu.querySelector('.msg').textContent, 'Saved: Light.');
  assert.equal(p.root.getAttribute('data-theme'), 'light');
});

test('the stored choice is marked and shown on the chip when the page loads', () => {
  const p = load({ store: { 'hejmo-theme': 'light' } });
  assert.deepEqual(marked(p), ['light']);
  assert.equal(p.chipName.lastChild.nodeValue, 'Light');
});

test('without a stored choice the chooser shows follow the device', () => {
  const p = load();
  assert.deepEqual(marked(p), ['device']);
  assert.equal(p.root.getAttribute('data-theme'), null);
});

test('a stored automatic without sun times behaves and shows as follow the device', () => {
  const p = load({ store: { 'hejmo-theme': 'auto' }, auto: false });
  assert.equal(p.root.getAttribute('data-theme'), null);
  assert.deepEqual(marked(p), ['device']);
  assert.equal(p.chipName.lastChild.nodeValue, 'Like your device');
});

test('automatic with sun times is shown as automatic', () => {
  const now = Date.now() / 1000;
  const p = load({ store: { 'hejmo-theme': 'auto' }, sun: [now - 3600, now + 3600] });
  assert.equal(p.root.getAttribute('data-theme'), 'light');
  assert.deepEqual(marked(p), ['auto']);
});

test('a browser that refuses to store still applies the choice and says it lasts until reload', () => {
  const p = load({ fail: true });
  p.pick('dark');
  assert.equal(p.root.getAttribute('data-theme'), 'dark');
  const err = p.menu.querySelector('.error');
  assert.equal(err.getAttribute('role'), 'alert');
  assert.equal(err.textContent, 'Could not be saved.');
  assert.equal(p.menu.querySelector('.msg'), null);
  assert.equal(p.menu.children[p.menu.children.indexOf(err) + 1], p.where);
});

test('without a chooser on the page the script only applies the theme', () => {
  const root = new El('html', '', {});
  const handlers = {};
  const document = { documentElement: root, addEventListener(ev, fn) { handlers[ev] = fn; }, querySelector: () => null };
  vm.runInNewContext(source, { document, localStorage: { getItem: () => 'dark' }, setInterval() {}, Date });
  handlers.DOMContentLoaded();
  assert.equal(root.getAttribute('data-theme'), 'dark');
});
