// Isolated local-server browser regression for the /swarmchasing explainer
// figures (assets/findings-viz.js). Same environment as browser_test.cjs.
//
// Every [data-finding] figure renders its SVG with no console error and no
// NaN/Infinity, plays when scrolled into view, and Replay plays it again.
// Checked in light and dark, at 1280 and 390 px (no horizontal scroll), and
// with reduced motion, where the figure shows its final state with no animation.
// FV_SHOTS=<dir> also saves a screenshot of each figure per pass.
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const path = require('node:path');
const origin = process.env.SWARMMEMO_TEST_URL || 'http://127.0.0.1:8089';
const shots = process.env.FV_SHOTS || '';

const sane = (page) => page.evaluate(() => {
  const out = [];
  document.querySelectorAll('[data-finding] svg *').forEach((n) => {
    for (const a of n.getAttributeNames()) {
      const v = n.getAttribute(a);
      if (/NaN|Infinity|undefined/.test(v)) out.push(n.nodeName + ' ' + a + '=' + v);
      if (['width', 'height', 'r'].includes(a) && Number(v) < 0) out.push(n.nodeName + ' ' + a + '=' + v);
    }
    if (n.childElementCount === 0 && /NaN|Infinity|undefined/.test(n.textContent)) out.push('text ' + n.textContent);
  });
  return out;
});

(async () => {
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    const passes = [
      {width: 1280, height: 900, scheme: 'light', motion: 'no-preference'},
      {width: 390, height: 844, scheme: 'dark', motion: 'no-preference'},
      {width: 390, height: 844, scheme: 'light', motion: 'reduce'},
      {width: 1280, height: 900, scheme: 'dark', motion: 'reduce'},
    ];
    for (const p of passes) {
      const where = `${p.width}px ${p.scheme} motion=${p.motion}`;
      const context = await browser.newContext({viewport: {width: p.width, height: p.height}, colorScheme: p.scheme, reducedMotion: p.motion});
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (e) => errors.push(e.message));
      page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
      await page.goto(origin + '/swarmchasing', {waitUntil: 'domcontentloaded'});
      const ids = await page.$$eval('[data-finding]', (els) => els.map((e) => e.dataset.finding));
      assert.ok(ids.length >= 1, 'the paper carries at least one explainer figure');
      for (const id of ids) {
        const fig = page.locator(`[data-finding="${id}"]`);
        await page.waitForFunction((id) => ['ready', 'playing', 'done'].includes(document.querySelector(`[data-finding="${id}"]`).dataset.state), id);
        assert.equal(await fig.getAttribute('data-plays'), '0', `${id} waits until it is scrolled into view (${where})`);
        console.log('  pass', where, id);
        await fig.scrollIntoViewIfNeeded();
        await page.waitForFunction((id) => document.querySelector(`[data-finding="${id}"]`).dataset.state === 'done', id, {timeout: 45000});
        assert.equal(await fig.getAttribute('data-plays'), '1', `${id} played once on view (${where})`);
        assert.ok(await fig.locator('svg rect, svg circle').count() > 3, `${id} drew its marks (${where})`);
        if (id === 'office-hours') assert.equal(await fig.locator('.fv-big').textContent(), '69.5%', 'office-hours ends on the measured share');
        // Replay animates again (or, with reduced motion, redraws the final state at once).
        await fig.getByRole('button', {name: /replay/i}).click();
        if (p.motion === 'reduce') assert.equal(await fig.getAttribute('data-state'), 'done', `${id} has no animation under reduced motion`);
        else assert.equal(await fig.getAttribute('data-state'), 'playing', `${id} replays (${where})`);
        await page.waitForFunction((id) => document.querySelector(`[data-finding="${id}"]`).dataset.state === 'done', id, {timeout: 45000});
        assert.equal(await fig.getAttribute('data-plays'), '2', `${id} replay counted (${where})`);
        assert.deepEqual(await sane(page), [], `no bad numbers in ${id} (${where})`);
        const box = await fig.boundingBox();
        assert.ok(box.width > 200 && box.width <= p.width, `${id} fits the column (${where}): ${box.width}`);
        if (shots) await fig.locator('xpath=..').screenshot({path: path.join(shots, `${id}-${p.width}-${p.scheme}-${p.motion}.png`)});
      }
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
      assert.ok(overflow <= 0, `no horizontal scroll at ${where}: ${overflow}px`);
      assert.deepEqual(errors, [], `no console errors (${where})`);
      await context.close();
    }
    console.log('findings_viz_test: ok');
  } finally {
    await browser.close();
  }
})().catch((err) => { console.error(err); process.exit(1); });
