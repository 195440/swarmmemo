'use strict';
// The /stats scripts against a stubbed page (no browser, no server): a
// malformed fragment (#%) leaves the chart readouts working, a resize while a
// readout shows places it again, a readout with no room above the plot goes
// below it, and the background field follows reduced motion switched on or
// off without a reload. These were reported in the /stats QA bounty
// (38516717).
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const vm = require('node:vm');

const asset = (name) => readFileSync(join(__dirname, 'assets', name), 'utf8');

function element(extra = {}) {
  const el = {
    style: {}, dataset: {}, children: [], listeners: {}, className: '', tabIndex: -1,
    classList: {
      set: new Set(),
      add(c) { this.set.add(c); }, remove(c) { this.set.delete(c); },
      contains(c) { return this.set.has(c); },
      toggle(c, on) { if (on) this.set.add(c); else this.set.delete(c); },
    },
    appendChild(c) { this.children.push(c); return c; },
    append(c) { this.children.push(c); },
    replaceChildren() { this.children = []; },
    setAttribute() {}, getAttribute() { return ''; },
    addEventListener(n, f) { this.listeners[n] = f; },
    get offsetWidth() { return 40; },
    get offsetHeight() { return 120; },
    ...extra,
  };
  return el;
}

function runStats(hash, plotTop = 400) {
  let width = 1200;
  const observers = [];
  const svg = element({
    getBoundingClientRect: () => ({left: 0, top: 0, width, height: 100}),
    querySelectorAll: () => [element({dataset: {label: 'posts', values: '1|2|3', ys: '10|20|30'}, getAttribute: () => 's-posts'})],
  });
  const plot = element({
    dataset: {x: 'a|b|c'},
    querySelector: () => svg,
    getBoundingClientRect: () => ({left: 0, top: plotTop, width, height: 100}),
  });
  const document = {
    activeElement: null,
    querySelector: () => ({getBoundingClientRect: () => ({bottom: 40})}),
    getElementById: () => null,
    querySelectorAll: () => [plot],
    createElement: () => element(),
  };
  class ResizeObserver {
    constructor(f) { this.f = f; observers.push(this); }
    observe() {}
  }
  vm.runInNewContext(asset('stats.js'), {
    document, location: {hash}, ResizeObserver, Math,
    window: {addEventListener() {}},
  });
  return {plot, observers, resize(w) { width = w; observers.forEach((o) => o.f()); }};
}

// A malformed fragment no longer aborts the readouts.
for (const hash of ['#%', '#%E0%A4%A']) {
  const {plot} = runStats(hash);
  assert.equal(plot.tabIndex, 0, `${hash}: plot focusable`);
  assert.ok(plot.classList.contains('has-readout'), `${hash}: readout installed`);
}

// A resize while the readout shows places it inside the new width.
{
  const page = runStats('');
  const {plot} = page;
  plot.listeners.focus();
  plot.listeners.keydown({key: 'End', preventDefault() {}});
  const guide = plot.children[0];
  assert.equal(guide.style.left, '1200px');
  page.resize(320);
  assert.equal(guide.style.left, '320px', 'guide follows the resize');
  plot.listeners.blur();
  page.resize(1200);
  assert.equal(guide.style.left, '320px', 'a hidden readout is left alone');
}

// A readout with no room above the plot (under the sticky contents bar)
// goes below it; with room it stays above.
{
  const high = runStats('', 60).plot;
  high.listeners.focus();
  assert.ok(high.children[2].classList.contains('below'), 'flips below under the bar');
  const low = runStats('', 400).plot;
  low.listeners.focus();
  assert.ok(!low.children[2].classList.contains('below'), 'stays above with room');
}

// The field follows prefers-reduced-motion live.
{
  const frames = [];
  const media = {matches: false, listeners: [], addEventListener(n, f) { this.listeners.push(f); }};
  const canvas = element({clientWidth: 10, clientHeight: 10, getContext: () => ({clearRect() {}, beginPath() {}, arc() {}, fill() {}})});
  const box = element();
  let made = 0;
  const document = {
    documentElement: {classList: {contains: () => false}},
    body: {prepend() {}},
    hidden: false,
    createElement: () => (made++ === 0 ? box : canvas),
    addEventListener() {},
  };
  let now = 0;
  vm.runInNewContext(asset('field.js'), {
    document, navigator: {}, Math, Date,
    matchMedia: () => media,
    getComputedStyle: () => ({color: '#000', getPropertyValue: () => ''}),
    addEventListener() {},
    performance: {now: () => now},
    requestAnimationFrame: (f) => { frames.push(f); return frames.length; },
  });
  const pump = (n) => { for (let i = 0; i < n && frames.length; i++) { now += 200; frames.shift()(now); } };
  assert.ok(frames.length > 0, 'animates by default');
  pump(3);
  assert.ok(frames.length > 0, 'keeps animating');
  media.matches = true;
  media.listeners.forEach((f) => f());
  pump(5);
  assert.equal(frames.length, 0, 'reduced motion switched on stops the loop without a reload');
  media.matches = false;
  media.listeners.forEach((f) => f());
  assert.ok(frames.length > 0, 'switched off, it animates again');
}

console.log('stats_assets_test: PASS');
