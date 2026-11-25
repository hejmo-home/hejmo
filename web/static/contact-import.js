// The review page of a contact file (ticket 300): the save bar says in words what a tap on "Kontakte übernehmen"
// does now, "Alle auswählen" / "Keinen auswählen" tick the new contacts, an unticked row says so, and the group
// line names the chosen groups. Without this script the page works: the buttons are hidden and the save bar
// keeps the state the server wrote.
(function (root) {
  "use strict";

  // fill puts a number into a text like "{n} neu".
  function fill(text, n) { return String(text).replace("{n}", String(n)); }

  // count says what the choices make: dups are the possible duplicates ({value, cards, infile}; an empty value is
  // no choice yet), takes tell for every new contact whether it is ticked.
  function count(dups, takes) {
    var c = { new: 0, completed: 0, skipped: 0, missing: 0 };
    dups.forEach(function (d) {
      if (!d.value) c.missing++;
      else if (d.value === "skip") c.skipped++;
      else if (d.value === "new") c.new += d.cards;
      else if (d.infile) c.new++;
      else c.completed++;
    });
    takes.forEach(function (t) { if (t) c.new++; else c.skipped++; });
    return c;
  }

  // state is the sentence of the save bar: "8 neu, 3 ergänzt, 1 weggelassen. Bei 1 fehlt deine Wahl."
  function state(c, t) {
    var parts = [];
    if (c.new) parts.push(fill(t.new, c.new));
    if (c.completed) parts.push(fill(t.completed, c.completed));
    if (c.skipped) parts.push(fill(t.skipped, c.skipped));
    var text = (parts.length ? parts.join(", ") : t.nothing) + ".";
    if (c.missing) text += " " + fill(t.missing, c.missing);
    return text;
  }

  root.HejmoImport = { fill: fill, count: count, state: state };

  if (typeof document === "undefined") return;
  var form = document.querySelector("form.imp-review");
  if (!form) return;
  var texts = {
    new: form.getAttribute("data-state-new"), completed: form.getAttribute("data-state-completed"), skipped: form.getAttribute("data-state-skipped"),
    nothing: form.getAttribute("data-state-nothing"), missing: form.getAttribute("data-state-missing"),
  };
  var taken = form.getAttribute("data-taken"), untaken = form.getAttribute("data-untaken");
  var dirty = form.querySelector(".imp-save .dirty");
  var boxes = Array.prototype.slice.call(form.querySelectorAll('.imp-rows input[name="take"]'));

  function refresh() {
    boxes.forEach(function (b) {
      var st = b.closest("li").querySelector(".st");
      if (st && taken && untaken) st.textContent = b.checked ? taken : untaken;
    });
    var chosen = [];
    form.querySelectorAll('.imp-grp input[name="group"]').forEach(function (g) { if (g.checked) chosen.push(g.parentNode.textContent.trim()); });
    var sum = form.querySelector(".imp-grp .sum");
    if (sum) sum.textContent = chosen.length ? chosen.join(", ") : sum.getAttribute("data-none");
    if (!dirty || !texts.new) return;
    var dups = Array.prototype.slice.call(form.querySelectorAll("li.imp-dup")).map(function (li) {
      var r = li.querySelector('.imp-pick input[type="radio"]:checked');
      return { value: r ? r.value : "", cards: Number(li.getAttribute("data-cards")) || 1, infile: li.hasAttribute("data-infile") };
    });
    dirty.textContent = state(count(dups, boxes.map(function (b) { return b.checked; })), texts);
  }

  form.addEventListener("change", refresh);
  var bulk = form.querySelector(".imp-bulk");
  if (bulk) {
    bulk.hidden = false;
    bulk.addEventListener("click", function (e) {
      var which = e.target && e.target.getAttribute && e.target.getAttribute("data-take");
      if (!which) return;
      boxes.forEach(function (b) { b.checked = which === "all"; });
      refresh();
    });
  }
})(typeof window !== "undefined" ? window : this);
