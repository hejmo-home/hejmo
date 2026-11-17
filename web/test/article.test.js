// Tests of the own article form's script (ticket 164):
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/article.js'), 'utf8');

class El {
  constructor(tag) { this.tagName = tag; this.children = []; this.attrs = {}; this.listeners = {}; this.hidden = false; this.className = ''; this.textContent = ''; this.parentNode = null; }
  get firstChild() { return this.children[0] || null; }
  appendChild(c) { c.parentNode = this; this.children.push(c); return c; }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); }
  setAttribute(k, v) { this.attrs[k] = v; }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  addEventListener(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); }
  fire(ev, e) { (this.listeners[ev] || []).forEach((fn) => fn(e)); }
  click() { this.clicked = true; }
  all() { return [this, ...this.children.flatMap((c) => c.all())]; }
}

function setup(answer) {
  const field = new El('textarea'); field.value = '';
  const box = new El('div'); box.hidden = true;
  box.attrs = { 'data-one': 'Ein Artikel heißt ähnlich.', 'data-many': '{n} Artikel heißen ähnlich.', 'data-more': 'Nicht dabei? Schreib einfach weiter.' };
  const root = new El('div'); root.attrs = { 'data-article-suggest': '/s', 'data-self': 'own-1' };
  root.querySelector = (sel) => (sel === 'textarea[name=name]' ? field : sel === '[data-suggest-box]' ? box : null);
  const doc = { createElement: (t) => new El(t), querySelector: () => root };
  const calls = [];
  const fetchFn = (url) => { calls.push(url); return Promise.resolve({ ok: true, json: () => Promise.resolve(answer) }); };
  const timers = [];
  const setTimer = (fn) => { timers.push(fn); return timers.length; };
  const clearTimer = (id) => { timers[id - 1] = null; };
  vm.runInNewContext(source, { document: undefined, setTimeout, clearTimeout });
  // The script runs its parts only in a page; load the helpers through a page-like context.
  const ctx = { document: doc, fetch: fetchFn, setTimeout: setTimer, clearTimeout: clearTimer };
  vm.runInNewContext(source, ctx);
  return { field, box, calls, timers };
}

const item = (name, href) => ({ Href: href, Name: name, Sub: 'von Hejmo · Getränke', Symbol: '🥣', Picture: '' });

test('similar articles appear under the name after a short pause', async () => {
  const { field, box, calls, timers } = setup({ items: [item('Haferflocken', '/settings/lists/library/oats'), item('Haferkekse', '/settings/lists/library/oat-biscuits')] });
  field.value = 'Hafer';
  field.fire('input');
  assert.equal(calls.length, 0, 'it waits for the typing to pause');
  timers.filter(Boolean)[0]();
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(calls, ['/s?q=Hafer']);
  assert.equal(box.hidden, false);
  const all = box.all();
  assert.equal(all.find((e) => e.className === 'place-q').textContent, '2 Artikel heißen ähnlich.');
  assert.equal(all.filter((e) => e.tagName === 'li').length, 2);
  assert.equal(all.find((e) => e.tagName === 'b').textContent, 'Haferflocken');
});

test('one similar article is said in the singular and the article itself is left out', async () => {
  const { field, box, timers } = setup({ items: [item('Hafer-Drink', '/settings/lists/library/own-1'), item('Haferflocken', '/settings/lists/library/oats')] });
  field.value = 'Hafer';
  field.fire('input');
  timers.filter(Boolean)[0]();
  await new Promise((r) => setImmediate(r));
  assert.equal(box.all().find((e) => e.className === 'place-q').textContent, 'Ein Artikel heißt ähnlich.');
  assert.equal(box.all().filter((e) => e.tagName === 'li').length, 1);
});

test('a name of one letter suggests nothing and hides the box', async () => {
  const { field, box, calls } = setup({ items: [item('Haferflocken', '/x/oats')] });
  field.value = 'H';
  field.fire('input');
  assert.equal(calls.length, 0);
  assert.equal(box.hidden, true);
});

test('names and texts are put in as text, never as markup', async () => {
  const { field, box, timers } = setup({ items: [{ Href: '/x/a', Name: '<img src=x onerror=1>', Sub: '<b>x</b>', Symbol: '', Picture: '' }] });
  field.value = 'ab';
  field.fire('input');
  timers.filter(Boolean)[0]();
  await new Promise((r) => setImmediate(r));
  assert.equal(box.all().find((e) => e.tagName === 'b').textContent, '<img src=x onerror=1>');
  assert.ok(!box.all().some((e) => e.tagName === 'img' && e.attrs.onerror), 'no element was made from the text');
});
