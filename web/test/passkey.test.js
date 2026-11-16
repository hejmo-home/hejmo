// Tests of the passkey script (ticket 95) with node's test runner:
//   cd web && node --test
// The browser's credentials API and the box are replaced by fakes; nothing real
// is created or signed in.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/passkey.js'), 'utf8');

function bytes(...n) { return new Uint8Array(n).buffer; }
function list(buffer) { return Array.from(new Uint8Array(buffer)); }
const flush = async () => { for (let i = 0; i < 12; i++) await new Promise((r) => setImmediate(r)); };

// An element with attributes, children found by hook, and a label that is a text node.
function element(attrs = {}, label = '') {
  const text = { nodeType: 3, nodeValue: label };
  const el = {
    hidden: false, disabled: false, attrs, childNodes: [text], hooks: {}, value: '',
    getAttribute(a) { return a in attrs ? attrs[a] : null; },
    setAttribute(a, v) { attrs[a] = v; },
    get textContent() { return text.nodeValue; },
    closest() { return null; },
    querySelector(sel) {
      const m = /data-hook="(\w+)"/.exec(sel);
      if (m) return el.hooks[m[1]] || null;
      if (sel.startsWith('input[name="name"]')) return el.nameInput || null;
      return null;
    },
    appendChild(c) { el.children = (el.children || []).concat([c]); },
    submit() { el.submitted = true; },
    label() { return text.nodeValue; },
  };
  return el;
}

// The page: a scope with the hooks of the offer, one button, and the script run against fakes.
function setup({ kind = 'add', supported = true, create, get, replies = [], script = null } = {}) {
  const scope = element({ 'data-next': '/tasks', 'data-redirect': '/account' });
  scope.hooks = { waiting: element(), fail: element(), have: element(), continue: element(), skip: element() };
  for (const h of Object.values(scope.hooks)) h.hidden = true;
  scope.nameInput = { value: 'Mac' };
  const button = element({ 'data-working': 'Passkey wird angelegt', 'data-retry': 'Nochmal versuchen' }, 'Passkey anlegen');
  button.closest = (sel) => (sel === `[data-passkey-${kind}]` ? button : sel === '[data-passkey-scope]' ? scope : sel === 'form' ? scope : null);
  const clicks = [];
  const calls = [];
  const assigned = [];
  const queue = replies.slice();
  const created = [];
  const document = {
    addEventListener(ev, fn) { if (ev === 'click') clicks.push(fn); },
    querySelector(sel) { return sel === 'script[data-gone]' ? script : null; },
    createTextNode(t) { return { nodeType: 3, nodeValue: t }; },
    createElement() { const i = { type: '', name: '', value: '' }; created.push(i); return i; },
  };
  const navigator = { credentials: supported ? { create: create || (async () => { throw new Error('not expected'); }), get } : undefined };
  const window = { PublicKeyCredential: supported ? function PublicKeyCredential() {} : undefined, location: { assign(u) { assigned.push(u); }, replace(u) { assigned.push('replace:' + u); } } };
  const fetch = (url, opts) => {
    calls.push({ url, body: opts.body ? JSON.parse(opts.body) : null });
    const r = queue.shift() || { status: 200, data: {} };
    return Promise.resolve({ status: r.status, json: () => Promise.resolve(r.data) });
  };
  vm.runInNewContext(source, { document, navigator, window, fetch, atob: (s) => Buffer.from(s, 'base64').toString('binary'), btoa: (s) => Buffer.from(s, 'binary').toString('base64'), Uint8Array, AbortController, Promise, JSON });
  const click = (target = button) => clicks.forEach((fn) => fn({ target, preventDefault() {} }));
  return { scope, button, click, calls, assigned, window, created };
}

const creationFromBox = { publicKey: { challenge: 'AQID', rp: { id: 'hejmo.local', name: 'Hejmo' }, user: { id: 'cC0x', name: 'mama', displayName: 'Mama' }, excludeCredentials: [{ type: 'public-key', id: 'BAU' }] } };

test('binary values travel as base64url and come back as bytes', () => {
  const { window } = setup();
  const codec = window.hejmoPasskeyCodec;
  assert.equal(codec.encode(bytes(251, 255, 254)), '-__-');
  assert.deepEqual(list(codec.decode('-__-')), [251, 255, 254]);
  assert.deepEqual(list(codec.decode(codec.encode(bytes(1, 2, 3, 4, 5)))), [1, 2, 3, 4, 5]);
});

test('options for creating a passkey have the challenge, the user and the excluded passkeys as bytes', () => {
  const opts = setup().window.hejmoPasskeyCodec.creationOptions(creationFromBox);
  assert.deepEqual(list(opts.publicKey.challenge), [1, 2, 3]);
  assert.deepEqual(list(opts.publicKey.user.id), [112, 45, 49]); // "p-1", the person's id
  assert.equal(opts.publicKey.user.displayName, 'Mama');
  assert.deepEqual(list(opts.publicKey.excludeCredentials[0].id), [4, 5]);
  assert.equal(opts.publicKey.rp.id, 'hejmo.local');
});

test('creating a passkey waits in words, sends the name and goes where the box says', async () => {
  const cred = { id: 'AQ', rawId: bytes(1), type: 'public-key', response: { attestationObject: bytes(7), clientDataJSON: bytes(8) } };
  const f = setup({
    create: async () => cred,
    replies: [{ status: 200, data: { ceremony: 'c1', options: creationFromBox } }, { status: 200, data: { redirect: '/setup/totp' } }],
  });
  f.click();
  assert.equal(f.button.disabled, true);
  assert.equal(f.button.label(), 'Passkey wird angelegt');
  assert.equal(f.scope.hooks.waiting.hidden, false);
  await flush();
  assert.equal(f.calls[0].url, '/account/passkeys/begin');
  assert.equal(f.calls[1].url, '/account/passkeys/finish');
  assert.equal(f.calls[1].body.ceremony, 'c1');
  assert.equal(f.calls[1].body.name, 'Mac');
  assert.equal(f.calls[1].body.credential.id, 'AQ');
  assert.equal(f.calls[1].body.credential.response.attestationObject, 'Bw');
  assert.deepEqual(f.assigned, ['/setup/totp']);
});

test('a cancelled window says so and offers to try again', async () => {
  const f = setup({
    create: async () => { const e = new Error('cancelled'); e.name = 'NotAllowedError'; throw e; },
    replies: [{ status: 200, data: { ceremony: 'c1', options: creationFromBox } }],
  });
  f.click();
  await flush();
  assert.equal(f.scope.hooks.fail.hidden, false);
  assert.equal(f.scope.hooks.waiting.hidden, true);
  assert.equal(f.button.disabled, false);
  assert.equal(f.button.label(), 'Nochmal versuchen');
  assert.deepEqual(f.assigned, []);
});

test('a device that has a passkey already says so and leads on', async () => {
  const f = setup({
    create: async () => { const e = new Error('exists'); e.name = 'InvalidStateError'; throw e; },
    replies: [{ status: 200, data: { ceremony: 'c1', options: creationFromBox } }],
  });
  f.click();
  await flush();
  assert.equal(f.scope.hooks.have.hidden, false);
  assert.equal(f.scope.hooks.continue.hidden, false);
  assert.equal(f.scope.hooks.skip.hidden, true);
  assert.equal(f.scope.hooks.fail.hidden, true);
});

test('a refusal of the box (another name than the box name) counts as nothing created', async () => {
  const f = setup({ create: async () => { throw new Error('not reached'); }, replies: [{ status: 409, data: { error: 'name' } }] });
  f.click();
  await flush();
  assert.equal(f.scope.hooks.fail.hidden, false);
  assert.equal(f.calls.length, 1);
});

const requestFromBox = { publicKey: { challenge: 'AQID', rpId: 'hejmo.local', allowCredentials: [], userVerification: 'required' } };
const asserted = { id: 'AQ', rawId: bytes(1), type: 'public-key', response: { authenticatorData: bytes(1), clientDataJSON: bytes(2), signature: bytes(3), userHandle: bytes(4) } };
const where = { 'data-cancelled': '/login?passkey=cancelled', 'data-gone': '/login?passkey=gone', 'data-failed': '/login?passkey=failed' };
const pageScript = () => element({ ...where });

test('signing in with a passkey sends it with the page to come back to', async () => {
  const f = setup({ kind: 'signin', get: async () => asserted, script: pageScript(), replies: [{ status: 200, data: { ceremony: 'c2', options: requestFromBox } }, { status: 200, data: { redirect: '/tasks' } }] });
  f.click();
  await flush();
  assert.equal(f.calls[0].url, '/login/passkey/begin');
  assert.equal(f.calls[1].url, '/login/passkey/finish');
  assert.equal(f.calls[1].body.next, '/tasks');
  assert.equal(f.calls[1].body.credential.response.userHandle, 'BA');
  assert.deepEqual(f.assigned, ['/tasks']);
});

test('a passkey the box no longer knows leads to the page that says why', async () => {
  const f = setup({ kind: 'signin', get: async () => asserted, script: pageScript(), replies: [{ status: 200, data: { ceremony: 'c2', options: requestFromBox } }, { status: 401, data: { error: 'passkey_gone' } }] });
  f.click();
  await flush();
  assert.deepEqual(f.assigned, ['/login?passkey=gone']);
});

test('another refusal says the sign-in did not work, and a cancelled window says cancelled', async () => {
  let f = setup({ kind: 'signin', get: async () => asserted, script: pageScript(), replies: [{ status: 200, data: { ceremony: 'c2', options: requestFromBox } }, { status: 401, data: { error: 'passkey_failed' } }] });
  f.click();
  await flush();
  assert.deepEqual(f.assigned, ['/login?passkey=failed']);
  f = setup({ kind: 'signin', get: async () => { const e = new Error('no'); e.name = 'NotAllowedError'; throw e; }, script: pageScript(), replies: [{ status: 200, data: { ceremony: 'c2', options: requestFromBox } }] });
  f.click();
  await flush();
  assert.deepEqual(f.assigned, ['/login?passkey=cancelled']);
});

test('confirming with a passkey adds the answer to the form and sends it', async () => {
  const f = setup({ kind: 'confirm', get: async () => asserted, replies: [{ status: 200, data: { ceremony: 'c3', options: requestFromBox } }] });
  f.click();
  await flush();
  assert.equal(f.calls[0].url, '/account/passkeys/assert/begin');
  assert.equal(f.scope.submitted, true);
  const fields = Object.fromEntries(f.created.map((i) => [i.name, i.value]));
  assert.equal(fields.ceremony, 'c3');
  assert.equal(JSON.parse(fields.assertion).id, 'AQ');
});

test('a browser without passkeys makes every attempt fail in words', async () => {
  const f = setup({ supported: false });
  f.click();
  await flush();
  assert.equal(f.scope.hooks.fail.hidden, false);
  assert.equal(f.calls.length, 0);
});
