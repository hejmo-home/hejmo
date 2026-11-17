// Measures one rendered page (ticket 161). Called as probe(width, names) after
// the fonts are loaded; returns a list of problems, each {kind, el, detail}.
// An empty list means the page fits.
async function probe(width, names) {
  await document.fonts.ready;
  const problems = [];
  const de = document.documentElement;

  const describe = (el) => {
    let s = el.tagName.toLowerCase();
    if (el.id) s += "#" + el.id;
    if (typeof el.className === "string" && el.className.trim()) s += "." + el.className.trim().split(/\s+/).join(".");
    const text = (el.textContent || "").replace(/\s+/g, " ").trim();
    return s + (text ? ' "' + text.slice(0, 40) + '"' : "");
  };
  const scrolls = (cs) => /auto|scroll/.test(cs.overflowX);
  // An element inside a box that scrolls or clips sideways cannot widen the page.
  const clippedByAncestor = (el) => {
    for (let a = el.parentElement; a && a !== de; a = a.parentElement) {
      if (/auto|scroll|hidden|clip/.test(getComputedStyle(a).overflowX)) return true;
    }
    return false;
  };
  const hidden = (el, cs, r) =>
    cs.display === "none" || cs.visibility === "hidden" || r.width <= 2 || r.height <= 2 || cs.clip !== "auto" && cs.position === "absolute";

  // 1. The page is no wider than the viewport.
  if (de.scrollWidth > width) {
    const wide = [];
    for (const el of document.body.querySelectorAll("*")) {
      const cs = getComputedStyle(el), r = el.getBoundingClientRect();
      if (hidden(el, cs, r) || clippedByAncestor(el)) continue;
      if (r.right > width + 0.5) wide.push({ el, right: r.right });
    }
    // The innermost widest elements are the cause; their ancestors are widened by them.
    const causes = wide.filter((w) => !wide.some((o) => o.el !== w.el && w.el.contains(o.el)));
    problems.push({
      kind: "page-wider-than-viewport",
      el: causes.length ? describe(causes[0].el) : describe(document.body),
      detail: "scrollWidth " + de.scrollWidth + " > " + width + (causes.length > 1 ? " (" + causes.length + " elements stick out)" : ""),
    });
  }

  // 1b. At desktop widths the frame's navigation stays a column beside the
  // content (ticket 181): it does not turn into rows above it.
  if (width >= 1024) {
    const nav = document.querySelector(".frame > .frame-nav"), main = document.querySelector(".frame > main");
    if (nav && main) {
      const n = nav.getBoundingClientRect(), m = main.getBoundingClientRect();
      if (n.right > m.left + 0.5) {
        problems.push({
          kind: "navigation-not-beside-content",
          el: describe(nav),
          detail: "navigation spans " + Math.round(n.left) + "-" + Math.round(n.right) + " px, content starts at " + Math.round(m.left) + " px",
        });
      }
    }
  }

  // 2. No text container overflows its box. A box whose content is wider than
  // the box is reported when no element inside it is wider than its own box
  // (the ancestors are widened by it), and when it holds text: its own, from
  // inline children (a link, a name in bold) or from grid and flex items (an
  // event card). The text is clipped or sticks out; either way it does not fit.
  const overflowing = [];
  for (const el of document.body.querySelectorAll("*")) {
    if (/^(INPUT|TEXTAREA|SELECT|SVG|SCRIPT|STYLE|OPTION)$/i.test(el.tagName) || el.closest("svg")) continue;
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (hidden(el, cs, r) || cs.display === "inline" || cs.display === "contents" || scrolls(cs) || cs.textOverflow === "ellipsis") continue;
    if (el.scrollWidth > el.clientWidth + 1) overflowing.push(el);
  }
  for (const el of overflowing) {
    if (overflowing.some((o) => o !== el && el.contains(o))) continue;
    if (!(el.innerText || "").trim()) continue;
    problems.push({ kind: "text-overflows-its-box", el: describe(el), detail: "scrollWidth " + el.scrollWidth + " > clientWidth " + el.clientWidth });
  }

  // 3. A person's name stays whole: it may wrap at a space or after a hyphen, never inside a word.
  // The names are looked up in one text node at a time, so a name split across
  // elements (<b>Max</b>imilian) is not checked.
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node; (node = walker.nextNode()); ) {
    const parent = node.parentElement;
    if (!parent || /^(SCRIPT|STYLE|TITLE|OPTION|TEXTAREA)$/i.test(parent.tagName)) continue;
    const pcs = getComputedStyle(parent);
    if (pcs.display === "none" || pcs.visibility === "hidden") continue;
    const text = node.textContent;
    for (const name of names) {
      for (let at = text.indexOf(name); at >= 0; at = text.indexOf(name, at + 1)) {
        let prev = null;
        for (let i = at; i < at + name.length; i++) {
          if (text[i] === " ") { prev = null; continue; }
          const range = document.createRange();
          range.setStart(node, i);
          range.setEnd(node, i + 1);
          const rects = range.getClientRects();
          if (!rects.length) { prev = null; continue; }
          const rect = rects[0];
          if (prev && rect.top > prev.top + prev.height / 2 && text[i - 1] !== "-") {
            problems.push({ kind: "name-broken-inside-the-word", el: describe(parent), detail: name + " breaks before \"" + text[i] + "\"" });
            break;
          }
          prev = rect;
        }
      }
    }
  }
  return problems;
}
