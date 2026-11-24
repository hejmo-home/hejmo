// Tests of the cooking view's script (ticket 286) with node's test runner:
//   node --test web/test
// The state of the view (ticks, step, view, portions) lives in the address and in sessionStorage; the screen
// stays on through the Screen Wake Lock, and where that is missing or refused there is no switch.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/cook.js'), 'utf8');

// The script runs on any page; without the form it does nothing.
const ctx = vm.createContext({ document: { querySelector: () => null }, Promise });
vm.runInContext(source, ctx);
const plain = (v) => JSON.parse(JSON.stringify(v));

function storage(initial) {
  const m = new Map(Object.entries(initial || {}));
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)); },
    removeItem: (k) => { m.delete(k); },
    key: (i) => [...m.keys()][i] ?? null,
    get length() { return m.size; },
    m,
  };
}

test('portions are a whole number from 1 to 99; anything else is none', () => {
  assert.equal(ctx.cookPortionsOf('4'), 4);
  assert.equal(ctx.cookPortionsOf(' 12 '), 12);
  assert.equal(ctx.cookPortionsOf('99'), 99);
  assert.equal(ctx.cookPortionsOf('07'), 7);
  for (const bad of ['0', '100', '', ' ', 'x', '1.5', '1,5', '-3', '+3', '123', null, undefined]) assert.equal(ctx.cookPortionsOf(bad), 0, String(bad));
});

test('the step stays between the first and the last', () => {
  assert.equal(ctx.cookStepOf('2', 4), 2);
  assert.equal(ctx.cookStepOf('9', 4), 4);
  assert.equal(ctx.cookStepOf('0', 4), 1);
  assert.equal(ctx.cookStepOf('-1', 4), 1);
  assert.equal(ctx.cookStepOf('x', 4), 1);
  assert.equal(ctx.cookStepOf('3', 0), 1);
});

test('the counts are written into the text of the locales', () => {
  assert.equal(ctx.cookFill('{k} von {n} abgehakt', 3, 10), '3 von 10 abgehakt');
  assert.equal(ctx.cookFill('Alle {n} bereit', 10, 10), 'Alle 10 bereit');
  assert.equal(ctx.cookFill('Step {k} of {n}', 2, 4), 'Step 2 of 4');
});

test('ticks, step, view and portions go into the address and come back out of it', () => {
  const q = ctx.cookQuery({ view: 'steps', step: 2, portions: 6, base: 4, have: [3, 0, 2] });
  assert.equal(q, 'view=steps&step=2&portions=6&have=0&have=2&have=3', 'ticks in order; the recipe\'s own portions are left out');
  assert.equal(ctx.cookQuery({ view: 'ings', step: 0, portions: 4, base: 4, have: [] }), 'view=ings');
  assert.deepEqual(plain(ctx.cookParseSearch('?' + q)), { view: 'steps', step: 2, portions: 6, have: [0, 2, 3], hasState: true });
  assert.deepEqual(plain(ctx.cookParseSearch('?have=1&have=1&have=x&have=-2&view=zzz&portions=0')), { view: '', step: 0, portions: 0, have: [1], hasState: true }, 'crafted values are ignored');
  assert.equal(ctx.cookParseSearch('').hasState, false, 'an empty address has no state: the kept one may be used');
  assert.equal(ctx.cookParseSearch('?portions=6').hasState, false, 'portions alone are no state of the visit');
});

test('ticks and the step are kept per recipe and portions for this visit', () => {
  const st = storage();
  ctx.cookSave(st, 'r1', 4, { step: 3, view: 'steps', have: [0, 2] });
  assert.deepEqual(plain(ctx.cookLoad(st, 'r1', 4)), { step: 3, view: 'steps', have: [0, 2] });
  assert.equal(ctx.cookLoad(st, 'r1', 6), null, 'other portions, other state');
  assert.equal(ctx.cookLoad(st, 'r2', 4), null, 'other recipe');
  ctx.cookSave(st, 'r1', 6, { step: 1, view: 'ings', have: [1] });
  ctx.cookSave(st, 'r2', 4, { step: 2, view: 'steps', have: [] });
  ctx.cookForget(st, 'r1');
  assert.equal(ctx.cookLoad(st, 'r1', 4), null, 'cooked: the next time starts afresh');
  assert.equal(ctx.cookLoad(st, 'r1', 6), null);
  assert.notEqual(ctx.cookLoad(st, 'r2', 4), null, 'another recipe is left alone');
});

test('what was kept is checked when it is read back', () => {
  const st = storage({ 'hejmo-cook:r:4': JSON.stringify({ step: 'x', view: 'zzz', have: [1, -1, 1.5, 'a', 2] }) });
  assert.deepEqual(plain(ctx.cookLoad(st, 'r', 4)), { step: 0, view: 'ings', have: [1, 2] });
  assert.equal(ctx.cookLoad(storage({ 'hejmo-cook:r:4': 'not json' }), 'r', 4), null);
  assert.equal(ctx.cookLoad(storage({ 'hejmo-cook:r:4': '{"have":3}' }), 'r', 4), null);
});

test('a browser that refuses storage does not stop the view', () => {
  const refusing = { setItem() { throw new Error('denied'); }, getItem() { throw new Error('denied'); }, removeItem() { throw new Error('denied'); }, key() { throw new Error('denied'); }, length: 1 };
  assert.doesNotThrow(() => ctx.cookSave(refusing, 'r', 4, { step: 1, view: 'ings', have: [] }));
  assert.equal(ctx.cookLoad(refusing, 'r', 4), null);
  assert.doesNotThrow(() => ctx.cookForget(refusing, 'r'));
});

// ---- the screen stays on

function fakeDoc() {
  const listeners = [];
  return { visibilityState: 'visible', addEventListener: (n, fn) => { if (n === 'visibilitychange') listeners.push(fn); }, show(state) { this.visibilityState = state; listeners.forEach((fn) => fn()); } };
}
function fakeUI() {
  const ui = { log: [], on: null, switchShown: null, noticeShown: false };
  ui.available = () => { ui.switchShown = true; ui.noticeShown = false; ui.log.push('available'); };
  ui.unavailable = () => { ui.switchShown = false; ui.noticeShown = true; ui.log.push('unavailable'); };
  ui.state = (on) => { ui.on = on; ui.log.push(on ? 'An' : 'Aus'); };
  return ui;
}
function fakeNav(reject) {
  const nav = { requests: [], sentinels: [] };
  nav.wakeLock = {
    request(type) {
      nav.requests.push(type);
      if (reject) return Promise.reject(new Error('NotAllowedError'));
      const s = { released: false, release() { this.released = true; return Promise.resolve(); }, addEventListener() {} };
      nav.sentinels.push(s);
      return Promise.resolve(s);
    },
  };
  return nav;
}
const flush = () => new Promise((resolve) => setImmediate(resolve));

test('the lock is requested for the screen when the view opens, with the switch on and saying so', async () => {
  const nav = fakeNav(), ui = fakeUI(), doc = fakeDoc();
  const lock = ctx.cookWakeLock(nav, doc, ui);
  await flush();
  assert.deepEqual(nav.requests, ['screen']);
  assert.equal(ui.switchShown, true);
  assert.equal(ui.noticeShown, false);
  assert.equal(ui.on, true);
  assert.ok(lock && typeof lock.set === 'function');
});

test('switching off releases the lock and says "Aus"; switching on asks again', async () => {
  const nav = fakeNav(), ui = fakeUI(), doc = fakeDoc();
  const lock = ctx.cookWakeLock(nav, doc, ui);
  await flush();
  lock.set(false);
  assert.equal(nav.sentinels[0].released, true);
  assert.equal(ui.on, false);
  lock.set(true);
  await flush();
  assert.equal(nav.requests.length, 2);
  assert.equal(ui.on, true);
  assert.equal(nav.sentinels[1].released, false);
});

test('the browser lets the lock go when the page is hidden; it is asked for again when the page is visible', async () => {
  const fire = [], nav = fakeNav(), doc = fakeDoc(), ui = fakeUI();
  nav.wakeLock.request = function () {
    nav.requests.push('screen');
    return Promise.resolve({ release() {}, addEventListener: (n, fn) => fire.push(fn) });
  };
  ctx.cookWakeLock(nav, doc, ui);
  await flush();
  assert.equal(nav.requests.length, 1);
  doc.show('visible');
  await flush();
  assert.equal(nav.requests.length, 1, 'a lock still held is not asked for twice');
  fire[0](); // the browser released it
  doc.show('hidden');
  await flush();
  assert.equal(nav.requests.length, 1, 'hidden: nothing to ask');
  doc.show('visible');
  await flush();
  assert.equal(nav.requests.length, 2, 'visible again: asked for again');
  assert.equal(ui.switchShown, true);
});

test('while the switch is off, a page that becomes visible does not ask again', async () => {
  const fire = [], nav = fakeNav(), doc = fakeDoc(), ui = fakeUI();
  nav.wakeLock.request = function () { nav.requests.push('screen'); return Promise.resolve({ release() {}, addEventListener: (n, fn) => fire.push(fn) }); };
  const lock = ctx.cookWakeLock(nav, doc, ui);
  await flush();
  lock.set(false);
  doc.show('visible');
  await flush();
  assert.equal(nav.requests.length, 1);
});

test('without the API there is no switch and a notice says what to do', () => {
  for (const nav of [{}, { wakeLock: undefined }, { wakeLock: {} }, null]) {
    const ui = fakeUI();
    assert.equal(ctx.cookWakeLock(nav, fakeDoc(), ui), null);
    assert.equal(ui.switchShown, false);
    assert.equal(ui.noticeShown, true);
    assert.deepEqual(ui.log, ['unavailable']);
  }
});

test('when the browser refuses the lock there is no switch and the notice shows', async () => {
  const nav = fakeNav(true), ui = fakeUI(), doc = fakeDoc();
  ctx.cookWakeLock(nav, doc, ui);
  await flush();
  assert.equal(ui.switchShown, false);
  assert.equal(ui.noticeShown, true);
  doc.show('visible');
  await flush();
  assert.equal(nav.requests.length, 1, 'a refusing browser is not asked again and again');
});

test('a request that throws at once is a refusal too', async () => {
  const nav = { wakeLock: { request() { throw new Error('SecurityError'); } } }, ui = fakeUI();
  ctx.cookWakeLock(nav, fakeDoc(), ui);
  await flush();
  assert.equal(ui.noticeShown, true);
  assert.equal(ui.switchShown, false);
});

test('a lock that arrives after the switch was turned off is let go at once', async () => {
  const nav = fakeNav(), ui = fakeUI(), doc = fakeDoc();
  const lock = ctx.cookWakeLock(nav, doc, ui);
  lock.set(false); // before the first answer
  await flush();
  assert.equal(nav.sentinels[0].released, true);
});

test('the service worker keeps the script of the cooking view, so steps and ticks work without a connection', () => {
  const sw = fs.readFileSync(path.join(__dirname, '../static/sw.js'), 'utf8');
  const list = sw.match(/const ASSETS = (\[[^\]]*\])/);
  assert.ok(list, 'sw.js has an ASSETS list');
  assert.ok(JSON.parse(list[1].replace(/'/g, '"')).includes('/static/cook.js'));
});
