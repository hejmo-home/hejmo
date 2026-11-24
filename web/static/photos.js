// Adding photos and choosing them (ticket 178, ADR 0020).
//
// The photos page uploads one file at a time and tells in words what happens to
// each: waiting, converting, uploading, done, or why it failed. A photo that
// is not a JPEG or PNG is looked at by its first bytes, not its name. HEIC
// photos are converted to JPEG here, in the browser: the browser's own decoder
// when it has one (Safari does), else libheif (LGPL-3.0, vendored under
// /static/vendor/libheif, loaded only when needed). The box then shrinks the
// photo to display size and drops the location.
//
// Without script the form posts the files and the box answers the page.
(function (global) {
  "use strict";

  var HEIC_BRANDS = ["heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs", "mif1", "msf1", "heif"];

  // sniff names what a file is by its first bytes: "jpeg", "png", "heic" or "".
  function sniff(b) {
    if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return "jpeg";
    if (b.length >= 8 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47) return "png";
    if (b.length >= 12 && String.fromCharCode(b[4], b[5], b[6], b[7]) === "ftyp") {
      var brand = String.fromCharCode(b[8], b[9], b[10], b[11]);
      if (HEIC_BRANDS.indexOf(brand) >= 0) return "heic";
    }
    return "";
  }

  function jpegName(name) { return name.replace(/\.[^.\/\\]*$/, "") + ".jpg"; }

  function fill(text, vals) {
    return String(text).replace(/\{(\w+)\}/g, function (m, k) { return k in vals ? vals[k] : m; });
  }

  // upload runs the files one after the other. deps: head(file) -> bytes,
  // convert(file) -> Blob, send(blob, name, onProgress) -> {status}. change(items,
  // phase) is called whenever something changed. A full box ends the sending:
  // the files left are refused at once, with the same reason.
  function upload(files, deps, change) {
    var items = files.map(function (f) { return { file: f, name: f.name, state: "wait", progress: 0, reason: "" }; });
    var ctl = { items: items, stopped: false, full: false };
    ctl.skip = function () { ctl.stopped = true; };
    ctl.done = (async function () {
      change(items, "start");
      for (var i = 0; i < items.length; i++) {
        var it = items[i];
        if (ctl.stopped) { items.splice(i); break; }
        if (ctl.full) { it.state = "fail"; it.reason = "full"; continue; }
        try {
          var kind = sniff(await deps.head(it.file));
          if (!kind) { it.state = "fail"; it.reason = "not_photo"; change(items, "step"); continue; }
          var blob = it.file, name = it.file.name;
          if (kind === "heic") {
            it.state = "convert"; change(items, "step");
            try { blob = await deps.convert(it.file); name = jpegName(name); }
            catch (e) { it.state = "fail"; it.reason = "heic"; change(items, "step"); continue; }
          }
          it.state = "upload"; it.progress = 0; change(items, "step");
          var res = await deps.send(blob, name, function (p) { it.progress = p; change(items, "progress"); });
          if (res.status === 201) { it.state = "done"; }
          else {
            it.state = "fail";
            it.reason = res.status === 507 ? "full" : res.status === 413 ? "too_large" : res.status === 422 ? "not_photo" : "failed";
            if (res.status === 507) ctl.full = true;
          }
        } catch (e) { it.state = "fail"; it.reason = "failed"; }
        change(items, "step");
      }
      change(items, "end");
      return items;
    })();
    return ctl;
  }

  // head and convert are the browser's part and are used by the contact form too (ticket 297); both are
  // function declarations further down, so they exist here already.
  global.HejmoPhotos = { sniff: sniff, jpegName: jpegName, fill: fill, upload: upload, head: head, convert: convert };

  var root = global.document && global.document.querySelector && global.document.querySelector("[data-photo-upload]");
  var doc = global.document;
  if (doc && doc.querySelector && doc.querySelector("[data-selection]")) wireSelection(doc);
  if (!doc || !root) return;

  // ---- the browser's part ----
  var T = {};
  try { T = JSON.parse(root.getAttribute("data-texts") || "{}"); } catch (e) {}
  var STORE = "hejmo-upload-result";

  function head(file) { return file.slice(0, 16).arrayBuffer().then(function (b) { return new Uint8Array(b); }); }

  function loadHeif() {
    if (loadHeif.p) return loadHeif.p;
    loadHeif.p = new Promise(function (ok, no) {
      var s = doc.createElement("script");
      s.src = "/static/vendor/libheif/libheif.js";
      s.onload = function () {
        Promise.resolve(global.libheif({ locateFile: function (f) { return "/static/vendor/libheif/" + f; } })).then(ok, no);
      };
      s.onerror = no;
      doc.head.appendChild(s);
    });
    return loadHeif.p;
  }

  function toJpeg(drawable, w, h) {
    var max = 3000, k = Math.min(1, max / Math.max(w, h));
    var c = doc.createElement("canvas");
    c.width = Math.round(w * k); c.height = Math.round(h * k);
    c.getContext("2d").drawImage(drawable, 0, 0, c.width, c.height);
    return new Promise(function (ok, no) { c.toBlob(function (b) { b ? ok(b) : no(new Error("toBlob")); }, "image/jpeg", 0.9); });
  }

  async function convert(file) {
    if (global.createImageBitmap) {
      try { var bmp = await global.createImageBitmap(file); return await toJpeg(bmp, bmp.width, bmp.height); } catch (e) { /* no decoder here: libheif */ }
    }
    var heif = await loadHeif();
    var images = new heif.HeifDecoder().decode(new Uint8Array(await file.arrayBuffer()));
    if (!images || !images.length) throw new Error("no image");
    var img = images[0], w = img.get_width(), h = img.get_height();
    var data = doc.createElement("canvas").getContext("2d").createImageData(w, h);
    await new Promise(function (ok, no) { img.display(data, function (d) { d ? ok() : no(new Error("heif")); }); });
    var full = doc.createElement("canvas");
    full.width = w; full.height = h;
    full.getContext("2d").putImageData(data, 0, 0);
    return toJpeg(full, w, h);
  }

  function send(albumID) {
    return function (blob, name, onProgress) {
      return new Promise(function (ok) {
        var x = new XMLHttpRequest();
        x.open("POST", root.getAttribute("data-api") + albumID + "/photos");
        x.upload.onprogress = function (e) { if (e.lengthComputable) onProgress(Math.round(100 * e.loaded / e.total)); };
        x.onload = function () { ok({ status: x.status }); };
        x.onerror = function () { ok({ status: 0 }); };
        var f = new FormData();
        f.append("file", blob, name);
        x.send(f);
      });
    };
  }

  var ICONS = {
    wait: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
    convert: '<path d="M4 12a8 8 0 0 1 14-5l2 2M20 4v5h-5M20 12a8 8 0 0 1-14 5l-2-2M4 20v-5h5"/>',
    upload: '<path d="M12 16V5M7.5 9.5L12 5l4.5 4.5M5 19h14"/>',
    done: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
    fail: '<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.5"/>'
  };
  function icon(kind) {
    var s = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
    s.setAttribute("class", "icon"); s.setAttribute("width", "18"); s.setAttribute("height", "18");
    s.setAttribute("viewBox", "0 0 24 24"); s.setAttribute("aria-hidden", "true");
    s.innerHTML = ICONS[kind];
    return s;
  }
  function el(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function meter(pct) {
    var m = el("div", "meter"), s = el("span");
    s.style.setProperty("--pct", pct);
    m.appendChild(s);
    return m;
  }

  function render(box, items, albumName, ctl) {
    var done = items.filter(function (i) { return i.state === "done"; }).length;
    var failed = items.filter(function (i) { return i.state === "fail"; }).length;
    var pct = items.length ? Math.round(100 * (done + failed) / items.length) : 100;
    box.textContent = "";
    box.appendChild(el("h2", "", T.uploading));
    var line = el("div", "progress-line");
    line.appendChild(el("b", "", fill(T.progress, { done: done, total: items.length })));
    var sub = fill(T.to_album, { album: albumName }) + (failed ? ", " + fill(T.n_failed, { n: failed }) : "");
    line.appendChild(el("span", "hint", sub));
    box.appendChild(line);
    box.appendChild(meter(pct));
    box.appendChild(el("p", "notice", T.keep_open));
    var ol = el("ol", "uplist");
    items.forEach(function (it) {
      var li = el("li");
      li.setAttribute("data-state", it.state === "convert" || it.state === "upload" ? "busy" : it.state);
      var th = el("span", "th"); th.appendChild(icon(it.state === "done" ? "done" : it.state === "fail" ? "fail" : "wait"));
      var tx = el("span", "tx");
      tx.appendChild(el("span", "nm", it.name));
      var st = el("span", "st");
      st.appendChild(icon(it.state === "fail" ? "fail" : it.state === "wait" ? "wait" : it.state));
      st.appendChild(el("span", "", it.state === "fail" ? T[it.reason] : T[it.state]));
      tx.appendChild(st);
      if (it.state === "upload") tx.appendChild(meter(it.progress));
      else if (it.state === "convert") tx.appendChild(meter(35));
      li.appendChild(th); li.appendChild(tx);
      ol.appendChild(li);
    });
    box.appendChild(ol);
    if (!ctl.finished) {
      var d = el("div"), b = el("button", "button secondary", T.skip);
      b.type = "button"; b.onclick = function () { ctl.skip(); b.disabled = true; };
      d.appendChild(b); box.appendChild(d);
    }
  }

  function start(fileList) {
    var files = Array.prototype.slice.call(fileList);
    if (!files.length) return;
    var select = root.querySelector("select[name=album]");
    var albumID = select ? select.value : root.getAttribute("data-album");
    var albumName = select ? select.options[select.selectedIndex].text : root.getAttribute("data-album-name");
    var box = root.querySelector("[data-upload-progress]");
    Array.prototype.forEach.call(root.children, function (c) { if (c !== box) c.hidden = true; });
    box.hidden = false;
    var ctl;
    ctl = upload(files, { head: head, convert: convert, send: send(albumID) }, function (items, phase) {
      if (ctl) ctl.finished = phase === "end";
      render(box, items, albumName, ctl || {});
    });
    ctl.done.then(function (items) {
      var added = items.filter(function (i) { return i.state === "done"; }).length;
      var refused = items.filter(function (i) { return i.state === "fail"; }).map(function (i) {
        return { name: i.name, reason: i.reason === "full" ? "" : T[i.reason] };
      });
      var allFull = refused.length > 0 && items.every(function (i) { return i.state !== "fail" || i.reason === "full"; });
      try {
        global.sessionStorage.setItem(STORE, JSON.stringify({ added: added, album: albumName, refused: refused, full: allFull }));
      } catch (e) {}
      global.location.reload();
    });
  }

  var pick = root.querySelector("[data-pick]");
  if (pick) pick.addEventListener("change", function () { start(pick.files); });

  // The result of the last upload, shown on the page that follows it.
  try {
    var raw = global.sessionStorage.getItem(STORE);
    if (raw) {
      global.sessionStorage.removeItem(STORE);
      var r = JSON.parse(raw), main = doc.getElementById("content");
      if (r.added > 0) {
        var msg = el("p", "msg ok", fill(T[r.added === 1 ? "added_text.one" : "added_text.other"], { n: r.added, album: r.album }));
        msg.setAttribute("role", "status");
        main.insertBefore(msg, main.querySelector("h1"));
      }
      if (r.refused.length) {
        var bx = el("div", "refused");
        bx.setAttribute("role", "alert");
        var n = r.refused.length;
        bx.appendChild(el("p", "", fill(T[(r.full ? "refused_full." : "refused_text.") + (n === 1 ? "one" : "other")], { n: n })));
        var ul = el("ul");
        r.refused.forEach(function (f) {
          var li = el("li");
          li.appendChild(el("b", "", f.name));
          if (f.reason) li.appendChild(doc.createTextNode(": " + f.reason));
          ul.appendChild(li);
        });
        bx.appendChild(ul);
        root.parentNode.insertBefore(bx, root);
      }
    }
  } catch (e) {}

  // ---- choosing photos on an album page ----
  function wireSelection(d) {
    var form = d.querySelector("[data-selection]");
    var boxes = function () { return d.querySelectorAll("input[name=photo][form=selection]"); };
    function update() {
      var n = 0;
      Array.prototype.forEach.call(boxes(), function (b) { if (b.checked) n++; });
      var form_ = d.querySelector("[data-selection]");
      var one = n === 1 ? "one" : "other";
      function set(sel, key) {
        var e = form_.querySelector(sel);
        if (e) e.textContent = form_.getAttribute("data-" + key + "-" + one).replace("{n}", n);
      }
      set("[data-sel-count]", "n");
      set("[data-sel-delete]", "delete");
      set("[data-sel-explain]", "explain");
      set("[data-sel-confirm]", "confirm");
    }
    d.addEventListener("change", function (e) { if (e.target && e.target.name === "photo") update(); });
    var clear = form.querySelector("[data-sel-clear]");
    if (clear) clear.addEventListener("click", function () {
      Array.prototype.forEach.call(boxes(), function (b) { b.checked = false; });
      update();
    });
    update();
  }
})(typeof window !== "undefined" ? window : globalThis);
