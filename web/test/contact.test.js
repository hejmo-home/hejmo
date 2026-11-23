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
const TEMPLATE = rowHtml('phone', '{n}', true);

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
    insertAdjacentHTML(where, h) { this.html.push(h); const n = this.items.length + 1; this.items.push(line('phone', n, h)); },
  };
  initial.forEach((remove, i) => list.items.push(line('phone', i + 1, rowHtml('phone', i + 1, remove).replace('class="line"', remove ? 'class="line"' : 'class="line blank"'))));
  const more = { hidden: false };
  const dirty = { hidden: true };
  const handlers = {};
  const tpl = { innerHTML: TEMPLATE };
  const form = {
    addEventListener(t, f) { handlers[t] = f; },
    querySelector(sel) {
      if (sel === 'ul.lines[data-lines="phone"]') return list;
      if (sel === 'template[data-line="phone"]') return tpl;
      if (sel === 'button[name="more"][value="phone"]') return more;
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
  assert.equal(t.click('more', 'phone'), true);
  assert.equal(t.list.items.length, 3);
  assert.match(t.list.html[0], /id="phone3"/);
  assert.match(t.list.html[0], /aria-label="Nummer 3 entfernen"/);
  assert.equal(t.list.items[2].children.val.attrs['aria-label'], 'Nummer 3');
  assert.equal(t.dirty.hidden, false);
});

test('the only blank line gets its "Entfernen" when a second line is added', () => {
  const t = setup([false]);
  assert.equal(t.list.items[0].classList.contains('blank'), true);
  t.click('more', 'phone');
  const first = t.list.items[0];
  assert.equal(first.classList.contains('blank'), false);
  assert.match(first.children.val.inserted[0][1], /name="remove" value="phone1"/);
});

test('no more than ten lines: the button hides at ten and nothing is added past it', () => {
  const t = setup(Array(9).fill(true));
  t.click('more', 'phone');
  assert.equal(t.list.items.length, 10);
  assert.equal(t.more.hidden, true);
  t.click('more', 'phone');
  assert.equal(t.list.items.length, 10);
});

test('"Entfernen" removes the line and renumbers the rest', () => {
  const t = setup([true, true, true]);
  assert.equal(t.click('remove', 'phone2', t.list.items[1]), true);
  assert.equal(t.list.items.length, 2);
  assert.equal(t.list.items[1].getAttribute('id'), 'phone2');
  assert.equal(t.list.items[1].children.rm.attrs.value, 'phone2');
  assert.equal(t.list.items[1].children.val.attrs['aria-label'], 'Nummer 2');
  assert.equal(t.list.items[1].children.rm.attrs['aria-label'], 'Nummer 2 entfernen');
});

test('removing the last line empties it instead of leaving no line', () => {
  const t = setup([true]);
  const li = t.list.items[0];
  t.click('remove', 'phone1', li);
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
