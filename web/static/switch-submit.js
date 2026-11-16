// A switch saves when it is flipped (waste collection on the calendars page).
// Without this script the form shows a Save button instead (<noscript>).
document.addEventListener("change", function (e) {
  var form = e.target.closest && e.target.closest("form[data-autosubmit]");
  if (form) form.requestSubmit();
});
