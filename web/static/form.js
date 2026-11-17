// Shared form patterns (ticket 5.16). Everything works without this script:
// the server reads "no person chosen" as the whole family. The script only
// keeps the "Whole family" chip in step with the person chips while you choose.
document.addEventListener("change", function (e) {
  var input = e.target;
  var set = input.closest && input.closest("fieldset.chips");
  if (!set) return;
  var whole = set.querySelector("input[data-whole]");
  if (!whole) return;
  var others = Array.prototype.slice.call(set.querySelectorAll("input:not([data-whole])"));
  if (input === whole) {
    if (whole.checked) others.forEach(function (o) { o.checked = false; });
    else whole.checked = !others.some(function (o) { return o.checked; });
  } else {
    whole.checked = !others.some(function (o) { return o.checked; });
  }
});
// Meal plan: a dish field grows with its text where field-sizing is missing,
// Enter saves instead of starting a new line, and the save bar says in words
// when something is not saved yet. The server strips newlines anyway.
(function () {
  function grow(t) { t.style.height = "auto"; t.style.height = t.scrollHeight + 2 + "px"; }
  var supports = window.CSS && CSS.supports && CSS.supports("field-sizing", "content");
  document.querySelectorAll(".mrow textarea").forEach(function (t) {
    if (!supports) grow(t);
    t.addEventListener("input", function () {
      if (!supports) grow(t);
      var d = document.querySelector(".savebar .dirty"); if (d) d.hidden = false;
    });
    t.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && !e.isComposing) { e.preventDefault(); t.form.requestSubmit(); }
    });
  });
})();

// Meal plan template form: the sentence that spells out the weeks the template
// fills follows the fields, and the save bar says when something is not saved
// yet. The server builds the same sentence for the first render (and without
// this script). The interval counts whole weeks from the start week, so the
// turn of the year (2026 has a week 53) comes out right.
(function () {
  var hint = document.querySelector(".tpl-form .weeks-hint");
  if (!hint) return;
  var form = hint.closest("form");
  var every = form.elements["every"], start = form.elements["start"];
  var DAY = 86400000, WEEK = 7 * DAY, MAX = 8;
  function parse(s) { var m = /^(\d{4})-(\d\d)-(\d\d)$/.exec(s || ""); return m ? Date.UTC(+m[1], +m[2] - 1, +m[3]) : NaN; }
  function monday(ms) { return ms - ((new Date(ms).getUTCDay() + 6) % 7) * DAY; }
  function isoYear(ms) { return new Date(ms + 3 * DAY).getUTCFullYear(); }
  function isoWeek(ms) { var th = ms + 3 * DAY; return Math.floor((th - Date.UTC(new Date(th).getUTCFullYear(), 0, 1)) / WEEK) + 1; }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function date(ms) { var d = new Date(ms), day = pad(d.getUTCDate()), mon = pad(d.getUTCMonth() + 1); return hint.getAttribute("data-date-format") === "mdy" ? mon + "/" + day + "/" + d.getUTCFullYear() : day + "." + mon + "." + d.getUTCFullYear(); }
  function esc(t) { return String(t).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
  function build() {
    var n = parseInt(every.value, 10), st = parse(start.value), today = monday(parse(hint.getAttribute("data-today")));
    if (!(n >= 1 && n <= 52) || isNaN(st) || isNaN(today)) { hint.textContent = ""; return; }
    var w = st;
    if (today > st) w = st + Math.ceil(Math.round((today - st) / WEEK) / n) * n * WEEK;
    var year = isoYear(w), k = 0, then = "", out = esc(hint.getAttribute("data-lead"));
    for (var i = 0; i < 60; i++, w += n * WEEK) {
      if (isoYear(w) !== year) {
        then = esc(hint.getAttribute("data-then")).replace("{week}", "<b>" + isoWeek(w) + "</b>").replace("{year}", isoYear(w)).replace("{date}", date(w));
        break;
      }
      if (k === MAX) break;
      out += (k > 0 ? "," : "") + " <b>" + isoWeek(w) + "</b>" + (k === 0 && w === today ? esc(hint.getAttribute("data-this")) : "");
      k++;
    }
    hint.innerHTML = out + then + esc(hint.getAttribute("data-tail"));
  }
  every.addEventListener("input", build);
  start.addEventListener("change", build);
  form.addEventListener("input", function () {
    var d = form.querySelector(".savebar .dirty"); if (d) d.hidden = false;
  });
  form.addEventListener("change", function () {
    var d = form.querySelector(".savebar .dirty"); if (d) d.hidden = false;
  });
})();

// Time picker (shared by every time field). The time sits in a capsule
// (input.tcap). On iPhone, iPad and Android the capsule stays the system time
// field, so the system picker opens. On a computer and on displays, click or
// Enter opens a panel below it: the time large, the hours as tap targets, the
// minutes in 5-minute steps (with AM/PM in the 12-hour format). No wheel, no
// scrolling, so it works with a mouse and on E-Ink. Typing into the capsule
// still works, and without this script the plain time field works too.
(function () {
  var script = document.currentScript;
  var texts = (script && script.dataset) || {};
  var mobile = /Android|iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  var fine = !!(window.matchMedia && matchMedia("(pointer: fine)").matches);
  if (mobile && !fine) return;
  var twelve = texts.timeFormat === "12h"; // data-time-format of this script tag
  var count = 0;

  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function parse(v) { var m = /^(\d\d):(\d\d)/.exec(v || ""); return m ? { h: +m[1], m: +m[2] } : null; }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  // A radio of the picker; "form" points at no form, so it is never submitted.
  function choice(name, value, text, label) {
    var l = el("label"), r = el("input");
    r.type = "radio"; r.name = name; r.value = value; r.setAttribute("form", "tpick-none");
    l.appendChild(r); l.appendChild(document.createTextNode(text));
    label.appendChild(l);
    return r;
  }
  function group(legend, extra) {
    var f = el("fieldset", extra), l = el("legend", legend.cls, legend.text);
    f.appendChild(l);
    return f;
  }

  function open(input) {
    if (input.getAttribute("aria-expanded") === "true") return;
    var id = ++count;
    var panel = el("div", "tpick");
    panel.id = "tpick-" + id;
    panel.setAttribute("role", "group");
    var holder = input.closest("label");
    panel.setAttribute("aria-label", (holder ? holder.textContent : input.getAttribute("aria-label") || "").trim());
    var now = el("div", "now");
    now.setAttribute("aria-live", "polite");
    panel.appendChild(now);

    var hoursSet = group({ text: texts.hour || "Hour" }), hours = el("div", "grid");
    hoursSet.appendChild(hours);
    var hourRadios = [];
    for (var i = twelve ? 1 : 0; i < (twelve ? 13 : 24); i++) {
      hourRadios.push(choice("tp_h" + id, String(i), twelve ? String(i) : pad(i), hours));
    }
    panel.appendChild(hoursSet);

    var minutesSet = group({ text: texts.minute || "Minute" }), minutes = el("div", "grid");
    minutesSet.appendChild(minutes);
    var minuteRadios = [];
    for (var m = 0; m < 60; m += 5) minuteRadios.push(choice("tp_m" + id, String(m), pad(m), minutes));
    panel.appendChild(minutesSet);

    var am, pm;
    if (twelve) {
      var seg = group({ text: "AM / PM", cls: "sr" }, "seg");
      am = choice("tp_p" + id, "am", "AM", seg);
      pm = choice("tp_p" + id, "pm", "PM", seg);
      panel.appendChild(seg);
    }
    var done = el("button", "button secondary done", texts.done || "Done");
    done.type = "button";
    panel.appendChild(done);

    function show() {
      var t = parse(input.value);
      hourRadios.forEach(function (r) { r.checked = !!t && (twelve ? (t.h % 12 || 12) : t.h) === +r.value; });
      minuteRadios.forEach(function (r) { r.checked = !!t && t.m === +r.value; });
      if (twelve) { am.checked = !!t && t.h < 12; pm.checked = !!t && t.h >= 12; }
      now.textContent = t ? (twelve ? (t.h % 12 || 12) + ":" + pad(t.m) + " " + (t.h < 12 ? "AM" : "PM") : pad(t.h) + ":" + pad(t.m)) : "--:--";
    }
    function write() {
      var t = parse(input.value) || { h: 0, m: 0 };
      var h = +(hourRadios.filter(function (r) { return r.checked; })[0] || { value: twelve ? t.h % 12 || 12 : t.h }).value;
      var min = +(minuteRadios.filter(function (r) { return r.checked; })[0] || { value: t.m }).value;
      if (twelve) h = (h % 12) + ((pm.checked || (!am.checked && t.h >= 12)) ? 12 : 0);
      input.value = pad(h) + ":" + pad(min);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      show();
    }
    function close() {
      input.removeEventListener("input", show);
      input.removeEventListener("keydown", onKey);
      panel.remove();
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-controls");
      input.focus();
    }
    function onKey(e) { if (e.key === "Escape") { e.preventDefault(); close(); } }
    panel.addEventListener("change", function (e) { if (e.target.type === "radio") write(); });
    panel.addEventListener("keydown", onKey);
    done.addEventListener("click", close);
    input.addEventListener("input", show);
    input.addEventListener("keydown", onKey);

    (holder || input).insertAdjacentElement("afterend", panel);
    input.setAttribute("aria-expanded", "true");
    input.setAttribute("aria-controls", panel.id);
    show();
  }

  document.querySelectorAll("input.tcap[type=time]").forEach(function (input) {
    input.setAttribute("aria-expanded", "false");
    input.addEventListener("click", function () { open(input); });
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); open(input); }
    });
  });
})();

// People form (ticket 76): a colour and marker pair another person already
// has is disabled, so it cannot be chosen. Picking a colour updates which
// markers are disabled and the other way round. Without this script the server
// still refuses such a pair with the field error.
(function () {
  function sync(form) {
    var color = form.querySelector('input[name="color"]:checked');
    var shape = form.querySelector('input[name="shape"]:checked');
    function update(name, other) {
      Array.prototype.forEach.call(form.querySelectorAll('input[name="' + name + '"]'), function (o) {
        var taken = o.getAttribute("data-taken-with");
        var want = other && other.value.toUpperCase();
        o.disabled = !o.checked && !!other && !!taken && taken.toUpperCase().split(" ").indexOf(want) >= 0;
      });
    }
    update("color", shape);
    update("shape", color);
  }
  document.addEventListener("change", function (e) {
    var input = e.target;
    if (input && input.form && (input.name === "color" || input.name === "shape") && input.hasAttribute("data-taken-with")) sync(input.form);
  });
})();

// Busy state (ticket 133): a form that takes a while (getting the certificate
// can take up to 3 minutes) greys out its submit button, says what happens in
// words and shows the hidden check line. Nothing animates (E-Ink). Without
// this script the form works the same, the page just waits.
(function () {
  function reset(form) {
    var button = form.querySelector("[data-busy-text]");
    if (button && button.hasAttribute("data-idle-text")) {
      button.disabled = false;
      button.textContent = button.getAttribute("data-idle-text");
    }
    var line = form.querySelector('[data-hook="busy"]');
    if (line) line.hidden = true;
  }
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.hasAttribute || !form.hasAttribute("data-busy")) return;
    var button = form.querySelector("[data-busy-text]");
    if (!button) return;
    button.setAttribute("data-idle-text", button.textContent);
    // Disable after the browser has collected the form, so nothing is lost.
    setTimeout(function () {
      button.disabled = true;
      button.textContent = button.getAttribute("data-busy-text");
    }, 0);
    var line = form.querySelector('[data-hook="busy"]');
    if (line) line.hidden = false;
  });
  // Back from the page cache: the form is usable again.
  window.addEventListener("pageshow", function (e) {
    if (!e.persisted) return;
    Array.prototype.forEach.call(document.querySelectorAll("form[data-busy]"), reset);
  });
})();

// Password with a "Show" button in words (ticket 133).
document.addEventListener("click", function (e) {
  var button = e.target.closest && e.target.closest("[data-pw-toggle]");
  if (!button) return;
  var input = button.closest(".pw").querySelector("input");
  var show = input.type === "password";
  input.type = show ? "text" : "password";
  button.textContent = button.getAttribute(show ? "data-hide-text" : "data-show-text");
  button.setAttribute("aria-pressed", show ? "true" : "false");
});

// "Print" on the recovery codes page (ticket 133); the print styles show only the codes.
document.addEventListener("click", function (e) {
  if (e.target.closest && e.target.closest("[data-print]")) window.print();
});

// "Copy" button (ticket 133, design pass 2): puts the text of data-copy on the
// clipboard and says "Copied" in words (data-copied-text) for a moment. The box
// may be opened over plain http, where navigator.clipboard does not exist: a
// hidden field and the old copy command do the same there.
document.addEventListener("click", function (e) {
  var button = e.target.closest && e.target.closest("[data-copy]");
  if (!button) return;
  var text = button.getAttribute("data-copy");
  function said() {
    var idle = button.getAttribute("data-idle-text") || button.textContent;
    button.setAttribute("data-idle-text", idle);
    button.textContent = button.getAttribute("data-copied-text") || idle;
    setTimeout(function () { button.textContent = idle; }, 2000);
  }
  function fallback() {
    var field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.appendChild(field);
    field.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
    document.body.removeChild(field);
    if (ok) said();
  }
  if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(said, fallback);
  else fallback();
});

// Language switcher (ticket 134). The menu is a <details> and works without
// this script; the script adds what a native <details> lacks: Escape closes it
// (focus goes back to its button) and so does a click outside it. The theme
// chooser of ticket 125 is the same kind of menu.
(function () {
  function open() {
    return document.querySelector("details.lang[open], details.theme-pick[open]");
  }
  document.addEventListener("keydown", function (e) {
    var menu = e.key === "Escape" && open();
    if (!menu) return;
    menu.removeAttribute("open");
    var summary = menu.querySelector("summary");
    if (summary) summary.focus();
  });
  document.addEventListener("click", function (e) {
    var menu = open();
    if (menu && !menu.contains(e.target)) menu.removeAttribute("open");
  });
})();
