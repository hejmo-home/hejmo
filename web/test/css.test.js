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
