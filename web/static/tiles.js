// Ticking a tile of a list posts in the background (ticket 166). If the box
// cannot be reached or answers with an error, the tile stays as it was, gets a
// red frame (aria-invalid) and the error stands under it. When the box answers
// with its redirect the page is reloaded, so the success line the box left for
// the next page is shown once. Without script the form posts as usual.
(function () {
  var script = document.currentScript;
  var message = script && script.getAttribute("data-error");
  if (!message || !window.fetch || !window.FormData) return;

  // An error line as the stylesheet knows it (.ferr): icon and text.
  function errorLine(text) {
    var ns = "http://www.w3.org/2000/svg";
    var p = document.createElement("p");
    p.className = "ferr";
    p.setAttribute("role", "alert");
    var svg = document.createElementNS(ns, "svg");
    svg.setAttribute("class", "icon");
    svg.setAttribute("width", "18");
    svg.setAttribute("height", "18");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    var circle = document.createElementNS(ns, "circle");
    circle.setAttribute("cx", "12");
    circle.setAttribute("cy", "12");
    circle.setAttribute("r", "9");
    var path = document.createElementNS(ns, "path");
    path.setAttribute("d", "M12 7.5v5.5M12 16.2v.3");
    svg.appendChild(circle);
    svg.appendChild(path);
    var span = document.createElement("span");
    span.appendChild(document.createTextNode(text));
    p.appendChild(svg);
    p.appendChild(span);
    return p;
  }

  document.addEventListener("submit", function (e) {
    var form = e.target;
    var tile = form && form.closest && form.closest("ul.tiles > li");
    if (!tile) return;
    e.preventDefault();
    var old = tile.querySelector(".ferr");
    if (old) tile.removeChild(old);
    tile.removeAttribute("aria-invalid");
    fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)), credentials: "same-origin", redirect: "manual" })
      .then(function (r) {
        // "manual": the box's 303 comes back as an opaque redirect, which is success.
        if (r.type !== "opaqueredirect" && !r.ok) throw new Error("tick " + r.status);
        location.reload();
      })
      .catch(function () {
        tile.appendChild(errorLine(message));
        tile.setAttribute("aria-invalid", "true");
      });
  });
})();
