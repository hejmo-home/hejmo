// A stray piece of markup in hejmo.css once left a brace open and switched off
// every rule after it (ticket 180). Cheap guard: no '<' outside comments and
// strings, and the braces balance.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const css = fs.readFileSync(path.join(__dirname, '../static/hejmo.css'), 'utf8');
const bare = css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'/g, '""');

test('hejmo.css has no stray markup outside comments and strings', () => {
  const at = bare.indexOf('<');
  assert.equal(at, -1, 'stray "<" near: ' + (at < 0 ? '' : bare.slice(Math.max(0, at - 40), at + 40)));
});

test('hejmo.css has balanced braces', () => {
  let depth = 0;
  for (const c of bare) {
    if (c === '{') depth++;
    if (c === '}') depth--;
    assert.ok(depth >= 0, 'a closing brace without an opening one');
  }
  assert.equal(depth, 0, 'unclosed braces: ' + depth);
});

// Ticket 208: on a phone the family view scrolls as one page. Every rule is
// scoped to .fv / :has(.fv), so the lists view and the personal view on the
// same .family-page wrapper keep their fixed layout.
test('the phone block of the family view is scoped to .fv', () => {
  const m = bare.match(/@media \(max-width: 599px\), \(max-height: 500px\) \{([\s\S]*?)\n\}/);
  assert.ok(m, 'no @media (max-width: 599px), (max-height: 500px) block');
  const rules = m[1].match(/[^{}]+(?=\{)/g).map((s) => s.trim());
  assert.ok(rules.length >= 10, 'rules missing: ' + rules.length);
  for (const sel of rules) {
    assert.match(sel, /\.fv(?![-\w])|:has\(\.fv\)/, 'unscoped phone rule: ' + sel);
  }
  assert.match(m[1], /body:has\(\.fv\) \{ overflow: auto;/);
  assert.match(m[1], /\.fv \.column \{ overflow: visible;/);
  assert.match(m[1], /\.fv \.fv-footer \.tile \{ flex-basis: 100%;/);
});

test('the below line is hidden unless the phone block shows it', () => {
  assert.match(bare, /^\.fv-below \{ display: none; \}/m);
  assert.match(bare, /@media \(max-width: 599px\) \{\s*\.fv \.chips \[data-clock\] \{ display: none; \}\s*\}/);
});

// Ticket 208 (product owner 23/11/2026): one person's column in full width. The
// hints, the bar and "nothing today" exist on wall displays too but stay hidden
// there; on a phone the state set by display.js shows one column alone.
test('the full-width column is hidden outside the phone breakpoint', () => {
  const nocomment = css.replace(/\/\*[\s\S]*?\*\//g, '');
  assert.match(nocomment, /^\.fv \.col-hint, \.fv \.fv-fullbar, \.fv \.col-none \{ display: none; \}/m);
  const i = nocomment.indexOf('.fv .col-hint, .fv .fv-fullbar');
  const m = nocomment.slice(i).match(/@media \(max-width: 599px\), \(max-height: 500px\) \{([\s\S]*?)\n\}/);
  assert.ok(m, 'no phone block after the hidden rule');
  const b = m[1];
  assert.match(b, /\.fv:not\(\[data-full-person\]\) \.col-hint\.grow \{ display: block; \}/);
  assert.match(b, /\.fv\[data-full-person\] \.fv-grid \{ grid-auto-columns: 100%; overflow-x: visible; \}/);
  assert.match(b, /\.column:not\(\[data-full\]\) \{ display: none; \}/);
  assert.match(b, /\.column\[data-full\] \.col-hint\.to-pv \{ display: block; \}/);
  assert.match(b, /\.column\[data-full\] \.col-none \{ display: block;/);
  assert.match(b, /repeat\(auto-fill, minmax\(min\(100%, 230px\), 1fr\)\)/);
  assert.match(b, /\.tomorrow \{ grid-column: 1 \/ -1;/);
  assert.match(b, /\[data-theme="bw"\] \.fv \.fv-back \{ border: 2px solid var\(--text\);/);
  assert.match(b, /\[data-theme="bw"\] \.fv \.fv-back \.icon \{ stroke: var\(--text\); \}/);
});
