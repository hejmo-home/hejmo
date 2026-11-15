// A display stays current without polling the view (ticket 5.5, ADR 0015):
// the box announces each new version of the family view over Server-Sent
// Events, and the script swaps in only the changed markup. On connect the box
// announces the current version, so a display that lost its connection
// catches up when EventSource reconnects. The weather chip is part of the
// view, so a new reading arrives as an ordinary change. The clock chip changes
// once a minute, as a partial update (ADR 0016): the script ticks it itself in
// the household's time zone and time format, it is not part of the announced
// version.
//
// In quiet mode an E-Ink display reloads the whole page from the box once an
// hour against ghosting (ADR 0015): the night page marks itself with
// data-reload-seconds, and the timer runs only while that markup is shown.
// The reload happens only if the box answers; otherwise it retries in 5 minutes.
//
// Separately, a display asks the box now and then whether it is still what its
// page says (paired, or waiting with a code) and reloads when that changed:
// after a parent confirmed the code, revoked the display, or the code ran out.
// The same answer carries the theme the display should show now.
//
// A personal view (ticket 5.6) works the same way on its own addresses
// (data-view-url, data-events-url), and returns to the family view after
// data-return-seconds without interaction; refreshes do not count as one.
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
        if (s.state !== state || (s.code || "") !== code) { location.reload(); return; }
        // Automatic themes switch at sunset and sunrise without a reload (ADR 0015).
        if (s.theme && document.documentElement.getAttribute("data-theme") !== s.theme) {
          document.documentElement.setAttribute("data-theme", s.theme);
        }
      })
      .catch(function () {});
  }
  setInterval(check, every * 1000);

  // A personal view returns to the family view after the given seconds
  // without interaction (ADR 0015). Every touch, key press or scroll
  // restarts the countdown.
  var back = Number(el.getAttribute("data-return-seconds"));
  var target = el.getAttribute("data-return-url");
  if (back > 0 && target) {
    var timer;
    var restart = function () {
      clearTimeout(timer);
      timer = setTimeout(function () { location.href = target; }, back * 1000);
    };
    ["pointerdown", "touchstart", "keydown", "scroll"].forEach(function (name) {
      document.addEventListener(name, restart, { passive: true, capture: true });
    });
    restart();
  }

  // The family view by default; a personal view names its own addresses.
  var viewURL = el.getAttribute("data-view-url") || "/display/view";
  var eventsURL = el.getAttribute("data-events-url") || "/display/events";
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
    fetch(viewURL, { cache: "no-store", credentials: "same-origin" })
      .then(function (r) {
        // A personal view whose person is gone has nothing left to show.
        if (r.status === 404 && target) location.href = target;
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
  // household's zone (data-clock-zone) and time format (data-clock-format):
  // 24-hour hh:mm, or 12-hour h:mm AM/PM.
  var fmt = {};
  function tickClock() {
    var chip = view.querySelector("[data-clock]");
    if (!chip) return;
    var zone = chip.getAttribute("data-clock-zone") || undefined;
    try {
      fmt[zone] = fmt[zone] || new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: zone });
      var parts = fmt[zone].formatToParts(new Date());
      var h = 0, m = "00";
      parts.forEach(function (p) { if (p.type === "hour") h = Number(p.value); if (p.type === "minute") m = p.value; });
      var text = chip.getAttribute("data-clock-format") === "12h"
        ? ((h + 11) % 12 + 1) + ":" + m + " " + (h < 12 ? "AM" : "PM")
        : (h < 10 ? "0" : "") + h + ":" + m;
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
    var source = new EventSource(eventsURL);
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
