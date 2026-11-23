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
    set(html) { view.html = html; if (opts.scrolled) sandbox.scrollY = 0; view.field = newField(''); if (state.active) state.active = null; },
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
  sandbox.scrollY = opts.scrolled || 0;
  sandbox.scrollTo = (x, y) => { state.scrolledTo = [x, y]; sandbox.scrollY = y; };
  vm.runInNewContext(source, sandbox);
  return { sandbox, state, view, form, slot, host, announce: (v) => state.es.on.view({ data: v }), location };
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

// The family view with the screensaver timer (ticket 178). The page is rendered
// once and stays for days: the timer asks the box whether the screensaver is due.
function family(answers, attrs, size) {
  const timers = [];
  const intervals = [];
  const requests = [];
  const display = element({ attrs: Object.assign({
    'data-display': '', 'data-state': 'paired', 'data-poll-seconds': '30', 'data-version': 'v1',
    'data-saver-seconds': '600', 'data-saver-url': '/display/saver',
  }, attrs) });
  display.querySelector = () => null; // no view needed here
  const listeners = {};
  const document = {
    querySelector: (sel) => (sel === '[data-display]' ? display : null),
    addEventListener: (n, fn) => { (listeners[n] = listeners[n] || []).push(fn); },
  };
  const fetch = (url) => {
    requests.push(url);
    const body = answers.shift();
    if (!body) return Promise.reject(new Error('down'));
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
  };
  const location = { href: '', reload() { location.reloaded = true; } };
  const setTimeout = (fn, ms) => { const t = { fn, ms, live: true }; timers.push(t); return t; };
  const clearTimeout = (t) => { if (t) t.live = false; };
  const sandbox = { document, fetch, Intl, Date, encodeURIComponent, location, setInterval: (fn) => intervals.push(fn), setTimeout, clearTimeout };
  sandbox.window = sandbox;
  Object.assign(sandbox, size || {});
  vm.runInNewContext(source, sandbox);
  const live = () => timers.filter((t) => t.live);
  return { timers, live, intervals, requests, location, listeners };
}

test('the idle timer asks the box and starts the screensaver only when it is due', async () => {
  const p = family([
    { state: 'paired', saver: '0', saver_seconds: '600' }, // quiet mode: not due
    { state: 'paired', saver: '1', saver_seconds: '600' }, // the day begins
  ]);
  assert.equal(p.live().length, 1);
  assert.equal(p.live()[0].ms, 600 * 1000);

  p.live()[0].fn();
  await tick();
  assert.deepEqual(p.requests, ['/display/state']);
  assert.equal(p.location.href, '', 'quiet mode: the display stays on the day');
  assert.equal(p.live().length, 1, 'and asks again after the idle time');
  assert.equal(p.live()[0].ms, 600 * 1000);

  p.live()[0].fn();
  await tick();
  assert.equal(p.location.href, '/display/saver', 'the next time it is due');
});

test('the idle timer follows the idle minutes the box announces', async () => {
  const p = family([{ state: 'paired', saver: '0', saver_seconds: '300' }]);
  p.live()[0].fn();
  await tick();
  assert.equal(p.live()[0].ms, 300 * 1000);
});

test('a touch restarts the idle timer, an unreachable box too', async () => {
  const p = family([]); // every request fails
  const first = p.live()[0];
  p.listeners.pointerdown.forEach((fn) => fn({}));
  assert.ok(!first.live, 'the old countdown is gone');
  assert.equal(p.live().length, 1);
  p.live()[0].fn();
  await tick();
  assert.equal(p.location.href, '');
  assert.equal(p.live().length, 1, 'it tries again later');
});

test('a family view with the screensaver off has no countdown, but listens for it being switched on', () => {
  const p = family([], { 'data-saver-seconds': '0' });
  assert.equal(p.live().length, 0);
  assert.ok(p.listeners.pointerdown.length > 0);
});

test('the poll starts the countdown when the screensaver is switched on, and ends it when switched off', async () => {
  const p = family([
    { state: 'paired', saver: '1', saver_seconds: '300' },
    { state: 'paired', saver: '0', saver_seconds: '0' },
  ], { 'data-saver-seconds': '0' });
  p.intervals[0]();
  await tick();
  assert.equal(p.live().length, 1);
  assert.equal(p.live()[0].ms, 300 * 1000);
  p.intervals[0]();
  await tick();
  assert.equal(p.live().length, 0);
  assert.ok(!p.location.reloaded);
});

// The quiet page of a display a kiosk app switched off listens for a touch
// (ticket 179): it tells the box, at most every 15 seconds, and only when the
// markup in the view names an address.
function quiet(wakeURL) {
  const requests = [];
  const listeners = {};
  const display = element({ attrs: { 'data-display': '', 'data-state': 'paired', 'data-poll-seconds': '30', 'data-version': 'v1' } });
  const view = element({ addEventListener() {}, querySelectorAll: () => [] });
  view.querySelector = (sel) => (sel === '[data-wake-url]' && view.wake ? view.wake : null);
  view.wake = wakeURL ? element({ attrs: { 'data-wake-url': wakeURL } }) : null;
  display.querySelector = (sel) => (sel === '[data-view]' ? view : null);
  let now = 1000000;
  class FakeDate extends Date { static now() { return now; } }
  const document = {
    querySelector: (sel) => (sel === '[data-display]' ? display : null),
    addEventListener: (n, fn) => { (listeners[n] = listeners[n] || []).push(fn); },
    visibilityState: 'visible',
  };
  const fetch = (url, init) => { requests.push({ url, init }); return Promise.resolve({ ok: true, status: 204 }); };
  class EventSource { constructor() { this.on = {}; } addEventListener() {} }
  const sandbox = { document, fetch, EventSource, Intl, Date: FakeDate, encodeURIComponent, location: { href: '', reload() {} }, setInterval: () => 0, setTimeout: () => 0, clearTimeout() {} };
  sandbox.window = sandbox;
  vm.runInNewContext(source, sandbox);
  return { requests, listeners, view, advance(ms) { now += ms; } };
}

test('a touch on a quiet page with a wake address tells the box, at most every 15 seconds', () => {
  const p = quiet('/display/wake');
  p.listeners.pointerdown.forEach((fn) => fn({}));
  assert.equal(p.requests.length, 1);
  assert.equal(p.requests[0].url, '/display/wake');
  assert.equal(p.requests[0].init.method, 'POST');
  p.listeners.touchstart.forEach((fn) => fn({}));
  assert.equal(p.requests.length, 1, 'a second touch right after is not sent');
  p.advance(16000);
  p.listeners.pointerdown.forEach((fn) => fn({}));
  assert.equal(p.requests.length, 2, 'a later touch keeps the display awake');
});

test('the screen coming back on counts as a touch', () => {
  const p = quiet('/display/wake');
  p.listeners.visibilitychange.forEach((fn) => fn({}));
  assert.equal(p.requests.length, 1);
});

test('a page without a wake address sends nothing', () => {
  const p = quiet('');
  p.listeners.pointerdown.forEach((fn) => fn({}));
  p.listeners.visibilitychange.forEach((fn) => fn({}));
  assert.equal(p.requests.length, 0);
});

// Ticket 166 (preview-166): a tile is a tick button like a row. If the box
// cannot be reached, the tile stays open, is marked invalid (red frame) and the
// error stands under it; the next tap clears both.
function tileForm(p) {
  const li = element();
  li.queryFerr = () => li.children.find((c) => c.className === 'ferr') || null;
  li.querySelector = (sel) => (sel === '.ferr' ? li.queryFerr() : null);
  li.removeAttribute = (n) => { delete li.attrs[n]; };
  const form = element({ action: '/display/lists/L1/entries/E1/done' });
  form.parentNode = li;
  const host = element({ attrs: { 'data-tick-error': 'Not saved. Tap again.' } });
  form.closest = (sel) => (sel === '[data-tick-error]' ? host : null);
  form.querySelector = (sel) => (/tile-check/.test(sel) ? element({ className: 'tile-check' }) : null);
  return { form, li };
}

test('a tile that cannot be saved stays open with a red frame and the error under it', async () => {
  const p = lists({ responses: [{ reject: true }] });
  const { form, li } = tileForm(p);
  const e = submitEvent(form);
  p.state.listeners.submit(e);
  await tick();
  assert.ok(e.prevented, 'the tick is posted in the background');
  assert.equal(li.attrs['aria-invalid'], 'true', 'the tile is marked invalid');
  const line = li.queryFerr();
  assert.ok(line, 'the error stands under the tile');
  assert.equal(line.children[line.children.length - 1].text, 'Not saved. Tap again.');
});

test('the next tap on a tile clears its error and its red frame', async () => {
  const p = lists({ responses: [{ reject: true }, { status: 200, body: '<p>ok</p>', headers: { 'X-Hejmo-View': 'v2' } }] });
  const { form, li } = tileForm(p);
  p.state.listeners.submit(submitEvent(form));
  await tick();
  p.state.listeners.submit(submitEvent(form));
  assert.ok(!li.queryFerr(), 'the old error is gone');
  assert.ok(!('aria-invalid' in li.attrs), 'and the frame with it');
});

// Ticket 180: the box learns how the display hangs, so the layout preview on the
// settings page is drawn that way.
test('the regular check tells the box whether the display hangs in portrait or landscape', () => {
  const portrait = family([{ state: 'paired' }], {}, { innerWidth: 768, innerHeight: 1024 });
  portrait.intervals[0]();
  assert.equal(portrait.requests[0], '/display/state?orientation=portrait');
  const landscape = family([{ state: 'paired' }], {}, { innerWidth: 1024, innerHeight: 768 });
  landscape.intervals[0]();
  assert.equal(landscape.requests[0], '/display/state?orientation=landscape');
});

// Ticket 208: a phone that has scrolled down to the tiles stays there when the
// box swaps the view (the family view scrolls as one page on phones).
test('a refresh keeps the scroll position of the page', async () => {
  const p = lists({ scrolled: 420 });
  p.announce('v2');
  await tick();
  assert.equal(p.view.html, '<p>fresh</p>');
  assert.deepEqual(p.state.scrolledTo, [0, 420]);
  assert.equal(p.sandbox.scrollY, 420);
});

test('a page that has not scrolled is not scrolled by a refresh', async () => {
  const p = lists();
  p.announce('v2');
  await tick();
  assert.equal(p.state.scrolledTo, undefined);
});

test('a tap on the Below-the-columns anchor is left to the browser', () => {
  const p = lists();
  const e = { target: { closest: () => null }, preventDefault() { this.prevented = true; } };
  p.state.listeners.click(e);
  assert.ok(!e.prevented);
});

// Ticket 208, product owner feedback 23/11/2026: on a phone a tap on a person's
// column shows that column alone in full width; the back button or a swipe to
// the right brings all columns back. A fake family view: a grid with columns
// (slots), a back button and the markup a refresh puts in.
function fullWidth(opts = {}) {
  const state = { listeners: {}, docListeners: {}, requests: [], scrolledTo: null, grid: null };
  const slots = opts.slots || [1, 2, 3];
  const display = element({ attrs: {
    'data-display': '', 'data-state': 'paired', 'data-poll-seconds': '30', 'data-version': 'v1',
  } });
  const view = element();
  let fv;
  let columns;
  const closestOf = (el, map) => { el.closest = (sel) => (sel in map ? map[sel] : null); return el; };
  function build(present) {
    fv = element();
    const grid = element({ scrollLeft: 0, offsetLeft: 14 });
    grid.getBoundingClientRect = () => ({ top: opts.gridTop === undefined ? 300 : opts.gridTop });
    state.grid = grid;
    columns = present.map((slot, i) => {
      const col = element({ attrs: { 'data-person': String(slot) }, offsetLeft: 14 + i * 158 });
      col.name = closestOf(element(), { '.column': col, 'a': element() });
      col.entry = closestOf(element(), { '.column': col });
      col.pbar = closestOf(element(), { '.column': col, '.pbar': element(), 'a': element() });
      return col;
    });
    grid.querySelectorAll = () => columns;
    fv.back = closestOf(element(), { '[data-full-close]': element() });
    fv.querySelector = (sel) => {
      if (sel === '.fv-grid') return grid;
      if (sel === '.column[data-full]') return columns.find((c) => 'data-full' in c.attrs) || null;
      return null;
    };
    fv.removeAttribute = function (n) { delete this.attrs[n]; };
    columns.forEach((c) => { c.removeAttribute = function (n) { delete this.attrs[n]; }; });
  }
  build(slots);
  view.querySelector = (sel) => {
    if (sel === '.fv') return fv;
    const m = /^\.column\[data-person="(\d+)"\]$/.exec(sel);
    if (m) return columns.find((c) => c.attrs['data-person'] === m[1]) || null;
    return null;
  };
  view.querySelectorAll = () => [];
  view.addEventListener = (n, fn) => { state.listeners[n] = fn; };
  Object.defineProperty(view, 'innerHTML', {
    set(html) { view.html = html; build(state.next || slots); },
    get() { return view.html; },
  });
  display.querySelector = (sel) => (sel === '[data-view]' ? view : null);
  const document = {
    querySelector: (sel) => (sel === '[data-display]' ? display : null),
    addEventListener: (n, fn) => { (state.docListeners[n] = state.docListeners[n] || []).push(fn); },
    get activeElement() { return null; },
  };
  class EventSource { constructor() { state.es = this; this.on = {}; } addEventListener(n, fn) { this.on[n] = fn; } }
  const fetch = (url) => {
    state.requests.push(url);
    return Promise.resolve({ ok: true, status: 200, headers: { get: () => 'v2' }, text: () => Promise.resolve('<p>fresh</p>') });
  };
  const queries = [];
  const sandbox = {
    document, fetch, EventSource, URLSearchParams, URL, Intl, Date, encodeURIComponent,
    location: { href: '', reload() {} }, setInterval: () => 0, setTimeout: () => 0, clearTimeout() {},
    matchMedia: (q) => { queries.push(q); return { matches: opts.phone !== false }; },
    scrollY: opts.scrollY || 0,
    scrollTo: (x, y) => { state.scrolledTo = [x, y]; sandbox.scrollY = y; },
  };
  sandbox.window = sandbox;
  vm.runInNewContext(source, sandbox);
  const click = (target) => {
    const e = { target, preventDefault() { this.prevented = true; } };
    state.listeners.click(e);
    return e;
  };
  const swipe = (dx, dy) => {
    const fire = (n, x, y) => (state.docListeners[n] || []).forEach((fn) => fn({ clientX: x, clientY: y, changedTouches: [{ clientX: x, clientY: y }] }));
    fire('pointerdown', 300, 400);
    fire('pointerup', 300 + dx, 400 + dy);
  };
  return {
    state, view, queries, click, swipe, sandbox,
    get fv() { return fv; }, get columns() { return columns; }, get grid() { return state.grid; },
    refresh: async (next) => { state.next = next; state.es.on.view({ data: 'v2' }); await tick(); },
  };
}

const isFull = (p) => p.fv.attrs['data-full-person'];

test('a tap on a column on a phone shows that column alone', () => {
  const p = fullWidth();
  const e = p.click(p.columns[1].entry);
  assert.ok(e.prevented, 'the tap does not navigate');
  assert.equal(isFull(p), '2');
  assert.ok('data-full' in p.columns[1].attrs);
  assert.ok(!('data-full' in p.columns[0].attrs));
  assert.equal(p.queries[0], '(max-width: 599px), (max-height: 500px)');
});

test('a tap on the name opens the column too, the second tap on it opens the personal view', () => {
  const p = fullWidth();
  assert.ok(p.click(p.columns[0].name).prevented);
  assert.equal(isFull(p), '1');
  assert.ok(!p.click(p.columns[0].name).prevented, 'the browser follows the link');
  assert.equal(isFull(p), '1', 'and the column stays until the view is left');
});

test('a tap on the progress block opens the personal view, also with a column full', () => {
  const p = fullWidth();
  assert.ok(!p.click(p.columns[0].pbar).prevented);
  assert.equal(isFull(p), undefined, 'nothing opened');
  p.click(p.columns[0].entry);
  assert.ok(!p.click(p.columns[0].pbar).prevented);
});

test('on a wall display a tap is left to the browser', () => {
  const p = fullWidth({ phone: false });
  assert.ok(!p.click(p.columns[0].name).prevented);
  assert.ok(!p.click(p.columns[0].entry).prevented);
  assert.equal(isFull(p), undefined);
});

test('the back button shows all columns, that column first in view', () => {
  const p = fullWidth({ scrollY: 100 });
  p.click(p.columns[2].entry);
  const e = p.click(p.fv.back);
  assert.ok(e.prevented);
  assert.equal(isFull(p), undefined);
  assert.ok(!('data-full' in p.columns[2].attrs));
  assert.equal(p.grid.scrollLeft, 14 + 2 * 158 - 14, 'the grid starts at that column');
  assert.equal(p.state.scrolledTo, null, 'the page is above the columns already');
});

test('closing scrolls back to the top of the columns only if the page is below it', () => {
  const p = fullWidth({ scrollY: 900, gridTop: -600 });
  p.click(p.columns[0].entry);
  p.click(p.fv.back);
  assert.deepEqual(p.state.scrolledTo, [0, 900 - 600 - 14]);
});

test('a swipe to the right of at least 64 px, mostly sideways, closes the column', () => {
  for (const [dx, dy, closes] of [[64, 0, true], [63, 0, false], [200, 99, true], [200, 100, false], [200, -150, false], [120, -59, true]]) {
    const p = fullWidth();
    p.click(p.columns[1].entry);
    p.swipe(dx, dy);
    assert.equal(isFull(p) === undefined, closes, `dx ${dx}, dy ${dy}`);
  }
});

test('a swipe to the left does nothing, nor does a swipe with all columns shown', () => {
  const p = fullWidth();
  p.click(p.columns[1].entry);
  p.swipe(-300, 0);
  assert.equal(isFull(p), '2');
  const q = fullWidth();
  q.swipe(300, 0);
  assert.equal(isFull(q), undefined);
  assert.equal(q.state.scrolledTo, null);
});

// A mouse drag to the right ends in a click on the column under the pointer;
// it must not open the column that the drag just closed.
test('the click that follows a closing swipe is ignored, a later tap is not', () => {
  const p = fullWidth();
  let now = 1000;
  p.sandbox.Date = { now: () => now };
  p.click(p.columns[1].entry);
  p.swipe(200, 0);
  assert.equal(isFull(p), undefined);
  now += 50;
  const e = p.click(p.columns[1].entry);
  assert.ok(e.prevented, 'the stray click is swallowed');
  assert.equal(isFull(p), undefined, 'the column stays closed');
  now += 1000;
  p.click(p.columns[1].entry);
  assert.equal(isFull(p), '2', 'a tap after that opens it');
});

test('a swipe that does not close the column does not swallow the next tap', () => {
  const p = fullWidth();
  p.sandbox.Date = { now: () => 1000 };
  p.swipe(30, 0);
  p.click(p.columns[0].entry);
  assert.equal(isFull(p), '1');
});

test('a touch swipe closes the column as well', () => {
  const p = fullWidth();
  p.click(p.columns[1].entry);
  (p.state.docListeners.touchstart || []).forEach((fn) => fn({ changedTouches: [{ clientX: 50, clientY: 300 }] }));
  (p.state.docListeners.touchend || []).forEach((fn) => fn({ changedTouches: [{ clientX: 150, clientY: 310 }] }));
  assert.equal(isFull(p), undefined);
});

test('a swipe on a wall display does nothing', () => {
  const p = fullWidth({ phone: false });
  p.fv.setAttribute('data-full-person', '1');
  p.swipe(300, 0);
  assert.equal(isFull(p), '1');
});

test('a refresh keeps the full column on the new markup', async () => {
  const p = fullWidth();
  p.click(p.columns[1].entry);
  await p.refresh([1, 2, 3]);
  assert.equal(isFull(p), '2', 'the new view shows that column alone');
  assert.ok('data-full' in p.columns[1].attrs);
  assert.ok(!('data-full' in p.columns[0].attrs));
});

test('a refresh without that person shows all columns', async () => {
  const p = fullWidth();
  p.click(p.columns[1].entry);
  await p.refresh([1, 3]);
  assert.equal(isFull(p), undefined);
  assert.ok(p.columns.every((c) => !('data-full' in c.attrs)));
});
