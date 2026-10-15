// Local fixtures only: runs its own owned test binary with TRUST=on on a
// fresh loopback data directory; never production.
// The trust network figure (C354, redrawn as SVG in C358) on /agents/network
// and /trust#network, against a served stand-in of /api/trust/graph shaped
// like the live one (42 agents, two hubs of about 20 edges, a third of 10,
// seven agents with no edge, half with no standing, a handle that is
// markup). In light and dark, at 1280, 390 and 320px, with and without
// reduced motion: every agent drawn, or named in the line for agents with no
// edge; the drawing fills at least 60% of its panel; dots read 3 to 9px and
// never overlap; no label overlaps another label or a dot or leaves the
// panel; hubs are labelled; every stroke and fill is a grey (ink, not
// colour); the legend keeps only what is drawn; every position is a finite
// non-negative number; the figure is a focusable image with a live tip;
// hover and focus show an agent with its neighbours labelled and Escape
// clears it; a handle is only ever text; no console error and no horizontal
// scroll.
const assert = require('node:assert/strict');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {mkdtempSync} = require('node:fs');
const {launch, noHorizontalScroll} = require('./testdata/messages_mock.cjs');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');

const freePort = () => new Promise((ok, fail) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => { const {port} = s.address(); s.close(() => ok(port)); });
  s.on('error', fail);
});
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

// A stand-in graph: hubs 0 and 12 take accepted work from leaves, 17 a few
// more, witnesses along some of the same pairs; 35 to 41 have no edge.
function standIn() {
  const hex = (i) => (i + 1).toString(16).padStart(4, '0').repeat(16);
  const nodes = [], edges = [];
  for (let i = 0; i < 42; i++) {
    const standing = i < 21 ? Math.round((1.9 - i * 0.07) * 100) / 100 : 0;
    nodes.push({id: hex(i), handle: i === 3 ? '<img src=x id=injected onerror="window.__pwned=1">' : i % 9 === 8 ? '' : 'agent-' + i,
      standing, standing_cents: Math.round(10 ** standing - 1), fake_cost: 'about $0.10 to fake', band: i === 1 ? 3 : i === 2 ? 2 : 1,
      band_name: i === 1 ? 'trusted' : i === 2 ? 'proven' : 'signed', roots: i < 18 && i % 2 ? ['wallet'] : [], penalised: false, core: standing > 0});
  }
  const add = (a, b, kind, count) => edges.push({from: hex(a), to: hex(b), kind, count, weight: count * 10});
  for (let i = 1; i < 35; i++) {
    if (i === 12 || i === 17) continue;
    add(i % 2 ? 0 : 12, i, 'work_accept', 1 + (i % 5));
    if (i % 4 === 1) add(0, i, 'witness', 1);
  }
  for (const i of [21, 22, 23, 24, 25, 26]) add(17, i, 'work_accept', 2);
  add(0, 12, 'work_accept', 3); add(12, 17, 'work_accept', 2);
  return {mode: 'on', run: 9, as_of: 1792022400, params_version: 11, limits: {core: 100, nodes: 200, edges: 2000}, nodes, edges,
    truncated: {core: 0, nodes: 0, edges: 0}, scope: 'stand-in'};
}

(async () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'trust-network-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const server = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, DATA_DIR: path.join(dir, 'data'), SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port, TRUST: 'on'},
    stdio: 'ignore',
  });
  process.on('exit', () => server.kill());
  for (let i = 0; ; i++) {
    try { if ((await fetch(origin + '/api/messages?limit=1')).ok) break; } catch {}
    assert.ok(i < 80, 'server did not start');
    await wait(250);
  }
  const graph = standIn(), body = JSON.stringify({ok: true, data: graph});
  const kinds = new Set(graph.edges.map((e) => e.kind));
  const browser = await launch();
  try {
    const runs = [
      {scheme: 'dark', width: 1280, reduced: false, url: '/agents/network'},
      {scheme: 'light', width: 1280, reduced: true, url: '/trust#network'},
      {scheme: 'light', width: 390, reduced: false, url: '/agents/network'},
      {scheme: 'dark', width: 390, reduced: true, url: '/trust#network'},
      {scheme: 'light', width: 320, reduced: false, url: '/agents/network'},
    ];
    for (const {scheme, width, reduced, url} of runs) {
      const tag = `${url} ${scheme} ${width}px${reduced ? ' reduced' : ''}`;
      const phone = width < 600;
      const context = await browser.newContext({viewport: {width, height: phone ? 844 : 900}, colorScheme: scheme, reducedMotion: reduced ? 'reduce' : 'no-preference', isMobile: phone, hasTouch: phone, deviceScaleFactor: phone ? 2 : 1});
      const page = await context.newPage(), errors = [];
      page.on('pageerror', (e) => errors.push(e.message));
      page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
      await page.route('**/api/trust/graph**', (route) => route.fulfill({status: 200, contentType: 'application/json', body}));
      // The stand-in agents exist only in the graph: their profile reads (the avatars app.js adds to agent links) answer as the live server would.
      await page.route(/\/api\/agent\/[0-9a-f]{64}$/, (route) => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({ok: true, agent: {id: route.request().url().slice(-64)}})}));
      await page.goto(origin + url, {waitUntil: 'load'});
      const fig = page.locator('[data-fig="network"]');
      await fig.scrollIntoViewIfNeeded();
      await page.waitForFunction(() => { const f = document.querySelector('[data-fig="network"]'); return f.dataset.ready || f.dataset.error; }, null, {timeout: 20000});
      assert.equal(await fig.getAttribute('data-error'), null, tag + ': ' + await fig.getAttribute('data-error'));
      assert.equal(Number(await fig.getAttribute('data-ready')), graph.nodes.length, tag + ': every agent drawn');
      await page.waitForTimeout(reduced ? 200 : 1200); // labels fade in once the discs have grown
      if (reduced) assert.equal(await page.locator('.tn-arriving').count(), 0, tag + ': no entrance under reduced motion');

      const shot = await page.evaluate(() => {
        const f = document.querySelector('[data-fig="network"]'), s = f.tnScreen(), plot = f.querySelector('.tn-plot').getBoundingClientRect();
        const labels = [...f.querySelectorAll('.tn-label:not([hidden])')].map((e) => { const r = e.getBoundingClientRect(); return [r.left - plot.left, r.top - plot.top, r.right - plot.left, r.bottom - plot.top, e.textContent]; });
        const loose = [...f.querySelectorAll('.tn-loose a')].map((a) => a.textContent);
        // Every paint in the drawing and its legend, as rgb triples.
        const paints = [], probe = document.createElement('canvas').getContext('2d', {willReadFrequently: true});
        for (const e of f.querySelectorAll('.tn-plot :is(path,circle,text), .tn-legend svg :is(path,circle)')) {
          const cs = getComputedStyle(e);
          for (const v of [cs.stroke, cs.fill]) {
            if (!v || v === 'none') continue;
            probe.clearRect(0, 0, 1, 1); probe.fillStyle = '#000'; probe.fillStyle = v; probe.fillRect(0, 0, 1, 1);
            paints.push([...probe.getImageData(0, 0, 1, 1).data.slice(0, 3)]);
          }
        }
        return {...s, labels, loose, paints, stageH: f.querySelector('.tn-stage').getBoundingClientRect().height,
          plot: [f.querySelector('.tn-plot').getAttribute('role'), f.querySelector('.tn-plot').getAttribute('tabindex'), !!f.querySelector('.tn-plot').getAttribute('aria-label')]};
      });
      // Every agent: a dot, or a link in the line for agents with no edge.
      assert.equal(shot.discs.length, 35, tag + ': the 35 agents with an edge are drawn');
      assert.equal(shot.discs.length + shot.loose.length, graph.nodes.length, tag + ': every agent drawn or named');
      assert.deepEqual(shot.loose, ['agent-35', 'agent-36', 'agent-37', 'agent-38', 'agent-39', 'agent-40', 'agent-41'].map((h, k) => k === 0 ? graph.nodes[35].id.slice(0, 10) : h), tag + ': the agents with no edge');
      assert.match(await page.locator('.tn-loose').textContent(), /^7 agents have no public edges yet: /);
      // The drawing fills the panel.
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const [x, y, , R] of shot.discs) { x0 = Math.min(x0, x - R); y0 = Math.min(y0, y - R); x1 = Math.max(x1, x + R); y1 = Math.max(y1, y + R); }
      const fill = (x1 - x0) * (y1 - y0) / (shot.w * shot.h);
      assert.ok(fill >= 0.6, `${tag}: the drawing fills ${fill.toFixed(2)} of the panel`);
      assert.ok(x0 >= -1 && y0 >= -1 && x1 <= shot.w + 1 && y1 <= shot.h + 1, tag + ': the drawing stays in the panel');
      assert.ok(shot.stageH <= (phone ? 844 : 900) * 0.75 + 1, tag + ': the panel is no taller than the drawing needs');
      // Dots: small ink marks, never on top of each other.
      const radii = shot.discs.map((d) => d[2]);
      assert.ok(Math.min(...radii) >= 2.9 && Math.max(...radii) <= 9.1, `${tag}: dot radii ${Math.min(...radii).toFixed(1)}-${Math.max(...radii).toFixed(1)}px`);
      for (let i = 0; i < shot.discs.length; i++) {
        for (let j = i + 1; j < shot.discs.length; j++) {
          const [ax, ay, ar] = shot.discs[i], [bx, by, br] = shot.discs[j];
          assert.ok(Math.hypot(ax - bx, ay - by) >= ar + br, `${tag}: dots ${i} and ${j} overlap`);
        }
      }
      // Ink, not colour: every stroke and fill is a grey.
      assert.ok(shot.paints.length > 100, tag + ': paints read');
      for (const [r, g, b] of shot.paints) assert.ok(Math.max(r, g, b) - Math.min(r, g, b) <= 12, `${tag}: a coloured paint rgb(${r}, ${g}, ${b})`);
      // The figure is a focusable image.
      assert.deepEqual(shot.plot, ['img', '0', true], tag + ': the drawing is a focusable image with a label');
      assert.equal(await page.locator('.tn-tip').getAttribute('role'), 'status');
      // Labels: some, none overlapping another, a disc or the panel's edge.
      assert.ok(shot.labels.length >= (phone ? 3 : 6), `${tag}: ${shot.labels.length} labels`);
      for (const h of ['agent-0', 'agent-12']) assert.ok(shot.labels.some((l) => l[4] === h), `${tag}: hub ${h} labelled`);
      shot.labels.forEach(([a, b, c, d, t], k) => {
        assert.ok(a >= 0 && b >= 0 && c <= shot.w && d <= shot.h, `${tag}: label ${t} leaves the panel`);
        shot.labels.forEach(([e, f, g, h, u], m) => { if (m > k) assert.ok(c <= e || g <= a || d <= f || h <= b, `${tag}: labels ${t} and ${u} overlap`); });
        for (const [x, y, rr] of shot.discs) {
          const cx = Math.max(a, Math.min(x, c)), cy = Math.max(b, Math.min(y, d));
          assert.ok(Math.hypot(cx - x, cy - y) >= rr - 0.5, `${tag}: label ${t} covers a disc`);
        }
      });
      // The legend keeps what is drawn.
      // Agents with an edge: roots and no standing yet among them, no penalty.
      const legend = await page.$$eval('.tn-legend [data-kind]', (lis) => lis.map((li) => [li.dataset.kind, !li.hidden]));
      assert.equal(legend.length, 8, tag + ': legend entries');
      for (const [kind, shown] of legend) {
        const has = kind in {vouch: 1, work_accept: 1, witness: 1, key_link: 1} ? kinds.has(kind) : kind !== 'penalty';
        assert.equal(shown, has, `${tag}: legend ${kind}`);
      }
      // Hover: the pointer on hub agent-12 lights it and labels its neighbours.
      const hub = await page.evaluate(() => { const f = document.querySelector('[data-fig="network"]'), [x, y] = f.tnScreen().discs[12], r = f.querySelector('.tn-plot').getBoundingClientRect(); return [r.left + x, r.top + y]; });
      await page.mouse.move(hub[0], hub[1]);
      await page.locator('.tn-tip').waitFor({state: 'visible'});
      assert.match(await page.locator('.tn-tip').textContent(), /^agent-12Standing/, tag + ': hover shows the agent');
      assert.equal(await page.locator('.tn-plot.tn-lit').count(), 1, tag + ': hover lights the drawing');
      assert.equal(await page.locator('.tn-label-on').textContent(), 'agent-12');
      await page.mouse.move(1, 1);
      assert.ok(await page.locator('.tn-tip').isHidden(), tag + ': leaving clears the agent');
      // Focus: the top agent, its neighbours labelled; Escape clears.
      await page.locator('.tn-plot').focus();
      const tip = page.locator('.tn-tip');
      await tip.waitFor({state: 'visible'});
      assert.match(await tip.textContent(), /^agent-0Standing 1\.9/);
      const lit = await page.locator('.tn-label:not([hidden])').count();
      assert.ok(lit >= 5, `${tag}: focus labels the neighbours (${lit})`);
      assert.equal(await page.locator('.tn-label-on').textContent(), 'agent-0');
      await page.keyboard.press('ArrowRight');
      assert.match(await tip.textContent(), /Band: trusted/);
      await page.keyboard.press('Escape');
      assert.ok(await tip.isHidden(), tag + ': Escape hides the agent');
      assert.equal(await page.locator('.tn-plot.tn-lit').count(), 0, tag + ': Escape fades nothing');
      // Every position set from script is a finite, non-negative number (the explainer's fuzz rule).
      const styles = await page.$$eval('[data-fig="network"] [style]', (els) => els.map((e) => e.getAttribute('style')).filter((t) => /NaN|Infinity|-\d/.test(t)));
      assert.deepEqual(styles, [], tag + ': styles');
      // A handle is only ever text.
      assert.equal(await page.evaluate(() => !!document.getElementById('injected') || !!window.__pwned), false, tag + ': a handle stays text');
      assert.ok(await noHorizontalScroll(page), tag + ': no horizontal scroll');
      assert.deepEqual(errors, [], tag + ': no console error');
      await context.close();
    }
    console.log('trust network: every agent drawn or named, fills its panel, dots 3-9px apart, ink only, labels clear of labels and dots, legend, hover, focus and text-only handles pass, light and dark, 1280/390/320px, reduced motion');
  } finally {
    await browser.close();
  }
  process.exit(0); // the exit handler stops the owned server
})().catch((e) => { console.error(e); process.exit(1); });
