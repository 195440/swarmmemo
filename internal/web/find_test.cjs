// Local fixtures only. This test registers a labeled disposable agent on loopback.
// Find (C188): /find at 360px and 1280px, with and without JavaScript: the
// search box and its scope switch, type chips, applied filters as chips that
// clear, results with a why line, the header with Find on one row, the home
// search box sending "Services and agents" to /find, and the same answer as
// GET /api/find.
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const {pathToFileURL} = require('node:url');
const path = require('node:path');
const os = require('node:os');
(async () => {
  const origin = process.env.SWARMMEMO_TEST_URL || 'http://127.0.0.1:8100';
  assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/, 'find browser tests require local loopback');
  const {Client, generateKey} = await import(pathToFileURL(path.resolve('clients/javascript/swarmmemo.mjs')));
  const agent = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const send = command => agent.send(agent.prepare(command));
  const marker = 'zq' + Date.now().toString(36);
  const handle = 'find-' + marker;
  await send({operation: 'agent.register', handle});
  await send({operation: 'agent.profile.publish', data: JSON.stringify({schema: 1, description: 'Disposable browser fixture: forecasts ' + marker + ' weather. <img src=x onerror=alert(1)>', capabilities: ['forecast'], availability: 'available'})});
  const api = await (await fetch(origin + '/api/find?q=' + marker)).json();
  assert.equal(api.ok, true, JSON.stringify(api));
  assert.equal(api.data.results[0].url, '/@' + handle, 'the API finds the fixture first');
  assert.deepEqual(api.data.filters, {type: [], category: '', max_price: '', output: '', network: '', protocol: ''}, 'every filter is on the wire');
  // The protocol filter (C202c): an agent takes no payment protocol, so it drops out.
  const byProtocol = await (await fetch(origin + '/api/find?q=' + marker + '&protocol=mpp')).json();
  assert.equal(byProtocol.ok, true, JSON.stringify(byProtocol));
  assert.equal(byProtocol.data.filters.protocol, 'mpp', 'the protocol filter is on the wire');
  assert.ok(!byProtocol.data.results.some(r => r.url === '/@' + handle), 'agents leave a protocol filter');
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    for (const width of [360, 1280]) {
      const ctx = await browser.newContext({viewport: {width, height: 900}});
      const page = await ctx.newPage(), errors = [], writes = [];
      page.on('pageerror', e => errors.push(e.message));
      page.on('dialog', d => {errors.push('dialog: ' + d.message()); d.dismiss();});
      page.on('request', r => {const p = new URL(r.url()).pathname; if (!['GET', 'HEAD', 'OPTIONS'].includes(r.method()) || /^\/(w|w64|c64)\//.test(p)) writes.push(p);});
      const response = await page.goto(origin + '/find?q=' + marker);
      assert.equal(response.status(), 200);
      // The header: Find is a top-level link, current here, and the header keeps one row
      // of links (at 360px the links sit on their own row under the brand and Me).
      const nav = page.locator('header.topbar .site-nav a');
      assert.deepEqual(await nav.allTextContents(), ['Feed', 'Rooms', 'Agents', 'Find', 'Docs']);
      assert.equal(await page.locator('header.topbar .site-nav a[href="/find"]').getAttribute('aria-current'), 'page');
      const tops = await nav.evaluateAll(links => links.map(a => Math.round(a.getBoundingClientRect().top)));
      assert.ok(Math.max(...tops) - Math.min(...tops) <= 2, width + 'px: the nav links are on one row ' + tops);
      const [brand, me, first] = await Promise.all(['header.topbar .brand', 'header.topbar .workspace-link', 'header.topbar .site-nav a >> nth=0'].map(s => page.locator(s).boundingBox()));
      const mid = b => b.y + b.height / 2;
      assert.ok(Math.abs(mid(brand) - mid(me)) < 8, width + 'px: brand and Me share a row');
      if (width >= 1280) assert.ok(Math.abs(mid(brand) - mid(first)) < 8, '1280px: one header row');
      const lastLink = await nav.last().boundingBox();
      assert.ok(lastLink.x + lastLink.width <= width, width + 'px: the last nav link is on screen');
      // The result: the agent, with its badge, link, untrusted text inert and a why line.
      const result = page.locator('.find-result').filter({has: page.locator('a[href="/@' + handle + '"]')});
      assert.equal(await result.count(), 1, 'the fixture is a result');
      assert.equal(await result.locator('.badge').first().textContent(), 'agent');
      assert.match(await result.locator('.find-why').textContent(), /Matches “zq/);
      assert.equal(await page.locator('.find-result img, .find-result script').count(), 0, 'descriptions are inert');
      assert.equal(await page.locator('.find-types a').count(), 6, 'type chips');
      assert.equal(await page.locator('.find-types a[aria-current]').textContent(), 'All');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px find overflow');
      for (const row of await page.locator('.find-result').all()) {
        const box = await row.boundingBox();
        assert.ok(box && box.x >= 0 && box.x + box.width <= width + 1, width + 'px result outside the viewport');
      }
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-find-' + width + '.png'), fullPage: true});
      // A type chip narrows, and is current.
      await page.locator('.find-types a', {hasText: 'Agents'}).click();
      await page.waitForURL(/type=agent/);
      assert.equal(await page.locator('.find-types a[aria-current]').textContent(), 'Agents');
      assert.equal(await page.locator('.find-result a[href="/@' + handle + '"]').count(), 1);
      // Words become a filter chip; an agent has no price per call, so it drops out; the chip clears.
      await page.goto(origin + '/find?q=' + encodeURIComponent(marker + ' under a cent'));
      const chip = page.locator('.find-filters a', {hasText: '$0.01'});
      assert.equal(await chip.count(), 1, 'the max price filter is shown');
      assert.equal(await page.locator('.find-result a[href="/@' + handle + '"]').count(), 0, 'unpriced entries leave a price filter');
      await chip.click();
      await page.waitForURL(/max_price=any/);
      assert.equal(await page.locator('.find-filters a', {hasText: '$0.01'}).count(), 0);
      assert.equal(await page.locator('.find-result a[href="/@' + handle + '"]').count(), 1, 'cleared, it is back');
      // Edit filters: the form sets them explicitly.
      await page.locator('.find-edit summary').click();
      await page.locator('.find-edit-form select[name="output"]').selectOption('json');
      await page.locator('.find-edit-form button').click();
      await page.waitForURL(/output=json/);
      assert.equal(await page.locator('.find-filters a', {hasText: 'json'}).count(), 1);
      // The home page's one search box: Services and agents goes to Find.
      await page.goto(origin + '/');
      await page.locator('.search-panel .search-scope label', {hasText: 'Services and agents'}).click();
      await page.locator('#search').fill(marker);
      await page.locator('#search').press('Enter');
      await page.waitForURL(u => new URL(u).pathname === '/find');
      assert.equal(await page.locator('.find-result a[href="/@' + handle + '"]').count(), 1);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px home overflow');
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await ctx.close();
    }
    // Without JavaScript: the same page, and the scope switch works through the server.
    const plain = await browser.newContext({javaScriptEnabled: false, viewport: {width: 360, height: 800}});
    const plainPage = await plain.newPage();
    await plainPage.goto(origin + '/?q=' + marker + '&in=find');
    assert.equal(new URL(plainPage.url()).pathname, '/find');
    assert.equal(await plainPage.locator('.find-result a[href="/@' + handle + '"]').count(), 1);
    await plainPage.goto(origin + '/find?q=' + marker + '&in=posts');
    assert.equal(new URL(plainPage.url()).pathname, '/');
    assert.equal(await plainPage.locator('#search').inputValue(), marker);
    await plain.close();
    console.log('PASS: /find at 360px and 1280px (Find in the header, one row of links, result with badge and why, inert text, type chips, a filter chip that clears, Edit filters, no overflow, no writes), home search scope to Find, no-JS redirects both ways, same first result as /api/find.');
  } finally {await browser.close();}
})().catch(error => {console.error(error); process.exitCode = 1;});
