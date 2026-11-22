// Tests of the pure parts of the layout section on the display page (ticket 180)
// with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/layout.js'), 'utf8');
const sandbox = { document: { querySelector: () => null } };
sandbox.window = sandbox;
vm.runInNewContext(source, sandbox);
const { moved, tomorrow, words } = sandbox.HejmoLayout;

const texts = {
  order: 'Left to right: {names}.', orderToday: 'Today, left to right: {names}.',
  fitLandscape: '{n} columns fit side by side in landscape.', fitPortrait: '{n} columns fit side by side in portrait.',
  swipe: '{names} you reach by swiping sideways.', none: 'Nobody chosen yet.',
  one: 'Only {person} chosen: “{name}” shows {person}’s personal view.', and: ' and ', name: 'Kitchen',
};

test('an arrow moves a person or a block one place, and the ends stay', () => {
  assert.deepEqual([...moved(['a', 'b', 'c'], 1, -1)], ['b', 'a', 'c']);
  assert.deepEqual([...moved(['a', 'b', 'c'], 1, 1)], ['a', 'c', 'b']);
  assert.deepEqual([...moved(['a', 'b', 'c'], 0, -1)], ['a', 'b', 'c']);
  assert.deepEqual([...moved(['a', 'b', 'c'], 2, 1)], ['a', 'b', 'c']);
  const original = ['a', 'b'];
  moved(original, 0, 1);
  assert.deepEqual(original, ['a', 'b'], 'the list given is not changed');
});

test('tomorrow every person is one column on, the rightmost goes to the left (Q91)', () => {
  assert.deepEqual([...tomorrow(['Mama', 'Papa', 'Mia'])], ['Mia', 'Mama', 'Papa']);
  assert.deepEqual([...tomorrow(['Mia'])], ['Mia']);
  assert.deepEqual([...tomorrow([])], []);
});

test('the preview says the order in words, and names the people beyond the visible columns', () => {
  assert.equal(words(texts, ['Mama', 'Papa'], false, false), 'Left to right: Mama, Papa.');
  assert.equal(words(texts, ['Mama', 'Papa'], false, true), 'Today, left to right: Mama, Papa.');
  const eight = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H'];
  assert.equal(words(texts, eight, true, false),
    'Left to right: A, B, C, D, E, F, G, H. 4 columns fit side by side in portrait. E, F, G and H you reach by swiping sideways.');
  assert.equal(words(texts, eight.slice(0, 6), false, false), 'Left to right: A, B, C, D, E, F.', 'six fit in landscape');
  assert.match(words(texts, eight.slice(0, 7), false, false), /^Left to right: .* 6 columns fit side by side in landscape\. G you reach by swiping sideways\.$/);
});

test('nobody and one person have their own sentence', () => {
  assert.equal(words(texts, [], false, false), 'Nobody chosen yet.');
  assert.equal(words(texts, ['Mia'], false, true), 'Only Mia chosen: “Kitchen” shows Mia’s personal view.');
});
