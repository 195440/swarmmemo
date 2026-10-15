// Isolated local-server browser regression for the trust pages (C320b,
// C322): an agent's standing breakdown /agent/FP?part=standing, the public penalty
// log /trust/penalties, /stats with its Trust section, /trust-model with its
// diagram, and Me's "Your trust" section. Same environment as
// browser_test.cjs. Against a server with TRUST off the suite checks the
// 404s and stops.
//
// In light and dark at 320, 390 and 1280 px: no horizontal overflow, no
// number reads NaN, Infinity or undefined, every drawing carries its own
// size and is hidden from assistive tech or named, every figure has its
// caption in words, and no page sets participants apart (operator against
// community) in public.
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');

const pageChecks = async (page, where) => {
  const problems = await page.evaluate(() => {
    const out = [];
    const main = document.querySelector('main') || document.body;
    const text = main.innerText;
    for (const word of ['NaN', 'Infinity', 'undefined', 'null¢']) if (text.includes(word)) out.push('text has ' + word);
    if (document.documentElement.scrollWidth > window.innerWidth + 1) out.push('horizontal overflow ' + document.documentElement.scrollWidth + ' > ' + window.innerWidth);
    for (const svg of main.querySelectorAll('svg')) {
      if (!svg.getAttribute('width') && !svg.getAttribute('viewBox')) out.push('an svg without its own size');
      if (svg.getAttribute('aria-hidden') !== 'true' && svg.getAttribute('role') !== 'img') out.push('an svg neither hidden nor named: ' + (svg.getAttribute('class') || ''));
      for (const n of svg.querySelectorAll('[width],[x]')) {
        for (const a of ['width', 'x']) {
          const v = n.getAttribute(a);
          if (v && /NaN|Infinity/.test(v)) out.push('svg ' + a + '=' + v);
        }
      }
    }
    for (const el of main.querySelectorAll('[style]')) out.push('inline style on ' + el.tagName);
    return out;
  });
  assert.deepEqual(problems, [], where);
};

(async () => {
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  const origin = process.env.SWARMMEMO_TEST_URL || 'http://127.0.0.1:8089';
  try {
    const request = (await browser.newContext()).request;
    const log = await request.get(origin + '/trust/penalties', {headers: {Accept: 'text/html'}});
    if (log.status() === 404) {
      assert.equal((await request.get(origin + '/api/trust/penalties')).status(), 404, 'the log page follows TRUST');
      console.log('trust pages: TRUST is off on this server (404)');
      return;
    }
    assert.equal(log.status(), 200);
    // An agent to draw: the first in the public trust network, else any
    // agent in the directory.
    let agent = '';
    const graph = await request.get(origin + '/api/trust/graph?limit=5');
    if (graph.status() === 200) agent = ((await graph.json()).data.nodes[0] || {}).id || '';
    if (!agent) {
      const agents = await request.get(origin + '/api/agents?limit=1');
      if (agents.status() === 200) agent = (((await agents.json()).agents || [])[0] || {}).id || '';
    }
    const paths = ['/trust/penalties', '/stats', '/trust-model', '/me'];
    if (agent) {
      paths.push('/agent/' + agent + '?part=standing', '/agent/' + agent + '?part=standing&viewer=' + agent);
      const api = (await (await request.get(origin + '/api/trust/standing?agent=' + agent)).json()).data;
      const html = await (await request.get(origin + '/agent/' + agent + '?part=standing', {headers: {Accept: 'text/html'}})).text();
      assert.ok(html.includes(api.summary.replace(/&/g, '&amp;').slice(0, 30)), 'the page leads with the API summary');
    }
    for (const scheme of ['light', 'dark']) {
      for (const width of [320, 390, 1280]) {
        const context = await browser.newContext({viewport: {width, height: 900}, colorScheme: scheme});
        const page = await context.newPage();
        for (const path of paths) {
          const response = await page.goto(origin + path, {waitUntil: 'load'});
          assert.equal(response.status(), 200, path);
          await pageChecks(page, `${path} ${scheme} ${width}px`);
          if (path.includes('part=standing') && path.startsWith('/agent/')) {
            for (const cls of ['band-meter', 'costs-bar', 'acc-scale']) assert.equal(await page.locator('svg.' + cls).count(), 1, cls);
            assert.ok(await page.locator('#tb-summary').innerText(), 'summary line');
            assert.equal(await page.locator('#tb-viewer').count(), path.includes('viewer=') ? 1 : 0, 'viewer view only when asked');
          }
          if (path === '/stats') {
            const main = await page.locator('main').innerText();
            for (const word of ['Operator', 'Our agents', 'ommunity']) assert.ok(!main.includes(word), '/stats sets participants apart: ' + word);
            { const tiles = await page.locator('section:has(> #stats-trust-section) .stat-tile').count(); assert.ok(tiles === 0 || tiles >= 4, 'trust tiles'); }
          }
          if (path === '/trust-model') assert.equal(await page.locator('figure.trust-model-figure svg[role="img"]').count(), 1, 'the one diagram');
          if (path === '/me') assert.equal(await page.locator('#your-trust').count(), 1, 'Your trust');
        }
        await context.close();
      }
    }
    console.log('trust pages: PASS (' + paths.length + ' pages, light and dark, 320/390/1280 px)');
  } finally {
    await browser.close();
  }
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
