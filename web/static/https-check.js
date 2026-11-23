// Checks the page runs by itself, in words (ADR 0014, ticket 133). Only the
// browser knows whether it can reach the box under its HTTPS address, so it
// tries: a no-cors request to /healthz succeeds when the name resolves and the
// certificate is trusted, and fails otherwise. It tries again every few seconds
// and when the page becomes visible again.
//
// Two pages use it:
//   data-check="name"   the name check of the wizard: shows router help while
//                       the name does not resolve and goes on by itself;
//   data-check="trust"  the trust page: tells when this device trusts the box.
(function () {
  var box = document.querySelector("[data-check]");
  if (!box) return;
  var mode = box.getAttribute("data-check");
  var url = box.getAttribute("data-check-url");
  var next = box.getAttribute("data-continue-url");
  var wait = parseInt(box.getAttribute("data-retry-seconds") || "5", 10) * 1000;
  var timer = null;
  var running = false;
  var solved = false;

  function hook(name) { return box.querySelector('[data-hook="' + name + '"]'); }
  function show(el, on) { if (el) el.hidden = !on; }
  function clock() {
    var d = new Date();
    function two(n) { return (n < 10 ? "0" : "") + n; }
    return two(d.getHours()) + ":" + two(d.getMinutes()) + ":" + two(d.getSeconds());
  }
  // The "last tried" line keeps its sentence in data-last; {time} is replaced.
  function stamp(line) {
    var small = line && line.querySelector("small");
    if (!small) return;
    if (!small.hasAttribute("data-last")) small.setAttribute("data-last", small.textContent);
    small.textContent = small.getAttribute("data-last").replace(/(\d\d:\d\d:\d\d|–|-)\s*$/, clock());
  }

  function probe(onOk, onFail) {
    if (running || solved) return;
    running = true;
    clearTimeout(timer);
    fetch(url, { mode: "no-cors", cache: "no-store" }).then(function () {
      running = false;
      onOk();
    }, function () {
      running = false;
      onFail();
      if (!solved) timer = setTimeout(function () { probe(onOk, onFail); }, wait);
    });
  }

  // --- the name check of the wizard ---
  function nameCheck() {
    var help = box.querySelector("[data-rebind-help]");
    var link = box.querySelector("[data-continue-link]");
    var waiting = hook("waiting"), fail = hook("fail"), ok = hook("ok");
    var tried = false;
    function onOk() {
      solved = true;
      show(waiting, false); show(fail, false); show(help, false);
      show(ok, true); show(link, true);
      window.location.href = next;
    }
    function onFail() {
      tried = true;
      show(waiting, false); show(fail, true); show(help, true);
      show(link, false); // not while the name does not resolve
      stamp(fail);
    }
    function again() {
      if (!tried) show(waiting, true);
      probe(onOk, onFail);
    }
    var button = box.querySelector("[data-check-again]");
    if (button) button.addEventListener("click", function () { clearTimeout(timer); running = false; again(); });
    again();
  }

  // --- the trust page ---
  function trustCheck() {
    // An iPad asking for the desktop site calls itself a Mac. It has a touch
    // screen, so it gets the iPhone and iPad steps.
    if (box.getAttribute("data-device") === "mac" && box.hasAttribute("data-detected") && navigator.maxTouchPoints > 1) {
      var u = new URL(window.location.href);
      u.searchParams.set("device", "ios");
      window.location.replace(u.toString());
      return;
    }
    var downloaded = false;
    var waiting = hook("waiting"), failLine = hook("fail"), ok = hook("ok");
    var cont = box.querySelector("[data-continue-link]");
    var steps = Array.prototype.slice.call(box.querySelectorAll("ol.howto > li"));
    var failStep = box.querySelector("[data-fail-step]");

    function markDownloaded(link) {
      downloaded = true;
      var li = link.closest("li");
      if (li) {
        li.setAttribute("data-done", "");
        var p = li.querySelector("p[data-after]");
        if (p) p.textContent = p.getAttribute("data-after");
      }
      var again = link.getAttribute("data-again-text");
      if (again) link.textContent = again;
      link.classList.add("secondary");
    }
    Array.prototype.forEach.call(box.querySelectorAll("[data-download]"), function (link) {
      link.addEventListener("click", function () { markDownloaded(link); });
    });

    function onOk() {
      solved = true;
      steps.forEach(function (li) { li.setAttribute("data-done", ""); });
      Array.prototype.forEach.call(box.querySelectorAll("[data-download]"), function (link) {
        var again = link.getAttribute("data-again-text");
        if (again) link.textContent = again;
        link.classList.add("secondary");
      });
      show(waiting, false); show(failLine, false); show(ok, true); show(cont, true);
    }
    function onFail() {
      show(ok, false); show(cont, false);
      // After a download and a return to this page the check still fails: say
      // so at the step that usually goes wrong, not at the end of the list.
      if (downloaded && failLine && failStep) {
        if (failLine.parentNode !== failStep) failStep.appendChild(failLine);
        show(failLine, true);
        stamp(failLine);
        show(waiting, false);
      } else {
        show(waiting, true);
      }
      stamp(waiting);
    }
    function run() { probe(onOk, onFail); }
    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState === "visible" && !solved) { clearTimeout(timer); running = false; run(); }
    });
    // A personal device leaves the browser to install the profile; the page comes back
    // from the page cache without a visibilitychange on some browsers.
    window.addEventListener("pageshow", function () { if (!solved) { clearTimeout(timer); running = false; run(); } });
    run();
  }

  if (mode === "trust") trustCheck(); else nameCheck();
})();
