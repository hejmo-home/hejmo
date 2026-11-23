// Tests of the script of "always on the list" (ticket 168): node --test web/test
// It builds the addresses that ask the box for the sentence under the card and
// under the holiday; the sentences themselves are the box's.
const test = require('node:test');
const assert = require('node:assert/strict');
const a = require('../static/always.js');

test('the card asks for its sentence with what is chosen so far', () => {
  const url = a.planURL('/settings/lists/library/plan/milk', { list: 'l1', weekday: '1', every: '2', start: '2026-11-16', qmode: 'qty', amount: '3', unit: 'l' });
  assert.equal(url, '/settings/lists/library/plan/milk?list=l1&weekday=1&every=2&start=2026-11-16&qmode=qty&amount=3&unit=l');
});

test('without a quantity the amount and the unit are not sent', () => {
  const url = a.planURL('/p', { list: 'l1', weekday: '', every: '1', start: '2026-11-16', qmode: 'none', amount: '3', unit: 'l' });
  assert.equal(url, '/p?list=l1&every=1&start=2026-11-16&qmode=none');
});

test('values are escaped', () => {
  assert.equal(a.planURL('/p', { list: 'a&b', weekday: '1', every: '1', start: '', qmode: 'none' }), '/p?list=a%26b&weekday=1&every=1&qmode=none');
});

test('the holiday asks only while its switch is on', () => {
  assert.equal(a.holidayURL('/h', { on: false, from: '2026-12-19', to: '2027-01-03' }), null);
  assert.equal(a.holidayURL('/h', { on: true, from: '2026-12-19', to: '' }), '/h?from=2026-12-19');
});
