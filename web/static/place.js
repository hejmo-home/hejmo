// Home location picker (ADR 0023, ticket 86): setup wizard and Settings →
// Household. Everything works without this script: each suggestion is a submit
// button and the server renders the list for the posted text. The script only
// updates the list while you type (from GET /places, the list on the box;
// nothing goes to the internet), moves through it with the arrow keys, and
// swaps in the confirmed place when you pick one. The texts come as data
// attributes of the script tag.
(function () {
  var cfg = document.currentScript && document.currentScript.dataset;
  if (!cfg) return;
  var pick = document.querySelector(".place-pick");
  if (!pick) return;
  var form = pick.closest("form");
  var input = pick.querySelector("input[name=home_name]");
  if (!input || !form) return; // the confirmed state: Change is a plain submit
  var lang = document.documentElement.lang || "en";
  var timer = null, seq = 0, active = -1, regionTouched = false;

  var region = form.elements.region;
  if (region) region.addEventListener("change", function () { regionTouched = true; });

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function list() { return pick.querySelector(".place-list"); }
  function rows() { var l = list(); return l ? Array.prototype.slice.call(l.children) : []; }

  // Takes the list, its question and the "not in the list" hint away.
  function close() {
    ["place-q", "place-list"].forEach(function (c) { var e = pick.querySelector("." + c); if (e) e.remove(); });
    Array.prototype.forEach.call(pick.querySelectorAll(".hint:not(.place-privacy)"), function (h) { h.remove(); });
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-controls");
    input.removeAttribute("aria-autocomplete");
    input.removeAttribute("aria-activedescendant");
    active = -1;
  }

  function clearError() {
    var f = pick.querySelector(".ferr");
    if (f) f.remove();
    pick.classList.remove("invalid");
    input.removeAttribute("aria-invalid");
    input.removeAttribute("aria-describedby");
  }

  function show(places, more) {
    close();
    if (!places.length) return;
    var q = places.length === 1 ? cfg.one : more ? cfg.more : (cfg.many || "").replace("{n}", places.length);
    var qe = el("p", "place-q", q); qe.id = "pl-q";
    var ul = el("ul", "place-list"); ul.id = "pl"; ul.setAttribute("role", "listbox"); ul.setAttribute("aria-labelledby", "pl-q");
    places.forEach(function (p, i) {
      var li = el("li"); li.id = "pl-" + i; li.setAttribute("role", "option"); li.setAttribute("aria-selected", "false");
      li.dataset.id = p.id; li.dataset.region = p.region_code;
      li.appendChild(el("b", null, p.name)); li.appendChild(el("small", null, p.region));
      ul.appendChild(li);
    });
    var anchor = pick.querySelector(".place-privacy");
    pick.insertBefore(qe, anchor); pick.insertBefore(ul, anchor);
    pick.insertBefore(el("p", "hint", cfg.notThere), anchor);
    input.setAttribute("aria-expanded", "true");
    input.setAttribute("aria-controls", "pl");
    input.setAttribute("aria-autocomplete", "list");
  }

  function search() {
    var text = input.value.trim(), mine = ++seq;
    if (text.replace(/\s/g, "").length < 2) { close(); return; }
    fetch(cfg.url + "?name=" + encodeURIComponent(text) + "&lang=" + encodeURIComponent(lang), { headers: { Accept: "application/json" } })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (d) { if (mine === seq) show(d.places || [], !!d.more); })
      .catch(function () { /* the list stays as it is; submitting still works */ });
  }

  input.addEventListener("input", function () {
    clearError();
    clearTimeout(timer);
    timer = setTimeout(search, 150);
  });

  // No row is highlighted until the arrow keys move into the list.
  function move(to) {
    var r = rows();
    if (!r.length) return;
    active = Math.max(0, Math.min(r.length - 1, to));
    r.forEach(function (li, i) { li.setAttribute("aria-selected", i === active ? "true" : "false"); });
    input.setAttribute("aria-activedescendant", r[active].id);
    r[active].scrollIntoView({ block: "nearest" });
  }
  input.addEventListener("keydown", function (e) {
    if (e.isComposing) return;
    if (e.key === "ArrowDown") { e.preventDefault(); move(active < 0 ? 0 : active + 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); if (active > 0) move(active - 1); }
    else if (e.key === "Escape") { if (list()) { e.preventDefault(); close(); } }
    else if (e.key === "Enter" && active >= 0) { e.preventDefault(); choose(rows()[active]); }
  });

  pick.addEventListener("click", function (e) {
    var li = e.target.closest && e.target.closest("li[role=option]");
    if (!li) return;
    e.preventDefault(); // a row of the list sent by the server is a submit button
    choose(li);
  });

  // The picked place replaces the field: the same box the server renders.
  function choose(li) {
    var btn = li.querySelector("button");
    var id = li.dataset.id || (btn && btn.value);
    var code = li.dataset.region || (btn && btn.dataset.region) || "";
    var name = li.querySelector("b").textContent, where = li.querySelector("small").textContent;
    var label = pick.querySelector("label");
    var title = label ? label.firstChild.textContent.trim() : "";
    var tick = '<svg class="icon" width="20" height="20" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
    var state = cfg.old && cfg.pending ? cfg.pending.replace("{old}", cfg.old) : cfg.chosen;
    pick.className = "f place-pick";
    pick.textContent = "";
    pick.appendChild(el("p", "lab", title));
    var box = el("div", "place-status ok");
    box.setAttribute("role", "status"); box.setAttribute("data-location", "");
    box.innerHTML = tick;
    var text = el("div");
    text.appendChild(el("b", null, name)); text.appendChild(el("small", null, where)); text.appendChild(el("small", "st", state));
    box.appendChild(text);
    var change = el("button", "button secondary", cfg.change);
    change.type = "submit"; change.name = "place_change"; change.value = "1";
    box.appendChild(change);
    pick.appendChild(box);
    var hidden = document.createElement("input");
    hidden.type = "hidden"; hidden.name = "place_id"; hidden.value = id;
    pick.appendChild(hidden);
    // What was only the way back to the saved place or the news that none is chosen goes.
    Array.prototype.forEach.call(form.querySelectorAll(".place-keep, .place-status.missing"), function (e) { e.remove(); });
    // The wizard takes the state or canton from the place, unless the parent chose one.
    if (region && code && !regionTouched) {
      var empty = region.querySelector("option[value='']");
      if (empty) empty.remove();
      region.value = code;
      var hint = region.closest(".f").querySelector(".hint");
      if (hint && cfg.taken) hint.textContent = cfg.taken;
    }
    change.focus();
  }
})();
