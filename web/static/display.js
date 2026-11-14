// A display asks the box now and then whether it is still what its page says
// (paired, or waiting with a code) and reloads when that changed: after a
// parent confirmed the code, revoked the display, or the code ran out.
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
})();
