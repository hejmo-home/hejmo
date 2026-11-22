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
// A quiet display that a kiosk app switched off (ticket 179) marks its page
// with data-wake-url: a touch, or the screen coming back on, posts there so the
// box wakes the display for a few minutes.
//
// A personal view (ticket 5.6) works the same way on its own addresses
// (data-view-url, data-events-url), and returns to the family view after
// data-return-seconds without interaction; refreshes do not count as one.
//
// The lists view (ticket 6.2) works like a personal view and adds a field: a
// redraw never clears or takes the focus from the field marked data-keep, and
// adding an entry posts in the background (partial=1), shows the box's answer
// and keeps the new entry named (?added=) until the next tap. On a list with
// suggestions (ticket 165) the quantity of an entry opens under it: the script
// names the open entry (?qty=) and the line that says what happened (?r=), and
// saves the editor in the background like an added entry.
(function () {
  var el = document.querySelector("[data-display]");
  if (!el) return;
  var state = el.getAttribute("data-state");
  var code = el.getAttribute("data-code") || "";
  var every = Number(el.getAttribute("data-poll-seconds")) || 30;
  var saverAfter = Number(el.getAttribute("data-saver-seconds")) || 0;
  var saverURL = el.getAttribute("data-saver-url");
  var saverTimer;
  function saverRestart() {
    clearTimeout(saverTimer);
    if (saverAfter > 0) saverTimer = setTimeout(saverDue, saverAfter * 1000);
  }
  // saverSeconds takes over the idle seconds the box announced.
  function saverSeconds(s) {
    if (!saverURL || s.saver_seconds === undefined) return;
    var n = Number(s.saver_seconds) || 0;
    if (n !== saverAfter) { saverAfter = n; saverRestart(); }
  }
  function saverDue() {
    fetch("/display/state", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state === "paired" && s.saver === "1") { location.href = saverURL; return; }
        saverSeconds(s);
        saverRestart();
      })
      .catch(saverRestart);
  }
  function check() {
    fetch("/display/state", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state !== state || (s.code || "") !== code) { location.reload(); return; }
        saverSeconds(s);
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

  // The family view starts the photo screensaver after data-saver-seconds
  // without a touch (ticket 178): the box serves it as a page of its own, and
  // a touch there brings the day back at once. Refreshes do not count.
  // The page is rendered once and stays for days, so the timer is there
  // whenever the screensaver is on (also in quiet mode), and when it fires the
  // display asks the box (/display/state) whether the screensaver is due now.
  // The same answer, and the regular check, carry the idle seconds: switching
  // the screensaver on or changing the minutes reaches the display without a reload.
  if (saverURL) {
    ["pointerdown", "touchstart", "keydown", "scroll"].forEach(function (name) {
      document.addEventListener(name, saverRestart, { passive: true, capture: true });
    });
    saverRestart();
  }

  // The family view by default; a personal view names its own addresses.
  var viewURL = el.getAttribute("data-view-url") || "/display/view";
  var eventsURL = el.getAttribute("data-events-url") || "/display/events";
  var view = el.querySelector("[data-view]");
  if (!view) return; // the pairing screen has no view
  var version = el.getAttribute("data-version") || "";
  var loading = false;
  var reloadTimer = null;
  // The entry just added on the lists view: refreshes name it (?added=) so its
  // row keeps a frame, until the next tap.
  var addedId = "";
  // The entry whose quantity is open, and the token of the line that says what
  // the last add or change did (kept in the box's memory for two minutes).
  var editId = "";
  var resultToken = "";
  function viewQuery() {
    var q = [];
    if (addedId) q.push("added=" + encodeURIComponent(addedId));
    if (editId) q.push("qty=" + encodeURIComponent(editId));
    if (resultToken) q.push("r=" + encodeURIComponent(resultToken));
    return q.length ? "?" + q.join("&") : "";
  }
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
  // Quiet mode with a kiosk app (ticket 179, Q79): the page it shows names
  // data-wake-url, and a touch, or the screen coming back on, tells the box to
  // wake the display for a while. The box decides; at most one message every 15 seconds.
  var wakeSent = 0;
  function wake() {
    var q = view.querySelector("[data-wake-url]");
    if (!q) return;
    var now = Date.now();
    if (now - wakeSent < 15000) return;
    wakeSent = now;
    fetch(q.getAttribute("data-wake-url"), { method: "POST", cache: "no-store", credentials: "same-origin" }).catch(function () {});
  }
  ["pointerdown", "touchstart", "keydown"].forEach(function (name) {
    document.addEventListener(name, wake, { passive: true, capture: true });
  });
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState !== "hidden") wake();
  });
  // The field marked data-keep survives a redraw: what is typed in it and its
  // focus (a change from another display must not take the keyboard away).
  function holdField() {
    var f = view.querySelector("[data-keep]");
    if (!f) return null;
    return { value: f.value, focus: document.activeElement === f, start: f.selectionStart, end: f.selectionEnd };
  }
  function restoreField(held) {
    var f = view.querySelector("[data-keep]");
    if (!held || !f) return;
    if (!f.value && held.value) f.value = held.value;
    if (held.focus && f.focus) {
      f.focus();
      try { f.setSelectionRange(held.start, held.end); } catch (e) {}
    }
  }
  // swap puts new markup into the view; keep holds the field across it.
  function swap(html, keep) {
    var held = keep ? holdField() : null;
    view.innerHTML = html;
    restoreField(held);
    tickClock();
    armReload();
  }
  function refresh() {
    if (loading) return;
    loading = true;
    fetch(viewURL + viewQuery(), { cache: "no-store", credentials: "same-origin" })
      .then(function (r) {
        // A personal view whose person is gone has nothing left to show.
        if (r.status === 404 && target) location.href = target;
        if (!r.ok) throw new Error("view " + r.status);
        var v = r.headers.get("X-Hejmo-View") || "";
        return r.text().then(function (html) { return { v: v, html: html }; });
      })
      .then(function (res) {
        swap(res.html, true);
        version = res.v;
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

  // An error line as the stylesheet knows it (.ferr): icon and text.
  function errorLine(text) {
    var p = document.createElement("p");
    p.className = "ferr";
    p.setAttribute("role", "alert");
    var ns = "http://www.w3.org/2000/svg";
    var svg = document.createElementNS(ns, "svg");
    svg.setAttribute("class", "icon");
    svg.setAttribute("width", "18");
    svg.setAttribute("height", "18");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    var path = document.createElementNS(ns, "path");
    path.setAttribute("d", "M12 8v5M12 16.5v.5");
    var circle = document.createElementNS(ns, "circle");
    circle.setAttribute("cx", "12");
    circle.setAttribute("cy", "12");
    circle.setAttribute("r", "9");
    svg.appendChild(path);
    svg.appendChild(circle);
    p.appendChild(svg);
    p.appendChild(document.createTextNode(text));
    return p;
  }

  // Adding an entry (lists view) posts in the background. The box answers with
  // the new view (200) or with the view and the field error (422); both are
  // shown. If the box cannot be reached the typed text stays and the error sits
  // under the field. Without script the form posts as usual.
  function addEntry(e, form, host) {
    e.preventDefault();
    var group = form.querySelector(".f");
    var old = form.querySelector(".ferr");
    if (old) old.parentNode.removeChild(old);
    var body = new URLSearchParams(new FormData(form));
    body.set("partial", "1");
    fetch(form.action, { method: "POST", body: body, credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (r.redirected && new URL(r.url).pathname === "/display") { location.href = "/display"; return; }
        if (!r.ok && r.status !== 422) throw new Error("add " + r.status);
        var v = r.headers.get("X-Hejmo-View") || "";
        var id = r.ok ? r.headers.get("X-Hejmo-Added") || "" : "";
        var token = r.ok ? r.headers.get("X-Hejmo-Result") || "" : "";
        return r.text().then(function (html) {
          swap(html, false);
          version = v;
          if (r.ok) { addedId = id; resultToken = token; editId = ""; }
          var f = view.querySelector("[data-keep]");
          if (f && f.focus) f.focus();
          var row = view.querySelector(".added");
          if (row && row.scrollIntoView) row.scrollIntoView({ block: "nearest" });
        });
      })
      .catch(function () {
        if (!group) return;
        group.className = (group.className + " invalid").trim();
        var line = errorLine(host.getAttribute("data-add-error"));
        line.setAttribute("id", "err-text");
        group.appendChild(line);
      });
  }
  // The editor of a quantity saves in the background: 200 shows the entry
  // changed, 422 keeps the editor open with the error at the number, 409 says
  // the entry was ticked off or removed meanwhile.
  function saveQuantity(e, form) {
    e.preventDefault();
    var body = new URLSearchParams(new FormData(form));
    body.set("partial", "1");
    fetch(form.action, { method: "POST", body: body, credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (r.redirected && new URL(r.url).pathname === "/display") { location.href = "/display"; return; }
        if (!r.ok && r.status !== 422 && r.status !== 409) throw new Error("quantity " + r.status);
        var v = r.headers.get("X-Hejmo-View") || "";
        return r.text().then(function (html) {
          swap(html, true);
          version = v;
          if (r.ok) { addedId = r.headers.get("X-Hejmo-Added") || ""; resultToken = r.headers.get("X-Hejmo-Result") || ""; editId = ""; }
          else if (r.status === 409) editId = "";
        });
      })
      .catch(function () {
        var row = form.parentNode;
        var old = row.querySelector(".ferr.save");
        if (old) row.removeChild(old);
        var line = errorLine(form.closest("[data-tick-error]") ? form.closest("[data-tick-error]").getAttribute("data-tick-error") : "");
        line.className += " save";
        row.appendChild(line);
      });
  }
  // The quantity button opens or closes the editor of its entry.
  view.addEventListener("click", function (e) {
    var open = e.target.closest && e.target.closest("[data-qty-open]");
    var close = e.target.closest && e.target.closest("[data-qty-close]");
    if (!open && !close) return;
    e.preventDefault();
    addedId = "";
    resultToken = "";
    editId = open && open.getAttribute("data-qty-open") !== editId ? open.getAttribute("data-qty-open") : "";
    refresh();
  });
  // The frame of the entry just added goes at the next tap.
  document.addEventListener("pointerdown", function () {
    if (!addedId) return;
    addedId = "";
    resultToken = "";
    var rows = view.querySelectorAll(".added");
    for (var i = 0; i < rows.length; i++) rows[i].classList.remove("added");
  }, { passive: true, capture: true });

  // Ticking a task or an entry posts in the background. If the box cannot
  // be reached or answers with an error, it keeps its state and an
  // error is announced under its row (data-tick-error); on success the view
  // is replaced, as for any change. Without script the form posts as usual.
  view.addEventListener("submit", function (e) {
    var form = e.target;
    var host = form.closest && form.closest("[data-add-error]");
    if (host && /(^| )add-entry( |$)/.test(form.className || "") && window.fetch && window.FormData) {
      addEntry(e, form, host);
      return;
    }
    if (/(^| )qty-edit( |$)/.test(form.className || "") && window.fetch && window.FormData) {
      saveQuantity(e, form);
      return;
    }
    var btn = form.querySelector && form.querySelector(".big-check, .tile-check");
    var tile = form.closest && form.closest("[data-tick-error]");
    if (!btn || !tile || !window.fetch || !window.FormData) return;
    e.preventDefault();
    var row = form.parentNode;
    var old = row.querySelector(".ferr");
    if (old) row.removeChild(old);
    if (row.removeAttribute) row.removeAttribute("aria-invalid"); // a tile's red frame goes with the old error
    var body = new URLSearchParams(new FormData(form));
    fetch(form.action, { method: "POST", body: body, credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("tick " + r.status);
        // fetch follows the 303: a revoked display lands on the pairing screen
        // (or a deleted task on /display), which a plain form post would show.
        if (r.redirected && new URL(r.url).pathname === "/display") { location.href = "/display"; return; }
        refresh();
      })
      .catch(function () {
        row.appendChild(errorLine(tile.getAttribute("data-tick-error")));
        if (btn.className && /tile-check/.test(btn.className)) row.setAttribute("aria-invalid", "true");
      });
  });

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
