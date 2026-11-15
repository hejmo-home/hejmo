// Tests of the service worker (ticket 5.13) with node's test runner:
//   cd web && node --test
// sw.js is run in a sandbox with a small fake of the worker environment.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.join(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'static/sw.js'), 'utf8');
const ORIGIN = 'https://box.home';

// A minimal CacheStorage: named caches keyed by URL.
function fakeCaches() {
  const stores = new Map();
  const cache = (name) => {
    if (!stores.has(name)) stores.set(name, new Map());
    const m = stores.get(name);
    return {
      async put(k, v) { m.delete(typeof k === 'string' ? k : k.url); m.set(typeof k === 'string' ? k : k.url, v); },
      async match(k) { const r = m.get(typeof k === 'string' ? k : k.url); return r ? r.clone() : undefined; },
      async keys() { return [...m.keys()].map((url) => ({ url })); },
      async delete(k) { return m.delete(typeof k === 'string' ? k : k.url); },
      async addAll(urls) {
        for (const u of urls) {
          const res = await sandbox.fetch(new URL(u, ORIGIN).href);
          if (!res.ok) throw new Error('precache ' + u + ' ' + res.status);
          m.set(new URL(u, ORIGIN).href, res);
        }
      },
    };
  };
  return { stores, open: async (n) => cache(n), delete: async (n) => stores.delete(n), match: async (k) => { for (const n of stores.keys()) { const r = await cache(n).match(k); if (r) return r; } } };
}

let sandbox;
function load(network) {
  const listeners = {};
  const caches = fakeCaches();
  sandbox = {
    self: { addEventListener: (t, f) => { listeners[t] = f; }, skipWaiting() {}, clients: { claim: async () => {} } },
    caches, Response, Headers, URL, Date, Promise, AbortController,
    // Timers run at once-ish so the test of the timeout does not wait seconds.
    setTimeout: (f) => setTimeout(f, 5), clearTimeout,
    fetch: async (u, opts) => network(typeof u === 'string' ? u : u.url, opts),
  };
  vm.runInNewContext(source, sandbox);
  const run = async (type, extra) => {
    const waits = [];
    let answer;
    const event = { waitUntil: (p) => waits.push(p), respondWith: (p) => { answer = p; }, ...extra };
    listeners[type](event);
    await Promise.all(waits);
    return answer && (await answer);
  };
  const get = (url, mode = 'no-cors') => run('fetch', { request: { method: 'GET', mode, url: ORIGIN + url } });
  return { caches, run, get, listeners };
}

// The assets layout.html loads (not the ones in {{if}} templating only: every /static/ reference).
function layoutAssets() {
  const html = fs.readFileSync(path.join(root, 'templates/layout.html'), 'utf8');
  return [...html.matchAll(/(?:href|src)="(\/static\/[^"]+)"/g)].map((m) => m[1]).filter((u) => !u.includes('/icons/'));
}

const page = (body) => new Response(body, { status: 200, headers: { 'Content-Type': 'text/html; charset=utf-8' } });
const asset = (body, type) => new Response(body, { status: 200, headers: { 'Content-Type': type } });

function box(online) {
  return (u) => {
    if (!online.up) throw new TypeError('offline');
    const p = new URL(u).pathname;
    if (p === '/') return page('<html lang="en"><body>home</body></html>');
    if (p.endsWith('.css')) return asset('body{}', 'text/css');
    if (p.endsWith('.js')) return asset('//js', 'text/javascript');
    if (p.endsWith('.ttf')) return asset('font', 'font/ttf');
    return new Response('', { status: 404 });
  };
}

test('the precache list covers every asset layout.html loads', async () => {
  const net = { up: true };
  const sw = load(box(net));
  await sw.run('install');
  const cached = [...sw.caches.stores.values()].flatMap((m) => [...m.keys()]);
  for (const a of layoutAssets()) assert.ok(cached.includes(ORIGIN + a), a + ' is not precached');
  assert.ok(layoutAssets().length >= 3, 'layout.html loads hejmo.css, form.js and offline.js');
});

test('offline, styles and scripts come from the cache', async () => {
  const net = { up: true };
  const sw = load(box(net));
  await sw.run('install');
  net.up = false;
  for (const a of layoutAssets()) {
    const res = await sw.get(a);
    assert.ok(res && res.status === 200, a + ' is not served offline');
  }
});

test('offline, a visited page is served with the time it was loaded', async () => {
  const net = { up: true };
  const sw = load(box(net));
  await sw.run('install');
  await sw.get('/', 'navigate');
  net.up = false;
  const res = await sw.get('/', 'navigate');
  assert.equal(res.status, 200);
  assert.match(await res.text(), /<html data-offline="\d+" lang="en">/);
});

test('online, a changed asset is fetched and the new copy is kept', async () => {
  const net = { up: true };
  let css = 'one';
  const base = box(net);
  const sw = load((u) => (new URL(u).pathname.endsWith('.css') && net.up ? asset(css, 'text/css') : base(u)));
  await sw.run('install');
  css = 'two';
  assert.equal(await (await sw.get('/static/hejmo.css')).text(), 'two');
  net.up = false;
  assert.equal(await (await sw.get('/static/hejmo.css')).text(), 'two');
});

test('signing in again deletes the kept pages but not the assets', async () => {
  const net = { up: true };
  const sw = load(box(net));
  await sw.run('install');
  await sw.get('/', 'navigate');
  await sw.get('/login', 'navigate');
  net.up = false;
  const res = await sw.get('/', 'navigate');
  assert.equal(res.status, 503, 'the last person pages are gone');
  assert.equal((await sw.get('/static/hejmo.css')).status, 200);
});

test('every precached asset exists in web/static', () => {
  const list = source.match(/const ASSETS = (\[[^\]]*\])/);
  assert.ok(list, 'sw.js has an ASSETS list');
  for (const a of JSON.parse(list[1].replace(/'/g, '"'))) {
    assert.ok(fs.existsSync(path.join(root, a.replace('/static/', 'static/'))), a + ' does not exist');
  }
});

test('a box that does not answer is given up on, and the kept page is shown', async () => {
  const net = { up: true, hang: false };
  const base = box(net);
  const sw = load((u, opts) => {
    if (!net.hang) return base(u);
    if (!opts || !opts.signal) return new Promise(() => {}); // hangs for ever without a timeout
    return new Promise((_, reject) => opts.signal.addEventListener('abort', () => reject(new TypeError('aborted'))));
  });
  await sw.run('install');
  await sw.get('/', 'navigate');
  net.hang = true;
  const res = await sw.get('/', 'navigate');
  assert.equal(res.status, 200);
  assert.match(await res.text(), /data-offline="\d+"/);
  assert.equal((await sw.get('/static/hejmo.css')).status, 200, 'assets fall back too');
});
