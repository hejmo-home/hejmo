// "Copy token" (ticket 111): shown only when a script runs; without one the
// token stays plain, selectable text.
(function () {
  document.querySelectorAll("button[data-copy]").forEach(function (btn) {
    btn.hidden = false;
    btn.addEventListener("click", function () {
      var done = function () {
        var label = btn.textContent;
        btn.textContent = btn.getAttribute("data-copied") || label;
        setTimeout(function () { btn.textContent = label; }, 2000);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(btn.getAttribute("data-copy")).then(done, selectToken);
      } else {
        selectToken();
      }
    });
  });
  function selectToken() {
    var el = document.querySelector("[data-device-token], [data-invite-link]");
    if (!el) return;
    var range = document.createRange();
    range.selectNodeContents(el);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }
})();
