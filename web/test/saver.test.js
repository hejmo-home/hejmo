// Tests of the screensaver's shuffle bag (ticket 178, Q68) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/saver.js'), 'utf8');
const sandbox = { document: { querySelector: () => null } };
sandbox.window = sandbox;
vm.runInNewContext(source, sandbox);
const { createBag } = sandbox.HejmoSaver;

// A repeatable "random": a small linear congruential generator.
function seeded(seed) {
  let x = seed;
  return () => { x = (x * 1664525 + 1013904223) % 4294967296; return x / 4294967296; };
}

const ids = ['a', 'b', 'c', 'd', 'e'];

test('every photo is shown once before any comes again', () => {
  for (let seed = 1; seed <= 50; seed++) {
    const bag = createBag(seeded(seed));
    const shown = [];
    let current = 'c'; // the photo the box put on the page
    for (let i = 0; i < ids.length * 3; i++) {
      current = bag.next(ids, current);
      shown.push(current);
    }
    const first = ['c'].concat(shown.slice(0, ids.length - 1));
    assert.deepEqual([...first].sort(), ids, `seed ${seed}: the first round, with the photo already on screen, shows each once`);
    for (let r = 1; r < 3; r++) {
      const round = shown.slice(ids.length - 1 + (r - 1) * ids.length, ids.length - 1 + r * ids.length);
      assert.deepEqual([...round].sort(), ids, `seed ${seed}: round ${r + 1} shows each once`);
    }
  }
});

test('the photo on screen is not shown again in the first round', () => {
  const bag = createBag(seeded(7));
  const firstRound = [];
  for (let i = 0; i < ids.length - 1; i++) firstRound.push(bag.next(ids, 'a'));
  assert.ok(!firstRound.includes('a'));
  assert.equal(new Set(firstRound).size, ids.length - 1);
});

test('a new round does not open with the photo just shown', () => {
  for (let seed = 1; seed <= 100; seed++) {
    const bag = createBag(seeded(seed));
    let last = 'a';
    for (let i = 0; i < ids.length - 1; i++) last = bag.next(ids, last);
    assert.notEqual(bag.next(ids, last), last, `seed ${seed}`);
  }
});

test('one photo is shown again and again, none is no photo', () => {
  const bag = createBag(seeded(3));
  assert.equal(bag.next(['x'], 'x'), 'x');
  assert.equal(bag.next(['x'], 'x'), 'x');
  assert.equal(createBag(seeded(3)).next([], ''), '');
});

test('a photo that left is not shown, one that came waits for the next round', () => {
  const bag = createBag(seeded(5));
  const first = bag.next(['a', 'b', 'c'], 'a');
  bag.keep(['a', 'c', 'z']);
  const second = bag.next(['a', 'c', 'z'], first);
  assert.ok(['a', 'c', 'z'].includes(second), 'b left');
  assert.notEqual(second, first);
});
