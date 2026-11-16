// Tests of the offline line (ticket 149) with node's test runner:
//   cd web && node --test
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/offline.js'), 'utf8');

const TEXT = { en: 'As of {time}, not connected', de: 'Stand {time}, nicht verbunden' };
const nbsp = ' ';

// Runs offline.js on a page loaded at `loaded` and returns the banner text.
// `attrs` are the data attributes on the script tag; lang is the page language.
function bannerFor(loaded, attrs) {
  const lang = attrs.lang || 'en';
  const scriptAttrs = Object.assign({ 'data-offline-text': TEXT[lang] }, attrs.script);
  const script = { getAttribute: (n) => (n in scriptAttrs ? scriptAttrs[n] : null) };
  const rootAttrs = { 'data-offline': String(loaded) };
  const root = { getAttribute: (n) => (n in rootAttrs ? rootAttrs[n] : null), lang };
  let banner = null;
  const main = { insertBefore(el) { banner = el; }, firstChild: null };
  const document = {
    currentScript: script,
    documentElement: root,
    createElement() { return { setAttribute() {} }; },
    getElementById() { return main; },
    querySelectorAll() { return []; },
    addEventListener() {},
    body: main,
  };
  vm.runInNewContext(source, { document, window: { addEventListener() {} }, navigator: {}, Date });
  return banner.textContent;
}

// 24/12/2026 18:30 local time: not today, so the line carries the date.
const christmas = new Date(2026, 11, 24, 18, 30).getTime();
// 24/12/2026 06:05, the morning.
const morning = new Date(2026, 11, 24, 6, 5).getTime();

for (const lang of ['en', 'de']) {
  test(`${lang}: defaults show 24-hour time and a day-first date`, () => {
    assert.equal(bannerFor(christmas, { lang }), TEXT[lang].replace('{time}', `24.12.2026, 18:30`));
  });

  test(`${lang}: no attributes fall back to the defaults`, () => {
    assert.equal(bannerFor(christmas, { lang, script: {} }), TEXT[lang].replace('{time}', `24.12.2026, 18:30`));
  });

  test(`${lang}: 12-hour time and month-first dates`, () => {
    const attrs = { lang, script: { 'data-time-format': '12h', 'data-date-format': 'mdy' } };
    assert.equal(bannerFor(christmas, attrs), TEXT[lang].replace('{time}', `12/24/2026, 6:30${nbsp}PM`));
    assert.equal(bannerFor(morning, attrs), TEXT[lang].replace('{time}', `12/24/2026, 6:05${nbsp}AM`));
  });

  test(`${lang}: 12-hour time with day-first dates, and noon and midnight`, () => {
    const attrs = { lang, script: { 'data-time-format': '12h', 'data-date-format': 'dmy' } };
    const noon = new Date(2026, 11, 24, 12, 0).getTime();
    const midnight = new Date(2026, 11, 24, 0, 7).getTime();
    assert.equal(bannerFor(noon, attrs), TEXT[lang].replace('{time}', `24.12.2026, 12:00${nbsp}PM`));
    assert.equal(bannerFor(midnight, attrs), TEXT[lang].replace('{time}', `24.12.2026, 12:07${nbsp}AM`));
  });
}

test('a copy from today shows the time only, in the household clock', () => {
  const now = new Date();
  const t = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 15, 9).getTime();
  assert.equal(bannerFor(t, { lang: 'en' }), 'As of 15:09, not connected');
  assert.equal(bannerFor(t, { lang: 'de', script: { 'data-time-format': '12h' } }), `Stand 3:09${nbsp}PM, nicht verbunden`);
});
