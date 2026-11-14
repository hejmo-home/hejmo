// A display stays current without polling the view (ticket 5.5, ADR 0015):
// the box announces each new version of the family view over Server-Sent
// Events, and the script swaps in only the changed markup. On connect the box
// announces the current version, so a display that lost its connection
// catches up when EventSource reconnects. The clock chip changes once a
// minute, as a partial update (ADR 0016): the script ticks it itself in the
// household's time zone, it is not part of the announced version.
//
// In quiet mode an E-Ink display reloads the whole page from the box once an
// hour against ghosting (ADR 0015): the night page marks itself with
// data-reload-seconds, and the timer runs only while that markup is shown.
// The reload happens only if the box answers; otherwise it retries in 5 minutes.
//
// Separately, a display asks the box now and then whether it is still what its
// page says (paired, or waiting with a code) and reloads when that changed:
// after a parent confirmed the code, revoked the display, or the code ran out.
(function () {
  var el = document.querySelector("[data-display]");
  if (!el) return;
  var state = el.getAttribute("data-state");
  var code = el.getAttribute("data-code") || "";
  var every = Number(el.getAttribute("data-poll-seconds")) || 30;
  function check() {
    fetch("/display/state", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state !== state || (s.code || "") !== code) location.reload();
      })
      .catch(function () {});
  }
  setInterval(check, every * 1000);

  var view = el.querySelector("[data-view]");
  if (!view) return; // the pairing screen has no view
  var version = el.getAttribute("data-version") || "";
  var loading = false;
  var reloadTimer = null;
  function armReload() {
    var q = view.querySelector("[data-reload-seconds]");
    if (!q) {
      if (reloadTimer) { clearTimeout(reloadTimer); reloadTimer = null; }
      return;
    }
    if (reloadTimer) return;
    var secs = Number(q.getAttribute("data-reload-seconds")) || 3600;
    reloadTimer = setTimeout(function () { reloadIfBoxAnswers(secs); }, secs * 1000);
  }
  // Reload only when the box answers: an unreachable box would leave the
  // browser's error page on the display all night. Otherwise try again later.
  function reloadIfBoxAnswers(secs) {
    fetch("/display/view", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) {
        if (!r.ok) throw new Error("view " + r.status);
        location.reload();
      })
      .catch(function () {
        reloadTimer = setTimeout(function () { reloadIfBoxAnswers(secs); }, Math.min(secs, 300) * 1000);
      });
  }
  armReload();
  function refresh() {
    if (loading) return;
    loading = true;
    fetch("/display/view", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) {
        if (!r.ok) throw new Error("view " + r.status);
        var v = r.headers.get("X-Hejmo-View") || "";
        return r.text().then(function (html) { return { v: v, html: html }; });
      })
      .then(function (res) {
        view.innerHTML = res.html;
        version = res.v;
        tickClock();
        armReload();
      })
      .catch(function () {})
      .then(function () { loading = false; });
  }
  // The clock chip is ticked here, not announced: once a minute, in the
  // household's zone (data-clock-zone), 24-hour hh:mm.
  var fmt = {};
  function tickClock() {
    var chip = view.querySelector("[data-clock]");
    if (!chip) return;
    var zone = chip.getAttribute("data-clock-zone") || undefined;
    try {
      fmt[zone] = fmt[zone] || new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: zone });
      var text = fmt[zone].format(new Date());
      if (chip.textContent !== text) chip.textContent = text;
    } catch (e) {}
  }
  function scheduleClock() {
    tickClock();
    setTimeout(scheduleClock, 60000 - (Date.now() % 60000) + 50);
  }
  scheduleClock();

  var retry = 1000;
  function connect() {
    var source = new EventSource("/display/events");
    source.addEventListener("view", function (e) {
      retry = 1000;
      if (e.data !== version) refresh();
    });
    // EventSource reconnects by itself after a lost connection, but gives up
    // for good after an error response (a 500 on a database hiccup): start a
    // new one, backing off up to a minute.
    source.onerror = function () {
      if (source.readyState !== 2) return; // CLOSED
      source.close();
      setTimeout(connect, retry);
      retry = Math.min(retry * 2, 60000);
    };
  }
  if (window.EventSource) {
    connect();
  } else {
    // A browser without EventSource still catches up, once a minute.
    setInterval(refresh, 60 * 1000);
  }
})();
