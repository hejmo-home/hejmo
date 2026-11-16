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

  // The household's clock and date layout (ticket 149), written like the
  // server does (internal/datefmt); the defaults are 24-hour and day-first.
  var twelve = script.getAttribute('data-time-format') === '12h';
  var monthFirst = script.getAttribute('data-date-format') === 'mdy';
  function two(n) { return (n < 10 ? '0' : '') + n; }
  function clock(d) {
    if (!twelve) return two(d.getHours()) + ':' + two(d.getMinutes());
    return (d.getHours() % 12 || 12) + ':' + two(d.getMinutes()) + '\u00a0' + (d.getHours() < 12 ? 'AM' : 'PM');
  }
  function day(d) {
    var dm = monthFirst ? two(d.getMonth() + 1) + '/' + two(d.getDate()) : two(d.getDate()) + '.' + two(d.getMonth() + 1);
    return dm + (monthFirst ? '/' : '.') + d.getFullYear();
  }

  // The time, with the date when the copy is not from today.
  var when = new Date(loaded);
  var time = clock(when);
  if (when.toDateString() !== new Date().toDateString()) time = day(when) + ', ' + time;
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
