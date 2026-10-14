'use strict';
// The /stats scripts against a stubbed page (no browser, no server): a
// malformed fragment (#%) leaves the chart readouts working, a resize while a
// readout shows places it again, the day's values are a status line above
// the plot, and the background field follows reduced motion switched on or
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
  const figure = element({insertBefore(c) { this.children.push(c); return c; }});
  const plot = element({
    parentNode: figure,
    closest: () => figure,
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
  return {plot, figure, observers, resize(w) { width = w; observers.forEach((o) => o.f()); }};
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

// The day's values are one line above the plot (never a floating box over
// the chart, its axis or the page): it starts on the latest day, follows the
// keys, and is a role=status line outside the role=img plot.
{
  const {plot, figure} = runStats('');
  const line = figure.children[0];
  assert.equal(line.className, 'chart-readout', 'value line before the plot');
  assert.equal(plot.children.length, 2, 'only the guide and the dot sit on the plot');
  assert.equal(line.children[0].textContent, 'c (so far)', 'latest day by default');
  plot.listeners.focus();
  plot.listeners.keydown({key: 'Home', preventDefault() {}});
  assert.equal(line.children[0].textContent, 'a');
  assert.equal(line.children[1].children[2].textContent, '1', 'first day value');
  plot.listeners.blur();
  assert.equal(line.children[0].textContent, 'c (so far)', 'back to the latest day when the chart is left (report e3540b6c)');
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

// On a phone only the labels the server marks minor are hidden: the axis
// itself and an unmarked label (the dated first tick of each UTC day on the
// hourly axis) stay, so the Hour chart always shows its days (reported by
// survivor).
{
  const css = asset('style.css');
  const hides = [...css.matchAll(/\.chart-axis[^{}]*\{[^}]*display:\s*none/g)].map((m) => m[0].split('{')[0].trim());
  assert.deepEqual(hides, ['.chart-axis .minor'], 'only .chart-axis .minor is hidden');
  const template = readFileSync(join(__dirname, 'templates', 'stats.html'), 'utf8');
  assert.match(template, /\{\{if \.Minor\}\} class="minor"\{\{end\}\}/, 'the minor class follows axisLabel.Minor alone');
}

console.log('stats_assets_test: PASS');
