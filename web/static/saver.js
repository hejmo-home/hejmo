// The photo screensaver (ticket 178, ADR 0020): one photo that fills the
// screen, changed with a hard cut, and the time and date small in a corner.
// Every photo of the display is shown once, in random order, before any comes
// again. A touch, a key or a click brings the day back at once. The page asks
// the box now and then whether the screensaver is still on (quiet mode, a
// switch in the settings) and for new photos; display.js (loaded first) keeps
// the display's theme current and notices when it is no longer paired.
(function () {
  var root = document.querySelector("[data-saver]");
  if (!root) return;
  var dayURL = root.getAttribute("data-day-url") || "/display";
  var photosURL = root.getAttribute("data-photos-url");
  var photoURL = root.getAttribute("data-photo-url");
  var interval = (Number(root.getAttribute("data-interval")) || 60) * 1000;
  var hasPhotos = root.getAttribute("data-has-photos") === "true";

  function leave() { location.href = dayURL; }
  ["pointerdown", "touchstart", "keydown", "mousedown"].forEach(function (name) {
    document.addEventListener(name, leave, { passive: true, capture: true });
  });

  // Time and date: ticked here, in the household's zone, like the family view.
  var chip = root.querySelector("[data-clock]");
  var dateEl = root.querySelector("[data-date]");
  var zone = root.getAttribute("data-clock-zone") || undefined;
  var twelve = root.getAttribute("data-clock-format") === "12h";
  var lang = root.getAttribute("data-lang") === "en" ? "en-GB" : "de-DE";
  function tick() {
    try {
      var now = new Date();
      var parts = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: zone }).formatToParts(now);
      var h = 0, m = "00";
      parts.forEach(function (p) { if (p.type === "hour") h = Number(p.value); if (p.type === "minute") m = p.value; });
      var text = twelve ? ((h + 11) % 12 + 1) + ":" + m + " " + (h < 12 ? "AM" : "PM") : (h < 10 ? "0" : "") + h + ":" + m;
      if (chip && chip.textContent !== text) chip.textContent = text;
      var d = new Intl.DateTimeFormat(lang, { weekday: "long", day: "numeric", month: "long", timeZone: zone }).format(now);
      if (dateEl && dateEl.textContent !== d) dateEl.textContent = d;
    } catch (e) {}
  }
  function scheduleTick() {
    tick();
    setTimeout(scheduleTick, 60000 - (Date.now() % 60000) + 50);
  }
  scheduleTick();

  // The photos: a shuffled bag, each shown once before any returns.
  var img = root.querySelector(".saver-photo");
  var bag = [];
  var current = img ? (img.getAttribute("src") || "").replace(photoURL, "") : "";
  var known = [];
  function shuffle(a) {
    for (var i = a.length - 1; i > 0; i--) {
      var j = Math.floor(Math.random() * (i + 1));
      var t = a[i]; a[i] = a[j]; a[j] = t;
    }
    return a;
  }
  function next() {
    if (!known.length || !img) return;
    if (!bag.length) {
      bag = shuffle(known.slice());
      if (bag.length > 1 && bag[bag.length - 1] === current) { // the photo just shown does not open the next round
        bag.unshift(bag.pop());
      }
    }
    var id = bag.pop();
    var loader = new Image();
    loader.onload = function () { img.src = photoURL + id; current = id; };
    loader.src = photoURL + id;
  }
  function load() {
    fetch(photosURL, { cache: "no-store", credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("saver " + r.status); return r.json(); })
      .then(function (s) {
        // Off, quiet mode or unpaired: the day. Photos that came or went: the page again.
        if (!s.active) { leave(); return; }
        if ((s.photos.length > 0) !== hasPhotos) { location.reload(); return; }
        known = s.photos;
        bag = bag.filter(function (id) { return known.indexOf(id) >= 0; });
      })
      .catch(function () {});
  }
  load();
  setInterval(load, 5 * 60 * 1000);
  if (hasPhotos) setInterval(next, interval);
})();
