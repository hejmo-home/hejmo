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
