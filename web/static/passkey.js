// Passkeys (ticket 95, ADR 0018): the page side of creating a passkey, signing
// in with one and confirming a reset with one. The box does all the checking;
// this script only passes what the browser's credentials API wants and answers
// between the page and the box. Without it nothing here works, and the pages
// say so in words: the password and the one-time code stay the way in.
(function () {
  "use strict";

  // The box sends binary values as base64url text; the browser wants bytes.
  function decode(text) {
    var s = text.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var raw = atob(s), out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out.buffer;
  }
  function encode(buffer) {
    var bytes = new Uint8Array(buffer), s = "";
    for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function decodeDescriptors(list) {
    return (list || []).map(function (d) {
      var out = { type: d.type, id: decode(d.id) };
      if (d.transports) out.transports = d.transports;
      return out;
    });
  }

  // Options for navigator.credentials.create, from the box's JSON.
  function creationOptions(options) {
    var pk = options.publicKey;
    var out = {};
    for (var k in pk) out[k] = pk[k];
    out.challenge = decode(pk.challenge);
    out.user = { id: decode(pk.user.id), name: pk.user.name, displayName: pk.user.displayName };
    out.excludeCredentials = decodeDescriptors(pk.excludeCredentials);
    return { publicKey: out };
  }

  // Options for navigator.credentials.get, from the box's JSON.
  function requestOptions(options) {
    var pk = options.publicKey;
    var out = {};
    for (var k in pk) out[k] = pk[k];
    out.challenge = decode(pk.challenge);
    out.allowCredentials = decodeDescriptors(pk.allowCredentials);
    return { publicKey: out };
  }

  // What the box checks: the credential the browser made, as JSON text.
  function createdJSON(cred) {
    var r = cred.response;
    var out = {
      id: cred.id, rawId: encode(cred.rawId), type: cred.type,
      response: { attestationObject: encode(r.attestationObject), clientDataJSON: encode(r.clientDataJSON) },
      clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {}
    };
    if (r.getTransports) out.response.transports = r.getTransports();
    if (cred.authenticatorAttachment) out.authenticatorAttachment = cred.authenticatorAttachment;
    return out;
  }

  function assertedJSON(cred) {
    var r = cred.response;
    var out = {
      id: cred.id, rawId: encode(cred.rawId), type: cred.type,
      response: {
        authenticatorData: encode(r.authenticatorData), clientDataJSON: encode(r.clientDataJSON),
        signature: encode(r.signature), userHandle: r.userHandle ? encode(r.userHandle) : ""
      },
      clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {}
    };
    if (cred.authenticatorAttachment) out.authenticatorAttachment = cred.authenticatorAttachment;
    return out;
  }

  var codec = { decode: decode, encode: encode, creationOptions: creationOptions, requestOptions: requestOptions, createdJSON: createdJSON, assertedJSON: assertedJSON };
  if (typeof window !== "undefined") window.hejmoPasskeyCodec = codec;
  if (typeof document === "undefined" || typeof navigator === "undefined") return;

  var supported = !!(window.PublicKeyCredential && navigator.credentials && navigator.credentials.create);

  function post(url, body) {
    return fetch(url, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: body === undefined ? "" : JSON.stringify(body)
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) { return { status: res.status, data: data }; });
    });
  }

  function hook(scope, name) { return scope.querySelector('[data-hook="' + name + '"]'); }
  function show(el, on) { if (el) el.hidden = !on; }

  // The state of a creation, in words on the page (no animation: E-Ink).
  function waiting(scope, button, on) {
    show(hook(scope, "waiting"), on);
    if (on) { show(hook(scope, "fail"), false); show(hook(scope, "have"), false); }
    if (!button) return;
    if (on) {
      button.disabled = true;
      if (button.getAttribute("data-label") === null) button.setAttribute("data-label", button.textContent.trim());
      setLabel(button, button.getAttribute("data-working"));
    } else {
      button.disabled = false;
      setLabel(button, button.getAttribute("data-label"));
    }
  }
  // Only the text changes; the key icon in front of it stays.
  function setLabel(button, text) {
    if (text === null || text === undefined) return;
    var nodes = button.childNodes;
    for (var i = nodes.length - 1; i >= 0; i--) {
      if (nodes[i].nodeType === 3) { nodes[i].nodeValue = text; return; }
    }
    button.appendChild(document.createTextNode(text));
  }

  function create(scope, button) {
    var nameInput = scope.querySelector('input[name="name"]');
    waiting(scope, button, true);
    var failed = function (e) {
      waiting(scope, button, false);
      if (e && e.name === "InvalidStateError") { // this device has a passkey for the account already
        show(hook(scope, "have"), !!hook(scope, "have"));
        show(hook(scope, "continue"), true);
        show(hook(scope, "skip"), false);
        if (hook(scope, "have")) { if (button && hook(scope, "continue")) button.hidden = true; return; }
      }
      show(hook(scope, "fail"), true);
      if (button && button.getAttribute("data-retry")) setLabel(button, button.getAttribute("data-retry"));
    };
    if (!supported) return failed(null);
    post("/account/passkeys/begin").then(function (begun) {
      if (begun.status !== 200) throw new Error("begin " + begun.status);
      return navigator.credentials.create(creationOptions(begun.data.options)).then(function (cred) {
        if (!cred) throw new Error("no credential");
        return post("/account/passkeys/finish", { ceremony: begun.data.ceremony, credential: createdJSON(cred), name: nameInput ? nameInput.value : "" });
      });
    }).then(function (done) {
      if (done.status !== 200) throw new Error("finish " + done.status);
      window.location.assign(done.data.redirect || scope.getAttribute("data-redirect") || "/account");
    }).catch(failed);
  }

  var conditional = null; // the pending autofill request of the sign-in page

  function signIn(scope, button) {
    var next = scope.getAttribute("data-next") || "";
    var script = document.querySelector("script[data-gone]");
    var where = function (key, fallback) { return (script && script.getAttribute("data-" + key)) || fallback; };
    var go = function (key, fallback) { window.location.assign(where(key, fallback)); };
    if (conditional) { conditional.abort(); conditional = null; }
    if (button) button.disabled = true;
    post("/login/passkey/begin").then(function (begun) {
      if (begun.status !== 200) throw new Error("begin " + begun.status);
      return navigator.credentials.get(requestOptions(begun.data.options)).then(function (cred) {
        if (!cred) throw new Error("no credential");
        return post("/login/passkey/finish", { ceremony: begun.data.ceremony, credential: assertedJSON(cred), next: next });
      });
    }).then(function (done) {
      if (done.status === 200) return window.location.assign(done.data.redirect || "/");
      if (done.status === 401 && done.data.error === "passkey_gone") return go("gone", "/login?passkey=gone");
      return go("failed", "/login?passkey=failed");
    }).catch(function () { go("cancelled", "/login?passkey=cancelled"); });
  }

  // A browser that can offer passkeys in the user name field does so; choosing one
  // there signs in just like the button.
  function offerInField(scope) {
    if (!supported || !window.PublicKeyCredential.isConditionalMediationAvailable) return;
    window.PublicKeyCredential.isConditionalMediationAvailable().then(function (ok) {
      if (!ok) return;
      conditional = new AbortController();
      var signal = conditional.signal;
      var next = scope.getAttribute("data-next") || "";
      post("/login/passkey/begin").then(function (begun) {
        if (begun.status !== 200) return;
        var opts = requestOptions(begun.data.options);
        opts.mediation = "conditional";
        opts.signal = signal;
        return navigator.credentials.get(opts).then(function (cred) {
          if (!cred) return;
          return post("/login/passkey/finish", { ceremony: begun.data.ceremony, credential: assertedJSON(cred), next: next }).then(function (done) {
            if (done.status === 200) window.location.assign(done.data.redirect || "/");
          });
        });
      }).catch(function () { /* nothing was chosen; the form and the button stay */ });
    }).catch(function () {});
  }

  // Confirming the reset of another parent's one-time codes with one's own passkey.
  function confirm(button) {
    var form = button.closest("form");
    var fail = form && form.querySelector('[data-hook="fail"]');
    show(fail, false);
    button.disabled = true;
    var back = function () { button.disabled = false; show(fail, true); };
    if (!supported) return back();
    post("/account/passkeys/assert/begin").then(function (begun) {
      if (begun.status !== 200) throw new Error("begin " + begun.status);
      return navigator.credentials.get(requestOptions(begun.data.options)).then(function (cred) {
        if (!cred) throw new Error("no credential");
        [["ceremony", begun.data.ceremony], ["assertion", JSON.stringify(assertedJSON(cred))]].forEach(function (f) {
          var input = document.createElement("input");
          input.type = "hidden"; input.name = f[0]; input.value = f[1];
          form.appendChild(input);
        });
        form.submit();
      });
    }).catch(back);
  }

  document.addEventListener("click", function (e) {
    var t = e.target && e.target.closest ? e.target : null;
    if (!t) return;
    var add = t.closest("[data-passkey-add]");
    if (add) { e.preventDefault(); create(add.closest("[data-passkey-scope]") || document, add); return; }
    var sign = t.closest("[data-passkey-signin]");
    if (sign) { e.preventDefault(); signIn(sign.closest("[data-passkey-scope]") || document, sign); return; }
    var conf = t.closest("[data-passkey-confirm]");
    if (conf) { e.preventDefault(); confirm(conf); }
  });

  // The offer in the setup: a browser that cannot do passkeys skips it, without a message.
  var offer = document.querySelector("form[data-no-support]");
  if (offer && !supported) window.location.replace(offer.getAttribute("data-no-support"));
  var field = document.querySelector("[data-passkey-signin]");
  if (field) offerInField(field.closest("[data-passkey-scope]") || document);
})();
