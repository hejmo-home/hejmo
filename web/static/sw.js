// Hejmo service worker (ticket 5.13).
//
// It keeps the last pages a signed-in person loaded. When the box cannot be
// reached, a page is shown from that copy, read-only, with the time it was
// loaded (offline.js draws the notice and switches the controls off). Nothing
// is changed offline. The copies go as soon as the box says the person is
// signed out (revoked personal device, sign-out).
//
// The styles and scripts every page needs are kept apart (they hold nothing
// personal) and are loaded when the service worker is installed, so a kept page
// has its styles, its offline notice and its locked controls without a network.
const CACHE = 'hejmo-pages-v1';
const ASSET_CACHE = 'hejmo-assets-v1';
// Must list every asset web/templates/layout.html loads (checked by web/test/sw.test.js), and the script of the
// cooking view, whose steps and ticks go on without a connection.
const ASSETS = ['/static/hejmo.css', '/static/form.js', '/static/offline.js', '/static/theme.js', '/static/cook.js', '/static/fonts/Figtree-wght.ttf', '/static/flags/de.svg', '/static/flags/gb.svg'];
const MAX_PAGES = 30;
const STAMP = 'x-hejmo-cached-at';
// A box that does not answer (the personal device is away from home) is given up on
// after this long, so the kept copy shows instead of a spinner.
const TIMEOUT_MS = 4000;

function reach(url, options) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), TIMEOUT_MS);
  return fetch(url, { ...options, signal: ctl.signal }).finally(() => clearTimeout(timer));
}

// Never kept: displays, the API and CalDAV, pairing and setup, sign-in.
const SKIP = ['/display', '/api', '/dav', '/ca', '/setup', '/personal-devices/join'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(ASSET_CACHE).then((cache) => cache.addAll(ASSETS)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const path = new URL(req.url).pathname;
  if (ASSETS.includes(path)) {
    event.respondWith(asset(req));
    return;
  }
  if (req.mode !== 'navigate') return;
  if (path === '/login') {
    // Whoever signs in next must not see the last person's pages.
    event.waitUntil(caches.delete(CACHE));
    return;
  }
  if (SKIP.some((p) => path === p || path.startsWith(p + '/'))) return;
  event.respondWith(page(req));
});

// An asset comes from the box when it can be reached (and the copy is
// renewed), otherwise from the copy kept at installation.
async function asset(req) {
  const cache = await caches.open(ASSET_CACHE);
  try {
    const res = await reach(req.url, { credentials: 'same-origin' });
    if (res.ok) await cache.put(req.url, res.clone());
    return res;
  } catch (e) {
    const hit = await cache.match(req.url);
    if (hit) return hit;
    throw e;
  }
}

async function page(req) {
  let res;
  try {
    res = await reach(req.url, { credentials: 'same-origin', redirect: 'follow' });
  } catch (e) {
    return offline(req);
  }
  if (res.redirected) {
    // A redirect to the sign-in page: no longer signed in.
    if (new URL(res.url).pathname === '/login') await caches.delete(CACHE);
    return Response.redirect(res.url, 303);
  }
  if (res.status === 502 || res.status === 503 || res.status === 504) return offline(req, res);
  if (res.ok && (res.headers.get('content-type') || '').startsWith('text/html')) {
    await keep(req.url, res.clone());
  }
  return res;
}

async function keep(url, res) {
  const headers = new Headers(res.headers);
  headers.set(STAMP, String(Date.now()));
  const cache = await caches.open(CACHE);
  await cache.put(url, new Response(await res.blob(), { status: res.status, headers }));
  const keys = await cache.keys();
  for (const old of keys.slice(0, Math.max(0, keys.length - MAX_PAGES))) await cache.delete(old);
}

async function offline(req, failed) {
  const cache = await caches.open(CACHE);
  const hit = (await cache.match(req.url)) || (await cache.match(new URL('/', req.url).href));
  if (!hit) {
    return failed || new Response('Not connected', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
  }
  const html = (await hit.text()).replace('<html', '<html data-offline="' + hit.headers.get(STAMP) + '"');
  return new Response(html, { status: 200, headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}
