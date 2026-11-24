// Tests of the meal plan form's script (ticket 281) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/meal-suggest.js'), 'utf8');

// The script runs on every page with the form; without it the script does nothing.
const ctx = vm.createContext({ document: { querySelector: () => null } });
vm.runInContext(source, ctx);

test('suggestions are asked for from two letters on, for the field of the cell', () => {
  assert.equal(ctx.mealSuggestUrl('/meals/suggest', 'C', 'meal_2026-11-14_dinner'), '');
  assert.equal(ctx.mealSuggestUrl('/meals/suggest', '   ', 'x'), '');
  assert.equal(ctx.mealSuggestUrl('/meals/suggest', 'Cu', 'meal_2026-11-14_dinner'), '/meals/suggest?q=Cu&cell=meal_2026-11-14_dinner');
  assert.equal(ctx.mealSuggestUrl('/meals/suggest', '  Hähnchen   Curry ', 'c'), '/meals/suggest?q=H%C3%A4hnchen%20Curry&cell=c');
  assert.equal(ctx.mealSuggestUrl('/s', '\u{1F35B}\u{1F35C}', 'c'), '/s?q=%F0%9F%8D%9B%F0%9F%8D%9C&cell=c', 'two emoji are two letters');
});

test('the arrow keys move through the options and the ends meet', () => {
  assert.equal(ctx.mealMove(-1, 4, 1), 0, 'down from nothing goes to the first');
  assert.equal(ctx.mealMove(-1, 4, -1), 3, 'up from nothing goes to the last');
  assert.equal(ctx.mealMove(0, 4, 1), 1);
  assert.equal(ctx.mealMove(3, 4, 1), 0);
  assert.equal(ctx.mealMove(0, 4, -1), 3);
  assert.equal(ctx.mealMove(2, 0, 1), -1, 'no options, nothing to select');
  assert.equal(ctx.mealMove(9, 3, 1), 0, 'a stale index starts again');
});

test('portions stay between 1 and 99 and start from a sensible number', () => {
  assert.equal(ctx.mealStep('4', 1), 5);
  assert.equal(ctx.mealStep('4', -1), 3);
  assert.equal(ctx.mealStep('1', -1), 1);
  assert.equal(ctx.mealStep('99', 1), 99);
  assert.equal(ctx.mealStep('', 1), 1);
  assert.equal(ctx.mealStep('viele', -1), 1);
  assert.equal(ctx.mealStep(' 12 ', 1), 13);
});

test('the portions say in words whether they are for everybody', () => {
  assert.equal(ctx.mealFor('4', '4', 'für alle {n}', 'ihr seid {n}'), 'für alle 4');
  assert.equal(ctx.mealFor('6', '4', 'für alle {n}', 'ihr seid {n}'), 'ihr seid 4');
  assert.equal(ctx.mealFor('0', '4', 'for all {n}', 'you are {n}'), 'you are 4');
  assert.equal(ctx.mealFor('', '4', 'for all {n}', 'you are {n}'), 'you are 4');
  assert.equal(ctx.mealFor(' 8 ', '8', 'for all {n}', 'you are {n}'), 'for all 8');
});

test('choosing a recipe swaps in its box, choosing anything else writes its text', () => {
  assert.deepEqual(JSON.parse(JSON.stringify(ctx.mealChoice({ recipe: 'r1', text: null }))), { kind: 'recipe', id: 'r1' });
  assert.deepEqual(JSON.parse(JSON.stringify(ctx.mealChoice({ recipe: null, text: 'Papas Gemüsecurry' }))), { kind: 'text', text: 'Papas Gemüsecurry' });
  assert.deepEqual(JSON.parse(JSON.stringify(ctx.mealChoice({}))), { kind: 'text', text: '' });
});
