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
