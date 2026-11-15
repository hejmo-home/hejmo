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
