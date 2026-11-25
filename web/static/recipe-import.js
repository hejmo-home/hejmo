// The import page (ticket 289). Without this script the browser simply waits for the next page. With it, the
// button says what is happening: it is blocked (aria-disabled, no animation) and a line names the website that
// is read and how long it may take at most.

// rimpHost is the host of a typed link in lower case, "" when the text is no link.
function rimpHost(typed) {
  var t = String(typed || "").trim();
  if (!/^https?:\/\//i.test(t)) return "";
  var m = /^https?:\/\/(?:[^\/?#@]*@)?(\[[^\]]+\]|[^\/?#:]+)/i.exec(t);
  return m ? m[1].toLowerCase() : "";
}

// rimpWaitText writes the waiting line from its format with {host} in it; without a host the space before it goes too.
function rimpWaitText(format, host) {
  return String(format).replace(host ? "{host}" : " {host}", host);
}

(function () {
  var form = document.querySelector("form.rcp-imp");
  if (!form) return;
  var button = form.querySelector('button[type="submit"]');
  var wait = form.querySelector(".imp-wait");
  var field = form.querySelector('input[name="url"]');
  var busy = false;
  form.addEventListener("submit", function (e) {
    if (busy) { e.preventDefault(); return; }
    busy = true;
    if (button) { button.setAttribute("aria-disabled", "true"); }
    if (wait) {
      var b = wait.querySelector("[data-wait]");
      if (b) {
        var host = rimpHost(field ? field.value : "");
        b.innerHTML = "";
        b.appendChild(document.createTextNode(rimpWaitText(form.getAttribute("data-wait") || "", host)));
      }
      wait.hidden = false;
    }
  });
})();
