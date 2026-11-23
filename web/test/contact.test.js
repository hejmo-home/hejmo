// Tests of the contact form's lines (ticket 296) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/contact.js'), 'utf8');

// A line as the server renders it: id, a select, the value input, "Entfernen" (not on a blank single line).
function rowHtml(kind, n, remove) {
  return '<li class="line" id="' + kind + n + '"><select aria-label="Art ' + n + '"></select><input class="val" aria-label="Nummer ' + n + '">' +
    (remove ? '<button class="button secondary sq rm" type="submit" name="remove" value="' + kind + n + '" aria-label="Nummer ' + n + ' entfernen">x</button>' : '') + '</li>';
}
const TEMPLATE = rowHtml('number', '{n}', true);

// A small document: only what the script touches.
function setup(initial) {
  const attrs = (el) => ({ get: (k) => (k in el.attrs ? el.attrs[k] : null), set: (k, v) => { el.attrs[k] = v; } });
  function line(kind, n, html) {
    const el = {
      attrs: { id: kind + n }, removed: false, classes: new Set(html.includes('blank') ? ['line', 'blank'] : ['line']),
      children: { rm: html.includes('class="button secondary sq rm"') ? { attrs: { value: kind + n, 'aria-label': 'Nummer ' + n + ' entfernen' } } : null,
        select: { attrs: { 'aria-label': 'Art ' + n }, selectedIndex: 2 }, val: { attrs: { 'aria-label': 'Nummer ' + n }, value: '0171 1', inserted: [], insertAdjacentHTML(w, h) { this.inserted.push([w, h]); }, focus() { this.focused = true; } } },
      getAttribute(k) { return attrs(this).get(k); }, setAttribute(k, v) { attrs(this).set(k, v); },
      classList: { contains: (c) => el.classes.has(c), remove: (c) => el.classes.delete(c) },
      querySelector(sel) { return sel === 'button.rm' ? this.children.rm : sel === 'select' ? this.children.select : sel === 'input.val' ? this.children.val : null; },
      querySelectorAll(sel) {
        if (sel === '[aria-label]') return [this.children.select, this.children.val, this.children.rm].filter(Boolean).map((c) => Object.assign(c, { getAttribute: (k) => c.attrs[k], setAttribute: (k, v) => { c.attrs[k] = v; } }));
        if (sel === 'input') return [this.children.val];
        return [];
      },
      remove() { this.removed = true; list.items = list.items.filter((x) => x !== this); },
    };
    Object.assign(el.children.val, { getAttribute: (k) => el.children.val.attrs[k] });
    return el;
  }
  const list = {
    items: [], html: [],
    querySelectorAll() { return this.items; },
    insertAdjacentHTML(where, h) { this.html.push(h); const n = this.items.length + 1; this.items.push(line('number', n, h)); },
  };
  initial.forEach((remove, i) => list.items.push(line('number', i + 1, rowHtml('number', i + 1, remove).replace('class="line"', remove ? 'class="line"' : 'class="line blank"'))));
  const more = { hidden: false };
  const dirty = { hidden: true };
  const handlers = {};
  const tpl = { innerHTML: TEMPLATE };
  const form = {
    addEventListener(t, f) { handlers[t] = f; },
    querySelector(sel) {
      if (sel === 'ul.lines[data-lines="number"]') return list;
      if (sel === 'template[data-line="number"]') return tpl;
      if (sel === 'button[name="more"][value="number"]') return more;
      if (sel === '.savebar .dirty') return dirty;
      return null;
    },
  };
  const document = { querySelector: (sel) => (sel === 'form.ct-form' ? form : null) };
  vm.runInNewContext(source, { document });
  const click = (name, value, li) => {
    const button = { getAttribute: (k) => (k === 'name' ? name : value), closest: (sel) => (sel === 'li.line' ? li : null) };
    let prevented = false;
    handlers.click({ target: { closest: () => button }, preventDefault() { prevented = true; } });
    return prevented;
  };
  return { list, more, dirty, click, handlers };
}

test('"Weitere Nummer" adds a numbered line from the template and does not submit', () => {
  const t = setup([true, true]);
  assert.equal(t.click('more', 'number'), true);
  assert.equal(t.list.items.length, 3);
  assert.match(t.list.html[0], /id="number3"/);
  assert.match(t.list.html[0], /aria-label="Nummer 3 entfernen"/);
  assert.equal(t.list.items[2].children.val.attrs['aria-label'], 'Nummer 3');
  assert.equal(t.dirty.hidden, false);
});

test('the only blank line gets its "Entfernen" when a second line is added', () => {
  const t = setup([false]);
  assert.equal(t.list.items[0].classList.contains('blank'), true);
  t.click('more', 'number');
  const first = t.list.items[0];
  assert.equal(first.classList.contains('blank'), false);
  assert.match(first.children.val.inserted[0][1], /name="remove" value="number1"/);
});

test('no more than ten lines: the button hides at ten and nothing is added past it', () => {
  const t = setup(Array(9).fill(true));
  t.click('more', 'number');
  assert.equal(t.list.items.length, 10);
  assert.equal(t.more.hidden, true);
  t.click('more', 'number');
  assert.equal(t.list.items.length, 10);
});

test('"Entfernen" removes the line and renumbers the rest', () => {
  const t = setup([true, true, true]);
  assert.equal(t.click('remove', 'number2', t.list.items[1]), true);
  assert.equal(t.list.items.length, 2);
  assert.equal(t.list.items[1].getAttribute('id'), 'number2');
  assert.equal(t.list.items[1].children.rm.attrs.value, 'number2');
  assert.equal(t.list.items[1].children.val.attrs['aria-label'], 'Nummer 2');
  assert.equal(t.list.items[1].children.rm.attrs['aria-label'], 'Nummer 2 entfernen');
});

test('removing the last line empties it instead of leaving no line', () => {
  const t = setup([true]);
  const li = t.list.items[0];
  t.click('remove', 'number1', li);
  assert.equal(t.list.items.length, 1);
  assert.equal(li.children.val.value, '');
  assert.equal(li.children.select.selectedIndex, 0);
});

test('typing marks the form as not saved yet', () => {
  const t = setup([false]);
  assert.equal(t.dirty.hidden, true);
  t.handlers.input({});
  assert.equal(t.dirty.hidden, false);
});

// ---- Relations (ticket 298): the sentence under each line follows the word and the chips ----

const DE = { of: 'von', whole: 'der ganzen Familie', and: 'und' };
const EN = { of: 'of', whole: 'of the whole family', and: 'and' };

test('relSentence writes every name out, in German and in English, and one for the whole family', () => {
  const sandbox = { document: { querySelector: () => null }, module: { exports: {} } };
  vm.runInNewContext(source, sandbox);
  const { relSentence } = sandbox.module.exports;
  assert.equal(relSentence('Oma', ['Mia'], false, DE), 'Oma von Mia');
  assert.equal(relSentence('Oma', ['Mia', 'Ben'], false, DE), 'Oma von Mia und Ben');
  assert.equal(relSentence('Oma', ['Sebastian', 'Mia', 'Charlotte-Sophie', 'Jonas', 'Lotta', 'Ben'], false, DE), 'Oma von Sebastian, Mia, Charlotte-Sophie, Jonas, Lotta und Ben');
  assert.equal(relSentence('Grandma', ['Mia', 'Ben', 'Lotta'], false, EN), 'Grandma of Mia, Ben and Lotta');
  assert.equal(relSentence(' Nachbarin ', [], true, DE), 'Nachbarin der ganzen Familie');
  assert.equal(relSentence('Neighbour', [], true, EN), 'Neighbour of the whole family');
  assert.equal(relSentence('', ['Mia'], false, DE), '', 'no word, no sentence');
  assert.equal(relSentence('Oma', [], false, DE), '', 'nobody and not the whole family: nothing to say');
});

// A relation line with a word, the chip "Ganze Familie" and one chip per person.
function relSetup(names) {
  const say = { hidden: true, b: { textContent: '' }, querySelector(sel) { return sel === 'b' ? this.b : null; } };
  const word = { value: '', hasAttribute: () => false };
  const whole = { checked: true, hasAttribute: (a) => a === 'data-whole' };
  const chips = names.map((n) => ({ checked: false, hasAttribute: () => false, closest: () => ({ textContent: '\n ' + n + ' ' }) }));
  const li = {
    removed: false,
    classes: new Set(['rel-line']),
    classList: { contains: (c) => li.classes.has(c), remove: (c) => li.classes.delete(c) },
    querySelector(sel) { return sel === '.rel-say' ? say : sel === 'input[name$="_word"]' ? word : sel === 'input[data-whole]' ? whole : null; },
    querySelectorAll(sel) { return sel === '.pchip:not(.all) input' ? chips : []; },
    remove() { li.removed = true; ul.items = ul.items.filter((x) => x !== li); },
  };
  word.closest = () => li; whole.closest = () => li; chips.forEach((c) => { c.lineOf = li; });
  const ul = { items: [li], querySelectorAll: () => ul.items };
  const handlers = {};
  const card = { getAttribute: (k) => ({ 'data-of': 'von', 'data-whole': 'der ganzen Familie', 'data-and': 'und' })[k] || null };
  const dirty = { hidden: true };
  const form = {
    addEventListener(t, f) { (handlers[t] = handlers[t] || []).push(f); },
    querySelector(sel) { return sel === 'fieldset#rels' ? card : sel === 'ul.rel-lines' ? ul : sel === '.savebar .dirty' ? dirty : null; },
  };
  vm.runInNewContext(source, { document: { querySelector: (sel) => (sel === 'form.ct-form' ? form : null) } });
  const fire = (type, target) => (handlers[type] || []).forEach((h) => h({ target }));
  // The target knows its line: closest('li.rel-line') is the line.
  [word, whole, ...chips].forEach((el) => { el.closest = (sel) => (sel === 'li.rel-line' ? li : el.label || li); });
  chips.forEach((c, i) => { c.label = { textContent: '\n ' + names[i] + ' ' }; c.closest = (sel) => (sel === 'li.rel-line' ? li : c.label); });
  return { li, say, word, whole, chips, fire, dirty, ul };
}

test('the sentence under a line follows the word and the chips while you type', () => {
  const t = relSetup(['Mama', 'Papa', 'Sebastian']);
  assert.equal(t.say.hidden, true);
  t.word.value = 'Oma';
  t.fire('input', t.word);
  assert.equal(t.say.b.textContent, 'Oma der ganzen Familie', 'whole family is chosen from the start');
  assert.equal(t.say.hidden, false);
  t.chips[0].checked = true; t.chips[2].checked = true;
  t.fire('change', t.chips[2]);
  assert.equal(t.say.b.textContent, 'Oma von Mama und Sebastian');
  t.chips[0].checked = false; t.chips[2].checked = false;
  t.whole.checked = true;
  t.fire('change', t.whole);
  assert.equal(t.say.b.textContent, 'Oma der ganzen Familie');
  t.word.value = '  ';
  t.fire('input', t.word);
  assert.equal(t.say.b.textContent, '');
  assert.equal(t.say.hidden, true, 'no word: nothing is said');
  assert.equal(t.dirty.hidden, false);
});

test('choosing "Ganze Familie" while people are still ticked says the whole family', () => {
  const t = relSetup(['Mama', 'Papa']);
  t.word.value = 'Nachbarin';
  t.chips[1].checked = true; // form.js clears it only after this handler
  t.whole.checked = true;
  t.fire('change', t.whole);
  assert.equal(t.say.b.textContent, 'Nachbarin der ganzen Familie');
});

test('submitting leaves out a blank line for the whole family and keeps every other line', () => {
  const t = relSetup(['Mama']);
  t.fire('submit', null);
  assert.equal(t.li.removed, true, 'no word and the whole family');
  const u = relSetup(['Mama']);
  u.word.value = 'Oma';
  u.fire('submit', null);
  assert.equal(u.li.removed, false);
  const v = relSetup(['Mama']);
  v.whole.checked = false; v.chips[0].checked = true; // people but no word: the server tells the parent
  v.fire('submit', null);
  assert.equal(v.li.removed, false);
});
