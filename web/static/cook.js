// The cooking view (ticket 286, preview-286.html). Everything works without this script: the whole view is one
// GET form, so Weiter, the step numbers, the tabs, the portion buttons and the ticks (have) travel in the address
// and the box draws the page again. With the script the same form works in place: all steps are in the page
// (<template data-cook-step>), ticks and steps need no connection, and the address is kept up to date with
// history.replaceState. Only a change of the portions asks the box, which scales the quantities. Ticks and the
// step are also kept in sessionStorage per recipe and portions, so they survive leaving the page for a moment.
// The Screen Wake Lock keeps the screen on while the view is open; where the browser cannot, there is no switch
// and a notice says what to do instead.

var COOK_MIN = 1, COOK_MAX = 99;
var COOK_TICK = '<svg class="icon" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';

// cookFill puts numbers into a text of the locales: {k} and {n}.
function cookFill(tpl, k, n) {
  return String(tpl).split("{k}").join(String(k)).split("{n}").join(String(n));
}

// cookClamp keeps n between lo and hi.
function cookClamp(n, lo, hi) { return Math.min(Math.max(n, lo), hi); }

// cookPortionsOf reads typed portions: a whole number from 1 to 99, spaces around it ignored; 0 for anything else.
function cookPortionsOf(text) {
  var t = String(text == null ? "" : text).trim();
  if (!/^\d{1,2}$/.test(t)) return 0;
  var n = parseInt(t, 10);
  return n >= COOK_MIN && n <= COOK_MAX ? n : 0;
}

// cookStepOf keeps a step between 1 and the number of steps.
function cookStepOf(step, n) {
  var v = parseInt(step, 10);
  if (!(v >= 1)) return 1;
  return Math.min(v, Math.max(n, 1));
}

// cookParseSearch reads the address of the page: the view, the step, the portions (0: none) and the ticked
// ingredients. has says which of them the address carried.
function cookParseSearch(search) {
  var out = { view: "", step: 0, portions: 0, have: [], hasState: false };
  String(search || "").replace(/^\?/, "").split("&").forEach(function (pair) {
    if (!pair) return;
    var i = pair.indexOf("="), k, v;
    try {
      k = decodeURIComponent(pair.slice(0, i < 0 ? pair.length : i));
      v = i < 0 ? "" : decodeURIComponent(pair.slice(i + 1).replace(/\+/g, " "));
    } catch (e) { return; } // a malformed pair is skipped, the rest still counts
    if (k === "view" && (v === "ings" || v === "steps")) { out.view = v; out.hasState = true; }
    else if (k === "step") { out.step = parseInt(v, 10) || 0; out.hasState = true; }
    else if (k === "portions") out.portions = cookPortionsOf(v);
    else if (k === "have") {
      var n = parseInt(v, 10);
      if (n >= 0 && out.have.indexOf(n) < 0) out.have.push(n);
      out.hasState = true;
    }
  });
  return out;
}

// cookQuery is the address of a state: view, step, portions (when not the recipe's own) and the ticks.
function cookQuery(s) {
  var p = [];
  if (s.view) p.push("view=" + s.view);
  if (s.step) p.push("step=" + s.step);
  if (s.portions && s.portions !== s.base) p.push("portions=" + s.portions);
  (s.have || []).slice().sort(function (a, b) { return a - b; }).forEach(function (h) { p.push("have=" + h); });
  return p.join("&");
}

// cookStoreKey is where the ticks and the step of a recipe at some portions are kept.
function cookStoreKey(recipe, portions) { return "hejmo-cook:" + recipe + ":" + portions; }

// cookSave keeps the ticks, the step and the view for this visit; a browser that refuses storage is ignored.
function cookSave(storage, recipe, portions, s) {
  try { storage.setItem(cookStoreKey(recipe, portions), JSON.stringify({ step: s.step, view: s.view, have: s.have })); } catch (e) { /* kept in the address only */ }
}

// cookLoad returns what cookSave kept, or null.
function cookLoad(storage, recipe, portions) {
  try {
    var v = JSON.parse(storage.getItem(cookStoreKey(recipe, portions)));
    if (v && Array.isArray(v.have)) return { step: parseInt(v.step, 10) || 0, view: v.view === "steps" ? "steps" : "ings", have: v.have.filter(function (n) { return n >= 0 && n % 1 === 0; }) };
  } catch (e) { /* nothing kept */ }
  return null;
}

// cookForget drops everything kept for a recipe: it is cooked, the next time starts afresh.
function cookForget(storage, recipe) {
  try {
    var drop = [], prefix = "hejmo-cook:" + recipe + ":";
    for (var i = 0; i < storage.length; i++) if (String(storage.key(i)).indexOf(prefix) === 0) drop.push(storage.key(i));
    drop.forEach(function (k) { storage.removeItem(k); });
  } catch (e) { /* nothing kept */ }
}

// cookWakeLock keeps the screen on through the Screen Wake Lock. The browser lets the lock go when the page is
// hidden, so it is asked for again when the page becomes visible while the switch is on. Where the API is
// missing, or the browser refuses (no HTTPS, power saving), ui.unavailable() is called and nothing else
// happens: a switch that does nothing is not shown. Returns the switch: set(true|false), or null.
function cookWakeLock(nav, doc, ui) {
  var lock = null, want = true, dead = false, asking = false;
  if (!nav || !nav.wakeLock || typeof nav.wakeLock.request !== "function") {
    ui.unavailable();
    return null;
  }
  function failed() { dead = true; asking = false; lock = null; ui.unavailable(); }
  function acquire() {
    if (dead || lock || asking) return;
    var got;
    asking = true;
    try { got = nav.wakeLock.request("screen"); } catch (e) { failed(); return; }
    Promise.resolve(got).then(function (sentinel) {
      asking = false;
      if (!want) { try { sentinel.release(); } catch (e) { /* already gone */ } return; }
      lock = sentinel;
      if (sentinel && sentinel.addEventListener) sentinel.addEventListener("release", function () { if (lock === sentinel) lock = null; });
    }, failed);
  }
  doc.addEventListener("visibilitychange", function () {
    if (doc.visibilityState === "visible" && want) acquire();
  });
  ui.available();
  ui.state(true);
  acquire();
  return {
    set: function (on) {
      want = !!on;
      ui.state(want);
      if (want) { acquire(); return; }
      var l = lock;
      lock = null;
      if (l) { try { l.release(); } catch (e) { /* already gone */ } }
    },
  };
}

// ---- the page

function cookInit(form) {
  var root = form.closest(".cook"), recipe = form.getAttribute("data-recipe"), base = parseInt(form.getAttribute("data-base"), 10) || 1;
  var action = form.getAttribute("action");
  var steps = form.parentNode.querySelectorAll("template[data-cook-step]").length;
  var storage = null;
  try { storage = window.sessionStorage; } catch (e) { storage = null; }
  var url = cookParseSearch(window.location.search);
  var field = form.querySelector("#portions");
  var portions = (field && cookPortionsOf(field.value)) || base;
  if (form.querySelector(".cook-portions.invalid")) portions = base; // the box drew the last valid number's quantities: the recipe's own
  var state = { view: root.getAttribute("data-view"), step: steps ? cookStepOf(readStep(), steps) : 0, portions: portions, base: base, have: ticked() };

  function readStep() {
    var cur = form.querySelector(".cook-dots [aria-current=step]");
    return cur ? cur.value : 1;
  }
  function boxes() { return form.querySelectorAll("input[name=have]"); }
  function ticked() {
    var out = [];
    Array.prototype.forEach.call(boxes(), function (b) { if (b.checked) out.push(parseInt(b.value, 10)); });
    return out;
  }
  function text(key) { return form.getAttribute("data-" + key); }
  function hidden(name, value) {
    var h = form.querySelector("input[type=hidden][name=" + name + "]");
    if (h) h.value = value;
  }

  // Ticks and the step may come from earlier in this visit when the address says nothing.
  if (!url.hasState && storage) {
    var kept = cookLoad(storage, recipe, state.portions);
    if (kept) {
      state.have = kept.have;
      Array.prototype.forEach.call(boxes(), function (b) { b.checked = kept.have.indexOf(parseInt(b.value, 10)) >= 0; });
      if (steps && kept.step) { state.step = cookStepOf(kept.step, steps); showStep(state.step, true); }
      state.view = kept.view;
    }
  }

  function remember() {
    state.have = ticked();
    root.setAttribute("data-view", state.view);
    hidden("view", state.view);
    if (steps) hidden("step", state.step);
    var tabs = form.querySelectorAll(".cook-tabs button");
    Array.prototype.forEach.call(tabs, function (b) {
      if (b.value === state.view) b.setAttribute("aria-current", "true"); else b.removeAttribute("aria-current");
    });
    if (storage) cookSave(storage, recipe, state.portions, state);
    try { window.history.replaceState(null, "", action + "?" + cookQuery(state)); } catch (e) { /* the address stays */ }
  }

  function counts() {
    var n = boxes().length, k = ticked().length;
    if (!n) return;
    var cnt = form.querySelector(".cook-ings-part > h2 .cnt"), line = cookFill(text("ticked"), k, n);
    var small = form.querySelector(".cook-tabs button[value=ings] small");
    if (small) small.textContent = line;
    if (cnt) {
      if (k === n) {
        cnt.innerHTML = COOK_TICK;
        cnt.appendChild(document.createTextNode(cookFill(text("allready"), k, n)));
      } else {
        cnt.textContent = line;
      }
    }
  }

  function showStep(k, quiet) {
    var tpl = form.parentNode.querySelector('template[data-cook-step="' + k + '"]');
    if (!tpl) return;
    var frag = tpl.content.cloneNode(true);
    var card = form.querySelector(".cook-step"), nav = form.querySelector(".cook-nav");
    var newCard = frag.querySelector(".cook-step"), newNav = frag.querySelector(".cook-nav");
    if (card && newCard) card.innerHTML = newCard.innerHTML; // the card stays, so its live region reads the new step
    if (nav && newNav) nav.innerHTML = newNav.innerHTML;
    state.step = k;
    var h = newCard && newCard.querySelector("h2"), small = form.querySelector(".cook-tabs button[value=steps] small");
    if (h && small) small.textContent = h.textContent;
    if (!quiet) remember();
  }

  function focusAfter(kind) {
    var el = null;
    if (kind === "dot") el = form.querySelector(".cook-dots [aria-current=step]");
    else if (kind === "fwd") el = form.querySelector(".cook-nav .for-steps:not(.secondary)");
    else if (kind === "back") el = form.querySelector(".cook-nav .for-steps.secondary");
    else if (kind === "ings") el = form.querySelector(".cook-nav .for-ings");
    if (el && el.offsetParent !== null) el.focus();
  }

  // ---- portions
  var lastValid = portions, pending = null;
  function portionsBox() { return form.querySelector(".cook-portions"); }
  function showError(msg) {
    var box = portionsBox(), input = form.querySelector("#portions");
    clearError();
    box.classList.add("invalid");
    input.setAttribute("aria-invalid", "true");
    input.setAttribute("aria-describedby", "err-portions");
    var p = document.createElement("p");
    p.className = "ferr"; p.id = "err-portions"; p.setAttribute("role", "alert");
    var icon = document.createElement("span");
    icon.innerHTML = '<svg class="icon" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 8v5M12 16.5v.5"/><circle cx="12" cy="12" r="9"/></svg>';
    p.appendChild(icon.firstChild);
    var span = document.createElement("span");
    span.textContent = msg;
    p.appendChild(span);
    box.appendChild(p);
  }
  function clearError() {
    var box = portionsBox(), input = form.querySelector("#portions");
    box.classList.remove("invalid");
    input.removeAttribute("aria-invalid");
    input.removeAttribute("aria-describedby");
    var e = box.querySelector(".ferr");
    if (e) e.parentNode.removeChild(e);
  }
  function sayFor(prefix) {
    return lastValid === 1 ? text(prefix + "-one") : cookFill(text(prefix), 0, lastValid);
  }
  // The two buttons of the stepper lead one below and one above the number in the field.
  function stepper(value) {
    var buttons = form.querySelectorAll(".stepper button[name=nudge]");
    var typed = cookPortionsOf(value) || parseInt(value, 10) || lastValid;
    if (buttons.length === 2) {
      buttons[0].value = cookClamp(typed - 1, COOK_MIN, COOK_MAX);
      buttons[1].value = cookClamp(typed + 1, COOK_MIN, COOK_MAX);
    }
  }
  function changePortions(typedText, focusIndex) {
    var input = form.querySelector("#portions");
    var n = cookPortionsOf(typedText);
    input.value = String(typedText).trim();
    stepper(typedText);
    if (!n) { showError(sayFor("portions-err")); return; }
    input.value = String(n);
    if (n === lastValid) { clearError(); stepper(n); return; }
    var query = cookQuery({ view: state.view, step: state.step, portions: n, base: base, have: ticked() });
    if (pending) pending.abort();
    var ctl = typeof AbortController === "function" ? new AbortController() : null;
    pending = ctl;
    var offline = function () { showError(sayFor("portions-offline")); };
    if (window.navigator && window.navigator.onLine === false) { offline(); return; }
    window.fetch(action + "?" + query, { credentials: "same-origin", headers: { Accept: "text/html" }, signal: ctl ? ctl.signal : undefined }).then(function (res) {
      if (!res.ok) throw new Error("status " + res.status);
      return res.text();
    }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html");
      var newBox = doc.querySelector(".cook-portions"), newList = doc.querySelector("ul.cook-ings");
      if (!newBox || !newList) throw new Error("no quantities");
      portionsBox().replaceWith(document.importNode(newBox, true));
      form.querySelector("ul.cook-ings").replaceWith(document.importNode(newList, true));
      lastValid = n; state.portions = n;
      remember();
      counts();
      var back = form.querySelectorAll(".stepper button, #portions");
      var target = focusIndex === 0 ? back[0] : focusIndex === 2 ? back[2] : back[1];
      if (target) target.focus();
    }).catch(function (err) {
      if (err && err.name === "AbortError") return;
      offline();
    });
  }

  // ---- events
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var s = e.submitter, name = s && s.name, value = s && s.value;
    if (name === "view") {
      state.view = value === "steps" ? "steps" : "ings";
      remember();
      focusAfter(state.view === "ings" ? "ings" : "fwd");
    } else if (name === "step") {
      showStep(cookStepOf(value, steps));
      focusAfter(s.closest(".cook-dots") ? "dot" : s.classList.contains("secondary") ? "back" : "fwd");
    } else if (name === "done") {
      if (storage) cookForget(storage, recipe);
      window.location.assign(action + "?done=1");
    } else if (name === "nudge") {
      changePortions(value, s === form.querySelector(".stepper button[name=nudge]") ? 0 : 2);
    } else if (form.querySelector("#portions")) {
      changePortions(form.querySelector("#portions").value, 1);
    }
  });
  // Enter in the field submits the form with its first button, which is a tab: it means the portions here.
  form.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && e.target && e.target.id === "portions") {
      e.preventDefault();
      changePortions(e.target.value, 1);
    }
  });
  form.addEventListener("change", function (e) {
    var t = e.target;
    if (t && t.name === "have") { counts(); remember(); }
    else if (t && t.id === "portions") changePortions(t.value, 1);
  });

  // ---- the screen stays on
  var wake = form.parentNode.querySelector(".cook-wake"), notice = form.parentNode.querySelector("p.notice[hidden]");
  var stateWord = wake && wake.querySelector(".state"), sw = wake && wake.querySelector("input");
  var lock = cookWakeLock(window.navigator, document, {
    available: function () { if (wake) wake.hidden = false; if (notice) notice.hidden = true; },
    unavailable: function () { if (wake) wake.hidden = true; if (notice) notice.hidden = false; },
    state: function (on) {
      if (stateWord) stateWord.textContent = stateWord.getAttribute(on ? "data-label-on" : "data-label-off");
      if (sw) sw.checked = on;
    },
  });
  if (lock && sw) sw.addEventListener("change", function () { lock.set(sw.checked); });

  counts();
  remember();
}

if (typeof document !== "undefined" && document.querySelector) {
  var cookForm = document.querySelector("form.cook-form");
  if (cookForm) cookInit(cookForm);
}
