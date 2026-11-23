// Tests of the script that tells the join page about a touch screen (#203, Q148) with node's
// test runner: since iPadOS 13 an iPad sends the user agent of a Mac, so the page cannot tell it
// from a Mac without help.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/screen-kind.js'), 'utf8');
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15';

function run({ userAgent = MAC, maxTouchPoints = 5, cookie = '', protocol = 'https:' } = {}) {
  const document = { cookie };
  let reloads = 0;
  vm.runInNewContext(source, {
    document,
    navigator: { userAgent, maxTouchPoints },
    location: { protocol, reload() { reloads++; } },
  });
  return { document, reloads };
}

test('a Mac user agent with a touch screen sets the cookie and loads the page again', () => {
  const r = run();
  assert.match(r.document.cookie, /^hejmo_touch=1; path=\/personal-devices\/join\/; max-age=\d+; samesite=lax; secure$/i);
  assert.equal(r.reloads, 1);
});

test('over plain HTTP the cookie is not marked secure', () => {
  assert.doesNotMatch(run({ protocol: 'http:' }).document.cookie, /secure/i);
});

test('a Mac without a touch screen reports nothing', () => {
  const r = run({ maxTouchPoints: 0 });
  assert.equal(r.document.cookie, '');
  assert.equal(r.reloads, 0);
});

test('a device that already says what it is reports nothing', () => {
  const r = run({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1' });
  assert.equal(r.document.cookie, '');
  assert.equal(r.reloads, 0);
});

test('with the cookie already set the page is not loaded again', () => {
  const r = run({ cookie: 'a=b; hejmo_touch=1' });
  assert.equal(r.reloads, 0);
});
