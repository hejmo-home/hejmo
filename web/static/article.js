// The own article form (ticket 164): similar articles appear under the name as
// you type, the button that fetches a web image says it is working, and Enter
// in the address field fetches instead of saving. Without this script the form
// works the same, only without the live list and the waiting line.
(function () {
  var DELAY = 200;

  // list builds the box under the name from the answer of the box.
  function list(doc, box, items, self) {
    while (box.firstChild) box.removeChild(box.firstChild);
    items = items.filter(function (it) { return it.Href.split("/").pop() !== self; });
    box.hidden = items.length === 0;
    if (!items.length) return;
    var q = doc.createElement("p");
    q.className = "place-q";
    q.id = "sl-q";
    q.textContent = items.length === 1 ? box.getAttribute("data-one") : box.getAttribute("data-many").replace("{n}", String(items.length));
    var ul = doc.createElement("ul");
    ul.className = "place-list art-sugg";
    ul.id = "sl";
    ul.setAttribute("role", "listbox");
    ul.setAttribute("aria-labelledby", "sl-q");
    items.forEach(function (it, i) {
      var li = doc.createElement("li");
      li.setAttribute("role", "option");
      li.id = "s-" + i;
      var a = doc.createElement("a");
      a.setAttribute("href", it.Href);
      var sym = doc.createElement("span");
      sym.className = "art-sym";
      sym.setAttribute("aria-hidden", "true");
      if (it.Picture) {
        var img = doc.createElement("img");
        img.setAttribute("src", it.Picture);
        img.setAttribute("alt", "");
        sym.appendChild(img);
      } else {
        sym.textContent = it.Symbol;
      }
      var b = doc.createElement("b");
      b.textContent = it.Name;
      var small = doc.createElement("small");
      small.textContent = it.Sub;
      a.appendChild(sym);
      a.appendChild(b);
      a.appendChild(small);
      li.appendChild(a);
      ul.appendChild(li);
    });
    var more = doc.createElement("p");
    more.className = "hint";
    more.textContent = box.getAttribute("data-more");
    box.appendChild(q);
    box.appendChild(ul);
    box.appendChild(more);
  }

  function suggest(root, doc, fetchFn, setTimer, clearTimer) {
    var field = root.querySelector("textarea[name=name]");
    var box = root.querySelector("[data-suggest-box]");
    if (!field || !box || !fetchFn) return;
    var base = root.getAttribute("data-article-suggest");
    var self = root.getAttribute("data-self") || "";
    var timer = null;
    var latest = 0;
    field.addEventListener("input", function () {
      if (timer) clearTimer(timer);
      var text = field.value.trim();
      var seq = ++latest;
      if (text.length < 2) { list(doc, box, [], self); return; }
      timer = setTimer(function () {
        fetchFn(base + "?q=" + encodeURIComponent(text), { credentials: "same-origin" })
          .then(function (r) { return r.ok ? r.json() : { items: [] }; })
          .then(function (data) { if (seq === latest) list(doc, box, data.items || [], self); })
          .catch(function () {});
      }, DELAY);
    });
  }

  // waiting turns the fetch button into the waiting state while the page loads.
  function waiting(form, doc, setTimer) {
    var btn = form.querySelector("[data-fetch]");
    var url = form.querySelector("[data-fetch-url]");
    if (!btn || !url) return;
    form.addEventListener("submit", function (e) {
      if (e.submitter !== btn) return;
      var host = url.value.replace(/^\s*[a-z]+:\/\//i, "").split(/[\/?#]/)[0];
      var hidden = doc.createElement("input");
      hidden.type = "hidden";
      hidden.name = "fetch";
      hidden.value = "1";
      form.appendChild(hidden);
      var line = doc.createElement("p");
      line.className = "checkline";
      line.setAttribute("data-state", "waiting");
      line.setAttribute("role", "status");
      var text = doc.createElement("span");
      var main = doc.createElement("span");
      main.textContent = btn.getAttribute("data-waiting").replace("{host}", host);
      var small = doc.createElement("small");
      small.textContent = btn.getAttribute("data-waiting-small");
      text.appendChild(main);
      text.appendChild(small);
      line.appendChild(text);
      btn.parentNode.parentNode.appendChild(line);
      btn.textContent = btn.getAttribute("data-busy");
      setTimer(function () { btn.disabled = true; }, 0);
    });
    url.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); btn.click(); }
    });
  }

  if (typeof document === "undefined") return;
  var root = document.querySelector("[data-article-suggest]");
  var form = root && root.closest ? root.closest("form") : null;
  suggest(root || document, document, typeof fetch === "function" ? fetch : null, setTimeout, clearTimeout);
  if (form) waiting(form, document, setTimeout);
})();
