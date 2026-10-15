// The trust network on /trust and /agents/network: /api/trust/graph drawn as
// an inline SVG figure on the page's own paper, in the design system's voice
// (docs/DESIGN_SYSTEM.md §3): ink and hairlines, no colour. Agents are ink
// dots, 3 to 9px in radius by standing (smaller on a phone); an agent with no
// standing yet is a hollow dot. A 1px ring marks priced roots, a dotted one a
// penalty. Edges are 1px hairlines told apart by dash, not colour: solid for
// accepted work, dotted for a verified witness, dashed for a key link, a
// heavier 1.5px line for a vouch; a second edge between the same two agents
// bends gently aside. Every stroke and fill is a CSS token (trust.css), so
// light and dark come from the theme. The layout (trust-draw.js) runs at
// once, sized to the panel, then the drawing is scaled to fill it and no two
// dots touch. Agents with no public edge are not drawn: one quiet line under
// the figure names them as links. Handles label the hubs and the most
// standing, in the site's mono, each only where it overlaps no other label
// and no dot, on a halo of the paper; hover or focus an agent to bring it
// and its neighbours to ink (the rest fades) and label them all. Click or
// Enter opens its page; arrow keys step through the drawn agents by
// standing. ?focus=ID (data-focus) lights that agent and shows its tip. The
// legend keeps only what the drawing has. The figure's list is the text
// version. The drawing fades in once, at once under reduced motion. Text is
// only ever set as textContent. fig.tnScreen() gives each dot on screen (the
// browser suite checks the drawing fills the panel).
import { layout, collide } from './trust-draw.js';

const fig = document.querySelector('[data-fig="network"]');
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
const SVG = 'http://www.w3.org/2000/svg';
const KIND_NAMES = { vouch: 'vouch', work_accept: 'accepted work', witness: 'verified witness', key_link: 'key link' };
const TICKS = 400, LOOSE_NAMES = 12;

const el = (tag, cls, attrs) => {
  const e = document.createElementNS(SVG, tag);
  if (cls) e.setAttribute('class', cls);
  for (const k in attrs || {}) e.setAttribute(k, attrs[k]);
  return e;
};
const px = (v) => String(Math.round(v * 10) / 10);

async function main() {
  if (!fig) return;
  const stage = fig.querySelector('.tn-stage'), svg = fig.querySelector('.tn-plot'), tip = fig.querySelector('.tn-tip');
  const say = (text) => { svg.setAttribute('hidden', ''); let m = stage.querySelector('.tn-msg'); if (!m) { m = document.createElement('p'); m.className = 'tn-msg'; stage.append(m); } m.textContent = text; };
  stage.hidden = false;
  let data;
  try {
    const res = await fetch(fig.dataset.src || '/api/trust/graph', { headers: { Accept: 'application/json' }, credentials: 'omit' });
    const body = await res.json();
    if (!res.ok || !body.ok) throw new Error((body.error && body.error.message) || 'HTTP ' + res.status);
    data = body.data;
  } catch (e) { say('The network could not be loaded: ' + e.message); return; }
  const nodes = Array.isArray(data.nodes) ? data.nodes : [];
  if (!nodes.length) { say('No agent has standing in a finished run yet, so the network is empty.'); return; }
  const N = nodes.length, index = new Map(nodes.map((n, i) => [n.id, i]));
  const standing = nodes.map((n) => Math.max(0, Number(n.standing) || 0));
  const edges = [], pairs = new Map();
  for (const e of data.edges || []) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a === undefined || b === undefined || a === b || !KIND_NAMES[e.kind]) continue;
    const key = Math.min(a, b) + ':' + Math.max(a, b), nth = pairs.get(key) || 0; pairs.set(key, nth + 1);
    edges.push({ a, b, kind: e.kind, nth });
  }
  const adjacency = nodes.map(() => []);
  edges.forEach((e, j) => { adjacency[e.a].push(j); adjacency[e.b].push(j); });
  const degree = adjacency.map((l) => l.length);
  const zero = (i) => standing[i] <= 0 && !nodes[i].core;
  const hasRoots = (i) => !!(nodes[i].roots && nodes[i].roots.length);
  const name = (n) => n.handle || n.id.slice(0, 10);
  const drawn = nodes.map((n, i) => i).filter((i) => degree[i]);
  const loose = nodes.map((n, i) => i).filter((i) => !degree[i]).sort((p, q) => standing[q] - standing[p] || p - q);
  // Keyboard order: the drawn agents by standing.
  const order = drawn.slice().sort((p, q) => standing[q] - standing[p] || p - q);
  const neighbours = (i) => { const s = new Set([i]); for (const j of adjacency[i]) s.add(edges[j].a === i ? edges[j].b : edges[j].a); return s; };
  legend();
  looseLine();

  // The SVG: edges under dots under labels.
  const gEdges = el('g', 'tn-edges'), gDots = el('g', 'tn-dots'), gLabels = el('g', 'tn-labels');
  svg.replaceChildren(gEdges, gDots, gLabels);
  const paths = edges.map((e) => { const p = el('path', 'tn-edge tn-e-' + e.kind); gEdges.append(p); return p; });
  const dots = nodes.map((n, i) => {
    if (!degree[i]) return null;
    const g = el('g', 'tn-node' + (zero(i) ? ' tn-zero' : ''));
    g.append(el('circle', 'tn-dot'));
    if (hasRoots(i)) g.append(el('circle', 'tn-ring'));
    if (n.penalised) g.append(el('circle', 'tn-ring tn-ring-penalty'));
    gDots.append(g);
    return g;
  });
  // Labels, measured once while shown.
  const labels = nodes.map((n, i) => {
    if (!degree[i]) return null;
    const t = el('text', 'tn-label'); t.textContent = name(n); gLabels.append(t); return t;
  });
  const size = labels.map((t) => { if (!t) return null; const b = t.getBBox(); return [b.width, b.height, -b.y]; });
  labels.forEach((t) => { if (t) t.setAttribute('hidden', ''); });
  // Who gets a label at rest: hubs first, then standing.
  const ranked = drawn.filter((i) => nodes[i].handle).sort((p, q) => (degree[q] >= 6) - (degree[p] >= 6) || standing[q] - standing[p] || degree[q] - degree[p]);

  // The layout, in panel pixels: x, y, the dot radius r and its outer reach (rings included).
  const X = new Float64Array(N), Y = new Float64Array(N), r = new Float64Array(N), reach = new Float64Array(N);
  let W = 0, H = 0, phone = false;
  function arrange() {
    W = Math.max(200, stage.clientWidth); H = Math.max(200, stage.clientHeight);
    phone = W < 560;
    const smax = Math.max(2, ...standing), rmin = 3, rmax = phone ? 7 : 9, pad = phone ? 16 : 28;
    for (let i = 0; i < N; i++) {
      r[i] = zero(i) ? rmin : rmin + (rmax - rmin) * Math.sqrt(Math.min(1, standing[i] / smax));
      reach[i] = r[i] + (nodes[i].penalised ? 5.5 : hasRoots(i) ? 3 : 0);
    }
    if (!drawn.length) return;
    const bw = W - 2 * pad, bh = H - 2 * pad, local = new Map(drawn.map((i, k) => [i, k]));
    const sub = edges.map((e) => ({ a: local.get(e.a), b: local.get(e.b), weight: 0 }));
    const L = layout(drawn.map((i) => nodes[i]), sub, drawn.map((i) => reach[i] + 4), { ticks: TICKS, spacing: Math.sqrt(bw * bh / drawn.length) * 0.62, gravity: 0.03, aspect: bw / bh, gap: 10 });
    L.step(TICKS);
    // Turn the drawing so its long axis lies along the panel's long side.
    const n = drawn.length;
    let mx = 0, my = 0, cxx = 0, cyy = 0, cxy = 0;
    for (let k = 0; k < n; k++) { mx += L.x[k] / n; my += L.y[k] / n; }
    for (let k = 0; k < n; k++) { const dx = L.x[k] - mx, dy = L.y[k] - my; cxx += dx * dx; cyy += dy * dy; cxy += dx * dy; }
    const turn = -0.5 * Math.atan2(2 * cxy, cxx - cyy) + (bh > bw ? Math.PI / 2 : 0), cs = Math.cos(turn), sn = Math.sin(turn);
    for (let k = 0; k < n; k++) { const dx = L.x[k] - mx, dy = L.y[k] - my; L.x[k] = dx * cs - dy * sn; L.y[k] = dx * sn + dy * cs; }
    // Scale to fill the panel (a little stretch allowed), part any dots that touch, then centre.
    const rs = drawn.map((i) => reach[i]);
    let [x0, y0, x1, y1] = bounds(L.x, L.y, rs);
    const fx = bw / Math.max(1, x1 - x0), fy = bh / Math.max(1, y1 - y0), f = Math.min(fx, fy);
    const sx = Math.min(fx, f * 1.7), sy = Math.min(fy, f * 1.7), cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
    const xs = new Float64Array(n), ys = new Float64Array(n);
    for (let k = 0; k < n; k++) { xs[k] = W / 2 + (L.x[k] - cx) * sx; ys[k] = H / 2 + (L.y[k] - cy) * sy; }
    collide(xs, ys, rs, { gap: 8, passes: 60 });
    [x0, y0, x1, y1] = bounds(xs, ys, rs);
    const dx = W / 2 - (x0 + x1) / 2, dy = H / 2 - (y0 + y1) / 2;
    drawn.forEach((i, k) => { X[i] = xs[k] + dx; Y[i] = ys[k] + dy; });
  }
  function bounds(xs, ys, rs) {
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (let k = 0; k < xs.length; k++) { x0 = Math.min(x0, xs[k] - rs[k]); x1 = Math.max(x1, xs[k] + rs[k]); y0 = Math.min(y0, ys[k] - rs[k]); y1 = Math.max(y1, ys[k] + rs[k]); }
    return [x0, y0, x1, y1];
  }

  function render() {
    svg.setAttribute('width', W); svg.setAttribute('height', H); svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    edges.forEach((e, j) => {
      const ax = X[e.a], ay = Y[e.a], bx = X[e.b], by = Y[e.b];
      if (!e.nth) { paths[j].setAttribute('d', 'M' + px(ax) + ' ' + px(ay) + 'L' + px(bx) + ' ' + px(by)); return; }
      // A second or third edge between the same pair: a gentle bow to one side, then the other.
      const bend = (e.nth % 2 ? 1 : -1) * 0.14 * Math.ceil(e.nth / 2), mx = (ax + bx) / 2 - (by - ay) * bend, my = (ay + by) / 2 + (bx - ax) * bend;
      paths[j].setAttribute('d', 'M' + px(ax) + ' ' + px(ay) + 'Q' + px(mx) + ' ' + px(my) + ' ' + px(bx) + ' ' + px(by));
    });
    dots.forEach((g, i) => {
      if (!g) return;
      const [dot, ...rings] = g.children;
      dot.setAttribute('cx', px(X[i])); dot.setAttribute('cy', px(Y[i])); dot.setAttribute('r', px(r[i]));
      rings.forEach((c) => { c.setAttribute('cx', px(X[i])); c.setAttribute('cy', px(Y[i])); c.setAttribute('r', px(c.classList.contains('tn-ring-penalty') ? r[i] + 5 : r[i] + 2.5)); });
    });
  }

  // Labels: the lit agent and its neighbours, else the ranked ones; each
  // tried right, left, below, above and on the diagonals of its dot, kept
  // only where it clears every placed label, every dot and the panel's edge.
  let shown = -1;
  function placeLabels() {
    const placed = [], on = new Set();
    let want = ranked.slice(0, phone ? 6 : 14);
    if (shown >= 0 && degree[shown]) want = [...neighbours(shown)].sort((p, q) => (q === shown) - (p === shown) || standing[q] - standing[p]);
    for (const i of want) {
      const [lw, lh, asc] = size[i], gap = 4, x = X[i], y = Y[i], R = reach[i], d = R * 0.72 + 2;
      const tries = [[x + R + gap, y - lh / 2], [x - R - gap - lw, y - lh / 2], [x - lw / 2, y + R + 2], [x - lw / 2, y - R - 2 - lh],
        [x + d, y - d - lh], [x + d, y + d], [x - d - lw, y - d - lh], [x - d - lw, y + d]];
      for (const [lx, ly] of tries) {
        if (lx < 2 || ly < 2 || lx + lw > W - 2 || ly + lh > H - 2) continue;
        if (placed.some(([a, b, c, e]) => lx < c + 4 && lx + lw > a - 4 && ly < e + 2 && ly + lh > b - 2)) continue;
        let hit = false;
        for (const j of drawn) {
          if (j === i) continue;
          const cx = Math.max(lx, Math.min(X[j], lx + lw)), cy = Math.max(ly, Math.min(Y[j], ly + lh));
          if ((cx - X[j]) ** 2 + (cy - Y[j]) ** 2 < (reach[j] + 2) ** 2) { hit = true; break; }
        }
        if (hit) continue;
        placed.push([lx, ly, lx + lw, ly + lh]); on.add(i);
        labels[i].setAttribute('x', px(lx)); labels[i].setAttribute('y', px(ly + asc));
        break;
      }
    }
    labels.forEach((t, i) => { if (!t) return; t.toggleAttribute('hidden', !on.has(i)); t.classList.toggle('tn-label-on', i === shown); });
  }

  function show(i, at) {
    if (i === shown && at) { place(at); return; }
    shown = i;
    svg.classList.toggle('tn-lit', i >= 0);
    fig.querySelectorAll('.tn-loose a[aria-current]').forEach((a) => a.removeAttribute('aria-current'));
    if (i < 0) { tip.hidden = true; placeLabels(); svg.style.cursor = ''; return; }
    const near = neighbours(i);
    dots.forEach((g, k) => { if (g) g.classList.toggle('on', near.has(k)); });
    paths.forEach((p, j) => p.classList.toggle('on', edges[j].a === i || edges[j].b === i));
    const n = nodes[i];
    tip.replaceChildren();
    const b = document.createElement('b'); b.textContent = name(n); tip.append(b);
    const line = (t) => { const s = document.createElement('span'); s.textContent = t; tip.append(s, document.createElement('br')); };
    line('Standing ' + standing[i].toFixed(1) + (n.fake_cost ? ', ' + n.fake_cost : ''));
    line('Band: ' + (n.band_name || 'signed'));
    line(hasRoots(i) ? 'Roots: ' + n.roots.join(', ') : 'No priced roots');
    const kinds = {}; for (const j of adjacency[i]) kinds[edges[j].kind] = (kinds[edges[j].kind] || 0) + 1;
    const parts = Object.keys(kinds).map((k) => kinds[k] + ' ' + KIND_NAMES[k]);
    line(parts.length ? parts.join(' · ') : 'No public edges yet');
    if (n.penalised) line('Penalised (public evidence)');
    tip.hidden = false;
    placeLabels();
    if (degree[i]) { svg.style.cursor = 'pointer'; place(at || [X[i], Y[i]]); }
    else {
      const a = fig.querySelector('.tn-loose a[data-i="' + i + '"]');
      if (a) a.setAttribute('aria-current', 'true');
      place([W / 2, H - 4]);
    }
  }
  function place([sx, sy]) {
    const tw = tip.offsetWidth, th = tip.offsetHeight;
    let x = sx + 14, y = sy + 14;
    // On a phone the tip sits in a corner away from the agent, not on its neighbours.
    if (phone) { x = sx < W / 2 ? W - tw - 4 : 4; y = sy < H / 2 ? H - th - 4 : 4; }
    if (x + tw > W - 4) x = sx - 14 - tw;
    if (y + th > H - 4) y = sy - 14 - th;
    x = Math.max(4, Math.min(W - tw - 4, x)); y = Math.max(4, Math.min(H - th - 4, y));
    tip.style.transform = 'translate(' + Math.round(x) + 'px,' + Math.round(y) + 'px)';
  }
  // The pointer: the nearest drawn agent within reach of a finger.
  const nearest = (ev) => {
    const rc = svg.getBoundingClientRect(), x = ev.clientX - rc.left, y = ev.clientY - rc.top;
    let best = -1, bd = Infinity;
    for (const i of drawn) { const d = Math.hypot(X[i] - x, Y[i] - y); if (d < Math.max(12, reach[i] + 6) && d < bd) { bd = d; best = i; } }
    return [best, [x, y]];
  };
  const open = (i) => { if (i >= 0) location.href = '/agent/' + encodeURIComponent(nodes[i].id); };
  svg.addEventListener('pointermove', (ev) => { const [i, at] = nearest(ev); if (i >= 0) show(i, at); else if (document.activeElement !== svg) show(-1); });
  svg.addEventListener('pointerleave', () => { if (document.activeElement !== svg) show(-1); });
  svg.addEventListener('click', (ev) => open(nearest(ev)[0]));
  svg.addEventListener('keydown', (e) => {
    const next = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (next && order.length) { e.preventDefault(); const k = order.indexOf(shown); show(order[k < 0 ? (next > 0 ? 0 : order.length - 1) : (k + next + order.length) % order.length]); }
    else if (e.key === 'Enter' && shown >= 0) open(shown);
    else if (e.key === 'Escape') show(-1);
  });
  svg.addEventListener('focus', () => { if (shown < 0 && order.length) show(order[0]); });
  svg.addEventListener('blur', () => show(-1));

  // The legend keeps what the drawing has; agents are always drawn.
  function legend() {
    const has = new Set(['agent', ...edges.map((e) => e.kind)]);
    for (const i of drawn) { if (hasRoots(i)) has.add('root'); if (zero(i)) has.add('zero'); if (nodes[i].penalised) has.add('penalty'); }
    fig.querySelectorAll('.tn-legend [data-kind]').forEach((li) => { li.hidden = !has.has(li.dataset.kind); });
    const ul = fig.querySelector('.tn-legend'); if (ul) ul.hidden = false;
  }
  // The agents with no public edge: one line of links under the drawing.
  function looseLine() {
    fig.querySelector('.tn-loose')?.remove();
    if (!loose.length) return;
    const p = document.createElement('p'); p.className = 'tn-loose';
    const all = loose.every((i) => standing[i] > 0);
    p.append((loose.length === 1 ? '1 agent ' + (all ? 'with standing has' : 'has') : loose.length + ' agents ' + (all ? 'with standing have' : 'have')) + ' no public edges yet: ');
    loose.slice(0, LOOSE_NAMES).forEach((i, k) => {
      if (k) p.append(', ');
      const a = document.createElement('a'); a.href = '/agent/' + encodeURIComponent(nodes[i].id); a.textContent = name(nodes[i]); a.dataset.i = String(i);
      p.append(a);
    });
    p.append(loose.length > LOOSE_NAMES ? ' and ' + (loose.length - LOOSE_NAMES) + ' more.' : '.');
    stage.after(p);
  }
  fig.tnScreen = () => ({ w: W, h: H, loose: fig.querySelectorAll('.tn-loose a').length + Math.max(0, loose.length - LOOSE_NAMES),
    discs: drawn.map((i) => [X[i], Y[i], r[i], reach[i]]) });

  const focus = fig.dataset.focus ? index.get(fig.dataset.focus) : undefined;
  function draw() { arrange(); render(); placeLabels(); if (shown >= 0) { const i = shown; shown = -1; show(i); } }
  draw();
  if (focus !== undefined) show(focus);
  if (!reduced) { stage.classList.add('tn-arriving'); requestAnimationFrame(() => requestAnimationFrame(() => stage.classList.remove('tn-arriving'))); }
  let drawnW = W, drawnH = H, pending = 0;
  new ResizeObserver(() => {
    clearTimeout(pending);
    pending = setTimeout(() => { if (stage.clientWidth !== drawnW || stage.clientHeight !== drawnH) { draw(); drawnW = W; drawnH = H; } }, 120);
  }).observe(stage);
  fig.dataset.ready = String(N);
}

main().catch((e) => { if (fig) { fig.dataset.error = e.message; } });
