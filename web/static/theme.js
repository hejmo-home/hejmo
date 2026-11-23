// Theme of the web interface (ADR 0015), stored per browser or personal device:
// "device" (default: no data-theme, so prefers-color-scheme decides), "light",
// "dark" or "auto" (dark from sunset to sunrise at home; the box puts today's
// sun times on <html> as data-sun-rise and data-sun-set, Unix seconds).
// Loaded in <head> so the page is painted in the right theme.
(function () {
  var html = document.documentElement;
  var KEY = "hejmo-theme";
  var memory = null; // a choice the browser refused to store: it lasts until the page reloads
  function stored() {
    if (memory) return memory;
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
  var CHECK = '<svg class="icon" width="20" height="20" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
  // The chooser of the brand bar (ticket 125): marks the stored choice, shows
  // it on the chip, and saves a new choice at once with a line that says so.
  document.addEventListener("DOMContentLoaded", function () {
    var menu = document.querySelector("[data-theme-choices]");
    if (!menu) return;
    var radios = menu.querySelectorAll("[data-theme-choice]");
    var chip = menu.parentNode.querySelector("summary");
    function optionOf(radio) { return radio.parentNode; }
    function wordOf(radio) { return optionOf(radio).querySelector(".opt-name").firstChild.nodeValue; }
    // Automatic needs sun times at home; without them the page follows the
    // device, and so the chooser says so.
    function shown(mode) {
      for (var i = 0; i < radios.length; i++) {
        if (radios[i].value === "auto" && radios[i].disabled && mode === "auto") return "device";
      }
      return mode;
    }
    function mark(mode) {
      var chosen = null;
      for (var i = 0; i < radios.length; i++) {
        radios[i].checked = radios[i].value === mode;
        if (radios[i].checked) chosen = radios[i];
      }
      if (!chosen || !chip) return;
      var swatch = optionOf(chosen).querySelector(".swatch").cloneNode(true);
      swatch.className += " sm";
      var old = chip.querySelector(".swatch");
      old.parentNode.replaceChild(swatch, old);
      chip.querySelector(".theme-name").lastChild.nodeValue = wordOf(chosen);
    }
    function say(kind, text) {
      var old = menu.querySelectorAll(".msg, .error");
      for (var i = 0; i < old.length; i++) old[i].parentNode.removeChild(old[i]);
      var line = document.createElement("p");
      if (kind === "ok") {
        line.className = "msg ok";
        line.setAttribute("role", "status");
        line.innerHTML = CHECK;
        line.appendChild(document.createTextNode(text));
      } else {
        line.className = "error";
        line.setAttribute("role", "alert");
        line.textContent = text;
      }
      menu.insertBefore(line, menu.querySelector(".where"));
    }
    mark(shown(stored()));
    for (var i = 0; i < radios.length; i++) {
      radios[i].addEventListener("change", function (e) {
        var mode = e.target.value;
        var kept = true;
        try { localStorage.setItem(KEY, mode); memory = null; } catch (err) { memory = mode; kept = false; }
        apply(mode);
        mark(mode);
        if (kept) say("ok", menu.getAttribute("data-saved").replace("{name}", wordOf(e.target)));
        else say("error", menu.getAttribute("data-error"));
      });
    }
  });
})();
