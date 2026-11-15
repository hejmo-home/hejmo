// Choose calendars: the number of events of each calendar is asked of the
// server after the page has been shown, so a slow calendar server cannot hold
// the page up. Every calendar says "Counting events…" until its answer
// arrives; if the answer fails, the count stays "unknown" and the rest of the
// page works as before. Without this script the line says that the count
// needs JavaScript.
(function () {
  var form = document.querySelector("form[data-counts-url]");
  if (!form) return;
  var cells = form.querySelectorAll("[data-count-for]");
  var loading = form.getAttribute("data-count-loading") || "…";
  var unknown = form.getAttribute("data-count-unknown") || "?";
  for (var i = 0; i < cells.length; i++) {
    cells[i].textContent = loading;
    cells[i].setAttribute("data-state", "loading");
  }
  function show(cell, state, text) {
    cell.setAttribute("data-state", state);
    cell.textContent = text;
  }
  fetch(form.getAttribute("data-counts-url"), { credentials: "same-origin", headers: { Accept: "application/json" } })
    .then(function (res) {
      if (!res.ok) throw new Error(String(res.status));
      return res.json();
    })
    .then(function (counts) {
      var byUrl = {};
      counts.forEach(function (c) { byUrl[c.url] = c; });
      for (var i = 0; i < cells.length; i++) {
        var c = byUrl[cells[i].getAttribute("data-count-for")];
        if (c) show(cells[i], c.state, c.text);
        else show(cells[i], "unknown", unknown);
      }
    })
    .catch(function () {
      for (var i = 0; i < cells.length; i++) show(cells[i], "unknown", unknown);
    });
})();
