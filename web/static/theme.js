// Theme of the web interface (ADR 0015), stored per browser or phone:
// "device" (default: no data-theme, so prefers-color-scheme decides), "light",
// "dark" or "auto" (dark from sunset to sunrise at home; the box puts today's
// sun times on <html> as data-sun-rise and data-sun-set, Unix seconds).
// Loaded in <head> so the page is painted in the right theme.
(function () {
  var html = document.documentElement;
  var KEY = "hejmo-theme";
  function stored() {
    try { return localStorage.getItem(KEY) || "device"; } catch (e) { return "device"; }
  }
  function apply(mode) {
    var theme = null;
    if (mode === "light" || mode === "dark") {
      theme = mode;
    } else if (mode === "auto") {
      var rise = Number(html.getAttribute("data-sun-rise")), set = Number(html.getAttribute("data-sun-set"));
      if (rise && set) {
        // The times are from page load; shift them by whole days so a tab
        // left open overnight keeps following the sun.
        var now = Date.now() / 1000, day = 86400, k = Math.floor((now - rise) / day);
        rise += k * day; set += k * day;
        theme = now < rise || now >= set ? "dark" : "light";
      }
    }
    if (theme) html.setAttribute("data-theme", theme); else html.removeAttribute("data-theme");
  }
  apply(stored());
  setInterval(function () { apply(stored()); }, 60 * 1000);
  document.addEventListener("DOMContentLoaded", function () {
    var choices = document.querySelectorAll("[data-theme-choice]");
    for (var i = 0; i < choices.length; i++) {
      choices[i].checked = choices[i].value === stored();
      choices[i].addEventListener("change", function (e) {
        try { localStorage.setItem(KEY, e.target.value); } catch (err) {}
        apply(e.target.value);
      });
    }
  });
})();
