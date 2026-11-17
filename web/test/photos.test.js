// Tests of the photo upload script (ticket 178) with node's test runner:
//   node --test web/test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/photos.js'), 'utf8');
const sandbox = {};
vm.runInNewContext(source, sandbox);
const { sniff, jpegName, fill, upload } = sandbox.HejmoPhotos;

const bytes = (...b) => Uint8Array.from(b);
const ftyp = (brand) => Uint8Array.from([0, 0, 0, 24, 0x66, 0x74, 0x79, 0x70, ...Buffer.from(brand), 0, 0, 0, 0]);

test('files are told apart by their first bytes, not their name', () => {
  assert.equal(sniff(bytes(0xff, 0xd8, 0xff, 0xe0)), 'jpeg');
  assert.equal(sniff(bytes(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)), 'png');
  assert.equal(sniff(ftyp('heic')), 'heic');
  assert.equal(sniff(ftyp('mif1')), 'heic');
  assert.equal(sniff(Buffer.from('%PDF-1.7 ......')), '');
  assert.equal(sniff(ftyp('mp42')), '', 'a video is no photo');
  assert.equal(sniff(bytes()), '');
});

test('a converted photo gets a .jpg name', () => {
  assert.equal(jpegName('IMG_4810.HEIC'), 'IMG_4810.jpg');
  assert.equal(jpegName('no extension'), 'no extension.jpg');
  assert.equal(jpegName('a.b.heif'), 'a.b.jpg');
});

test('texts are filled by name', () => {
  assert.equal(fill('{done} von {total} fertig', { done: 4, total: 12 }), '4 von 12 fertig');
});

function file(name, head) { return { name, head }; }

function deps(sent, answers = {}) {
  return {
    head: async (f) => f.head,
    convert: async (f) => { if (answers.convertFails) throw new Error('no'); return { converted: f.name }; },
    send: async (blob, name, progress) => { progress(50); sent.push(name); return { status: name in answers ? answers[name] : 201 }; },
  };
}

test('every file is waiting, converted when it is HEIC, uploaded and done', async () => {
  const sent = [];
  const states = [];
  const ctl = upload([file('a.jpg', bytes(0xff, 0xd8, 0xff)), file('b.HEIC', ftyp('heic'))], deps(sent), (items) => states.push(items.map((i) => i.state).join()));
  const items = await ctl.done;
  assert.deepEqual(sent, ['a.jpg', 'b.jpg'], 'the converted photo goes as .jpg');
  assert.deepEqual(items.map((i) => i.state), ['done', 'done']);
  assert.ok(states.some((s) => s === 'done,convert'), 'the conversion is visible: ' + states.join(' | '));
  assert.equal(states[0], 'wait,wait');
});

test('a file that is no photo fails with its reason and is not sent', async () => {
  const sent = [];
  const items = await upload([file('Reiseplan.pdf', Buffer.from('%PDF-1.7')), file('a.jpg', bytes(0xff, 0xd8, 0xff))], deps(sent), () => {}).done;
  assert.deepEqual(items.map((i) => [i.state, i.reason]), [['fail', 'not_photo'], ['done', '']]);
  assert.deepEqual(sent, ['a.jpg']);
});

test('a HEIC photo that cannot be converted fails and the next one goes on', async () => {
  const sent = [];
  const items = await upload([file('a.heic', ftyp('heic')), file('b.jpg', bytes(0xff, 0xd8, 0xff))], deps(sent, { convertFails: true }), () => {}).done;
  assert.deepEqual(items.map((i) => [i.state, i.reason]), [['fail', 'heic'], ['done', '']]);
});

test('a full box ends the sending and refuses the rest at once with the same reason', async () => {
  const sent = [];
  const jpg = (n) => file(n, bytes(0xff, 0xd8, 0xff));
  const items = await upload([jpg('1.jpg'), jpg('2.jpg'), jpg('3.jpg'), jpg('4.jpg')], deps(sent, { '2.jpg': 507 }), () => {}).done;
  assert.deepEqual(sent, ['1.jpg', '2.jpg']);
  assert.deepEqual(items.map((i) => [i.state, i.reason]), [['done', ''], ['fail', 'full'], ['fail', 'full'], ['fail', 'full']]);
});

test('answers of the box become reasons', async () => {
  const jpg = (n) => file(n, bytes(0xff, 0xd8, 0xff));
  const items = await upload([jpg('big.jpg'), jpg('odd.jpg'), jpg('down.jpg')], deps([], { 'big.jpg': 413, 'odd.jpg': 422, 'down.jpg': 0 }), () => {}).done;
  assert.deepEqual(items.map((i) => i.reason), ['too_large', 'not_photo', 'failed']);
});

test('"do not add the rest" drops the files that have not started', async () => {
  const sent = [];
  const jpg = (n) => file(n, bytes(0xff, 0xd8, 0xff));
  let ctl;
  ctl = upload([jpg('1.jpg'), jpg('2.jpg'), jpg('3.jpg')], { ...deps(sent), send: async (b, name) => { sent.push(name); ctl.skip(); return { status: 201 }; } }, () => {});
  const items = await ctl.done;
  assert.deepEqual(sent, ['1.jpg']);
  assert.equal(items.length, 1, 'the rest is not counted');
});

// The vendored libheif (LGPL-3.0) really decodes a HEIC photo: the fixture was
// made from a 64 x 48 test picture.
test('the vendored libheif decodes a HEIC photo', async () => {
  const dir = path.join(__dirname, '../static/vendor/libheif');
  const libheif = require(path.join(dir, 'libheif.js'));
  const mod = await libheif({ wasmBinary: fs.readFileSync(path.join(dir, 'libheif.wasm')) });
  const data = fs.readFileSync(path.join(__dirname, '../testdata/sample.heic'));
  assert.equal(sniff(data), 'heic');
  const images = new mod.HeifDecoder().decode(new Uint8Array(data));
  assert.equal(images.length, 1);
  assert.equal(images[0].get_width(), 64);
  assert.equal(images[0].get_height(), 48);
  const out = { data: new Uint8ClampedArray(64 * 48 * 4), width: 64, height: 48 };
  const shown = await new Promise((ok) => images[0].display(out, ok));
  assert.ok(shown, 'display() returned no image');
  assert.ok(out.data.some((v) => v > 0), 'the picture is blank');
});
