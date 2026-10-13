'use strict';
// The operator's funnel dashboard stylesheet (assets/funnel.css) against
// style.css (no browser, no server): it adds no colour of its own and
// redefines no token, every token it reads is style.css's, the acquisition
// setting has one rule per view, and the setting's labels are styled by the
// shared .stats-bucket rules. funnel_dashboard_test.go checks the same
// from the Go side, on the page as drawn.
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {join} = require('node:path');

const asset = (name) => readFileSync(join(__dirname, 'assets', name), 'utf8');
const strip = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '');

const style = asset('style.css');
const own = strip(asset('funnel.css'));
const tokens = style.slice(style.indexOf('/* ---------- tokens'), style.indexOf('/* ---------- base'));

// No literal colour, no property defined: only style.css tokens.
assert.doesNotMatch(own, /#[0-9a-f]{3,8}\b|rgb\(|hsl\(|oklch\(/i, 'funnel.css has a literal colour');
assert.doesNotMatch(own, /(^|[;{])\s*--[a-z0-9-]+\s*:/m, 'funnel.css defines a custom property');
for (const [, name] of own.matchAll(/var\((--[a-z0-9-]+)/g)) {
  if (name === '--heat') continue; // set per square by the page
  assert.ok(tokens.includes(name + ':'), `${name} is not a style.css token`);
}

// Dark mode comes from the same tokens, so the dashboard follows it.
assert.match(tokens, /prefers-color-scheme:dark/);

// One rule per acquisition view, keyed by its radio input.
for (const id of ['entry', 'transport', 'family']) {
  assert.ok(own.includes(`#acq-${id}:checked) .acq-${id}`), `no rule for the ${id} view`);
}

// The setting's labels share the Day / Hour setting's look.
assert.match(style, /\.stats-bucket :is\(a,label\)\{/);
assert.match(style, /\.stats-bucket :is\(a\[aria-current\],input:checked\+label\)\{/);

console.log('funnel_dashboard_test.cjs: ok');
