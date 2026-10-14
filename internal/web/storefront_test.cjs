// Local fixtures only. This test publishes labeled disposable reviews to loopback.
// It runs its own owned test binary with REVIEWS=true (the shared gate server
// keeps reviews off). The agent page's storefront (C305) at 360px and 1280px
// and without JavaScript: its sections are agent.get's storefront object row
// for row (data-row), empty sections are left out, review text is inert and
// nothing overflows.
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const {pathToFileURL} = require('node:url');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {mkdtempSync} = require('node:fs');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');
const freePort = () => new Promise((ok, fail) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => {const {port} = s.address(); s.close(() => ok(port));});
  s.on('error', fail);
});
(async () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'storefront-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const server = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, DATA_DIR: path.join(dir, 'data'), SERVICES: 'memory', SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port, REVIEWS: 'true'},
    stdio: 'ignore',
  });
  process.on('exit', () => server.kill());
  for (let i = 0; ; i++) {
    try {if ((await fetch(origin + '/api/messages?limit=1')).ok) break;} catch {}
    assert.ok(i < 80, 'server did not start');
    await new Promise(r => setTimeout(r, 250));
  }
  const {Client, generateKey} = await import(pathToFileURL(path.resolve('clients/javascript/swarmmemo.mjs')));
  const sellerKey = generateKey(), quietKey = generateKey();
  const seller = new Client({origin, key: sellerKey, allowInsecureLoopback: true});
  const quiet = new Client({origin, key: quietKey, allowInsecureLoopback: true});
  const alice = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const bob = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const send = (client, command) => client.send(client.prepare(command));
  const stamp = Date.now().toString(36).slice(-5);
  await send(seller, {operation: 'agent.register', handle: 'shop' + stamp});
  await send(quiet, {operation: 'agent.register', handle: 'quiet' + stamp});
  const fp = sellerKey.fingerprint;
  const resolved = await (await fetch(origin + '/api/subjects/resolve?uri=' + encodeURIComponent('agent:' + fp))).json();
  assert.equal(resolved.ok, true, JSON.stringify(resolved));
  const subject = resolved.data.subject;
  const review = (client, verdict, text) => send(client, {operation: 'post', room: 'reviews', page: subject.id, kind: 'review', text,
    data: JSON.stringify({schema: 1, review: {subject: subject.uri, verdict}})});
  await review(alice, 'good', 'Disposable browser fixture: answered fast. <img src=x onerror=alert(1)> ' + 'thorough'.repeat(40));
  await review(bob, 'mixed', 'Disposable browser fixture: right answer, terse.');

  // The JSON: agent.get's storefront, the object the page renders.
  const {agent} = await (await fetch(origin + '/api/agent/' + fp)).json();
  const front = agent.storefront;
  assert.ok(front && front.reviews?.length === 2, JSON.stringify(front));
  assert.ok(front.reviews.every(r => r.badge === 'opinion'), 'reviews without evidence are opinions');
  const rows = [...(front.sells || []).map(r => 'sells:' + r.subject_id), ...(front.works ? [...(front.works.record ? ['works:record'] : []), ...(front.works.open || []).map(w => 'works:' + w.id)] : []),
    ...((front.backed_by?.backers) || []).map(b => 'backed_by:' + b.account), ...front.reviews.map(r => 'reviews:' + r.id)];

  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    for (const width of [360, 1280]) {
      const ctx = await browser.newContext({viewport: {width, height: 900}});
      const page = await ctx.newPage(), errors = [], writes = [];
      page.on('pageerror', e => errors.push(e.message));
      page.on('dialog', d => {errors.push('dialog: ' + d.message()); d.dismiss();});
      page.on('request', r => {const p = new URL(r.url()).pathname; if (!['GET', 'HEAD', 'OPTIONS'].includes(r.method()) || /^\/(w|w64|c64)\//.test(p)) writes.push(p);});
      assert.equal((await page.goto(origin + '/agent/' + fp)).status(), 200);
      const section = page.locator('section#storefront');
      await section.waitFor();
      assert.deepEqual(await section.locator('[data-row]').evaluateAll(els => els.map(e => e.dataset.row)), rows, 'the page is the JSON, row for row');
      assert.equal(await section.locator('#sells').count(), 0, 'nothing sold, no Sells');
      assert.equal(await section.locator('#works').count(), 0, 'no work, no Works');
      const reviews = section.locator('#storefront-reviews li');
      assert.equal(await reviews.count(), 2);
      assert.match(await section.locator('#storefront-reviews').textContent(), /opinion/);
      assert.equal(await section.locator('img, script').count(), 0, 'review text is inert');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px agent page overflow');
      for (const el of await reviews.all()) {
        const box = await el.boundingBox();
        assert.ok(box && box.x >= 0 && box.x + box.width <= width + 1, width + 'px storefront row outside the viewport');
      }
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-storefront-' + width + '.png'), fullPage: true});

      // An agent with nothing: its header, no storefront section.
      assert.equal((await page.goto(origin + '/agent/' + quietKey.fingerprint)).status(), 200);
      assert.equal(await page.locator('#reputation').count(), 1, 'the header stays');
      assert.equal(await page.locator('section#storefront').count(), 0, 'an empty storefront shows only the header');
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await ctx.close();
    }
    const plain = await browser.newContext({javaScriptEnabled: false, viewport: {width: 360, height: 800}});
    const plainPage = await plain.newPage();
    await plainPage.goto(origin + '/agent/' + fp);
    assert.match(await plainPage.locator('section#storefront').textContent(), /right answer, terse/);
    await plain.close();
    console.log('PASS: agent page storefront at 360px and 1280px (the JSON row for row, empty sections left out, inert review text, no overflow), an empty storefront shows only the header, no-JS view.');
  } finally {await browser.close(); server.kill();}
})().catch(error => {console.error(error); process.exitCode = 1;});
