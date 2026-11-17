// Tests of the tile ticks on the web (ticket 166, preview-166):
//   node --test web/test
// A tile is posted in the background; if the box cannot be reached or answers
// with an error, the tile stays as it was, gets a red frame (aria-invalid) and
// the error stands under it. Without script the form posts as usual.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/tiles.js'), 'utf8');
const tick = () => new Promise((resolve) => setImmediate(resolve));

function element(extra) {
  return Object.assign({
    attrs: {}, children: [], className: '',
    getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; },
    setAttribute(n, v) { this.attrs[n] = String(v); },
    removeAttribute(n) { delete this.attrs[n]; },
    appendChild(c) { this.children.push(c); c.parentNode = this; return c; },
    removeChild(c) { this.children = this.children.filter((x) => x !== c); },
  }, extra);
}

function page(responses, opts = {}) {
  const state = { requests: [], listener: null, reloaded: 0 };
  const li = element();
  li.querySelector = (sel) => (sel === '.ferr' ? li.children.find((c) => c.className === 'ferr') || null : null);
  const form = element({ action: '/lists/L1/entries/E1/done' });
  form.parentNode = li;
  form.closest = (sel) => (sel === 'ul.tiles > li' && !opts.notATile ? li : null);
  const script = element({ attrs: { 'data-error': 'Not saved. Tap again.' } });
  const document = {
    currentScript: script,
    addEventListener: (n, fn) => { if (n === 'submit') state.listener = fn; },
    createElement: (n) => element({ tag: n }),
    createElementNS: (ns, n) => element({ tag: n }),
    createTextNode: (text) => ({ text }),
  };
  class FormData { constructor(f) { this.f = f; } *[Symbol.iterator]() { yield ['done', '1']; } }
  const fetch = (url, init) => {
    state.requests.push({ url, init });
    const next = responses.shift();
    if (next.reject) return Promise.reject(new Error('down'));
    return Promise.resolve(next);
  };
  const location = { reload() { state.reloaded++; } };
  const sandbox = { document, fetch, FormData, URLSearchParams, location };
  sandbox.window = sandbox;
  vm.runInNewContext(source, sandbox);
  return { state, form, li, submit() { const e = { target: form, preventDefault() { this.prevented = true; } }; state.listener(e); return e; } };
}

test('a tick posts in the background and reloads when the box answers with its redirect', async () => {
  const p = page([{ type: 'opaqueredirect', ok: false, status: 0 }]);
  const e = p.submit();
  await tick();
  assert.ok(e.prevented);
  assert.equal(p.state.requests[0].url, '/lists/L1/entries/E1/done');
  assert.equal(p.state.requests[0].init.method, 'POST');
  assert.equal(p.state.requests[0].init.redirect, 'manual', 'the success line must wait for the reload, not be used up here');
  assert.equal(p.state.reloaded, 1);
  assert.ok(!p.li.children.length, 'no error');
});

test('a tick that cannot reach the box marks the tile and says so under it', async () => {
  const p = page([{ reject: true }]);
  p.submit();
  await tick();
  assert.equal(p.state.reloaded, 0);
  assert.equal(p.li.attrs['aria-invalid'], 'true');
  const line = p.li.children.find((c) => c.className === 'ferr');
  assert.ok(line, 'the error stands under the tile');
  assert.equal(line.attrs.role, 'alert');
  assert.equal(line.children[line.children.length - 1].children[0].text, 'Not saved. Tap again.');
});

test('a tick the box answers with an error is not saved either', async () => {
  const p = page([{ type: 'basic', ok: false, status: 500 }]);
  p.submit();
  await tick();
  assert.equal(p.state.reloaded, 0);
  assert.equal(p.li.attrs['aria-invalid'], 'true');
});

test('the next tap clears the old error and the red frame first', async () => {
  const p = page([{ reject: true }, { type: 'opaqueredirect', ok: false, status: 0 }]);
  p.submit();
  await tick();
  p.submit();
  assert.ok(!p.li.children.length, 'the old error is gone at once');
  assert.ok(!('aria-invalid' in p.li.attrs));
  await tick();
  assert.equal(p.state.reloaded, 1);
});

test('a form that is not a tile posts as usual', () => {
  const p = page([], { notATile: true });
  const e = p.submit();
  assert.ok(!e.prevented);
  assert.equal(p.state.requests.length, 0);
});
