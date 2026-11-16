// Recovery codes card (ticket 176): "Download as PDF" and the required "saved"
// checkbox. Hejmo's own small PDF writer: one A4 page of real text in the
// standard fonts, no library. The codes are read from the page and the file is
// made here; nothing is sent anywhere and the box stores nothing.
(function (root) {
  "use strict";

  // ---- fonts -------------------------------------------------------------
  // Widths of the standard Helvetica fonts (per 1000 em), ASCII 32..126. Used
  // to wrap text and to right-align; letters with accents take the width of
  // the plain letter.
  var HELV = [278,278,355,556,556,889,667,191,333,333,389,584,278,333,278,278,556,556,556,556,556,556,556,556,556,556,278,278,584,584,584,556,1015,667,667,722,722,667,611,778,722,278,500,667,556,833,722,778,667,778,722,667,611,722,667,944,667,667,611,278,278,278,469,556,333,556,556,500,556,556,278,556,556,222,222,500,222,833,556,556,556,556,333,500,278,556,500,722,500,500,500,334,260,334,584];
  var BOLD = [278,333,474,556,556,889,722,238,333,333,389,584,278,333,278,278,556,556,556,556,556,556,556,556,556,556,333,333,584,584,584,611,975,722,722,722,722,667,611,778,722,278,556,722,611,833,722,778,667,778,722,667,611,722,667,944,667,667,611,333,278,333,584,556,333,556,611,556,611,556,333,611,611,278,278,556,278,889,611,611,611,611,389,556,333,611,556,778,556,556,500,389,280,389,584];
  var FONTS = { F1: { base: "Helvetica", table: HELV }, F2: { base: "Helvetica-Bold", table: BOLD }, F3: { base: "Courier-Bold", table: null } };

  function plain(ch) {
    var d = ch.normalize ? ch.normalize("NFD").charAt(0) : ch;
    return d && d.charCodeAt(0) < 127 ? d : "";
  }
  function charWidth(font, ch) {
    var f = FONTS[font], code = ch.charCodeAt(0);
    if (!f.table) return 600;
    if (code >= 32 && code < 127) return f.table[code - 32];
    if (code === 0xdf) return 611;
    if (code === 0xb7) return 278;
    if (code === 0x2022) return 350;
    var base = plain(ch);
    return base && base.charCodeAt(0) >= 32 ? f.table[base.charCodeAt(0) - 32] : 556;
  }
  function width(text, font, size) {
    var w = 0;
    for (var i = 0; i < text.length; i++) w += charWidth(font, text.charAt(i));
    return (w * size) / 1000;
  }

  // Text to the bytes of WinAnsiEncoding (Latin-1 plus a few signs); letters
  // outside it lose their accent or become "?", never break the file.
  function encode(text) {
    var out = "";
    for (var i = 0; i < text.length; i++) {
      var ch = text.charAt(i), c = ch.charCodeAt(0);
      if (c === 0x2022) out += "\x95";
      else if (c === 0x20ac) out += "\x80";
      else if (c === 0x2013) out += "\x96";
      else if ((c >= 32 && c < 127) || (c >= 0xa0 && c <= 0xff)) out += ch;
      else if (plain(ch) && plain(ch).charCodeAt(0) >= 32) out += plain(ch);
      else out += "?";
    }
    return out;
  }
  function literal(text) {
    return "(" + encode(text).replace(/[\\()]/g, "\\$&") + ")";
  }

  // Break text into lines of at most max points. Words stay whole unless one is
  // longer than a line (a long box name or user name): then it is cut anywhere.
  function wrap(text, font, size, max) {
    var lines = [], line = "";
    String(text).split(" ").forEach(function (word) {
      var tryLine = line ? line + " " + word : word;
      if (width(tryLine, font, size) <= max) { line = tryLine; return; }
      if (line) { lines.push(line); line = ""; }
      while (width(word, font, size) > max) {
        var cut = word.length - 1;
        while (cut > 1 && width(word.slice(0, cut), font, size) > max) cut--;
        lines.push(word.slice(0, cut));
        word = word.slice(cut);
      }
      line = word;
    });
    if (line) lines.push(line);
    return lines;
  }

  // ---- the page ----------------------------------------------------------
  var W = 595.28, H = 841.89, MX = 57, MT = 51;

  function n(v) { return (Math.round(v * 100) / 100).toString(); }

  function build(doc, codes) {
    var ops = [];
    var contentW = W - 2 * MX;
    function y(top) { return H - top; } // top-down coordinates
    function text(str, font, size, x, top, gray) {
      ops.push("BT /" + font + " " + n(size) + " Tf " + n(gray || 0) + " g " + n(x) + " " + n(y(top)) + " Td " + literal(str) + " Tj ET");
      ops.push("0 g");
    }
    function line(x1, t1, x2, t2, lw, gray) {
      ops.push(n(gray || 0) + " G " + n(lw) + " w " + n(x1) + " " + n(y(t1)) + " m " + n(x2) + " " + n(y(t2)) + " l S 0 G");
    }
    function rounded(x, top, w, h, r, lw) {
      var k = r * 0.5523, b = y(top + h), t = y(top), x2 = x + w;
      ops.push(n(lw) + " w " + n(x + r) + " " + n(t) + " m " + n(x2 - r) + " " + n(t) + " l " +
        n(x2 - r + k) + " " + n(t) + " " + n(x2) + " " + n(t - r + k) + " " + n(x2) + " " + n(t - r) + " c " +
        n(x2) + " " + n(b + r) + " l " + n(x2) + " " + n(b + r - k) + " " + n(x2 - r + k) + " " + n(b) + " " + n(x2 - r) + " " + n(b) + " c " +
        n(x + r) + " " + n(b) + " l " + n(x + r - k) + " " + n(b) + " " + n(x) + " " + n(b + r - k) + " " + n(x) + " " + n(b + r) + " c " +
        n(x) + " " + n(t - r) + " l " + n(x) + " " + n(t - r + k) + " " + n(x + r - k) + " " + n(t) + " " + n(x + r) + " " + n(t) + " c S");
    }

    // Header: brand, the document line at the right, a rule.
    var top = MT + 16.5;
    text("Hejmo", "F2", 16.5, MX, top);
    text(doc.page, "F1", 10.5, W - MX - width(doc.page, "F1", 10.5), top);
    line(MX, top + 8, W - MX, top + 8, 1.5);
    top += 8 + 22;

    // Title (wraps), then Box / Username / Created on.
    wrap(doc.title, "F2", 22.5, contentW).forEach(function (l) { top += 22.5; text(l, "F2", 22.5, MX, top); top += 3.4; });
    top += 14;
    var rows = [[doc.boxLabel, doc.box], [doc.userLabel, doc.username], [doc.dateLabel, doc.date]];
    var labelW = 0;
    rows.forEach(function (r) { labelW = Math.max(labelW, width(r[0], "F1", 12)); });
    var valueX = MX + labelW + 14, valueW = W - MX - valueX;
    rows.forEach(function (r) {
      var vl = wrap(r[1], "F2", 12, valueW);
      text(r[0], "F1", 12, MX, top + 12, 0.267);
      vl.forEach(function (l, i) { text(l, "F2", 12, valueX, top + 12 + i * 15); });
      top += vl.length * 15 + 4.5;
    });
    top += 12;

    // The codes: two columns, an empty box per code to tick off a used one.
    var colW = (contentW - 21) / 2, rowH = 38;
    codes.forEach(function (code, i) {
      var cx = MX + (i % 2) * (colW + 21), ct = top + Math.floor(i / 2) * (rowH + 9);
      rounded(cx, ct, colW, rowH, 7.5, 1.1);
      text((i + 1) + ".", "F1", 10.5, cx + 10, ct + 23.5, 0.267);
      text(code, "F3", 16.5, cx + 36, ct + 24.5);
      ops.push("% tick-box\n1.1 w " + n(cx + colW - 10 - 15) + " " + n(y(ct + (rowH - 15) / 2 + 15)) + " 15 15 re S");
    });
    top += Math.ceil(codes.length / 2) * (rowH + 9) - 9 + 10;
    text(doc.usedHint, "F1", 9.75, W - MX - width(doc.usedHint, "F1", 9.75), top + 9.75, 0.267);
    top += 9.75 + 18;

    // "How to use them" in a bordered box.
    var bulletW = contentW - 28 - 14;
    var items = doc.how.map(function (t) { return wrap(t, "F1", 11.25, bulletW); });
    var count = items.reduce(function (s, l) { return s + l.length; }, 0);
    var boxH = 10.5 + 12.75 + 6 + count * 16.3 + (items.length - 1) * 2.2 + 10.5;
    rounded(MX, top, contentW, boxH, 9, 1.1);
    var ty = top + 10.5 + 12.75;
    text(doc.howTitle, "F2", 12.75, MX + 14, ty);
    ty += 6;
    items.forEach(function (ls, idx) {
      ls.forEach(function (l, j) {
        ty += 16.3;
        if (j === 0) text("•", "F1", 11.25, MX + 14 + 3, ty);
        text(l, "F1", 11.25, MX + 14 + 14, ty);
      });
      if (idx < items.length - 1) ty += 2.2;
    });

    // Footer: that the box keeps no readable copy, and the box name.
    var fy = H - MT;
    line(MX, fy - 22, W - MX, fy - 22, 0.75, 0.6);
    var boxW = width(doc.box, "F1", 9), showBox = boxW < 170;
    var fl = wrap(doc.footer, "F1", 9, contentW - (showBox ? boxW + 16 : 0));
    fl.forEach(function (l, i) { text(l, "F1", 9, MX, fy - 8 - (fl.length - 1 - i) * 11, 0.267); });
    if (showBox) text(doc.box, "F1", 9, W - MX - boxW, fy - 8, 0.267);

    return assemble(ops.join("\n") + "\n", doc);
  }

  // Objects: 1 catalog, 2 pages, 3 page, 4 contents, 5-7 fonts, 8 info.
  function assemble(stream, doc) {
    var objs = [
      "<</Type /Catalog /Pages 2 0 R" + (doc.lang ? " /Lang " + literal(doc.lang) : "") + ">>",
      "<</Type /Pages /Kids [3 0 R] /Count 1>>",
      "<</Type /Page /Parent 2 0 R /MediaBox [0 0 595.28 841.89] /Resources <</Font <</F1 5 0 R /F2 6 0 R /F3 7 0 R>>>> /Contents 4 0 R>>",
      "<</Length " + stream.length + ">>\nstream\n" + stream + "\nendstream",
    ];
    ["F1", "F2", "F3"].forEach(function (k) { objs.push("<</Type /Font /Subtype /Type1 /BaseFont /" + FONTS[k].base + " /Encoding /WinAnsiEncoding>>"); });
    objs.push("<</Title " + literal(doc.title) + " /Producer (Hejmo)>>");
    var out = "%PDF-1.4\n", offsets = [];
    objs.forEach(function (o, i) { offsets.push(out.length); out += (i + 1) + " 0 obj\n" + o + "\nendobj\n"; });
    var xref = out.length;
    out += "xref\n0 " + (objs.length + 1) + "\n0000000000 65535 f \n";
    offsets.forEach(function (o) { out += ("0000000000" + o).slice(-10) + " 00000 n \n"; });
    out += "trailer\n<</Size " + (objs.length + 1) + " /Root 1 0 R /Info 8 0 R>>\nstartxref\n" + xref + "\n%%EOF\n";
    var bytes = new Uint8Array(out.length);
    for (var i = 0; i < out.length; i++) bytes[i] = out.charCodeAt(i) & 255;
    return bytes;
  }

  root.hejmoRecoveryPdf = { build: build, wrap: wrap, width: width };

  // ---- the card ----------------------------------------------------------
  var doc = typeof document !== "undefined" ? document : null;
  var form = doc && doc.querySelector("form[data-rc]");
  if (!form) return;
  var info;
  try { info = JSON.parse(doc.querySelector("[data-rc-doc]").textContent); } catch (e) { info = null; }
  var save = form.querySelector(".rc-save"), gate = form.querySelector(".rc-gate");
  var pdfButton = form.querySelector("[data-rc-pdf]");
  var box = form.querySelector('input[name="saved"]');
  var submit = form.querySelector('button[type="submit"]');
  var lock = form.querySelector("[data-rc-lock]");
  var ERR = '<svg class="icon" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 8v5M12 16.5v.5"/><circle cx="12" cy="12" r="9"/></svg>';
  var OK = '<svg class="icon" width="20" height="20" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';

  function esc(t) { return String(t).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
  function find(parent, attr, value) {
    return Array.prototype.filter.call(parent.children, function (c) { return c.getAttribute(attr) === value; });
  }
  // One message at the buttons at a time: the result of the last download.
  function say(kind, html) {
    find(save, "data-rc-msg", "1").forEach(function (m) { m.remove(); });
    var p = doc.createElement("p");
    p.setAttribute("data-rc-msg", "1");
    p.setAttribute("role", kind === "ok" ? "status" : "alert");
    p.className = kind === "ok" ? "msg ok" : "ferr";
    p.innerHTML = (kind === "ok" ? OK : ERR) + "<span>" + html + "</span>";
    save.insertBefore(p, save.querySelector(".hint"));
  }

  if (pdfButton && info) {
    pdfButton.addEventListener("click", function () {
      try {
        var codes = Array.prototype.map.call(form.querySelectorAll("[data-recovery-code]"), function (c) { return c.getAttribute("data-recovery-code"); });
        var file = info.file + ".pdf";
        var url = URL.createObjectURL(new Blob([build(info, codes)], { type: "application/pdf" }));
        var a = doc.createElement("a");
        a.href = url;
        a.setAttribute("download", file);
        doc.body.appendChild(a);
        a.click();
        doc.body.removeChild(a);
        setTimeout(function () { URL.revokeObjectURL(url); }, 10000);
        var parts = info.ok.split("{file}");
        say("ok", esc(parts[0]) + '<b class="fname">' + esc(file) + "</b>" + esc(parts.slice(1).join("{file}")));
      } catch (e) {
        say("err", esc(info.failed));
      }
    });
  }

  function clearError() {
    box.removeAttribute("aria-invalid");
    box.removeAttribute("aria-describedby");
    find(gate, "id", "err-saved").forEach(function (c) { c.remove(); });
  }
  function sync() {
    if (box.checked) {
      submit.removeAttribute("aria-disabled");
      if (lock) lock.hidden = true;
      clearError();
    } else {
      submit.setAttribute("aria-disabled", "true");
      if (lock) lock.hidden = false;
    }
  }
  if (box && submit) {
    box.addEventListener("change", sync);
    form.addEventListener("submit", function (e) {
      if (box.checked) return;
      e.preventDefault();
      if (!find(gate, "id", "err-saved").length) {
        var p = doc.createElement("p");
        p.className = "ferr";
        p.setAttribute("id", "err-saved");
        p.setAttribute("role", "alert");
        p.innerHTML = ERR + "<span>" + esc(info ? info.gateError : "") + "</span>";
        gate.insertBefore(p, gate.querySelector(".actions"));
      }
      box.setAttribute("aria-invalid", "true");
      box.setAttribute("aria-describedby", "err-saved");
      box.focus();
    });
  }
})(typeof window !== "undefined" ? window : this);
