// Tests of the recipe form's script (ticket 279) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/recipe.js'), 'utf8');

// The script runs on any page; without the form it does nothing.
const ctx = vm.createContext({ document: { querySelector: () => null } });
vm.runInContext(source, ctx);

test('a line to add gets its number where the template says {n}', () => {
  assert.equal(ctx.rcpFill('<li id="ing{n}"><input aria-label="Menge {n}"></li>', 4), '<li id="ing4"><input aria-label="Menge 4"></li>');
  assert.equal(ctx.rcpFill('no number', 4), 'no number');
});

test('suggestions are asked for from two letters on, with the text trimmed and encoded', () => {
  assert.equal(ctx.rcpSuggestUrl('/meals/recipes/suggest', 'E'), '');
  assert.equal(ctx.rcpSuggestUrl('/meals/recipes/suggest', '  '), '');
  assert.equal(ctx.rcpSuggestUrl('/meals/recipes/suggest', 'Er'), '/meals/recipes/suggest?q=Er');
  assert.equal(ctx.rcpSuggestUrl('/meals/recipes/suggest', '  Mehl   Type 405 '), '/meals/recipes/suggest?q=Mehl%20Type%20405');
  assert.equal(ctx.rcpSuggestUrl('/meals/recipes/suggest', 'Äpfel & Birnen'), '/meals/recipes/suggest?q=%C3%84pfel%20%26%20Birnen');
  assert.equal(ctx.rcpSuggestUrl('/x', '\u{1F34E}\u{1F34F}'), '/x?q=%F0%9F%8D%8E%F0%9F%8D%8F', 'two emoji are two letters');
});

test('a chosen suggestion writes the name and, after a middle dot, the variety', () => {
  assert.equal(ctx.rcpOptionText('Erdnussbutter', ''), 'Erdnussbutter');
  assert.equal(ctx.rcpOptionText('Erdnussbutter', 'cremig'), 'Erdnussbutter · cremig');
  assert.equal(ctx.rcpOptionText(' Mehl ', ' Type 405 '), 'Mehl · Type 405');
  assert.equal(ctx.rcpOptionText(null, null), '');
});

test('the portions stepper stays between 1 and 99 and starts from a sensible number', () => {
  assert.equal(ctx.rcpStep('4', 1), 5);
  assert.equal(ctx.rcpStep('4', -1), 3);
  assert.equal(ctx.rcpStep('1', -1), 1);
  assert.equal(ctx.rcpStep('99', 1), 99);
  assert.equal(ctx.rcpStep('', 1), 1);
  assert.equal(ctx.rcpStep('viele', -1), 1);
  assert.equal(ctx.rcpStep(' 12 ', 1), 13);
});

test('the counter of the tips counts letters, not bytes', () => {
  assert.equal(ctx.rcpCounter('%d von %d Zeichen', '', 2000), '0 von 2000 Zeichen');
  assert.equal(ctx.rcpCounter('%d von %d Zeichen', 'Süß \u{1F35B}', 2000), '5 von 2000 Zeichen');
  assert.equal(ctx.rcpCounter('%d of %d characters', 'abc', 10), '3 of 10 characters');
});

test('the first number of a label follows the line when lines move', () => {
  assert.equal(ctx.rcpLabel('Zutat 3 entfernen: Salz', 2), 'Zutat 2 entfernen: Salz');
  assert.equal(ctx.rcpLabel('Schritt 10 nach oben', 9), 'Schritt 9 nach oben');
  assert.equal(ctx.rcpLabel('Menge 1', 12), 'Menge 12');
});

test('after a line is removed the focus goes to the next, or the one before the end', () => {
  assert.equal(ctx.rcpFocusAfter(3, 0), 0);
  assert.equal(ctx.rcpFocusAfter(3, 2), 2);
  assert.equal(ctx.rcpFocusAfter(3, 3), 2);
  assert.equal(ctx.rcpFocusAfter(1, 0), 0);
});
