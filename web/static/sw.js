// Hejmo service worker (ticket 5.13).
//
// It keeps the last pages a signed-in person loaded. When the box cannot be
// reached, a page is shown from that copy, read-only, with the time it was
// loaded (offline.js draws the notice and switches the controls off). Nothing
// is changed offline. The copies go as soon as the box says the person is
// signed out (revoked phone, sign-out).
const CACHE = 'hejmo-pages-v1';
const MAX_PAGES = 30;
const STAMP = 'x-hejmo-cached-at';

// Never kept: displays, the API and CalDAV, pairing and setup, sign-in.
const SKIP = ['/display', '/api', '/dav', '/ca', '/setup', '/phones/join', '/static'];

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET' || req.mode !== 'navigate') return;
  const path = new URL(req.url).pathname;
  if (path === '/login') {
    // Whoever signs in next must not see the last person's pages.
    event.waitUntil(caches.delete(CACHE));
    return;
  }
  if (SKIP.some((p) => path === p || path.startsWith(p + '/'))) return;
  event.respondWith(page(req));
});

async function page(req) {
  let res;
  try {
    res = await fetch(req.url, { credentials: 'same-origin', redirect: 'follow' });
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
