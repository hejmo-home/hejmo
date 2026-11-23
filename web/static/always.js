// Articles that are always on the list (ticket 168, docs/design/preview-168.html).
// Without this script the card and the holiday form work as plain forms: the
// sentence under them says what happens as it was when the page was made. With
// it, the sentence follows the fields while they change. Every sentence comes
// from the box (…/plan); the rules live there, this script only asks and shows.
(function () {
  function query(pairs) {
    var out = [];
    Object.keys(pairs).forEach(function (k) {
      if (pairs[k] !== "" && pairs[k] != null) out.push(encodeURIComponent(k) + "=" + encodeURIComponent(pairs[k]));
    });
    return out.join("&");
  }

  // planURL is the address that answers the sentence of an article's card for
  // what is chosen so far (values: list, weekday, every, start, qmode, amount, unit).
  function planURL(base, v) {
    return base + "?" + query({ list: v.list, weekday: v.weekday, every: v.every, start: v.start, qmode: v.qmode, amount: v.qmode === "qty" ? v.amount : "", unit: v.qmode === "qty" ? v.unit : "" });
  }

  // holidayURL is the same for the holiday of all articles; with the switch
  // off the sentence is not asked for.
  function holidayURL(base, v) {
    if (!v.on) return null;
    return base + "?" + query({ from: v.from, to: v.to });
  }

  var api = { planURL: planURL, holidayURL: holidayURL };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  if (typeof document === "undefined" || !document.addEventListener) return;

  function checked(root, name) {
    var el = root.querySelector('input[name="' + name + '"]:checked');
    return el ? el.value : "";
  }
  function field(root, name) {
    var el = root.querySelector('[name="' + name + '"]');
    return el ? el.value : "";
  }

  var timers = {};
  function later(key, fn) { clearTimeout(timers[key]); timers[key] = setTimeout(fn, 250); }

  function swap(plan, html) {
    if (!html) return;
    var t = document.createElement("div");
    t.innerHTML = html;
    var next = t.firstElementChild;
    if (next && plan.parentNode) plan.parentNode.replaceChild(next, plan);
  }

  function ask(url, plan) {
    if (!window.fetch || !url) return;
    fetch(url, { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.ok ? r.text() : ""; })
      .then(function (html) { swap(plan, html); })
      .catch(function () {});
  }

  function refreshCard(card) {
    var plan = card.querySelector(".auto-plan");
    if (!plan) return;
    ask(planURL(card.getAttribute("data-plan"), {
      list: checked(card, "auto_list"), weekday: checked(card, "auto_weekday"), every: field(card, "auto_every"),
      start: field(card, "auto_start"), qmode: checked(card, "qmode"), amount: field(card, "amount"), unit: checked(card, "unit")
    }), plan);
  }

  function refreshHoliday(box) {
    var plan = box.querySelector(".auto-plan");
    var on = box.querySelector('input[name="holiday"]');
    var url = holidayURL(box.getAttribute("data-plan"), { on: on && on.checked, from: field(box, "holiday_from"), to: field(box, "holiday_to") });
    if (plan && url) ask(url, plan);
  }

  function root(el) {
    var card = el.closest && el.closest("[data-auto]");
    if (card) return { card: card };
    var box = el.closest && el.closest("[data-holiday]");
    return box ? { box: box } : null;
  }

  function onChange(e) {
    var r = root(e.target);
    if (!r) return;
    if (r.card) later("card", function () { refreshCard(r.card); });
    else later("holiday", function () { refreshHoliday(r.box); });
  }
  document.addEventListener("input", onChange);
  document.addEventListener("change", onChange);
  document.addEventListener("click", function (e) {
    var step = e.target.closest && e.target.closest("[data-step]");
    var r = step && root(step);
    if (r && r.card) later("card", function () { refreshCard(r.card); });
  });
})();
