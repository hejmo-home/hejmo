// Contact form (ticket 296): "Weitere Nummer", "Weitere E-Mail-Adresse" and "Entfernen" work in place.
// Without this script they are submit buttons (name more / remove) and the server shows the form again with
// everything typed kept and one line added or removed. A line to add comes from the <template data-line> of its
// fieldset, with {n} where its number goes. The save bar says when something is not saved yet.
//
// Relations (ticket 298): the same for "Weitere Beziehung" and "Entfernen" on a relation line, and the sentence
// under each line ("Oma von Sebastian und Mia") follows the word and the chips while you type. The server writes
// the same sentence for the first render; the three small words come from the data-of, data-whole and data-and
// attributes of the card. A blank line for the whole family is left out of what is sent.

// relSentence writes how a relation reads on the contact; "" when there is nothing to say yet.
function relSentence(word, names, whole, texts) {
  word = String(word || "").trim();
  if (!word) return "";
  if (whole) return word + " " + texts.whole;
  if (!names.length) return "";
  var list = names.length === 1 ? names[0] : names.slice(0, -1).join(", ") + " " + texts.and + " " + names[names.length - 1];
  return word + " " + texts.of + " " + list;
}

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
    if (rels && (value === "rel" || /^rel\d+$/.test(value))) {
      var line = button.closest("li.rel-line");
      if (name === "more") { ev.preventDefault(); rels.add(); }
      else if (line) { ev.preventDefault(); rels.remove(line); }
      markDirty();
      return;
    }
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

  // The relation lines.
  var MAXREL = 20;
  var rels = (function () {
    var card = form.querySelector("fieldset#rels");
    if (!card) return null;
    var texts = { of: card.getAttribute("data-of") || "", whole: card.getAttribute("data-whole") || "", and: card.getAttribute("data-and") || "" };
    var ul = form.querySelector("ul.rel-lines"), tpl = form.querySelector('template[data-line="rel"]');
    function rows() { return ul.querySelectorAll("li.rel-line"); }
    function wordOf(li) { return li.querySelector('input[name$="_word"]'); }
    function wholeOf(li) { return li.querySelector("input[data-whole]"); }
    function people(li) { return Array.prototype.slice.call(li.querySelectorAll(".pchip:not(.all) input")); }

    // update writes the sentence of a line. changed is the input that was touched: the chip "Ganze Familie" is
    // put in step with the people by form.js after this runs, so "whole" is read from what was just chosen.
    function update(li, changed) {
      var say = li.querySelector(".rel-say");
      if (!say) return;
      var names = people(li).filter(function (i) { return i.checked; }).map(function (i) { return i.closest("label").textContent.trim(); });
      var whole = names.length === 0 || (changed && changed.hasAttribute && changed.hasAttribute("data-whole") && changed.checked);
      var text = relSentence(wordOf(li).value, whole ? [] : names, whole, texts);
      say.querySelector("b").textContent = text;
      say.hidden = text === "";
    }

    function renumber() {
      var all = rows();
      for (var i = 0; i < all.length; i++) {
        var n = i + 1, li = all[i];
        li.setAttribute("id", "rel" + n);
        var named = li.querySelectorAll("[name]");
        for (var j = 0; j < named.length; j++) {
          var nm = named[j].getAttribute("name");
          if (/^rel\d+_/.test(nm)) named[j].setAttribute("name", nm.replace(/^rel\d+/, "rel" + n));
        }
        var labelled = li.querySelectorAll("[aria-label]");
        for (var k = 0; k < labelled.length; k++) labelled[k].setAttribute("aria-label", labelled[k].getAttribute("aria-label").replace(/\d+/, String(n)));
        var rm = li.querySelector("button.rm");
        if (rm) rm.setAttribute("value", "rel" + n);
      }
      var more = form.querySelector('button[name="more"][value="rel"]');
      if (more) more.hidden = all.length >= MAXREL;
    }

    return {
      update: update,
      add: function () {
        var all = rows(), n = all.length + 1;
        if (!tpl || n > MAXREL) return;
        var first = all[0];
        if (first && first.classList.contains("blank")) {
          // The only line had no "Entfernen"; with two lines each has one.
          first.classList.remove("blank");
          var m = /<button[^>]*\brm\b[\s\S]*?<\/button>/.exec(tpl.innerHTML);
          if (m) first.querySelector(".f").insertAdjacentHTML("afterend", fill(m[0], 1));
        }
        ul.insertAdjacentHTML("beforeend", fill(tpl.innerHTML, n));
        renumber();
        var input = wordOf(rows()[n - 1]);
        if (input && input.focus) input.focus();
      },
      remove: function (li) {
        if (rows().length <= 1) { // the last line stays, empty and for the whole family
          wordOf(li).value = "";
          people(li).forEach(function (i) { i.checked = false; });
          wholeOf(li).checked = true;
          update(li);
          return;
        }
        li.remove();
        renumber();
      },
      // Submitting leaves out a blank line for the whole family: the server skips it anyway.
      beforeSubmit: function () {
        var all = rows();
        for (var i = 0; i < all.length; i++) {
          if (wordOf(all[i]).value.trim() === "" && wholeOf(all[i]).checked) all[i].remove();
        }
      },
      lineOf: function (el) { return el && el.closest ? el.closest("li.rel-line") : null; },
    };
  })();

  form.addEventListener("input", function (ev) {
    markDirty();
    var li = rels && rels.lineOf(ev.target);
    if (li) rels.update(li, ev.target);
  });
  form.addEventListener("change", function (ev) {
    markDirty();
    var li = rels && rels.lineOf(ev.target);
    if (li) rels.update(li, ev.target);
  });
  if (rels) form.addEventListener("submit", function () { rels.beforeSubmit(); });
  // The photo card (ticket 297): choosing a file sends the form with picture=upload. The box prepares the photo,
  // shows it as it will appear and keeps everything typed; "Speichern" stores it. A HEIC photo is converted to
  // JPEG here first, with the code of the photos page (photos.js); if that fails the file goes up as it is and
  // the box names the problem at the card. Without this script the photo goes in with "Speichern".
  function submitUpload() {
    var mark = form.querySelector('input[name="picture"][data-upload]');
    if (!mark) {
      mark = document.createElement("input");
      mark.setAttribute("type", "hidden");
      mark.setAttribute("name", "picture");
      mark.setAttribute("data-upload", "");
      mark.name = "picture";
      form.appendChild(mark);
    }
    mark.value = "upload";
    if (form.requestSubmit) form.requestSubmit(); else form.submit();
  }

  function choosePhoto(input) {
    var file = input.files && input.files[0];
    if (!file) return;
    var others = form.querySelectorAll('input[type="file"][name="photo"]');
    for (var i = 0; i < others.length; i++) if (others[i] !== input) others[i].value = ""; // one file goes up
    var photos = typeof HejmoPhotos !== "undefined" ? HejmoPhotos : null;
    if (!photos) { submitUpload(); return; }
    Promise.resolve()
      .then(function () { return photos.head(file); })
      .then(function (head) {
        if (photos.sniff(head) !== "heic") return;
        return photos.convert(file).then(function (blob) {
          var jpeg = new File([blob], photos.jpegName(file.name), { type: "image/jpeg" });
          var dt = new DataTransfer();
          dt.items.add(jpeg);
          input.files = dt.files;
        });
      })
      .then(submitUpload, submitUpload);
  }

  form.addEventListener("input", markDirty);
  form.addEventListener("change", function (ev) {
    markDirty();
    var el = ev && ev.target;
    if (el && el.matches && el.matches('input[type="file"][name="photo"]')) choosePhoto(el);
  });
})();

if (typeof module !== "undefined" && module.exports) module.exports = { relSentence: relSentence };
