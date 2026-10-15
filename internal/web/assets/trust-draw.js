// trust-draw.js: what the trust drawings share on the graph-gl.js renderer,
// the network on /trust and /trust/network (trust-network.js) and the trust
// layer of /swarmchasing (trust-layer.js): CSS tokens to RGB and one
// force-directed layout. One copy each; no second graph engine.

// Every colour is a CSS token, resolved through a canvas into RGB.
const probe = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
export function rgbOf(css, fallback) {
  if (!css || !probe) return fallback;
  probe.clearRect(0, 0, 1, 1); probe.fillStyle = '#000'; probe.fillStyle = css; probe.fillRect(0, 0, 1, 1);
  const d = probe.getImageData(0, 0, 1, 1).data; return [d[0] / 255, d[1] / 255, d[2] / 255];
}

// A stable hash of a string in [0, 1).
export function hash(s) { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return (h >>> 0) / 4294967296; }

// layout places nodes ({id}) joined by edges ({a, b, weight}) with discs of
// radius[i]: a stable start (each node on a spiral by rank, nudged by a hash
// of its id), then ticks steps of repulsion between all nodes and springs
// along edges, cooling as it goes. A node with pinned[i] keeps the position
// in at.x/at.y it starts from. step(count) runs count more steps; done()
// says when all ticks ran. Deterministic: the same data gives the same map.
export function layout(nodes, edges, radius, { ticks = 320, pinned = null, at = null } = {}) {
  const n = nodes.length, x = new Float64Array(n), y = new Float64Array(n), vx = new Float64Array(n), vy = new Float64Array(n);
  for (let i = 0; i < n; i++) {
    if (pinned && pinned[i] && at) { x[i] = at.x[i]; y[i] = at.y[i]; continue; }
    const a = i * 2.39996 + hash(nodes[i].id) * 0.5, r = 40 * Math.sqrt(i + 1);
    x[i] = Math.cos(a) * r; y[i] = Math.sin(a) * r;
  }
  const area = Math.max(1, n) * 3600, k = Math.sqrt(area / Math.max(1, n));
  let tick = 0;
  return {
    x, y, done: () => tick >= ticks,
    step(count) {
      for (let c = 0; c < count && tick < ticks; c++, tick++) {
        const cool = 1 - tick / ticks;
        vx.fill(0); vy.fill(0);
        for (let i = 0; i < n; i++) {
          for (let j = i + 1; j < n; j++) {
            let dx = x[i] - x[j], dy = y[i] - y[j];
            let d2 = dx * dx + dy * dy;
            if (d2 < 1e-4) { dx = (hash(nodes[i].id + j) - 0.5); dy = (hash(nodes[j].id + i) - 0.5); d2 = dx * dx + dy * dy + 1e-4; }
            const minD = radius[i] + radius[j] + 6;
            const f = k * k / d2 + (d2 < minD * minD ? (minD * minD - d2) / d2 * 2 : 0);
            vx[i] += dx * f; vy[i] += dy * f; vx[j] -= dx * f; vy[j] -= dy * f;
          }
        }
        for (const e of edges) {
          const dx = x[e.b] - x[e.a], dy = y[e.b] - y[e.a], d = Math.sqrt(dx * dx + dy * dy) + 1e-6;
          const f = d / k * (1 + Math.min(2, e.weight / 25)) * (e.spring === undefined ? 1 : e.spring);
          vx[e.a] += dx * f; vy[e.a] += dy * f; vx[e.b] -= dx * f; vy[e.b] -= dy * f;
        }
        const limit = k * 2 * cool + 1;
        for (let i = 0; i < n; i++) {
          if (pinned && pinned[i]) continue;
          vx[i] -= x[i] * 0.04; vy[i] -= y[i] * 0.04; // gravity keeps islands near
          const m = Math.sqrt(vx[i] * vx[i] + vy[i] * vy[i]) + 1e-9, s = Math.min(m, limit) / m;
          x[i] += vx[i] * s; y[i] += vy[i] * s;
          if (!Number.isFinite(x[i]) || !Number.isFinite(y[i])) { x[i] = 0; y[i] = 0; }
        }
      }
    },
  };
}
