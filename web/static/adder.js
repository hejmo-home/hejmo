// The add card and the editor of a quantity (ticket 165, ADR 0019). Without
// this script they work as plain forms: the text is added as typed, the
// amount is typed. With it, articles of the library are suggested from two
// letters, a tap chooses one, - and + step the amount, a unit of the same kind
// converts the number in the editor, and the sentence under the unit says
// the result before anything is saved. Every sentence and every list of
// suggestions comes from the box (…/suggest, …/quantity); the rules live
// there, this script only asks and shows. Used on the web and on the displays.
(function () {
  var MAX = 9999;
  var EPS = 1e-9;

  // ---- the arithmetic of the steppers, as in internal/unit

  function step(unitKey) { return unitKey === "g" || unitKey === "ml" ? 50 : 1; }

  // parse reads an amount typed with a comma or a point; NaN when it is none.
  function parse(text) {
    text = String(text).replace(/^\s+|\s+$/g, "");
    if (!/^\d+([.,]\d+)?$/.test(text)) return NaN;
    var v = parseFloat(text.replace(",", "."));
    return v > 0 && v <= MAX ? v : NaN;
  }

  function stepUp(amount, st) { return Math.min(MAX, (Math.floor(amount / st + EPS) + 1) * st); }

  // stepDown goes to the next whole step below, or null when that is below one step.
  function stepDown(amount, st) {
    var v = (Math.ceil(amount / st - EPS) - 1) * st;
    return v < st - EPS ? null : v;
  }

  // show writes a number as typed: at most 4 decimals, a comma in German.
  function show(v, lang) {
    var s = String(Math.round(v * 10000) / 10000);
    return lang === "en" ? s : s.replace(".", ",");
  }

  // convert keeps the amount when the unit changes within one kind.
  function convert(amount, fromSize, toSize) { return amount * fromSize / toSize; }

  // markConverted tells the box that this page converts the number of the editor
  // when a unit is tapped; a post without it comes from a page without the script.
  function markConverted(form) {
    var c = form.querySelector('input[name="converted"]');
    if (c) c.value = "1";
  }

  var api = { step: step, parse: parse, stepUp: stepUp, stepDown: stepDown, show: show, convert: convert, markConverted: markConverted };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  if (typeof document === "undefined" || !document.addEventListener) return;

  var lang = (document.documentElement.getAttribute("lang") || "de").slice(0, 2);
  var timers = {};
  function later(key, fn, ms) { clearTimeout(timers[key]); timers[key] = setTimeout(fn, ms); }

  function closest(el, sel) { return el && el.closest ? el.closest(sel) : null; }

  function checked(root) {
    var u = root.querySelector('input[type="radio"][name="unit"]:checked, input[type="radio"][name="qunit"]:checked');
    return u;
  }

  // ---- mode, remembered per list on this device

  function modeKey(form) { return "hejmo-add-mode:" + (form.getAttribute("data-adder") || ""); }
  function setMode(form, mode, save) {
    form.setAttribute("data-mode", mode);
    var r = form.querySelector('input[name="mode"][value="' + mode + '"]');
    if (r) r.checked = true;
    if (save) { try { localStorage.setItem(modeKey(form), mode); } catch (e) {} }
  }
  function restoreMode(form) {
    if (form.getAttribute("data-mode-set")) return;
    form.setAttribute("data-mode-set", "1");
    var mode = null;
    try { mode = localStorage.getItem(modeKey(form)); } catch (e) {}
    if (mode === "qty" || mode === "quick") setMode(form, mode, false);
  }

  // ---- the parts of the card that the box sends

  function parts(html) {
    var t = document.createElement("div");
    t.innerHTML = html;
    return t;
  }

  var SUGG = ".sugg, .sugg-hidden, .sugg-unchecked";

  function putSuggestions(form, html) {
    var old = form.querySelectorAll(SUGG);
    for (var i = 0; i < old.length; i++) old[i].parentNode.removeChild(old[i]);
    var input = form.querySelector("#text");
    var holder = parts(html);
    var after = form.querySelector(".adder-text");
    var nodes = holder.children, list = [];
    for (var j = 0; j < nodes.length; j++) list.push(nodes[j]);
    list.forEach(function (n) { after.parentNode.insertBefore(n, after.nextSibling ? after.nextSibling : null); after = n; });
    if (input) input.setAttribute("aria-expanded", form.querySelector(".sugg") ? "true" : "false");
  }

  function putQuantity(form, html) {
    var old = form.querySelectorAll(".picked, fieldset.qty");
    var anchor = form.querySelector(".adder-go");
    for (var i = 0; i < old.length; i++) old[i].parentNode.removeChild(old[i]);
    var holder = parts(html), nodes = holder.children, list = [];
    for (var j = 0; j < nodes.length; j++) list.push(nodes[j]);
    list.forEach(function (n) { anchor.parentNode.insertBefore(n, anchor); });
  }

  function dropResult(form) {
    var m = form.querySelector(":scope > .msg");
    if (m) m.parentNode.removeChild(m);
  }

  function ask(url, then) {
    if (!window.fetch) return;
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) { return r.ok ? r.text() : ""; }).then(then).catch(function () {});
  }

  function mode(form) { return form.getAttribute("data-mode") === "qty" ? "qty" : "quick"; }

  function loadSuggestions(form) {
    var base = form.getAttribute("data-adder");
    var q = form.querySelector("#text").value;
    if (q.replace(/\s/g, "").length < 2) { putSuggestions(form, ""); return; }
    ask(base + "/suggest?q=" + encodeURIComponent(q) + "&mode=" + mode(form), function (html) { putSuggestions(form, html); });
  }

  function loadQuantity(form, article) {
    var base = form.getAttribute("data-adder");
    var amount = form.querySelector('input[name="amount"]');
    var u = form.querySelector('input[name="unit"]:checked');
    var url = base + "/quantity?article=" + encodeURIComponent(article || "") + "&text=" + encodeURIComponent(form.querySelector("#text").value) +
      "&amount=" + encodeURIComponent(amount && amount.value ? amount.value : "1") + (u && !article ? "&unit=" + encodeURIComponent(u.value) : "");
    ask(url, function (html) { if (html) putQuantity(form, html); });
  }

  // refreshSentence asks for the sentence under the unit and puts it in.
  function refreshSentence(form) {
    var holder = form.querySelector("[data-merge]");
    if (!holder) return;
    var base = form.getAttribute("data-adder");
    if (!base) { // the editor of an entry
      var action = form.getAttribute("action") || "";
      base = action.replace(/\/entries\/[^/]+\/quantity$/, "");
      var entry = (action.match(/\/entries\/([^/]+)\/quantity$/) || [])[1];
      var a = form.querySelector('input[name="amount"]');
      var u = form.querySelector('input[name="qunit"]:checked');
      var none = form.getAttribute("data-mode") === "none" ? "1" : "";
      ask(base + "/quantity?entry=" + encodeURIComponent(entry) + "&amount=" + encodeURIComponent(a ? a.value : "") + "&unit=" + encodeURIComponent(u ? u.value : "") + (none ? "&none=1" : ""), function (html) { holder.innerHTML = html; });
      return;
    }
    if (mode(form) !== "qty") return;
    var art = form.querySelector('input[name="article"]').value;
    var amount = form.querySelector('input[name="amount"]');
    var unit = form.querySelector('input[name="unit"]:checked');
    ask(base + "/quantity?part=merge&article=" + encodeURIComponent(art) + "&text=" + encodeURIComponent(form.querySelector("#text").value) +
      "&amount=" + encodeURIComponent(amount ? amount.value : "1") + "&unit=" + encodeURIComponent(unit ? unit.value : ""), function (html) { holder.innerHTML = html; });
  }

  function syncMinus(root) {
    var amount = root.querySelector('.amount input[type="text"]');
    var minus = root.querySelector('[data-step="down"]');
    var u = checked(root);
    if (!amount || !minus) return;
    var v = parse(amount.value);
    minus.disabled = isNaN(v) || stepDown(v, step(u ? u.value : "piece")) === null;
  }

  // ---- choosing an article

  function choose(form, li) {
    var input = form.querySelector("#text");
    var hidden = form.querySelector('input[name="article"]');
    if (li.hasAttribute("data-free")) {
      hidden.value = "";
      putSuggestions(form, "");
      if (mode(form) === "qty") loadQuantity(form, "");
      else form.requestSubmit ? form.requestSubmit() : form.submit();
      return;
    }
    var name = li.querySelector("b").textContent;
    input.value = name;
    hidden.value = li.getAttribute("data-article");
    putSuggestions(form, "");
    if (mode(form) === "qty") {
      loadQuantity(form, hidden.value);
    } else {
      form.requestSubmit ? form.requestSubmit() : form.submit();
    }
  }

  function move(form, delta) {
    var opts = form.querySelectorAll('.sugg [role="option"]');
    if (!opts.length) return false;
    var at = -1;
    for (var i = 0; i < opts.length; i++) if (opts[i].getAttribute("aria-selected") === "true") at = i;
    if (at >= 0) opts[at].removeAttribute("aria-selected");
    at = (at + delta + opts.length) % opts.length;
    opts[at].setAttribute("aria-selected", "true");
    form.querySelector("#text").setAttribute("aria-activedescendant", opts[at].id);
    return true;
  }

  // ---- events

  document.addEventListener("input", function (e) {
    var t = e.target, form = closest(t, "form.adder");
    if (form && t.id === "text") {
      form.querySelector('input[name="article"]').value = "";
      dropResult(form);
      var picked = form.querySelector(".picked");
      if (picked) { picked.parentNode.removeChild(picked); }
      later("sugg", function () { loadSuggestions(form); }, 150);
      return;
    }
    if (form && t.name === "amount") { syncMinus(form); later("merge", function () { refreshSentence(form); }, 250); return; }
    var ed = closest(t, "form.qty-edit");
    if (ed && t.name === "amount") { syncMinus(ed); later("merge-e", function () { refreshSentence(ed); }, 250); }
  });

  document.addEventListener("change", function (e) {
    var t = e.target, form = closest(t, "form.adder"), ed = closest(t, "form.qty-edit");
    if (form && t.name === "mode") { setMode(form, t.value, true); if (t.value === "qty") refreshSentence(form); loadSuggestions(form); return; }
    if (form && t.name === "unit") { syncMinus(form); refreshSentence(form); return; }
    if (ed && t.name === "qmode") { ed.setAttribute("data-mode", t.value); refreshSentence(ed); return; }
    if (ed && t.name === "qunit") {
      var amount = ed.querySelector('input[name="amount"]');
      var prev = ed.getAttribute("data-unit") ? ed.querySelector('input[name="qunit"][value="' + ed.getAttribute("data-unit") + '"]') : null;
      var v = amount ? parse(amount.value) : NaN;
      if (prev && !isNaN(v) && prev.getAttribute("data-kind") === t.getAttribute("data-kind") && t.getAttribute("data-kind") !== "pack" && t.getAttribute("data-kind") !== "bunch") {
        amount.value = show(convert(v, parseFloat(prev.getAttribute("data-size")), parseFloat(t.getAttribute("data-size"))), lang);
      }
      ed.setAttribute("data-unit", t.value);
      syncMinus(ed);
      refreshSentence(ed);
    }
  });

  document.addEventListener("click", function (e) {
    var t = e.target;
    var li = closest(t, ".sugg li[role=option]");
    if (li) { var f = closest(li, "form.adder"); if (f) { e.preventDefault(); choose(f, li); } return; }
    var btn = closest(t, "[data-step]");
    if (btn && closest(btn, ".amount")) {
      e.preventDefault();
      var root = closest(btn, "form");
      var amount = root.querySelector('.amount input[type="text"]');
      var u = checked(root);
      var st = step(u ? u.value : "piece");
      var v = parse(amount.value);
      if (isNaN(v)) v = 0;
      var next = btn.getAttribute("data-step") === "up" ? stepUp(v, st) : stepDown(v, st);
      if (next !== null) amount.value = show(next, lang);
      syncMinus(root);
      if (root.classList.contains("adder")) refreshSentence(root); else refreshSentence(root);
      return;
    }
    var change = closest(t, ".picked [data-change]");
    if (change) {
      var form = closest(change, "form.adder");
      form.querySelector('input[name="article"]').value = "";
      var p = form.querySelector(".picked");
      if (p) p.parentNode.removeChild(p);
      var field = form.querySelector("#text");
      field.focus();
      loadSuggestions(form);
    }
  });

  document.addEventListener("keydown", function (e) {
    var form = closest(e.target, "form.adder");
    if (!form || e.target.id !== "text") return;
    if (e.key === "ArrowDown" && move(form, 1)) e.preventDefault();
    else if (e.key === "ArrowUp" && move(form, -1)) e.preventDefault();
    else if (e.key === "Escape") putSuggestions(form, "");
    else if (e.key === "Enter") {
      var sel = form.querySelector('.sugg [role="option"][aria-selected="true"]');
      if (sel) { e.preventDefault(); choose(form, sel); }
    }
  });

  function init() {
    var forms = document.querySelectorAll("form.adder");
    for (var i = 0; i < forms.length; i++) { restoreMode(forms[i]); syncMinus(forms[i]); }
    var eds = document.querySelectorAll("form.qty-edit");
    for (var j = 0; j < eds.length; j++) {
      var u = eds[j].querySelector('input[name="qunit"]:checked');
      if (u) eds[j].setAttribute("data-unit", u.value);
      markConverted(eds[j]);
      syncMinus(eds[j]);
    }
  }
  init();
  // A display swaps its view; the cards in it start again.
  if (window.MutationObserver) {
    new MutationObserver(function () { init(); }).observe(document.body, { childList: true, subtree: true });
  }
})();
