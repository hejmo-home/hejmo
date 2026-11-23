// Pairing confirmation (#203, Q148): choosing a person puts the name proposed for that person in
// the name field, and choosing another person changes it, as long as the parent has not typed a
// name. The box renders the proposals (by the kind of screen the device opened the code with);
// without this script the parent types the name.
document.addEventListener("change", function (e) {
  var input = e.target;
  if (!input || input.name !== "person" || !input.checked || !input.form) return;
  var raw = input.form.getAttribute("data-name-proposals");
  var field = input.form.elements.name;
  if (!raw || !field) return;
  var proposals;
  try { proposals = JSON.parse(raw); } catch (err) { return; }
  var next = proposals[input.value];
  if (!next) return;
  if (field.value === "" || field.value === field.getAttribute("data-proposed")) {
    field.value = next;
    field.setAttribute("data-proposed", next);
  }
});
