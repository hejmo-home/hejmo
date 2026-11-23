// Tests of the update upload script (ticket 221) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/update-upload.js'), 'utf8');
const sandbox = {};
vm.runInNewContext(source, sandbox);
const { check, fill, megabytes, progress, poll } = sandbox.HejmoUpdate;

const cfg = { none: 'Choose the update file first.', name: 'Not an update file.', big: '“{name}” is {size} MB. At most {max} MB.', maxBytes: 500 * 1048576, maxMB: 500 };

test('sizes are whole megabytes, at least one', () => {
  assert.equal(megabytes(0), 1);
  assert.equal(megabytes(1), 1);
  assert.equal(megabytes(1048576), 1);
  assert.equal(megabytes(1048577), 2);
  assert.equal(megabytes(61 * 1048576 - 5), 61);
});

test('texts are filled by name and unknown names stay', () => {
  assert.equal(fill('{done} of {total} MB', { done: 23, total: 61 }), '23 of 61 MB');
  assert.equal(fill('{a} {b}', { a: 1 }), '1 {b}');
});

test('a file is checked in the browser before a single byte is sent', () => {
  assert.equal(check(null, cfg), cfg.none);
  assert.equal(check({ name: 'backup.tar', size: 10 }, cfg), cfg.name);
  assert.equal(check({ name: 'hejmo-0.2.4-arm64.update', size: 61 * 1048576 }, cfg), '');
  assert.equal(check({ name: 'HEJMO-0.2.4-ARM64.UPDATE', size: 1 }, cfg), '', 'the ending is no matter of case');
  assert.equal(check({ name: 'hejmo-backup-november.update', size: 812 * 1048576 }, cfg), '“hejmo-backup-november.update” is 812 MB. At most 500 MB.');
  assert.equal(check({ name: 'x.update', size: 500 * 1048576 }, cfg), '', 'exactly the limit is allowed');
});

test('progress is told in megabytes and a bar, never only a bar', () => {
  assert.deepEqual({ ...progress(23 * 1048576, 61 * 1048576, '{done} of {total} MB') }, { text: '23 of 61 MB', now: 23, max: 61, pct: 38 });
  assert.equal(progress(0, 0, '{done} of {total} MB').pct, 0);
  assert.equal(progress(100, 50, '{done} of {total} MB').pct, 100, 'never over a full bar');
});

const minute = 60 * 1000;

test('the installing page reloads when the step changes or the box has finished', () => {
  const s = { step: 'backup', failedSince: null };
  assert.deepEqual({ ...poll(s, { ok: true, json: { state: 'installing', step: 'backup' } }, 0) }, { reload: false, gone: false, failedSince: null });
  assert.equal(poll(s, { ok: true, json: { state: 'installing', step: 'start' } }, 0).reload, true);
  assert.equal(poll(s, { ok: true, json: { state: 'none' } }, 0).reload, true, 'the installation is over');
  assert.equal(poll(s, { ok: true, json: { state: 'ready' } }, 0).reload, true);
});

test('the page waits while the box is away and reloads when it is back', () => {
  let s = { step: 'start', failedSince: null };
  let r = poll(s, { ok: false }, 1000);
  assert.deepEqual({ ...r }, { reload: false, gone: false, failedSince: 1000 });
  s = { step: 'start', failedSince: r.failedSince };
  r = poll(s, { ok: false }, 1000 + 5000);
  assert.equal(r.failedSince, 1000, 'the first failure counts');
  assert.equal(r.gone, false);
  // After ten minutes without an answer the page says so.
  assert.equal(poll(s, { ok: false }, 1000 + 10 * minute).gone, true);
  assert.equal(poll(s, { ok: false }, 1000 + 10 * minute - 1).gone, false);
  // Whatever it answers once it is back, the page loads again.
  assert.equal(poll(s, { ok: true, json: { state: 'installing', step: 'start' } }, 1000 + 20000).reload, true);
});
