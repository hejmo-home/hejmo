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
