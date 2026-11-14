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
})();
