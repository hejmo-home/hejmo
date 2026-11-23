// Contact form (ticket 296): "Weitere Nummer", "Weitere E-Mail-Adresse" and "Entfernen" work in place.
// Without this script they are submit buttons (name more / remove) and the server shows the form again with
// everything typed kept and one line added or removed. A line to add comes from the <template data-line> of its
// fieldset, with {n} where its number goes. The save bar says when something is not saved yet.
(function () {
  var form = document.querySelector("form.ct-form");
  if (!form) return;
  var MAX = 10;

  function fill(html, n) { return html.split("{n}").join(String(n)); }
  function rmHtml(html, n) {
    var m = /<button[^>]*\brm\b[\s\S]*?<\/button>/.exec(html);
    return m ? fill(m[0], n) : "";
  }
  function list(kind) { return form.querySelector('ul.lines[data-lines="' + kind + '"]'); }
  function template(kind) { return form.querySelector('template[data-line="' + kind + '"]'); }
  function rows(kind) { return list(kind).querySelectorAll("li.line"); }

  function renumber(kind) {
    var all = rows(kind);
    for (var i = 0; i < all.length; i++) {
      var n = i + 1, li = all[i];
      li.setAttribute("id", kind + n);
      var labelled = li.querySelectorAll("[aria-label]");
      for (var j = 0; j < labelled.length; j++) {
        labelled[j].setAttribute("aria-label", labelled[j].getAttribute("aria-label").replace(/\d+/, String(n)));
      }
      var rm = li.querySelector("button.rm");
      if (rm) rm.setAttribute("value", kind + n);
    }
    var more = form.querySelector('button[name="more"][value="' + kind + '"]');
    if (more) more.hidden = all.length >= MAX;
  }

  function add(kind) {
    var tpl = template(kind), all = rows(kind), n = all.length + 1;
    if (!tpl || n > MAX) return;
    var first = all[0];
    if (first && first.classList.contains("blank")) {
      // The only line had no "Entfernen"; with two lines each has one.
      first.classList.remove("blank");
      first.querySelector("input.val").insertAdjacentHTML("afterend", rmHtml(tpl.innerHTML, 1));
    }
    list(kind).insertAdjacentHTML("beforeend", fill(tpl.innerHTML, n));
    renumber(kind);
    var made = rows(kind)[n - 1], input = made && made.querySelector("input.val");
    if (input && input.focus) input.focus();
  }

  function remove(kind, li) {
    if (rows(kind).length <= 1) { // the last line stays, empty
      var inputs = li.querySelectorAll("input");
      for (var i = 0; i < inputs.length; i++) inputs[i].value = "";
      var sel = li.querySelector("select");
      if (sel) sel.selectedIndex = 0;
      return;
    }
    li.remove();
    renumber(kind);
  }

  form.addEventListener("click", function (ev) {
    var button = ev.target && ev.target.closest && ev.target.closest('button[name="more"], button[name="remove"]');
    if (!button) return;
    var name = button.getAttribute("name"), value = button.getAttribute("value") || "";
    if (name === "more") {
      ev.preventDefault();
      add(value);
    } else if (name === "remove") {
      var li = button.closest("li.line"), kind = value.replace(/\d+$/, "");
      if (!li || !list(kind)) return;
      ev.preventDefault();
      remove(kind, li);
    }
    markDirty();
  });

  function markDirty() {
    var d = form.querySelector(".savebar .dirty");
    if (d) d.hidden = false;
  }
  form.addEventListener("input", markDirty);
  form.addEventListener("change", markDirty);
})();
