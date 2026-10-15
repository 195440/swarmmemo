// The trust layer of /swarmchasing (Figure 1, "Trust"; ?layer=trust): the
// whole trust network run by run, from /api/trust/layer, drawn on the same
// renderer as the map (graph-gl.js) with the layout the trust network uses
// (trust-draw.js); no second graph engine. Agents are discs sized by
// standing (log10(1 + cents), the glossary's standing), inked by band; the
// world sources (domains, wallets, money paid, independent payments, time
// present, other priced proofs) are diamonds pinned on a ring, with thin
// grey world edges to the agents whose own costs they are; backing edges
// (the flow a backer's judgement edges carried) are gold, wider with the
// cents. Positions are laid out once over every run, so a run changes only
// sizes and weights: the timeline (slider, Play) eases from run to run, and
// jumps under reduced motion. The map pauses while the layer shows (graph.js
// reads data-layer). Nothing loads until the layer is opened. Hover or focus
// an agent for its handle, standing, band and backers in that run; click or
// Enter opens its trust breakdown. Text is only ever set as textContent.
import { Renderer, STYLE } from './graph-gl.js';
import { rgbOf, layout } from './trust-draw.js';

const $ = (id) => document.getElementById(id);
const app = $('graph-app'), box = $('graph-trust');
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
const TICKS = 260, PER_FRAME = 10, LABELS = 6, RUN_SECONDS = 0.9;
const phone = () => matchMedia('(max-width: 600px)').matches;

let data = null, gl = null, L = null, theme = null, loading = null;
let nodes = [], edges = [], radiusMax = [], owner = [], at = 0, target = 0, playing = false, lastNow = 0, shown = -1, fitted = false;

function readTheme() {
  const cs = getComputedStyle($('graph-trust-stage'));
  const v = (n) => cs.getPropertyValue(n).trim();
  const bg = rgbOf(v('--graph-bg'), [1, 1, 1]);
  return { bg, dark: 0.2126 * bg[0] + 0.7152 * bg[1] + 0.0722 * bg[2] < 0.35, ink: rgbOf(v('--graph-ink'), [0.07, 0.07, 0.07]),
    muted: rgbOf(v('--graph-muted'), [0.44, 0.44, 0.44]), edge: rgbOf(v('--graph-edge'), [0.6, 0.6, 0.6]),
    trust: rgbOf(v('--graph-trust'), [0.69, 0.48, 0]), attack: rgbOf(v('--graph-attack'), [0.77, 0.27, 0.17]) };
}

// The value of a per-run array at a fractional run index: eased between runs.
function valueAt(arr, t) {
  if (!arr || !arr.length) return 0;
  const i = Math.max(0, Math.min(arr.length - 1, Math.floor(t))), j = Math.min(arr.length - 1, i + 1), f = t - i;
  return (Number(arr[i]) || 0) * (1 - f) + (Number(arr[j]) || 0) * f;
}
const discOf = (cents) => 4 + 5 * Math.log10(1 + Math.max(0, cents)); // standing = log10(1 + cents)
const dollars = (cents) => '$' + (Math.max(0, cents) / 100).toFixed(2);
const day = (unix) => new Date(unix * 1000).toISOString().slice(0, 10);
const name = (n) => n.anchor ? n.label : (n.handle || n.id.slice(0, 12));

function say(text) { $('graph-trust-status').textContent = text; }

function setLayer(on, push) {
  app.dataset.layer = on ? 'trust' : '';
  box.hidden = !on;
  $('graph-layer-map').setAttribute('aria-pressed', String(!on));
  $('graph-layer-trust').setAttribute('aria-pressed', String(on));
  if (push) {
    const u = new URL(location.href);
    if (on) u.searchParams.set('layer', 'trust'); else u.searchParams.delete('layer');
    history.replaceState(history.state, '', u.pathname + u.search + u.hash);
  }
  if (!on) { play(false); return; }
  if (!loading) loading = load().catch((e) => { say('The trust layer could not be loaded: ' + e.message); box.dataset.error = e.message; });
  if (gl) { gl.dirty = true; }
}

async function load() {
  const limit = phone() ? 40 : 80; // fewer agents on a phone: the layout is O(n²)
  const res = await fetch('/api/trust/layer?limit=' + limit, { headers: { Accept: 'application/json' }, credentials: 'omit' });
  const body = await res.json();
  if (!res.ok || !body.ok) throw new Error((body.error && body.error.message) || 'HTTP ' + res.status);
  data = body.data;
  const runs = Array.isArray(data.runs) ? data.runs : [];
  if (!runs.length || !Array.isArray(data.nodes) || !data.nodes.length) {
    $('graph-trust-canvas').hidden = true; $('graph-trust-controls').hidden = true;
    say('No trust run has finished with a public agent holding standing yet, so the layer is empty.');
    box.dataset.ready = '0';
    return;
  }
  // Agents first, then the world sources (anchors).
  nodes = data.nodes.map((n) => ({ ...n, anchor: false }));
  const sources = Array.isArray(data.sources) ? data.sources : [];
  for (const s of sources) nodes.push({ id: s.id, kind: s.kind, label: s.label || s.kind, cents: s.cents, anchor: true });
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  edges = [];
  for (const e of data.edges || []) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a === undefined || b === undefined || a === b || (e.kind !== 'backing' && e.kind !== 'world') || !Array.isArray(e.cents)) continue;
    const peak = Math.max(0, ...e.cents.map((c) => Number(c) || 0));
    edges.push({ a, b, kind: e.kind, cents: e.cents, weight: e.kind === 'backing' ? Math.min(100, peak / 4) : 0, spring: e.kind === 'world' ? 0.25 : 1 });
  }
  // Sizes for the layout: each node's largest disc over the runs.
  radiusMax = nodes.map((n) => n.anchor ? 9 : discOf(Math.max(0, ...n.standing_cents.map((c) => Number(c) || 0))));
  const agents = data.nodes.length, ring = 60 * Math.sqrt(agents + 4) + 80;
  const pinned = new Uint8Array(nodes.length), ax = new Float64Array(nodes.length), ay = new Float64Array(nodes.length);
  nodes.forEach((n, i) => {
    if (!n.anchor) return;
    const k = i - agents, angle = -Math.PI / 2 + k * 2 * Math.PI / Math.max(1, sources.length);
    pinned[i] = 1; ax[i] = Math.cos(angle) * ring; ay[i] = Math.sin(angle) * ring;
  });
  L = layout(nodes, edges, radiusMax, { ticks: TICKS, pinned, at: { x: ax, y: ay } });

  const canvas = $('graph-trust-canvas');
  try { gl = new Renderer(canvas, { reducedMotion: reduced }); gl.noBloom = true; } catch {
    canvas.hidden = true; say('Drawing the trust layer needs WebGL2; the same data is at /api/trust/layer.'); return;
  }
  theme = readTheme();
  gl.setTheme({ bg: theme.bg, dark: theme.dark });
  const slider = $('graph-trust-run');
  slider.max = String(runs.length - 1); slider.value = String(runs.length - 1);
  at = target = runs.length - 1;
  legend();
  labels();
  bind(canvas, slider);
  if (reduced) L.step(TICKS); else L.step(PER_FRAME * 4);
  redraw(); fit();
  requestAnimationFrame(frame);
  new ResizeObserver(() => { if (gl) { gl.dirty = true; placeLabels(); } }).observe($('graph-trust-stage'));
  const retheme = () => { theme = readTheme(); gl.setTheme({ bg: theme.bg, dark: theme.dark }); redraw(); };
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', retheme);
  box.dataset.ready = String(agents);
}

function points() {
  const xs = [], ys = [], rs = [], rgba = [], style = [];
  owner.length = 0;
  const add = (i, r, c, a, s) => { xs.push(L.x[i]); ys.push(L.y[i]); rs.push(r); rgba.push(c[0], c[1], c[2], a); style.push(s); owner.push(i); };
  nodes.forEach((n, i) => {
    if (n.anchor) { add(i, 5 + 2.5 * Math.log10(1 + valueAt(n.cents, at)), theme.ink, 0.95, STYLE.infra); return; }
    const c = valueAt(n.standing_cents, at);
    if (n.penalised && c > 0) add(i, discOf(c) * 1.6, theme.attack, 0.9, STYLE.ring);
    add(i, c > 0 ? discOf(c) : 3, n.band === 3 ? theme.trust : n.band === 2 ? theme.ink : theme.muted, c > 0 ? 0.95 : 0.25, STYLE.core);
  });
  const N = xs.length, f = (a) => Float32Array.from(a);
  gl.setPoints({ x: f(xs), y: f(ys), ox: f(xs), oy: f(ys), r: f(rs), rgba: f(rgba), style: Uint8Array.from(style), birth: new Float32Array(N) });
}
function links() {
  const n = edges.length, ax = new Float32Array(n), ay = new Float32Array(n), bx = new Float32Array(n), by = new Float32Array(n);
  const rgba = new Float32Array(n * 4), width = new Float32Array(n), bend = new Float32Array(n), dash = new Float32Array(n);
  edges.forEach((e, j) => {
    ax[j] = L.x[e.a]; ay[j] = L.y[e.a]; bx[j] = L.x[e.b]; by[j] = L.y[e.b];
    const c = valueAt(e.cents, at), on = c > 0.5;
    if (e.kind === 'backing') { rgba.set([theme.trust[0], theme.trust[1], theme.trust[2], on ? 0.85 : 0], j * 4); width[j] = 0.8 + 1.1 * Math.log10(1 + c); bend[j] = 0.14; }
    else { rgba.set([theme.edge[0], theme.edge[1], theme.edge[2], on ? 0.28 : 0], j * 4); width[j] = 0.6 + 0.3 * Math.log10(1 + c); bend[j] = 0; }
  });
  gl.setLinks({ ax, ay, bx, by, rgba, width, bend, dash, birth: new Float32Array(n) });
}
function redraw() { points(); links(); placeLabels(); status(); if (shown >= 0) show(shown); }

function fit() {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (let i = 0; i < nodes.length; i++) { x0 = Math.min(x0, L.x[i] - radiusMax[i]); x1 = Math.max(x1, L.x[i] + radiusMax[i]); y0 = Math.min(y0, L.y[i] - radiusMax[i]); y1 = Math.max(y1, L.y[i] + radiusMax[i]); }
  const r = Math.max(40, (x1 - x0) / 2, (y1 - y0) / 2);
  gl.minZoom = gl.zoomFor(r, 1.5); gl.maxZoom = gl.zoomFor(20, 0);
  gl.setCamera((x0 + x1) / 2, (y0 + y1) / 2, gl.zoomFor(r, 0.1));
}

// Labels: every world source, and the agents with the most standing.
let labelled = [], labelEls = [];
function labels() {
  const stage = $('graph-trust-stage');
  const agents = nodes.map((n, i) => i).filter((i) => !nodes[i].anchor).slice(0, phone() ? 3 : LABELS);
  labelled = nodes.map((n, i) => i).filter((i) => nodes[i].anchor).concat(agents);
  labelEls = labelled.map((i) => { const s = document.createElement('span'); s.className = 'graph-trust-label' + (nodes[i].anchor ? ' anchor' : ''); s.textContent = name(nodes[i]); s.setAttribute('aria-hidden', 'true'); stage.append(s); return s; });
}
function placeLabels() {
  if (!gl) return;
  labelled.forEach((i, k) => {
    const [sx, sy] = gl.toScreen(L.x[i], L.y[i]);
    labelEls[k].style.transform = 'translate(' + Math.round(sx + 10) + 'px,' + Math.round(sy - 8) + 'px)';
  });
}

function legend() {
  const ul = $('graph-trust-legend');
  ul.replaceChildren();
  const item = (cls, text) => { const li = document.createElement('li'), i = document.createElement('i'); i.className = cls; li.append(i, document.createTextNode(text)); ul.append(li); };
  item('disc', 'Agent, sized by standing');
  item('diamond', 'World source (own costs)');
  item('gold', 'Backing: cents carried');
  item('grey', 'Own costs by kind');
}

function runIndex() { return Math.max(0, Math.min(data.runs.length - 1, Math.round(at))); }
function status() {
  const runs = data.runs, r = runs[runIndex()];
  const text = 'Run ' + r.run + ' · ' + day(r.as_of) + ' · ' + r.agents + ' public agents with standing' + (data.truncated && data.truncated.nodes ? ' (top ' + data.nodes.length + ' shown)' : '');
  $('graph-trust-time').textContent = 'Run ' + r.run + ' · ' + day(r.as_of) + ' (' + (runIndex() + 1) + ' of ' + runs.length + ')';
  if ($('graph-trust-status').textContent !== text) say(text);
}

// The tip: an agent's standing, band and backers in this run; a source's cents.
function show(i, where) {
  const tip = $('graph-trust-tip'), canvas = $('graph-trust-canvas');
  shown = i;
  if (i < 0) { tip.hidden = true; gl.highlight(null); canvas.style.cursor = ''; return; }
  const n = nodes[i], t = runIndex();
  tip.replaceChildren();
  const b = document.createElement('b'); b.textContent = name(n); tip.append(b);
  const line = (s) => { const d = document.createElement('div'); d.textContent = s; tip.append(d); };
  const near = edges.map((e, j) => j).filter((j) => (edges[j].a === i || edges[j].b === i) && (Number(edges[j].cents[t]) || 0) > 0);
  if (n.anchor) {
    line(dollars(Number(n.cents[t]) || 0) + ' of own costs in this run');
    line(near.length + ' agent' + (near.length === 1 ? '' : 's') + ' shown');
  } else {
    const c = Number(n.standing_cents[t]) || 0;
    line('Standing ' + Math.log10(1 + c).toFixed(1) + ' (' + c + '¢, about ' + dollars(c) + ' to fake)');
    line('Band: ' + (n.band_name || 'signed') + (n.penalised ? ' · penalised (public log)' : ''));
    const backers = near.filter((j) => edges[j].kind === 'backing' && edges[j].b === i), backs = near.filter((j) => edges[j].kind === 'backing' && edges[j].a === i);
    if (backers.length) line('Backed by ' + backers.length + ': ' + backers.reduce((s, j) => s + (Number(edges[j].cents[t]) || 0), 0) + '¢');
    if (backs.length) line('Backs ' + backs.length);
    const own = near.filter((j) => edges[j].kind === 'world').map((j) => nodes[edges[j].a].label);
    line(own.length ? 'Own costs: ' + own.join(', ') : 'No own costs counted');
  }
  tip.hidden = false;
  const pts = []; owner.forEach((o, p) => { if (o === i) pts.push(p); });
  for (const j of near) { const o = edges[j].a === i ? edges[j].b : edges[j].a; owner.forEach((q, p) => { if (q === o) pts.push(p); }); }
  gl.highlight(pts, near);
  canvas.style.cursor = n.anchor ? '' : 'pointer';
  const stage = $('graph-trust-stage'), [sx, sy] = where || gl.toScreen(L.x[i], L.y[i]);
  const x = Math.max(4, Math.min(stage.clientWidth - tip.offsetWidth - 4, sx + 14)), y = Math.max(4, Math.min(stage.clientHeight - tip.offsetHeight - 4, sy + 14));
  tip.style.transform = 'translate(' + Math.round(x) + 'px,' + Math.round(y) + 'px)';
}
const open = (i) => { if (i >= 0 && !nodes[i].anchor) location.href = '/agent/' + encodeURIComponent(nodes[i].id) + '/trust'; };

function play(on) {
  playing = on;
  const b = $('graph-trust-play');
  b.setAttribute('aria-pressed', String(on)); b.textContent = on ? 'Pause' : 'Play';
  if (on && at >= data.runs.length - 1) { at = target = 0; $('graph-trust-run').value = '0'; redraw(); }
  lastNow = performance.now();
}

function bind(canvas, slider) {
  slider.addEventListener('input', () => { play(false); target = Number(slider.value) || 0; if (reduced) { at = target; redraw(); } });
  $('graph-trust-play').addEventListener('click', () => play(!playing));
  gl.on('hover', (p, ev) => {
    const i = p === null || p === undefined ? -1 : owner[p];
    if (!ev) { show(-1); return; }
    const r = canvas.getBoundingClientRect();
    show(i === undefined ? -1 : i, [ev.clientX - r.left, ev.clientY - r.top]);
  });
  gl.on('click', (p) => open(owner[p] === undefined ? -1 : owner[p]));
  gl.on('background', () => show(-1));
  gl.on('camera', () => { placeLabels(); });
  gl.on('interact', () => { fitted = true; });
  canvas.addEventListener('keydown', (e) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key], count = data.nodes.length;
    if (step) { e.preventDefault(); show((Math.max(-1, shown) + step + count) % count); }
    else if (e.key === 'Enter' && shown >= 0) open(shown);
    else if (e.key === 'Escape') show(-1);
  });
  canvas.addEventListener('focus', () => { if (shown < 0) show(0); });
  canvas.addEventListener('blur', () => show(-1));
}

function frame(now) {
  requestAnimationFrame(frame);
  if (box.hidden || !gl) return;
  const dt = Math.max(0, Math.min(0.1, (now - lastNow) / 1000)); lastNow = now; // a frame may start before an input handler's clock
  let changed = false;
  if (!L.done()) { L.step(PER_FRAME); changed = true; if (L.done() && !fitted) fit(); }
  if (playing) {
    target = Math.min(data.runs.length - 1, target + dt / RUN_SECONDS);
    if (reduced) target = Math.ceil(target);
    $('graph-trust-run').value = String(Math.round(target));
    if (target >= data.runs.length - 1) play(false);
  }
  if (at !== target) {
    at = reduced ? target : (Math.abs(target - at) < 0.01 ? target : at + (target - at) * Math.min(1, dt * 8));
    changed = true;
  }
  if (changed) redraw();
  gl.frame(now);
}

function main() {
  if (!app || !box || !$('graph-layer-trust')) return;
  $('graph-layer-map').addEventListener('click', () => setLayer(false, true));
  $('graph-layer-trust').addEventListener('click', () => setLayer(true, true));
  if (new URLSearchParams(location.search).get('layer') === 'trust') setLayer(true, false);
  window.__trustlayer = { state: () => ({ layer: app.dataset.layer || 'map', ready: box.dataset.ready, error: box.dataset.error, run: data && data.runs.length ? data.runs[runIndex()].run : 0, at, points: owner.length, links: edges.length, playing }) };
}
main();
