// The recipe form (ticket 279). Everything works without this script: the buttons that add, move and remove
// lines post the form, and the server shows it again with everything typed kept. With the script they work in
// place, the portions have their stepper, the tips count their characters and an ingredient gets suggestions
// from the library while you type (the server writes the list, as for the list adder). A line to add comes
// from the <template data-line> of its fieldset, with {n} where its number goes.

var RCP_MIN_LETTERS = 2;

// rcpFill puts a line's number into the text of a template.
function rcpFill(html, n) { return String(html).split("{n}").join(String(n)); }

// rcpSuggestUrl is the address of the suggestions for what was typed in an ingredient line, "" for less than two letters.
function rcpSuggestUrl(base, typed) {
  var q = String(typed || "").replace(/\s+/g, " ").trim();
  if (Array.from(q).length < RCP_MIN_LETTERS) return "";
  return base + "?q=" + encodeURIComponent(q);
}

// rcpOptionText is what a chosen suggestion writes into the name field: the name, and after a middle dot the variety.
function rcpOptionText(name, variety) {
  name = String(name || "").trim();
  variety = String(variety || "").trim();
  return variety ? name + " · " + variety : name;
}

// rcpOptionFields is what choosing a suggestion writes into the line: the text of the name field, the article and
// the variety (cleared when the option has none, so one left from another article does not stay). Free text has
// no article and no variety; its text stays as typed.
function rcpOptionFields(opt) {
  if (opt.free) return { article: "", variety: "" };
  var variety = String(opt.variety || "").trim();
  return { text: rcpOptionText(opt.name, variety), article: opt.article || "", variety: variety };
}

// rcpKeepsVariety says whether a line keeps its variety when its name changes from `was` to `now`: yes while
// the name before the middle dot is still the same one (ignoring case and spaces), and for a line that had no name.
function rcpKeepsVariety(was, now) {
  var key = function (t) { return String(t || "").split("·")[0].replace(/\s+/g, " ").trim().toLowerCase(); };
  return key(was) === "" || key(was) === key(now);
}

// rcpStep moves a number one step up or down within 1 and 99 (portions).
function rcpStep(value, dir) {
  var n = parseInt(String(value).trim(), 10);
  if (isNaN(n)) n = dir > 0 ? 0 : 2;
  return Math.min(99, Math.max(1, n + (dir > 0 ? 1 : -1)));
}

// rcpCounter writes "114 von 2000 Zeichen" from the text with %d in it, counting letters the way a person does.
function rcpCounter(format, text, max) {
  var n = Array.from(String(text)).length, i = 0;
  return String(format).replace(/%d/g, function () { return String(i++ === 0 ? n : max); });
}

// rcpLabel puts a line's number into the first number of a label: "Zutat 3 entfernen: Salz".
function rcpLabel(label, n) { return String(label).replace(/\d+/, String(n)); }

// rcpAfterRemoving says which line the focus goes to after one was removed: the next, else the one before.
function rcpFocusAfter(count, removed) { return Math.min(removed, count - 1); }

(function () {
  var form = document.querySelector("form.rcp-form");
  if (!form) return;
  var base = form.getAttribute("data-suggest");
  var MAX = { ing: 100, step: 100 };

  var named = form.querySelectorAll("input.in");
  // Only lines on the page at load (and chosen options) carry data-was; a line added later has none, which rcpKeepsVariety reads as "no name before".
  for (var q = 0; q < named.length; q++) named[q].setAttribute("data-was", named[q].value);

  function list(kind) { return form.querySelector('[data-lines="' + kind + '"]'); }
  function template(kind) { return form.querySelector('template[data-line="' + kind + '"]'); }
  function rows(kind) { return list(kind).querySelectorAll(kind === "ing" ? "li.ing" : "li.step"); }
  function field(li, sel) { return li.querySelector(sel); }
  function blank(kind, li) {
    if (kind === "ing") {
      return !li.querySelector("input.iq").value.trim() && !li.querySelector("select.iu").value && !li.querySelector("input.in").value.trim();
    }
    return !li.querySelector("textarea").value.trim();
  }

  function renumber(kind) {
    var all = rows(kind);
    var tpl = template(kind);
    var acts = tpl && tpl.content ? tpl.content.querySelector(".acts") : null;
    for (var i = 0; i < all.length; i++) {
      var n = i + 1, li = all[i];
      li.setAttribute("id", kind + n);
      var labelled = li.querySelectorAll("[aria-label]");
      for (var j = 0; j < labelled.length; j++) labelled[j].setAttribute("aria-label", rcpLabel(labelled[j].getAttribute("aria-label"), n));
      var named = li.querySelectorAll('button[name="act"]');
      for (var k = 0; k < named.length; k++) named[k].setAttribute("value", named[k].getAttribute("value").replace(/\d+$/, String(n)));
      var dl = li.querySelector("datalist"), inp = li.querySelector("input[list]");
      if (dl && inp) { dl.setAttribute("id", "var" + n); inp.setAttribute("list", "var" + n); }
      if (kind === "step" && acts) {
        // The buttons of a step: the first cannot go up, none goes down below the empty line at the end.
        var isBlank = blank(kind, li), box = li.querySelector(".acts");
        box.innerHTML = rcpFill(acts.innerHTML, n);
        if (!isBlank) {
          if (i === 0) { var up = box.querySelector('[value^="up-step-"]'); if (up) up.remove(); }
          if (i === all.length - 1 || blank(kind, all[i + 1])) { var down = box.querySelector('[value^="down-step-"]'); if (down) down.remove(); }
        }
      }
    }
  }

  function add(kind) {
    var tpl = template(kind), n = rows(kind).length + 1;
    if (!tpl || n > MAX[kind]) return;
    list(kind).insertAdjacentHTML("beforeend", rcpFill(tpl.innerHTML, n));
    renumber(kind);
    var li = rows(kind)[n - 1];
    var first = li.querySelector("input.iq, textarea");
    if (first) first.focus();
  }

  function remove(kind, n) {
    var all = rows(kind);
    if (n < 1 || n > all.length) return;
    all[n - 1].remove();
    renumber(kind);
    var left = rows(kind);
    if (left.length) {
      var focus = left[rcpFocusAfter(left.length, n - 1)].querySelector("input.in, textarea");
      if (focus) focus.focus();
    }
  }

  function move(n, dir) {
    var all = rows("step"), at = n - 1, to = at + dir;
    if (at < 0 || to < 0 || to >= all.length) return;
    var a = all[at].querySelector("textarea"), b = all[to].querySelector("textarea");
    var t = a.value; a.value = b.value; b.value = t;
    renumber("step");
    all[to].querySelector("textarea").focus();
  }

  function dirty() {
    var d = form.querySelector(".savebar .dirty");
    if (d) d.hidden = false;
  }

  form.addEventListener("click", function (e) {
    var button = e.target && e.target.closest ? e.target.closest("button") : null;
    if (!button || button.getAttribute("name") !== "act") {
      var step = button && button.getAttribute("data-step");
      if (step) {
        e.preventDefault();
        var input = form.querySelector("#portions");
        input.value = String(rcpStep(input.value, step === "up" ? 1 : -1));
        dirty();
      }
      return;
    }
    var act = button.getAttribute("value") || "", m = /^(more|rm|up|down)-(ing|step)(?:-(\d+))?$/.exec(act);
    if (!m) return;
    e.preventDefault();
    if (m[1] === "more") add(m[2]);
    else if (m[1] === "rm") remove(m[2], parseInt(m[3], 10));
    else move(parseInt(m[3], 10), m[1] === "up" ? -1 : 1);
    dirty();
  });

  // Suggestions under an ingredient line.
  var timer = null, serial = 0;
  function close(li) {
    var s = li.querySelector(".sugg");
    if (s) s.remove();
    var name = li.querySelector("input.in");
    if (name) name.setAttribute("aria-expanded", "false");
  }
  function ask(li, typed) {
    var url = rcpSuggestUrl(base, typed);
    if (!url || typeof fetch !== "function") { close(li); return; }
    var mine = ++serial;
    fetch(url + "&n=" + li.id.replace(/^ing/, ""), { credentials: "same-origin", headers: { Accept: "text/html" } }).then(function (r) { return r.ok ? r.text() : ""; }).then(function (html) {
      if (mine !== serial) return;
      close(li);
      if (!html.trim()) return;
      li.insertAdjacentHTML("beforeend", html);
      li.querySelector("input.in").setAttribute("aria-expanded", "true");
    }).catch(function () { close(li); });
  }
  form.addEventListener("input", function (e) {
    var el = e.target;
    if (el.matches && el.matches("textarea[name=tips]")) {
      var c = form.querySelector(".counter[data-format]");
      if (c) c.textContent = rcpCounter(c.getAttribute("data-format"), el.value, parseInt(c.getAttribute("data-max"), 10));
    }
    if (el.matches && el.matches("input.in")) {
      var li = el.closest("li.ing");
      li.querySelector('input[name="ing_article"]').value = ""; // typing again: Hejmo matches the name when it saves
      var vi = li.querySelector('input[name="ing_variety"]');
      if (vi && !rcpKeepsVariety(el.getAttribute("data-was"), el.value)) vi.value = ""; // another name, another article: not this variety
      clearTimeout(timer);
      timer = setTimeout(function () { ask(li, el.value); }, 200);
    }
    dirty();
  });
  form.addEventListener("click", function (e) {
    var opt = e.target && e.target.closest ? e.target.closest('li[role="option"]') : null;
    if (!opt) return;
    var li = opt.closest("li.ing");
    if (!li) return;
    e.preventDefault();
    var name = li.querySelector("input.in");
    var f = rcpOptionFields({ free: opt.hasAttribute("data-free"), name: opt.getAttribute("data-name"), variety: opt.getAttribute("data-variety"), article: opt.getAttribute("data-article") });
    if (f.text !== undefined) name.value = f.text;
    name.setAttribute("data-was", name.value);
    li.querySelector('input[name="ing_article"]').value = f.article;
    li.querySelector('input[name="ing_variety"]').value = f.variety;
    serial++;
    close(li);
    name.focus();
    dirty();
  });
})();
