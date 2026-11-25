// Tests of the script that adds photos to a recipe (ticket 280) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const sandbox = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/photos.js'), 'utf8'), sandbox);
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/recipe-photos.js'), 'utf8'), sandbox);
const { prepare, fill } = sandbox.HejmoRecipePhotos;
const P = sandbox.HejmoPhotos;

const ftyp = (brand) => Uint8Array.from([0, 0, 0, 24, 0x66, 0x74, 0x79, 0x70, ...Buffer.from(brand), 0, 0, 0, 0]);
const JPEG = Uint8Array.from([0xff, 0xd8, 0xff, 0xe0]);

test('a HEIC photo is converted, the others are sent as they are, in the order chosen', async () => {
  const files = [{ name: 'a.jpg', head: JPEG }, { name: 'IMG_2231.HEIC', head: ftyp('heic') }, { name: 'list.pdf', head: Buffer.from('%PDF-1.4') }];
  const steps = [];
  const out = await prepare(files, {
    head: async (f) => f.head, sniff: P.sniff, jpegName: P.jpegName,
    convert: async (f) => ({ converted: f.name }),
  }, (done, total, f, phase) => steps.push(`${done}/${total} ${f.name} ${phase}`));
  assert.deepEqual(Array.from(out, (o) => o.name), ['a.jpg', 'IMG_2231.jpg', 'list.pdf']);
  assert.equal(out[1].blob.converted, 'IMG_2231.HEIC');
  assert.equal(out[0].blob, files[0], 'a JPEG is not touched');
  assert.ok(steps.includes('1/3 IMG_2231.HEIC convert'));
});

test('a HEIC photo the browser cannot convert is sent as it is, so the box can say why it refuses it', async () => {
  const f = { name: 'x.heic', head: ftyp('heic') };
  const out = await prepare([f], { head: async (x) => x.head, sniff: P.sniff, jpegName: P.jpegName, convert: async () => { throw new Error('no decoder'); } }, () => {});
  assert.equal(out.length, 1);
  assert.equal(out[0].blob, f);
  assert.equal(out[0].name, 'x.heic');
});

test('texts are filled by name', () => {
  assert.equal(fill('{done} von {total} Fotos fertig', { done: 1, total: 3 }), '1 von 3 Fotos fertig');
});

// A small document: enough tree for wire() of the script, with the form, its buttons and the file inputs in it.
function fakePage(texts) {
  class El {
    constructor(tag) { this.tag = tag; this.kids = []; this.parent = null; this.attrs = {}; this.style = {}; this.listeners = {}; this.className = ''; this.text = ''; }
    get parentNode() { return this.parent; }
    get nextSibling() { const i = this.parent.kids.indexOf(this); return this.parent.kids[i + 1] || null; }
    appendChild(c) { c.parent = this; this.kids.push(c); return c; }
    insertBefore(n, ref) { n.parent = this; const i = ref ? this.kids.indexOf(ref) : -1; this.kids.splice(i < 0 ? this.kids.length : i, 0, n); return n; }
    set textContent(v) { if (v === '') { this.kids.forEach((k) => { k.parent = null; }); this.kids = []; } else this.text = v; }
    get textContent() { return this.text + this.kids.map((k) => k.textContent).join(''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    addEventListener(ev, fn) { this.listeners[ev] = fn; }
    contains(x) { for (let n = x; n; n = n.parent) if (n === this) return true; return false; }
    closest(sel) { return sel === '[data-rph-upload]' ? section : null; }
    querySelector(sel) { return sel === '.ph-pick' ? pick : null; }
    submit() { this.submitted = Array.from(inputs).filter((i) => form.contains(i) && i.files.length).map((i) => i.files.length); }
  }
  const section = new El('section'); section.attrs['data-texts'] = JSON.stringify(texts);
  const form = new El('form'); section.appendChild(form);
  const pick = new El('div'); form.appendChild(pick);
  const label = new El('label'); pick.appendChild(label);
  const input = new El('input'); input.type = 'file'; label.appendChild(input);
  const inputs = [input];
  const doc = {
    querySelectorAll: (sel) => (sel === 'form[data-rph]' ? [form] : []),
    createElement: (t) => new El(t), createElementNS: (ns, t) => new El(t), createTextNode: (s) => { const e = new El('#text'); e.text = s; return e; },
    head: new El('head'),
  };
  return { doc, form, pick, input };
}

test('choosing photos on the page "Fotos" keeps the file inputs in the form, so the form still sends the files', async () => {
  const page = fakePage({ uploading: 'Fotos werden gespeichert', progress: '{done} von {total}', send: '{name}', convert: '{name}' });
  class DT { constructor() { this.items = { add: (f) => { this.files.push(f); } }; this.files = []; } }
  const sb = { document: page.doc, DataTransfer: DT, File: class { constructor(parts, name) { this.name = name; } } };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/photos.js'), 'utf8'), sb);
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/recipe-photos.js'), 'utf8'), sb);
  const file = { name: 'a.jpg', type: 'image/jpeg', slice: () => ({ arrayBuffer: async () => JPEG.buffer.slice(0) }) };
  page.input.files = [file];
  await page.form.listeners.change({ target: page.input });
  assert.ok(page.form.contains(page.input), 'the file input is still in the form');
  assert.deepEqual(page.form.submitted, [1], 'the form is sent with the chosen photo');
});
