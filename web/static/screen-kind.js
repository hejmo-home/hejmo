// The pairing page tells the box about a touch screen (#203, Q148). Since iPadOS 13 Safari and
// Chrome on an iPad send the user agent of a Mac, so the box cannot tell an iPad from a Mac by
// it. A Mac user agent with a touch screen is an iPad: this sets a cookie the box reads for the
// pairing label and loads the page again once, so the label is right before a parent confirms.
(function () {
  if (!/Macintosh/.test(navigator.userAgent) || !(navigator.maxTouchPoints > 1)) return;
  if (/(^|; )hejmo_touch=1(;|$)/.test(document.cookie)) return;
  document.cookie = "hejmo_touch=1; path=/personal-devices/join/; max-age=900; samesite=lax" + (location.protocol === "https:" ? "; secure" : "");
  location.reload();
})();
