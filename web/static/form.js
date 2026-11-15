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
