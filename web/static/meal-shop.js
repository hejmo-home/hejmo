// The tick list of the meal plan (ticket 283): the count sentence and the main
// button follow the ticks at once, and a changed list or meal asks the box to
// work out the rows again. Without this script the server words the same
// sentences when the form is sent.
(function () {
  function fill(text, pairs) {
    return text.replace(/\{(\w+)\}/g, function (m, k) { return pairs[k] === undefined ? m : String(pairs[k]); });
  }

  // sentence words the count and the button, as the box does (internal/server/meal_shop.go).
  function sentence(p, c) {
    var on = c.tickedOn + c.same;
    var head, button;
    if (c.ticked === 0) {
      head = p["head.none"];
      button = p["button.none"];
      return { head: head, text: "", button: button };
    }
    if (c.ticked === 1) {
      head = p["head.one"];
      button = p["button.one"];
    } else {
      head = fill(p["head.many"], { n: c.ticked });
      button = fill(p["button.many"], { n: c.ticked });
    }
    if (c.tickedNew === 0) head = p["head.nothing"];
    var text = fill(p.count, { "new": c.tickedNew, on: on });
    if (c.left) text += " " + c.left;
    if (c.pantryLeft === 1) text += " " + p["pantry.one"];
    else if (c.pantryLeft > 1) text += " " + fill(p["pantry.many"], { n: c.pantryLeft });
    return { head: head, text: text, button: button };
  }

  var api = { sentence: sentence };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  if (typeof document === "undefined") return;

  var form = document.querySelector("form[data-shop]");
  if (!form) return;

  function recount() {
    var sum = form.querySelector(".shop-sum");
    if (!sum || !sum.getAttribute("data-patterns")) return;
    var c = { ticked: 0, tickedNew: 0, tickedOn: 0, same: Number(sum.getAttribute("data-same")) || 0, pantryLeft: 0, left: sum.getAttribute("data-left") || "" };
    form.querySelectorAll("li[data-out]").forEach(function (li) {
      var box = li.querySelector("input[name=pick]");
      if (!box) return;
      if (box.checked) {
        c.ticked++;
        if (li.getAttribute("data-out") === "new") c.tickedNew++; else c.tickedOn++;
      } else if (li.hasAttribute("data-pantry")) {
        c.pantryLeft++;
      }
    });
    var r = sentence(JSON.parse(sum.getAttribute("data-patterns")), c);
    var p = sum.querySelector(".shop-count");
    p.textContent = "";
    var b = document.createElement("b");
    b.textContent = r.head;
    p.appendChild(b);
    if (r.text) p.appendChild(document.createTextNode(" " + r.text));
    var btn = form.querySelector("[data-shop-button]");
    if (btn) btn.textContent = r.button;
  }

  // A meal or the list changed: the rows depend on it, so the box works them out again.
  var busy = false;
  function again() {
    if (busy || !window.fetch || !window.DOMParser) return;
    busy = true;
    var data = new FormData(form);
    data.set("recount", "1");
    fetch(form.getAttribute("action"), { method: "POST", body: new URLSearchParams(data), credentials: "same-origin", headers: { "Content-Type": "application/x-www-form-urlencoded" } })
      .then(function (res) { return res.ok ? res.text() : Promise.reject(res.status); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var next = doc.querySelector("form[data-shop]");
        if (next) form.innerHTML = next.innerHTML;
      })
      .catch(function () { form.submit(); })
      .then(function () { busy = false; });
  }

  form.addEventListener("change", function (e) {
    var t = e.target;
    if (t.name === "pick") recount();
    else if (t.name === "meal" || t.name === "list") again();
  });
})();
