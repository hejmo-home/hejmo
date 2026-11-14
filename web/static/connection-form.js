// Connection form: the server address and the "single calendar" box only make
// sense for "Other CalDAV server", so hide them for iCloud. Without this script
// both stay visible and the server ignores them for iCloud.
(function () {
  var form = document.querySelector("form[data-connection-form]");
  if (!form) return;
  var provider = form.querySelector("select[name=provider]");
  var fields = form.querySelectorAll("[data-other-only]");
  function update() {
    var other = provider.value === "caldav";
    for (var i = 0; i < fields.length; i++) fields[i].hidden = !other;
  }
  provider.addEventListener("change", update);
  update();
})();
