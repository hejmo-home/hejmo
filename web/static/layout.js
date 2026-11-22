// The Layout section of a display's settings page (ticket 180, preview-180).
// It keeps the small preview, the order in words and the list rows in step with
// what is chosen, before anything is saved: up and down arrows move a person or
// a block, a tap on a person chip adds or removes it, "Whole family" adds
// everyone. Without script the form still saves what is ticked, in the order the
// page shows. The pure parts are in window.HejmoLayout (tested with node).
(function () {
  // moved returns a copy of items with the one at index i moved by dir (-1, +1).
  function moved(items, i, dir) {
    var j = i + dir;
    var out = items.slice();
    if (i < 0 || i >= out.length || j < 0 || j >= out.length) return out;
    var x = out[i];
    out[i] = out[j];
    out[j] = x;
    return out;
  }

  // tomorrow is the order of the columns after the daily rotation: everyone
  // moves one column on, the rightmost goes to the left (Q91).
  function tomorrow(names) {
    if (names.length < 2) return names.slice();
    return [names[names.length - 1]].concat(names.slice(0, names.length - 1));
  }

  function fill(text, values) {
    return Object.keys(values).reduce(function (s, k) { return s.split("{" + k + "}").join(values[k]); }, text);
  }

  function join(names, and) {
    if (names.length < 2) return names.join("");
    return names.slice(0, names.length - 1).join(", ") + and + names[names.length - 1];
  }

  // words is the sentence under the preview; t holds the texts of the page
  // (order, orderToday, fitLandscape, fitPortrait, swipe, none, one, and, name).
  // personal: exactly one person chosen, so the display shows their personal view.
  function words(t, names, portrait, rotate, personal) {
    if (names.length === 0) return t.none;
    if (personal) return fill(t.one, { person: names[0], name: t.name });
    var out = fill(rotate ? t.orderToday : t.order, { names: names.join(", ") });
    var visible = portrait ? 4 : 6;
    if (names.length > visible) {
      out += " " + fill(portrait ? t.fitPortrait : t.fitLandscape, { n: String(visible) });
      out += " " + fill(t.swipe, { names: join(names.slice(visible), t.and) });
    }
    return out;
  }

  var api = { moved: moved, tomorrow: tomorrow, words: words, fill: fill };
  if (typeof window !== "undefined") window.HejmoLayout = api;

  var root = typeof document !== "undefined" && document.querySelector ? document.querySelector("[data-layout]") : null;
  if (!root) return;

  var portrait = root.getAttribute("data-portrait") === "true";
  var visible = Number(root.getAttribute("data-visible")) || 6;
  var fig = root.querySelector(".lay-prev");
  var mini = root.querySelector("[data-mini]");
  var list = root.querySelector("[data-porder]");
  var off = root.querySelector("[data-poff]");
  var whole = root.querySelector('input[name="whole"]');
  var rotate = root.querySelector('input[name="rotate"]');
  var rowTemplate = root.querySelector("template[data-prow]");
  var texts = {
    order: fig.getAttribute("data-words-order"), orderToday: fig.getAttribute("data-words-order-today"),
    fitLandscape: fig.getAttribute("data-words-fit-landscape"), fitPortrait: fig.getAttribute("data-words-fit-portrait"),
    swipe: fig.getAttribute("data-words-swipe"), none: fig.getAttribute("data-words-none"), one: fig.getAttribute("data-words-one"),
    and: fig.getAttribute("data-words-and"), name: fig.getAttribute("data-name")
  };

  function rows() { return Array.prototype.slice.call(list.querySelectorAll("li.prow")); }
  function nameOf(row) { return row.querySelector(".an").textContent; }

  // blockGroup is a block row with the sub-setting or hint that follows it.
  function blockGroups(container) {
    var groups = [];
    Array.prototype.forEach.call(container.children, function (el) {
      if (el.hasAttribute("data-block")) groups.push([el]);
      else if (groups.length) groups[groups.length - 1].push(el);
    });
    return groups;
  }

  function sync() {
    var people = rows();
    var names = people.map(nameOf);
    people.forEach(function (row, i) {
      var pos = row.querySelector(".pos");
      var label = i >= visible ? fig.getAttribute("data-col-swipe") : fig.getAttribute("data-col");
      pos.textContent = fill(label, { n: String(i + 1) });
      var up = row.querySelector('[data-move="up"]');
      var down = row.querySelector('[data-move="down"]');
      up.disabled = i === 0;
      down.disabled = i === people.length - 1;
    });
    off.hidden = off.querySelectorAll("label").length === 0;
    var personal = people.length === 1 && !whole.checked;
    root.toggleAttribute("data-personal", personal);
    var rot = root.querySelector("[data-rotate]");
    rot.hidden = people.length < 2;
    rot.querySelector("[data-rotate-on]").textContent = fill(fig.getAttribute("data-tomorrow-on"), { names: tomorrow(names).join(", ") });
    var hint = root.querySelector("[data-hint-whole]");
    hint.textContent = hint.getAttribute(whole.checked ? "data-hint-whole" : "data-hint-some");

    // Zones that do not apply to a personal view keep their values.
    Array.prototype.forEach.call(root.querySelectorAll(".zone.z-cols, .zone.z-foot"), function (z) {
      z.classList.toggle("off", personal);
      var tag = z.querySelector("[data-off-tag]"), why = z.querySelector("[data-off-why]"), blks = z.querySelector("[data-blocks]");
      if (tag) tag.hidden = !personal;
      if (why) why.hidden = !personal;
      if (blks) blks.hidden = personal;
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-blocks]"), function (container) {
      var groups = blockGroups(container);
      groups.forEach(function (g, i) {
        var row = g[0];
        row.querySelector('[data-move="up"]').disabled = i === 0;
        row.querySelector('[data-move="down"]').disabled = i === groups.length - 1;
      });
    });

    // The preview.
    var chosen = {};
    people.forEach(function (row, i) { chosen[row.getAttribute("data-pid")] = i; });
    Array.prototype.forEach.call(mini.querySelectorAll(".m-col"), function (col) {
      var id = col.getAttribute("data-pid");
      var at = chosen[id];
      col.hidden = at === undefined;
      col.style.order = at === undefined ? 99 : at;
    });
    Array.prototype.forEach.call(mini.querySelectorAll(".m-pvp"), function (el) {
      var at = chosen[el.getAttribute("data-pid")];
      el.hidden = !(personal && at !== undefined);
    });
    mini.querySelector(".m-cols").style.setProperty("--vis", String(Math.min(Math.max(people.length, 1), visible)));
    var empty = mini.querySelector(".m-empty");
    if (empty) empty.hidden = people.length > 0;
    mini.classList.toggle("personal", personal);
    // The order of the blocks, by their rows.
    Array.prototype.forEach.call(root.querySelectorAll("[data-blocks]"), function (container) {
      blockGroups(container).forEach(function (g, i) {
        var id = g[0].getAttribute("data-block");
        Array.prototype.forEach.call(mini.querySelectorAll('[data-b="' + id + '"]'), function (el) { el.style.order = i; });
      });
    });
    var w = root.querySelector(".lay-words");
    w.textContent = words(texts, names, portrait, rotate.checked, personal);
  }

  function dirty() {
    var s = fig.querySelector(".pstate");
    if (!s || s.classList.contains("dirty")) return;
    s.classList.add("dirty");
    s.textContent = s.getAttribute("data-dirty-text");
  }

  function addPerson(label) {
    var li = rowTemplate.content.firstElementChild.cloneNode(true);
    li.setAttribute("data-pid", label.getAttribute("data-pid"));
    li.insertBefore(label, li.querySelector(".mv"));
    var name = label.querySelector(".an").textContent;
    li.querySelector('[data-move="up"]').setAttribute("aria-label", fill(list.getAttribute("data-label-up"), { name: name }));
    li.querySelector('[data-move="down"]').setAttribute("aria-label", fill(list.getAttribute("data-label-down"), { name: name }));
    list.appendChild(li);
  }

  root.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-move]");
    if (!btn || btn.disabled) return;
    var dir = btn.getAttribute("data-move") === "up" ? -1 : 1;
    var row = btn.closest("li.prow");
    if (row) {
      var all = rows();
      var i = all.indexOf(row);
      var other = all[i + dir];
      if (!other) return;
      if (dir < 0) list.insertBefore(row, other);
      else list.insertBefore(other, row);
    } else {
      var block = btn.closest("[data-block]");
      var container = block.parentNode;
      var groups = blockGroups(container);
      var at = groups.findIndex(function (g) { return g[0] === block; });
      var neighbour = groups[at + dir];
      if (!neighbour) return;
      if (dir < 0) groups[at].forEach(function (el) { container.insertBefore(el, neighbour[0]); });
      else {
        var after = neighbour[neighbour.length - 1].nextSibling;
        groups[at].forEach(function (el) { container.insertBefore(el, after); });
      }
    }
    dirty();
    sync();
    btn.focus();
  });

  root.addEventListener("change", function (e) {
    var input = e.target;
    if (input.name === "person") {
      var label = input.closest("label");
      if (input.checked) {
        addPerson(label);
      } else {
        var row = input.closest("li.prow");
        off.querySelector(".pwrap").appendChild(label);
        if (row) row.parentNode.removeChild(row);
        whole.checked = false;
      }
    } else if (input === whole && whole.checked) {
      Array.prototype.slice.call(off.querySelectorAll("label")).forEach(function (label) {
        label.querySelector("input").checked = true;
        addPerson(label);
      });
    }
    dirty();
    sync();
  });
  sync();
})();
