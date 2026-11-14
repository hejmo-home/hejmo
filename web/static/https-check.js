// The wizard's name check (ADR 0014). Only the browser knows whether the
// router lets the box's name resolve to its LAN address, so the browser tries
// to reach the box under that name. A no-cors request succeeds when the name
// resolves and the certificate is trusted, and fails otherwise. The page then
// shows the router help and tries again until it works.
(function () {
  var box = document.querySelector("[data-check]");
  if (!box) return;
  var url = box.getAttribute("data-check-url");
  var next = box.getAttribute("data-continue-url");
  var wait = parseInt(box.getAttribute("data-retry-seconds"), 10) * 1000;
  var help = box.querySelector("[data-rebind-help]");
  var waiting = box.querySelector("[data-check-waiting]");
  var ok = box.querySelector("[data-check-ok]");
  var timer = null;

  function check() {
    clearTimeout(timer);
    if (help.hidden) waiting.hidden = false;
    fetch(url, { mode: "no-cors", cache: "no-store" }).then(function () {
      waiting.hidden = true;
      help.hidden = true;
      ok.hidden = false;
      window.location.href = next;
    }, function () {
      waiting.hidden = true;
      help.hidden = false;
      timer = setTimeout(check, wait);
    });
  }

  box.querySelector("[data-check-again]").addEventListener("click", check);
  check();
})();
