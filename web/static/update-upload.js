// Uploading an update file, and watching it install (ticket 221, Q154).
//
// The upload page shows the file's name and how much of it is on the box in
// words and megabytes, with a bar; the check of its name and size happens here
// before a single byte is sent. The installing page asks the box every five
// seconds how far the host script is and loads itself again when the step
// changed or the box is back from its restart. Without script the form posts the
// file and the box answers with the next page.
(function (global) {
  "use strict";

  var MIB = 1048576;
  var GONE_AFTER = 10 * 60 * 1000;

  function fill(text, vals) {
    return String(text).replace(/\{(\w+)\}/g, function (m, k) { return k in vals ? vals[k] : m; });
  }

  // megabytes is a size in whole megabytes, at least one.
  function megabytes(bytes) { return Math.max(1, Math.ceil(bytes / MIB)); }

  // check returns the error to show for the chosen file, or "".
  function check(file, cfg) {
    if (!file) return cfg.none;
    if (!/\.update$/i.test(file.name)) return cfg.name;
    if (file.size > cfg.maxBytes) return fill(cfg.big, { name: file.name, size: megabytes(file.size), max: cfg.maxMB });
    return "";
  }

  // progress is what the bar and its words say.
  function progress(loaded, total, template) {
    var done = Math.min(loaded, total);
    var pct = total > 0 ? Math.round((done / total) * 100) : 0;
    var now = Math.floor(done / MIB);
    var max = total > 0 ? megabytes(total) : 0;
    return { text: fill(template, { done: now, total: max }), now: now, max: max, pct: pct };
  }

  // poll decides what the installing page does with the box's answer (or its
  // silence). state: {step, failedSince}; answer: {ok, json}; now in ms.
  function poll(state, answer, now) {
    if (!answer.ok) {
      var since = state.failedSince === null || state.failedSince === undefined ? now : state.failedSince;
      return { reload: false, gone: now - since >= GONE_AFTER, failedSince: since };
    }
    var j = answer.json || {};
    var back = state.failedSince !== null && state.failedSince !== undefined;
    var changed = j.state !== "installing" || j.step !== state.step;
    return { reload: back || changed, gone: false, failedSince: null };
  }

  global.HejmoUpdate = { check: check, fill: fill, megabytes: megabytes, progress: progress, poll: poll };

  if (typeof document === "undefined") return;

  // ---- the upload form

  function initForm(form) {
    var input = form.querySelector("input[type=file]");
    var field = input.parentNode;
    var step = form.parentNode;
    var cfg = {
      none: form.getAttribute("data-err-none"), name: form.getAttribute("data-err-name"), big: form.getAttribute("data-err-big"),
      maxBytes: Number(form.getAttribute("data-max-bytes")), maxMB: Number(form.getAttribute("data-max-mb")),
    };
    var tpl = function (id) { return document.getElementById(id).content.cloneNode(true); };

    function showError(text) {
      var old = document.getElementById("err-update_file");
      if (old) old.parentNode.removeChild(old);
      var frag = tpl("upd-error");
      frag.querySelector("[data-text]").textContent = text;
      input.parentNode.insertBefore(frag, input.nextSibling);
      field.className = "f invalid";
      input.setAttribute("aria-invalid", "true");
      input.setAttribute("aria-describedby", "err-update_file hint-update_file");
    }

    form.addEventListener("submit", function (ev) {
      var file = input.files && input.files[0];
      var problem = check(file, cfg);
      if (problem) {
        ev.preventDefault();
        showError(problem);
        return;
      }
      if (typeof XMLHttpRequest === "undefined" || typeof FormData === "undefined") return; // the plain form works as well
      ev.preventDefault();

      var data = new FormData(form);
      var xhr = new XMLHttpRequest();
      var ofText = form.getAttribute("data-of");
      var doneText = form.getAttribute("data-done");
      var uploading = tpl("upd-uploading");
      uploading.querySelector("[data-name]").textContent = file.name;
      var bar = uploading.querySelector(".meter");
      var barFill = bar.querySelector("span");
      var words = uploading.querySelector("[data-progress]");
      var cancel = uploading.querySelector("[data-cancel]");
      var start = progress(0, file.size, ofText);
      words.textContent = start.text;
      bar.setAttribute("aria-valuemax", String(start.max));
      bar.setAttribute("aria-valuetext", start.text);
      form.hidden = true;
      var shown = [];
      while (uploading.firstChild) { shown.push(uploading.firstChild); step.appendChild(uploading.firstChild); }

      function restore(text) {
        for (var i = 0; i < shown.length; i++) if (shown[i].parentNode) shown[i].parentNode.removeChild(shown[i]);
        form.hidden = false;
        if (text) showError(text);
      }

      cancel.addEventListener("click", function () { xhr.abort(); restore(""); });
      xhr.upload.onprogress = function (e) {
        var p = progress(e.loaded, e.total || file.size, ofText);
        words.textContent = p.text;
        bar.setAttribute("aria-valuenow", String(p.now));
        bar.setAttribute("aria-valuetext", p.text);
        barFill.style.setProperty("--pct", String(p.pct));
      };
      xhr.upload.onload = function () {
        // The bytes are on the box; now it checks the signature.
        for (var i = 0; i < shown.length; i++) if (shown[i].parentNode) shown[i].parentNode.removeChild(shown[i]);
        var checking = tpl("upd-checking");
        checking.querySelector("[data-name]").textContent = file.name;
        checking.querySelector("[data-done]").textContent = fill(doneText, { size: megabytes(file.size) });
        shown = [];
        while (checking.firstChild) { shown.push(checking.firstChild); step.appendChild(checking.firstChild); }
      };
      xhr.onerror = function () { restore(form.getAttribute("data-err-interrupted")); };
      xhr.onload = function () {
        var target = xhr.responseURL ? new URL(xhr.responseURL).pathname : "";
        if (xhr.status < 400 && target && target !== new URL(form.action, location.href).pathname) {
          location.href = xhr.responseURL; // the confirmation page
          return;
        }
        // The box answered with the form again, and the reason at the field.
        document.open();
        document.write(xhr.responseText);
        document.close();
      };
      xhr.open("POST", form.action);
      xhr.setRequestHeader("Accept", "text/html");
      xhr.send(data);
    });
  }

  // ---- the installing page

  function initProgress(section) {
    var url = section.getAttribute("data-progress-url");
    var state = { step: section.getAttribute("data-step"), failedSince: null };
    var gone = document.getElementById("gone");
    function apply(r) {
      state.failedSince = r.failedSince;
      if (r.gone && gone) gone.hidden = false;
      if (r.reload) location.reload();
    }
    function ask() {
      fetch(url, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } })
        .then(function (res) {
          if (!res.ok) throw new Error(String(res.status));
          return res.json();
        })
        .then(function (json) { apply(poll(state, { ok: true, json: json }, Date.now())); })
        .catch(function () { apply(poll(state, { ok: false }, Date.now())); });
    }
    setInterval(ask, 5000);
  }

  var form = document.querySelector("form.upd-form");
  if (form) initForm(form);
  var section = document.querySelector("[data-progress-url]");
  if (section) initProgress(section);
})(globalThis);
