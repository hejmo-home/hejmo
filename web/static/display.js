// A display asks the box now and then whether it is still what its page says
// (paired, or waiting with a code) and reloads when that changed: after a
// parent confirmed the code, revoked the display, or the code ran out.
// A paired display also updates the clock and weather chips from the same
// answer, so the clock stays right without a page reload (ADR 0016). When the
// weather chip appears, vanishes or changes its icon the page reloads, so the
// chip is always the one the box would render now.
(function () {
  var el = document.querySelector("[data-display]");
  if (!el) return;
  var state = el.getAttribute("data-state");
  var code = el.getAttribute("data-code") || "";
  var icon = el.getAttribute("data-chip-icon") || "";
  var every = Number(el.getAttribute("data-poll-seconds")) || 30;
  function chip(selector, text) {
    var c = document.querySelector(selector);
    if (c && text && c.textContent !== text) c.textContent = text;
  }
  function check() {
    fetch("/display/state", { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state !== state || (s.code || "") !== code || (s.weather_icon || "") !== icon) { location.reload(); return; }
        chip("[data-clock]", s.clock);
        chip("[data-weather-text]", s.weather);
      })
      .catch(function () {});
  }
  setInterval(check, every * 1000);
})();
