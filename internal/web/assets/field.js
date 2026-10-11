// A slow halftone field behind the page: ink dots whose size follows drifting
// value noise, so the paper feels alive without moving the content. Adapted
// from the voiceio site's dither field (itself after KOLlateral's 1-bit
// grammar). It draws at most 10 frames a second at CSS-pixel resolution,
// stops while the tab is hidden, draws one still frame under reduced motion
// (following the setting live) and is skipped on styled rooms and Save-Data
// connections.
(() => {
  if (document.documentElement.classList.contains('room-styled') || navigator.connection?.saveData) return;
  const motion = matchMedia('(prefers-reduced-motion: reduce)');
  let still = motion.matches;
  const box = document.createElement('div');
  box.className = 'page-field'; box.setAttribute('aria-hidden', 'true');
  const canvas = document.createElement('canvas');
  box.append(canvas); document.body.prepend(box);
  const ctx = canvas.getContext('2d');
  const hash = (x, y) => {const s = Math.sin(x * 127.1 + y * 311.7) * 43758.5453; return s - Math.floor(s);};
  const noise = (x, y) => {
    const ix = Math.floor(x), iy = Math.floor(y), fx = x - ix, fy = y - iy;
    const ux = fx * fx * (3 - 2 * fx), uy = fy * fy * (3 - 2 * fy);
    return hash(ix, iy) * (1 - ux) * (1 - uy) + hash(ix + 1, iy) * ux * (1 - uy) + hash(ix, iy + 1) * (1 - ux) * uy + hash(ix + 1, iy + 1) * ux * uy;
  };
  const smooth = (a, b, x) => {const t = Math.max(0, Math.min(1, (x - a) / (b - a))); return t * t * (3 - 2 * t);};
  const cell = 7;
  // Interaction: a click or tap on the page's empty background drops a
  // stone in the water (below). Nothing follows the pointer, and clicks on
  // links, buttons and fields leave the field alone, so text stays readable.
  // The field runs at 10 fps at rest and 30 while a wave is moving.
  let lastInput = -1e9;
  const ripples = [];
  const interactive = 'a,button,input,textarea,select,label,summary,[role=button],[contenteditable],[tabindex]';
  addEventListener('pointerdown', e => {
    if (still || e.target?.closest?.(interactive)) return;
    ripples.push({x: e.clientX, y: e.clientY, at: performance.now()});
    if (ripples.length > 8) ripples.shift();
    lastInput = performance.now(); wake();
  }, {passive: true});
  // Wall-clock time, so the pattern continues from page to page instead of restarting.
  const clock = () => (Date.now() / 1000) % 86400;
  // The click wave is a stone dropped in water: the surface first dips under
  // the pointer, then rings of crest and trough spread out, slowing and
  // fading. A crest brightens and pushes the dots outward, a trough darkens
  // and draws them in, so the pattern itself moves, not just a ring on top.
  const waveSpeed = 150, waveLife = 4, waveLength = 72, waveDecay = 1.4, waveTrail = 130, waveFront = 34;
  const waveK = 2 * Math.PI / waveLength;
  let prev = performance.now();
  function draw(t) {
    const w = Math.max(1, canvas.clientWidth), h = Math.max(1, canvas.clientHeight);
    if (canvas.width !== w || canvas.height !== h) {canvas.width = w; canvas.height = h;}
    ctx.clearRect(0, 0, w, h);
    const style = getComputedStyle(box);
    ctx.fillStyle = style.color;
    const base = parseFloat(style.getPropertyValue('--field-alpha')) || 0.07;
    // How strongly the pointer and waves lift the dots: lower on dark pages,
    // where light dots on a dark ground read brighter.
    const glow = parseFloat(style.getPropertyValue('--field-glow')) || 1;
    const now = performance.now(), dt = Math.min(0.2, (now - prev) / 1000); prev = now;
    for (let i = ripples.length - 1; i >= 0; i--) if (now - ripples[i].at > waveLife * 1000) ripples.splice(i, 1);
    const asp = w / h, waves = ripples.map(r => ({x: r.x, y: r.y, age: (now - r.at) / 1000}));
    for (let gy = cell / 2; gy < h; gy += cell) {
      for (let gx = cell / 2; gx < w; gx += cell) {
        // Displacement of the sample point: a radial push riding each wave front.
        let sx = gx, sy = gy, e = 0;
        let hgt = 0, px2 = 0, py2 = 0;
        for (const r of waves) {
          const rx = gx - r.x, ry = gy - r.y, dist = Math.hypot(rx, ry) || 1, front = r.age * waveSpeed, d = dist - front;
          const ux = rx / dist, uy = ry / dist;
          // The shove: water the front has passed is pushed outward and
          // settles back over a few seconds, so the pattern itself moves.
          if (d < 0) { const shove = 22 * (1 - Math.exp(d / 40)) * Math.exp(-r.age / 2.2) * Math.exp(-dist / 260); px2 += ux * shove; py2 += uy * shove; }
          if (d > 3 * waveFront || d < -3 * waveTrail) continue;
          // Ahead of the front the water is still; behind it the rings fade.
          const env = d > 0 ? Math.exp(-(d * d) / (2 * waveFront * waveFront)) : Math.exp(d / waveTrail);
          const amp = Math.exp(-r.age / waveDecay) * env / Math.sqrt(1 + dist / 160);
          const z = -amp * Math.cos(waveK * d);
          hgt += z; px2 += ux * z * 14; py2 += uy * z * 14;
        }
        // Many quick clicks overlap: saturate the sum instead of adding without
        // bound, so a burst of taps stays a rough sea rather than a jolt.
        hgt = Math.tanh(hgt);
        const pm = Math.hypot(px2, py2);
        if (pm > 30) { px2 *= 30 / pm; py2 *= 30 / pm; }
        sx -= px2; sy -= py2;
        const cx = (sx / w - 0.5) * asp + 0.5, cy = sy / h;
        const c = smooth(0.5, 0.95, noise(cx * 3.2 + t * 0.06, cy * 3.2 - t * 0.045)) * 0.7;
        // A crest lifts dots (brighter, larger), a trough presses them down.
        const v = Math.max(0, Math.max(c, e * 0.9 * glow) * (1 + 0.9 * hgt * glow) + Math.max(0, hgt) * 0.45 * glow);
        e = Math.max(e, Math.max(0, hgt) * 0.35) * glow;
        if (v <= 0.04) continue;
        const j = (hash(gx, gy) - 0.5) * cell * 0.35;
        ctx.globalAlpha = Math.min(1, (0.35 + v) * (base + e * 0.4));
        ctx.beginPath();
        ctx.arc(gx + j, gy + j, Math.min(cell * 0.52, cell * 0.16 + v * cell * 0.42), 0, 6.2832);
        ctx.fill();
      }
    }
    ctx.globalAlpha = 1;
  }
  draw(clock());
  addEventListener('resize', () => draw(clock()), {passive: true});
  let raf = 0, last = 0;
  const loop = now => {
    const active = now - lastInput < 2500 || ripples.length > 0;
    if (still && !active) {raf = 0; draw(clock()); return;}
    if (now - last > 1000 / (active ? 30 : 10)) {last = now; draw(still ? 2 : clock());}
    raf = document.hidden ? 0 : requestAnimationFrame(loop);
  };
  function wake() {if (!raf && !document.hidden) raf = requestAnimationFrame(loop);}
  document.addEventListener('visibilitychange', wake);
  // Reduced motion switched on mid-visit stops the field at its next frame;
  // switched off, it starts again, without a reload.
  motion.addEventListener('change', () => {still = motion.matches; ripples.length = 0; wake();});
  if (!still) wake();
})();
