// A display stays current without polling the view (ticket 5.5, ADR 0015):
// the box announces each new version of the family view over Server-Sent
// Events, and the script swaps in only the changed markup. On connect the box
// announces the current version, so a display that lost its connection
// catches up when EventSource reconnects. The clock chip changes once a
// minute, as a partial update (ADR 0016), never a page reload.
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
      })
      .catch(function () {})
      .then(function () { loading = false; });
  }
  if (window.EventSource) {
    var source = new EventSource("/display/events");
    source.addEventListener("view", function (e) {
      if (e.data !== version) refresh();
    });
  } else {
    // A browser without EventSource still catches up, once a minute.
    setInterval(refresh, 60 * 1000);
  }
})();
