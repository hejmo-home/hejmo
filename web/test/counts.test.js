// Tests of the event counts on Choose calendars (ticket 5.17, card design #183)
// with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/connection-counts.js'), 'utf8');

// A count line as the template renders it: an icon and the text in .ct. The
// script must change the state and the text and leave the rest of the line
// (the icon, the two texts of an empty calendar) alone.
function line(url) {
  const attrs = { 'data-count-for': url, 'data-state': 'loading' };
  const ct = { textContent: 'needs JavaScript' };
  return {
    attrs, ct,
    getAttribute(a) { return a in attrs ? attrs[a] : null; },
    setAttribute(a, v) { attrs[a] = v; },
    querySelector(sel) { return sel === '.ct' ? ct : null; },
    get textContent() { throw new Error('the script must not rewrite the whole line'); },
    set textContent(v) { throw new Error('the script must not rewrite the whole line'); },
  };
}

async function run(lines, fetchImpl) {
  const formAttrs = { 'data-counts-url': '/counts', 'data-count-loading': 'Counting events…', 'data-count-unknown': 'Unknown' };
  const form = {
    getAttribute(a) { return a in formAttrs ? formAttrs[a] : null; },
    querySelectorAll(sel) { return sel === '[data-count-for]' ? lines : []; },
  };
  const document = { querySelector(sel) { return sel === 'form[data-counts-url]' ? form : null; } };
  const before = lines.map((l) => [l.attrs['data-state'], l.ct.textContent]);
  vm.runInNewContext(source, { document, fetch: fetchImpl });
  const started = lines.map((l) => [l.attrs['data-state'], l.ct.textContent]);
  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setImmediate(r));
  return { before, started };
}

const answer = (body) => () => Promise.resolve({ ok: true, json: () => Promise.resolve(body) });

test('each line says it is counting until the answer arrives, then shows state and text', async () => {
  const a = line('https://x/a/'), b = line('https://x/b/'), c = line('https://x/c/');
  const { started } = await run([a, b, c], answer([
    { url: 'https://x/a/', state: 'count', count: 4, text: '4 events' },
    { url: 'https://x/b/', state: 'empty', count: 0, text: 'No events' },
  ]));
  assert.deepEqual(started, [['loading', 'Counting events…'], ['loading', 'Counting events…'], ['loading', 'Counting events…']]);
  assert.deepEqual([a.attrs['data-state'], a.ct.textContent], ['count', '4 events']);
  // An empty calendar changes its state: the page shows its quiet or its warning text from the state.
  assert.equal(b.attrs['data-state'], 'empty');
  // A calendar the answer does not mention stays unknown.
  assert.deepEqual([c.attrs['data-state'], c.ct.textContent], ['unknown', 'Unknown']);
});

test('a failed request leaves every line unknown', async () => {
  const a = line('https://x/a/');
  await run([a], () => Promise.reject(new Error('offline')));
  assert.deepEqual([a.attrs['data-state'], a.ct.textContent], ['unknown', 'Unknown']);
  const b = line('https://x/b/');
  await run([b], () => Promise.resolve({ ok: false, status: 502 }));
  assert.deepEqual([b.attrs['data-state'], b.ct.textContent], ['unknown', 'Unknown']);
});
