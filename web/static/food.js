// The sample under the free-text allergy field shows, as you type, how the
// entries will appear everywhere (ticket 163). Without this script the page
// shows the sample of what is stored.
(function () {
  var sample = document.querySelector("[data-food-sample]");
  var input = document.querySelector("[data-food-other]");
  if (!sample || !input) return;
  function show() {
    var seen = {};
    var items = input.value.split(",").map(function (s) { return s.trim().replace(/\s+/g, " "); }).filter(function (s) {
      var k = s.toLowerCase();
      if (!s || seen[k]) return false;
      seen[k] = true;
      return true;
    });
    sample.textContent = items.length
      ? sample.getAttribute("data-template").replace("{text}", items.join(", "))
      : sample.getAttribute("data-empty");
  }
  document.addEventListener("input", function (e) {
    if (e.target === input) show();
  });
  show();
})();
