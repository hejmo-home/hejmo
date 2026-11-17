// Tests of the display script on the lists view (ticket 6.2) with node's test
// runner:
//   cd web && node --test
// display.js is run in a sandbox with a small fake document, view and fetch.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/display.js'), 'utf8');
const tick = () => new Promise((resolve) => setImmediate(resolve));

function element(extra) {
  const el = {
    attrs: {}, children: [], className: '',
    getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; },
    setAttribute(n, v) { this.attrs[n] = String(v); },
    appendChild(c) { this.children.push(c); c.parentNode = this; return c; },
    removeChild(c) { this.children = this.children.filter((x) => x !== c); },
  };
  return Object.assign(el, extra);
}

// A lists page: the display element, a view with a text field and an add form,
// and a log of every request.
function lists(opts = {}) {
  const state = { requests: [], focused: [], listeners: {}, docListeners: {}, ev: null, href: '' };
  const newField = (value) => {
    const f = element({ value, name: 'text', selectionStart: value.length, selectionEnd: value.length });
    f.focus = () => { state.focused.push(f); state.active = f; };
    f.setSelectionRange = () => {};
    return f;
  };
  const display = element({ attrs: {
    'data-display': '', 'data-state': 'paired', 'data-poll-seconds': '30', 'data-version': 'v1',
    'data-view-url': '/display/lists/L1/view', 'data-events-url': '/display/events?list=L1',
  } });
  const host = element({ attrs: { 'data-add-error': 'Not added: the box cannot be reached.' } });
  const slot = element(); // the .f of the add form
  const form = element({ className: 'add-entry', action: '/display/lists/L1/entries' });
  form.closest = (sel) => (sel === '[data-add-error]' ? host : null);
  form.querySelector = (sel) => (sel === '.f' ? slot : sel === '.ferr' ? slot.children.find((c) => c.className === 'ferr') || null : null);
  const view = element({ listeners: {}, added: [] });
  view.field = newField(opts.typed || '');
  view.querySelector = (sel) => (sel === '[data-keep]' ? view.field : null);
  view.querySelectorAll = (sel) => (sel === '.added' ? view.added : []);
  view.addEventListener = (n, fn) => { state.listeners[n] = fn; };
  Object.defineProperty(view, 'innerHTML', {
    set(html) { view.html = html; view.field = newField(''); if (state.active) state.active = null; },
    get() { return view.html; },
  });
  display.querySelector = (sel) => (sel === '[data-view]' ? view : null);
  const document = {
    querySelector: (sel) => (sel === '[data-display]' ? display : null),
    addEventListener: (n, fn) => { (state.docListeners[n] = state.docListeners[n] || []).push(fn); },
    createElement: (n) => element({ tag: n }),
    createElementNS: (ns, n) => element({ tag: n }),
    createTextNode: (text) => ({ text }),
    get activeElement() { return state.active || null; },
  };
  class EventSource { constructor(url) { state.es = this; this.url = url; this.on = {}; } addEventListener(n, fn) { this.on[n] = fn; } }
  class FormData { constructor(f) { this.f = f; } *[Symbol.iterator]() { yield ['text', f_text(this.f)]; } }
  const f_text = () => form.typed;
  const responses = opts.responses || [];
  const fetch = (url, init) => {
    state.requests.push({ url, init });
    const next = responses.shift();
    if (!next) return Promise.resolve({ ok: true, status: 200, headers: { get: () => 'v2' }, text: () => Promise.resolve('<p>fresh</p>') });
    if (next.reject) return Promise.reject(new Error('down'));
    return Promise.resolve({ ok: next.status < 400, status: next.status, redirected: false, url: 'https://box' + url,
      headers: { get: (h) => next.headers[h] || null }, text: () => Promise.resolve(next.body) });
  };
  const location = { href: '', reload() {} };
  const sandbox = {
    document, fetch, EventSource, FormData, URLSearchParams, URL, Intl, Date, encodeURIComponent,
    location, setInterval: () => 0, setTimeout: () => 0, clearTimeout() {},
  };
  sandbox.window = sandbox;
  vm.runInNewContext(source, sandbox);
  return { state, view, form, slot, host, announce: (v) => state.es.on.view({ data: v }), location };
}

const submitEvent = (form) => ({ target: form, preventDefault() { this.prevented = true; } });

test('a live refresh keeps what is typed in the field and its focus', async () => {
  const p = lists({ typed: 'Eier' });
  p.view.field.focus(); // the user is typing
  p.announce('v2');
  await tick();
  assert.equal(p.view.html, '<p>fresh</p>');
  assert.equal(p.view.field.value, 'Eier', 'the typed text survived the redraw');
  assert.equal(p.state.active, p.view.field, 'the field is focused again');
});

test('a live refresh does not focus a field nobody was typing in', async () => {
  const p = lists({ typed: '' });
  p.announce('v2');
  await tick();
  assert.ok(!p.state.active, "nothing is focused");
  assert.equal(p.view.field.value, '');
});

test('adding posts in the background, shows the new view, clears the field and focuses it', async () => {
  const p = lists({ typed: 'Pasta', responses: [{ status: 200, body: '<p>added</p>', headers: { 'X-Hejmo-View': 'v7', 'X-Hejmo-Added': 'E1' } }] });
  p.form.typed = 'Pasta';
  const e = submitEvent(p.form);
  p.state.listeners.submit(e);
  await tick();
  assert.ok(e.prevented, 'the form is not posted the old way');
  const req = p.state.requests[0];
  assert.equal(req.url, '/display/lists/L1/entries');
  assert.equal(req.init.method, 'POST');
  assert.equal(req.init.body.get('partial'), '1');
  assert.equal(req.init.body.get('text'), 'Pasta');
  assert.equal(p.view.html, '<p>added</p>');
  assert.equal(p.view.field.value, '', 'the field is empty for the next entry');
  assert.equal(p.state.active, p.view.field, 'and focused again');
});

test('the added entry stays named in refreshes until the next tap', async () => {
  const p = lists({ responses: [{ status: 200, body: '<p>added</p>', headers: { 'X-Hejmo-View': 'v7', 'X-Hejmo-Added': 'E1' } }] });
  p.form.typed = 'Pasta';
  p.state.listeners.submit(submitEvent(p.form));
  await tick();
  p.announce('v8');
  await tick();
  assert.equal(p.state.requests[1].url, '/display/lists/L1/view?added=E1');
  const framed = element();
  framed.classList = { removed: false, remove(c) { if (c === 'added') this.removed = true; } };
  p.view.added = [framed];
  p.state.docListeners.pointerdown.forEach((fn) => fn({}));
  assert.ok(framed.classList.removed, 'the frame goes at the next tap');
  p.announce('v9');
  await tick();
  assert.equal(p.state.requests[2].url, '/display/lists/L1/view');
});

test('an add the box refuses shows what the box answered, with the typed text kept by the box', async () => {
  const p = lists({ responses: [{ status: 422, body: '<p>error</p>', headers: { 'X-Hejmo-View': 'v7' } }] });
  p.form.typed = '';
  p.state.listeners.submit(submitEvent(p.form));
  await tick();
  assert.equal(p.view.html, '<p>error</p>');
});

test('an add that cannot reach the box says so under the field and keeps the text', async () => {
  const p = lists({ typed: 'Pasta', responses: [{ reject: true }] });
  p.view.field.value = 'Pasta';
  p.form.typed = 'Pasta';
  p.state.listeners.submit(submitEvent(p.form));
  await tick();
  assert.equal(p.view.html, undefined, 'nothing is redrawn');
  assert.equal(p.view.field.value, 'Pasta');
  const line = p.slot.children.find((c) => c.className === 'ferr');
  assert.ok(line, 'an error line sits in the field group');
  assert.equal(line.attrs.role, 'alert');
  assert.equal(line.children[line.children.length - 1].text, 'Not added: the box cannot be reached.');
});

// Ticket 165: the quantity of an entry opens under it, and the editor saves in the background.
const tapOn = (attrs) => ({
  target: { closest: (sel) => (sel === '[data-qty-open]' && attrs.open ? { getAttribute: () => attrs.open } : sel === '[data-qty-close]' && attrs.close ? {} : null) },
  preventDefault() { this.prevented = true; },
});

test('the quantity button opens the editor of its entry in the view, and a second tap closes it', async () => {
  const p = lists({ responses: [{ status: 200, body: '<p>editor</p>', headers: { 'X-Hejmo-View': 'v2' } }, { status: 200, body: '<p>closed</p>', headers: { 'X-Hejmo-View': 'v3' } }] });
  const open = tapOn({ open: 'E7' });
  p.state.listeners.click(open);
  await tick();
  assert.ok(open.prevented);
  assert.equal(p.state.requests[0].url, '/display/lists/L1/view?qty=E7');
  assert.equal(p.view.html, '<p>editor</p>');
  p.state.listeners.click(tapOn({ open: 'E7' }));
  await tick();
  assert.equal(p.state.requests[1].url, '/display/lists/L1/view', 'the same button closes it');
});

test('the editor stays open in refreshes, and Cancel closes it', async () => {
  const p = lists();
  p.state.listeners.click(tapOn({ open: 'E7' }));
  await tick();
  p.announce('v8');
  await tick();
  assert.equal(p.state.requests[1].url, '/display/lists/L1/view?qty=E7');
  p.state.listeners.click(tapOn({ close: true }));
  await tick();
  assert.equal(p.state.requests[2].url, '/display/lists/L1/view');
});

test('saving a quantity posts in the background; a refusal keeps the editor open, a save closes it and names the line', async () => {
  const p = lists({ responses: [
    { status: 200, body: '<p>editor</p>', headers: { 'X-Hejmo-View': 'v6' } },
    { status: 422, body: '<p>error</p>', headers: { 'X-Hejmo-View': 'v7' } },
    { status: 200, body: '<p>refreshed</p>', headers: { 'X-Hejmo-View': 'v9' } },
    { status: 200, body: '<p>saved</p>', headers: { 'X-Hejmo-View': 'v8', 'X-Hejmo-Added': 'E7', 'X-Hejmo-Result': 'tok1' } },
  ] });
  p.state.listeners.click(tapOn({ open: 'E7' }));
  await tick();
  const editor = element({ className: 'qty-edit', action: '/display/lists/L1/entries/E7/quantity' });
  editor.parentNode = element();
  editor.closest = () => null;
  const e = submitEvent(editor);
  p.state.listeners.submit(e);
  await tick();
  assert.ok(e.prevented, 'the editor is not posted the old way');
  const req = p.state.requests[1];
  assert.equal(req.url, '/display/lists/L1/entries/E7/quantity');
  assert.equal(req.init.body.get('partial'), '1');
  assert.equal(p.view.html, '<p>error</p>');
  p.announce('v9');
  await tick();
  assert.equal(p.state.requests[2].url, '/display/lists/L1/view?qty=E7', 'still open after a refusal');
  p.state.listeners.submit(submitEvent(editor));
  await tick();
  assert.equal(p.view.html, '<p>saved</p>');
  p.announce('v10');
  await tick();
  assert.equal(p.state.requests[4].url, '/display/lists/L1/view?added=E7&r=tok1', 'closed, the entry framed and the line kept');
});
