// The counter under a note field follows the text while you type. The server
// renders the same text without this script.
document.querySelectorAll("textarea[data-counter]").forEach(function (area) {
  var counter = area.closest(".f").querySelector(".counter[data-format]");
  if (!counter) return;
  function update() {
    counter.textContent = counter.getAttribute("data-format").replace("%d", Array.from(area.value.trim()).length);
  }
  area.addEventListener("input", update);
});
