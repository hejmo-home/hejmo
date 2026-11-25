// The live recount of the tick list (ticket 283): the count sentence and the
// button follow the ticks. The server words the same sentences without script.
const test = require('node:test');
const assert = require('node:assert/strict');
const s = require('../static/meal-shop.js');

const patterns = {
  'head.many': '{n} Zutaten kommen auf „Einkauf“:', 'head.one': '1 Zutat kommt auf „Einkauf“:', 'head.nothing': 'Nichts Neues für „Einkauf“:', 'head.none': 'Kein Haken gesetzt:',
  'button.many': '{n} Zutaten auf „Einkauf“ setzen', 'button.one': '1 Zutat auf „Einkauf“ setzen', 'button.none': 'Auf „Einkauf“ setzen',
  count: '{new} neu, {on} schon auf der Liste.', 'pantry.one': '1 Vorratsartikel lässt Hejmo weg.', 'pantry.many': '{n} Vorratsartikel lässt Hejmo weg.',
};

test('the sentence counts the ticked rows, the entries that stay and the pantry articles left out', () => {
  const r = s.sentence(patterns, { ticked: 8, tickedNew: 6, tickedOn: 2, same: 0, pantryLeft: 3, left: '' });
  assert.equal(r.head, '8 Zutaten kommen auf „Einkauf“:');
  assert.equal(r.text, '6 neu, 2 schon auf der Liste. 3 Vorratsartikel lässt Hejmo weg.');
  assert.equal(r.button, '8 Zutaten auf „Einkauf“ setzen');
});

test('one ingredient, nothing new and no tick have their own words', () => {
  assert.equal(s.sentence(patterns, { ticked: 1, tickedNew: 1, tickedOn: 0, same: 0, pantryLeft: 1, left: '' }).head, '1 Zutat kommt auf „Einkauf“:');
  assert.equal(s.sentence(patterns, { ticked: 1, tickedNew: 0, tickedOn: 1, same: 2, pantryLeft: 0, left: '' }).head, 'Nichts Neues für „Einkauf“:');
  assert.equal(s.sentence(patterns, { ticked: 1, tickedNew: 0, tickedOn: 1, same: 2, pantryLeft: 0, left: '' }).text, '0 neu, 3 schon auf der Liste.');
  const none = s.sentence(patterns, { ticked: 0, tickedNew: 0, tickedOn: 0, same: 0, pantryLeft: 2, left: '' });
  assert.equal(none.head, 'Kein Haken gesetzt:');
  assert.equal(none.button, 'Auf „Einkauf“ setzen');
});

test('meals that stay off are named in the sentence', () => {
  const r = s.sentence(patterns, { ticked: 2, tickedNew: 2, tickedOn: 0, same: 0, pantryLeft: 0, left: 'Kuchen bleibt weg.' });
  assert.equal(r.text, '2 neu, 0 schon auf der Liste. Kuchen bleibt weg.');
});
