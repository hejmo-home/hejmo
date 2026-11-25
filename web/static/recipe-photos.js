// Adding photos to a recipe (ticket 280). A photo is saved as soon as it is chosen: no second step. HEIC photos
// are turned into JPEG here first (the box has no HEIC decoder), one after the other, and the page says in words
// what happens. Without this script the form has its own "Hochladen" button and the box refuses HEIC.
(function (global) {
  "use strict";

  function fill(text, vals) {
    return String(text).replace(/\{(\w+)\}/g, function (m, k) { return k in vals ? vals[k] : m; });
  }

  // prepare goes through the files: a HEIC file is converted, anything else stays as it is (the box says what is
  // wrong with it). deps: head(file) -> bytes, convert(file) -> Blob, sniff(bytes) -> kind. step(done, total, file,
  // phase) is called before each file is looked at. It returns the files to send, each as {blob, name}.
  async function prepare(files, deps, step) {
    var out = [];
    for (var i = 0; i < files.length; i++) {
      var f = files[i], kind = "";
      step(i, files.length, f, "look");
      try { kind = deps.sniff(await deps.head(f)); } catch (e) { kind = ""; }
      if (kind === "heic") {
        step(i, files.length, f, "convert");
        try { out.push({ blob: await deps.convert(f), name: deps.jpegName(f.name) }); continue; } catch (e) { /* the box refuses it with its reason */ }
      }
      out.push({ blob: f, name: f.name });
    }
    return out;
  }

  global.HejmoRecipePhotos = { fill: fill, prepare: prepare };

  var doc = global.document;
  if (!doc || !doc.querySelectorAll || !global.HejmoPhotos) return;
  var P = global.HejmoPhotos;

  function icon() {
    var s = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
    s.setAttribute("class", "icon"); s.setAttribute("width", "20"); s.setAttribute("height", "20");
    s.setAttribute("viewBox", "0 0 24 24"); s.setAttribute("aria-hidden", "true");
    s.innerHTML = '<path d="M12 16V5M7.5 9.5L12 5l4.5 4.5M5 19h14"/>';
    return s;
  }

  function wire(form) {
    var section = form.closest("[data-rph-upload]");
    var T = {};
    try { T = JSON.parse((section || form).getAttribute("data-texts") || "{}"); } catch (e) {}
    var busy = false;
    form.addEventListener("change", async function (ev) {
      var input = ev.target;
      if (busy || !input || input.type !== "file" || !input.files || !input.files.length) return;
      busy = true;
      var files = Array.prototype.slice.call(input.files);
      var pick = form.querySelector(".ph-pick");
      var line = null;
      if (T.uploading && pick) {
        var btn = doc.createElement("span");
        btn.className = "button"; btn.setAttribute("aria-disabled", "true");
        btn.appendChild(icon()); btn.appendChild(doc.createTextNode(T.uploading));
        pick.textContent = ""; pick.appendChild(btn);
        line = doc.createElement("p");
        line.className = "progress-line"; line.setAttribute("role", "status");
        pick.parentNode.insertBefore(line, pick.nextSibling);
      }
      var sent = await prepare(files, { head: P.head, convert: P.convert, sniff: P.sniff, jpegName: P.jpegName }, function (done, total, f, phase) {
        if (!line) return;
        line.textContent = "";
        var b = doc.createElement("b"); b.textContent = fill(T.progress || "", { done: done, total: total });
        var sp = doc.createElement("span"); sp.textContent = fill(phase === "convert" ? T.convert || "" : T.send || "", { name: f.name });
        line.appendChild(b); line.appendChild(sp);
      });
      try {
        var dt = new global.DataTransfer();
        sent.forEach(function (s) { dt.items.add(new global.File([s.blob], s.name, { type: s.blob.type || "image/jpeg" })); });
        input.files = dt.files;
      } catch (e) { /* the files stay as they were chosen */ }
      form.submit();
    });
  }

  Array.prototype.forEach.call(doc.querySelectorAll("form[data-rph]"), wire);
})(typeof window !== "undefined" ? window : globalThis);
