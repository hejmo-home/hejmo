// Tests of the recovery codes script (ticket 176) with node's test runner:
//   node --test web/test
// It writes a one-page, text-only PDF in the browser; the codes never leave the
// page. The browser and the box are replaced by fakes.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/recovery-pdf.js'), 'utf8');

const CODES = ['647RA-KG4Y6', 'WAQVP-4F8WM', 'WXR3J-AY4QQ', 'AVM4G-YMB75', '8RW63-H3GUD', 'XFHGA-2MW5H', 'NTC2D-3JA74', 'NFZKF-GAEVB', '74A4E-JGZFK', 'KQ5QE-NA9MT'];
const DOC = {
  file: 'Hejmo-Wiederherstellungscodes-hejmo.local-mama-2026-11-16', lang: 'de',
  page: 'Wiederherstellungscodes · Seite 1 von 1', title: 'Wiederherstellungscodes für Mama',
  boxLabel: 'Box', box: 'hejmo.local', userLabel: 'Benutzername', username: 'mama', dateLabel: 'Erstellt am', date: '16.11.2026',
  usedHint: 'Kästchen: zum Abhaken, wenn ein Code benutzt ist.', howTitle: 'So benutzt du sie',
  how: ['Jeder Code funktioniert genau einmal.', 'Bei der Anmeldung ersetzt ein Code den Einmalcode aus der App, wenn dein Handy verloren ist.', 'Dein Passwort brauchst du trotzdem.', 'Bewahre dieses Blatt sicher auf, wie einen Schlüssel. Wer die Codes und dein Passwort hat, kommt in dein Konto.', 'Erstellst du unter Mein Konto neue Codes, gilt dieses Blatt nicht mehr.'],
  footer: 'Hejmo bewahrt diese Codes nicht lesbar auf. Dieses Blatt ist die einzige Kopie.',
  ok: 'PDF erstellt: {file}. Du findest sie bei deinen Downloads.', failed: 'Die PDF ließ sich in diesem Browser nicht erstellen.', gateError: 'Setze das Häkchen.',
};

function load(extra = {}) {
  const ctx = { window: {}, document: { addEventListener() {}, querySelector() { return null; } }, ...extra };
  ctx.window = ctx.window || {};
  vm.runInNewContext(source, ctx);
  return ctx.window.hejmoRecoveryPdf;
}
const latin1 = (bytes) => Buffer.from(bytes).toString('latin1');

test('the PDF is one page with real text: title, who and where, the ten codes, the how-to and the footer', () => {
  const pdf = latin1(load().build(DOC, CODES));
  assert.ok(pdf.startsWith('%PDF-1.4'));
  assert.ok(pdf.trimEnd().endsWith('%%EOF'));
  assert.equal((pdf.match(/\/Type \/Page\b/g) || []).length, 1);
  assert.match(pdf, /\/MediaBox \[0 0 595\.28 841\.89\]/);
  for (const code of CODES) assert.ok(pdf.includes('(' + code + ') Tj'), code);
  for (const text of ['Hejmo', 'Wiederherstellungscodes für Mama', 'Benutzername', 'mama', 'hejmo.local', '16.11.2026', 'So benutzt du sie', 'Jeder Code funktioniert genau einmal.', DOC.footer]) {
    assert.ok(pdf.includes('(' + text + ') Tj'), text);
  }
  assert.ok(pdf.includes('/BaseFont /Courier-Bold'), 'codes are monospace');
  assert.ok(pdf.includes('/Encoding /WinAnsiEncoding'));
});

test('an empty box per code is drawn to tick off a used one', () => {
  const pdf = latin1(load().build(DOC, CODES));
  assert.equal((pdf.match(/% tick-box/g) || []).length, 10);
});

test('the cross-reference table points at every object', () => {
  const pdf = latin1(load().build(DOC, CODES));
  const start = Number(/startxref\n(\d+)\n%%EOF/.exec(pdf)[1]);
  assert.ok(pdf.startsWith('xref\n', start));
  const lines = pdf.slice(start).split('\n');
  const count = Number(/^0 (\d+)$/.exec(lines[1])[1]);
  assert.equal(count, 9);
  for (let n = 1; n < count; n++) {
    const offset = Number(/^(\d{10}) 00000 n/.exec(lines[2 + n])[1]);
    assert.ok(pdf.startsWith(n + ' 0 obj\n', offset), 'object ' + n);
  }
  const length = /\/Length (\d+)>>\nstream\n/.exec(pdf);
  const body = pdf.slice(pdf.indexOf('stream\n') + 7);
  assert.equal(body.indexOf('\nendstream'), Number(length[1]), 'stream length');
});

test('umlauts are written in the font encoding; brackets and backslashes are escaped; unknown letters become a question mark', () => {
  const pdf = latin1(load().build({ ...DOC, title: 'Für (Mama) \\ 漢', username: 'a' }, CODES));
  assert.ok(pdf.includes('(Für \\(Mama\\) \\\\ ?) Tj'));
});

test('long names wrap inside the page and never run off it', () => {
  const api = load();
  const long = 'familie-wittelsbacher-oberhuber.example.org-und-noch-mehr-' + 'x'.repeat(80);
  for (const [font, size] of [['F1', 12], ['F2', 22.5]]) {
    const lines = api.wrap(long + ' ein paar Worte dahinter', font, size, 300);
    assert.ok(lines.length > 1);
    assert.equal(lines.join('').replace(/ /g, ''), (long + ' ein paar Worte dahinter').replace(/ /g, ''));
    for (const l of lines) assert.ok(api.width(l, font, size) <= 300, l);
  }
  const pdf = latin1(api.build({ ...DOC, box: long, username: long, title: 'Wiederherstellungscodes für ' + long }, CODES));
  assert.ok((pdf.match(/ Tj ET/g) || []).length > 30);
});

// ---- the page --------------------------------------------------------------

function element(tag, attrs = {}) {
  const el = {
    tag, attrs: { ...attrs }, children: [], listeners: {}, innerHTML: '', textContent: '', hidden: false, checked: false, className: '', style: {},
    getAttribute(a) { return a in el.attrs ? el.attrs[a] : null; },
    setAttribute(a, v) { el.attrs[a] = String(v); },
    removeAttribute(a) { delete el.attrs[a]; },
    addEventListener(ev, fn) { (el.listeners[ev] = el.listeners[ev] || []).push(fn); },
    fire(ev, e = {}) { const evt = { preventDefault() { evt.prevented = true; }, target: el, ...e }; (el.listeners[ev] || []).forEach((fn) => fn(evt)); return evt; },
    querySelector(sel) { return el.hooks && el.hooks[sel] || null; },
    querySelectorAll(sel) { return el.lists && el.lists[sel] || []; },
    insertBefore(node, ref) { const i = el.children.indexOf(ref); el.children.splice(i < 0 ? el.children.length : i, 0, node); node.parent = el; },
    appendChild(node) { el.children.push(node); node.parent = el; },
    removeChild(node) { el.children = el.children.filter((c) => c !== node); },
    remove() { if (el.parent) el.parent.children = el.parent.children.filter((c) => c !== el); },
    focus() { el.focused = true; },
    click() { el.clicked = true; },
  };
  return el;
}

function page({ blob = true } = {}) {
  const form = element('form');
  const save = element('div'); const gate = element('div');
  const pdfButton = element('button'); const box = element('input'); const submit = element('button', { 'aria-disabled': 'true' });
  const lock = element('p'); const actions = element('div'); const label = element('label');
  const docJson = element('script'); docJson.textContent = JSON.stringify(DOC);
  const codeEls = CODES.map((c) => element('code', { 'data-recovery-code': c }));
  form.hooks = { '.rc-save': save, '.rc-gate': gate, '[data-rc-pdf]': pdfButton, 'input[name="saved"]': box, 'button[type="submit"]': submit, '[data-rc-lock]': lock, '.rc-gate label.confirm': label };
  form.lists = { '[data-recovery-code]': codeEls };
  save.hooks = { '.actions': actions }; gate.hooks = {}; save.children = [actions]; gate.children = [label, submit];
  const created = [];
  const document = {
    querySelector(sel) { return sel === 'form[data-rc]' ? form : sel === '[data-rc-doc]' ? docJson : null; },
    createElement(tag) { const e = element(tag); created.push(e); return e; },
    body: element('body'),
    addEventListener() {},
  };
  const urls = [];
  const ctx = { window: {}, document, setTimeout(fn) { fn(); }, URL: { createObjectURL(b) { urls.push(b); return 'blob:x'; }, revokeObjectURL() {} } };
  if (blob) ctx.Blob = function (parts, opts) { this.parts = parts; this.type = opts.type; };
  vm.runInNewContext(source, ctx);
  return { form, save, gate, pdfButton, box, submit, lock, created, urls, label };
}

test('"Download as PDF" saves the file under its name and says so at the buttons, without ticking the box', () => {
  const p = page();
  p.pdfButton.fire('click');
  const a = p.created.find((e) => e.tag === 'a');
  assert.equal(a.attrs.download, DOC.file + '.pdf');
  assert.equal(a.clicked, true);
  assert.equal(p.urls[0].type, 'application/pdf');
  const msg = p.save.children.find((c) => c.attrs['data-rc-msg']);
  assert.match(msg.className, /\bmsg ok\b/);
  assert.ok(msg.innerHTML.includes('PDF erstellt: <b class="fname">' + DOC.file + '.pdf</b>. Du findest sie bei deinen Downloads.'));
  assert.equal(p.box.checked, false);
  assert.equal(p.submit.attrs['aria-disabled'], 'true');
});

test('when the browser cannot make the PDF the error names the other ways and the gate is unchanged', () => {
  const p = page({ blob: false });
  p.pdfButton.fire('click');
  const msg = p.save.children.find((c) => c.attrs['data-rc-msg']);
  assert.match(msg.className, /\bferr\b/);
  assert.equal(msg.attrs.role, 'alert');
  assert.ok(msg.innerHTML.includes(DOC.failed));
  assert.equal(p.submit.attrs['aria-disabled'], 'true');
});

test('a second download replaces the first message instead of stacking them', () => {
  const p = page();
  p.pdfButton.fire('click'); p.pdfButton.fire('click');
  assert.equal(p.save.children.filter((c) => c.attrs['data-rc-msg']).length, 1);
});

test('continuing without the tick explains at the checkbox and moves focus there', () => {
  const p = page();
  const form = p.form.fire('submit');
  assert.equal(form.prevented, true);
  assert.equal(p.box.attrs['aria-invalid'], 'true');
  assert.equal(p.box.focused, true);
  const err = p.gate.children.find((c) => c.attrs.id === 'err-saved');
  assert.equal(err.attrs.role, 'alert');
  assert.ok(err.innerHTML.includes(DOC.gateError));
  p.form.fire('submit');
  assert.equal(p.gate.children.filter((c) => c.attrs.id === 'err-saved').length, 1);
});

test('the tick makes the button available, clears the error and lets the form go', () => {
  const p = page();
  p.form.fire('submit');
  p.box.checked = true;
  p.box.fire('change');
  assert.equal('aria-disabled' in p.submit.attrs, false);
  assert.equal(p.lock.hidden, true);
  assert.equal(p.box.getAttribute('aria-invalid'), null);
  assert.equal(p.gate.children.some((c) => c.attrs.id === 'err-saved'), false);
  assert.equal(p.form.fire('submit').prevented, undefined);
  p.box.checked = false;
  p.box.fire('change');
  assert.equal(p.submit.attrs['aria-disabled'], 'true');
  assert.equal(p.lock.hidden, false);
});
