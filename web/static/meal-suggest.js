// The meal plan form (ticket 281). Everything works without this script: a text that is the name of a recipe
// becomes that recipe when the week is saved, "Anderes Gericht" posts the form, and the portions are a text
// field. With the script a list of recipes and earlier dishes opens under a field from two letters on (the
// server writes it, as for the ingredients of a recipe), the arrow keys and Enter choose from it, choosing a
// recipe puts its box in place of the field, and the stepper changes the portions and what they mean in words.

var MEAL_MIN_LETTERS = 2;

// mealSuggestUrl is the address of the suggestions for what was typed in the field of a cell, "" for less than two letters.
function mealSuggestUrl(base, typed, cell) {
  var q = String(typed || "").replace(/\s+/g, " ").trim();
  if (Array.from(q).length < MEAL_MIN_LETTERS) return "";
  return base + "?q=" + encodeURIComponent(q) + "&cell=" + encodeURIComponent(cell || "");
}

// mealMove is the option the arrow keys go to: dir is 1 (down) or -1 (up), current is -1 for none; the ends meet.
function mealMove(current, count, dir) {
  if (count <= 0) return -1;
  if (current < 0 || current >= count) return dir > 0 ? 0 : count - 1;
  return (current + (dir > 0 ? 1 : -1) + count) % count;
}

// mealStep moves portions one step up or down within 1 and 99.
function mealStep(value, dir) {
  var n = parseInt(String(value).trim(), 10);
  if (isNaN(n)) n = dir > 0 ? 0 : 2;
  return Math.min(99, Math.max(1, n + (dir > 0 ? 1 : -1)));
}

// mealFor says what the portions mean in words: "für alle 4" when they are the number of people, else "ihr seid 4".
function mealFor(portions, people, forAll, youAre) {
  var n = parseInt(String(portions).trim(), 10), all = parseInt(String(people), 10);
  if (!isNaN(n) && n === all) return String(forAll).replace("{n}", String(n));
  return String(youAre).replace("{n}", String(all));
}

// mealChoice says what choosing an option does: a recipe swaps in its box, anything else writes its text into the field.
function mealChoice(opt) {
  if (opt.recipe) return { kind: "recipe", id: opt.recipe };
  return { kind: "text", text: opt.text || "" };
}

(function () {
  var form = document.querySelector("form.meal-form[data-suggest]");
  if (!form) return;
  var base = form.getAttribute("data-suggest");
  var timer = null, serial = 0, quiet = null;

  function dirty() {
    var d = form.querySelector(".savebar .dirty");
    if (d) d.hidden = false;
  }
  function rowOf(el) { return el.closest ? el.closest(".mrow") : null; }
  function options(row) { return row ? Array.prototype.slice.call(row.querySelectorAll('.sugg li[role="option"]')) : []; }
  function selected(row) {
    var all = options(row);
    for (var i = 0; i < all.length; i++) if (all[i].getAttribute("aria-selected") === "true") return i;
    return -1;
  }
  function close(row) {
    var s = row.querySelector(".sugg");
    if (s) s.remove();
    var t = row.querySelector("textarea");
    if (t) {
      t.setAttribute("aria-expanded", "false");
      t.removeAttribute("aria-activedescendant");
      t.classList.remove("focus");
    }
  }
  function select(row, at) {
    var all = options(row), t = row.querySelector("textarea");
    for (var i = 0; i < all.length; i++) {
      if (i === at) {
        all[i].setAttribute("aria-selected", "true");
        if (!all[i].id) all[i].id = t.name + "-o" + i;
        if (t) t.setAttribute("aria-activedescendant", all[i].id);
        if (all[i].scrollIntoView) all[i].scrollIntoView({ block: "nearest" });
      } else {
        all[i].removeAttribute("aria-selected");
      }
    }
  }
  function ask(t) {
    var url = mealSuggestUrl(base, t.value, t.name), row = rowOf(t);
    if (!row) return;
    if (!url || typeof fetch !== "function") { close(row); return; }
    var mine = ++serial;
    fetch(url, { credentials: "same-origin", headers: { Accept: "text/html" } }).then(function (r) { return r.ok && r.status !== 204 ? r.text() : ""; }).then(function (html) {
      if (mine !== serial) return;
      close(row);
      if (!html.trim()) return;
      row.insertAdjacentHTML("beforeend", html);
      t.setAttribute("role", "combobox");
      t.setAttribute("aria-expanded", "true");
      t.setAttribute("aria-controls", t.name + "-sl");
    }).catch(function () { close(row); });
  }

  function updateFor(box) {
    var input = box.querySelector("input"), out = box.querySelector(".for");
    if (!input || !out) return;
    out.textContent = mealFor(input.value, box.getAttribute("data-people"), box.getAttribute("data-for-all"), box.getAttribute("data-you-are"));
  }

  // choose puts the option into its row: a recipe in place of the field, other text into the field.
  function choose(li) {
    var row = rowOf(li), t = row.querySelector("textarea");
    var c = mealChoice({ recipe: li.getAttribute("data-recipe"), text: li.getAttribute("data-text") });
    serial++;
    if (c.kind === "recipe") {
      var tpl = li.querySelector("template.rcp-box");
      if (!tpl) return;
      var keep = row.querySelector(".src");
      var gone = row.querySelectorAll("label.mt, textarea, input[type=hidden], .ferr, .sugg");
      for (var i = 0; i < gone.length; i++) gone[i].remove();
      row.classList.add("has-rcp");
      row.classList.remove("invalid");
      row.insertBefore(tpl.content.cloneNode(true), keep || null);
      var p = row.querySelector(".portions input");
      if (p) p.focus();
    } else if (t) {
      close(row);
      t.value = c.text;
      quiet = t;
      t.dispatchEvent(new Event("input", { bubbles: true }));
      quiet = null;
      t.focus();
    }
    dirty();
  }

  form.addEventListener("input", function (e) {
    var t = e.target;
    if (t.matches && t.matches(".mrow textarea")) {
      if (quiet === t) return;
      clearTimeout(timer);
      timer = setTimeout(function () { ask(t); }, 200);
      return;
    }
    if (t.matches && t.matches(".portions input")) {
      updateFor(t.closest(".portions"));
      dirty();
    }
  });

  // Arrow keys and Enter choose from an open list. This runs before the field's own Enter (which saves the week).
  form.addEventListener("keydown", function (e) {
    var t = e.target;
    if (!t.matches || !t.matches(".mrow textarea")) return;
    var row = rowOf(t), all = options(row);
    if (!all.length) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      select(row, mealMove(selected(row), all.length, e.key === "ArrowDown" ? 1 : -1));
    } else if (e.key === "Enter" && !e.isComposing && selected(row) >= 0) {
      e.preventDefault();
      e.stopPropagation();
      choose(all[selected(row)]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      close(row);
    }
  }, true);

  form.addEventListener("click", function (e) {
    var target = e.target;
    var opt = target && target.closest ? target.closest('.sugg li[role="option"]') : null;
    if (opt) {
      e.preventDefault();
      choose(opt);
      return;
    }
    var step = target && target.closest ? target.closest("button[data-step]") : null;
    if (step) {
      e.preventDefault();
      var box = step.closest(".portions"), input = box.querySelector("input");
      input.value = String(mealStep(input.value, step.getAttribute("data-step") === "up" ? 1 : -1));
      updateFor(box);
      dirty();
    }
  });
})();
