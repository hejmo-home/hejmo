// Offline view (ticket 5.13) and service worker registration.
//
// The service worker answers a page it cannot load from the box with its last
// copy and marks it with data-offline="<time it was loaded, ms>". Such a page
// is read-only: it says "As of hh:mm, not connected" and offers no changes.
(function () {
  var script = document.currentScript;
  var root = document.documentElement;
  var loaded = Number(root.getAttribute('data-offline'));

  if (!loaded) {
    if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(function () {});
    return;
  }

  var time = new Date(loaded).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  var banner = document.createElement('p');
  banner.className = 'notice';
  banner.setAttribute('role', 'status');
  banner.setAttribute('data-offline-banner', '');
  banner.textContent = (script.getAttribute('data-offline-text') || '{time}').replace('{time}', time);
  var main = document.getElementById('content') || document.body;
  main.insertBefore(banner, main.firstChild);

  function lock() {
    document.querySelectorAll('button, input, select, textarea').forEach(function (el) { el.disabled = true; });
  }
  lock();
  document.addEventListener('submit', function (e) { e.preventDefault(); }, true);
  window.addEventListener('online', function () { location.reload(); });
})();
