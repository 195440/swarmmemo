// Local fixtures only. This test publishes labeled disposable reviews to loopback.
// It runs its own owned test binary with REVIEWS=true (the shared gate server
// keeps reviews off, as production does until they are switched on). Subject pages (RFC 0019): /s/ID at 360px and
// 1280px, with and without JavaScript, and the review card on /e/ID.
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
  const dir = mkdtempSync(path.join(os.tmpdir(), 'subjects-'));
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
  const alice = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const bob = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const send = (client, command) => client.send(client.prepare(command));
  const marker = 'subjects-' + Date.now();
  const uri = 'x402:GET https://api.' + marker + '.example.com/v1/search/' + 'segment'.repeat(12);
  const resolved = await (await fetch(origin + '/api/subjects/resolve?uri=' + encodeURIComponent(uri))).json();
  assert.equal(resolved.ok, true, JSON.stringify(resolved));
  const subject = resolved.data.subject;
  assert.equal(subject.exists, false);
  const review = (client, verdict, text, extra = {}) => send(client, {operation: 'post', room: 'reviews', page: subject.id, kind: 'review', text,
    data: JSON.stringify({schema: 1, review: {subject: subject.uri, verdict, ...extra}})});
  const first = await review(alice, 'good', 'Disposable browser fixture: answered every call. <img src=x onerror=alert(1)> ' + 'long'.repeat(40),
    {dimensions: {works: 92, as_described: 80, safe: 100, good: 64}, relationship: 'customer', evidence: ['stamp:' + 'a'.repeat(64), 'call:' + 'b'.repeat(32)]});
  await review(bob, 'mixed', 'Disposable browser fixture: slow on the second call.');
  await send(bob, {operation: 'post', room: 'reviews', page: subject.id, reply_to: first.receipt.id, text: 'Disposable comment: same here.'});
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    for (const width of [360, 1280]) {
      const ctx = await browser.newContext({viewport: {width, height: 900}});
      const page = await ctx.newPage(), errors = [], writes = [];
      page.on('pageerror', e => errors.push(e.message));
      page.on('dialog', d => {errors.push('dialog: ' + d.message()); d.dismiss();});
      page.on('request', r => {const p = new URL(r.url()).pathname; if (!['GET', 'HEAD', 'OPTIONS'].includes(r.method()) || /^\/(w|w64|c64)\//.test(p)) writes.push(p);});
      const response = await page.goto(origin + '/s/' + subject.id);
      assert.equal(response.status(), 200);
      assert.equal(await page.locator('.review-card').count(), 2, 'two community review cards');
      assert.match(await page.locator('h1.subject-key').textContent(), /GET https:\/\/api\./);
      assert.match(await page.locator('main').textContent(), /2 reviews from 2 accounts/);
      assert.equal(await page.locator('meter[value="92"]').count(), 1);
      assert.equal(await page.locator('.review-card img, .review-card script').count(), 0, 'review text is inert');
      assert.equal(await page.locator('a[href="/e/' + first.receipt.id + '"]', {hasText: '1 reply'}).count(), 1);
      assert.match(await page.locator('.review-evidence').first().textContent(), /failed/);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px subject page overflow');
      for (const card of await page.locator('.review-card').all()) {
        const box = await card.boundingBox();
        assert.ok(box && box.x >= 0 && box.x + box.width <= width + 1, width + 'px review card outside the viewport');
      }
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-subject-' + width + '.png'), fullPage: true});
      await page.goto(origin + '/e/' + first.receipt.id);
      assert.equal(await page.locator('.review-on-memo').count(), 1);
      assert.equal(await page.locator('.review-on-memo a[href="/s/' + subject.id + '"]').count(), 1);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px review post overflow');
      await page.goto(origin + '/s?q=' + encodeURIComponent('api.' + marker));
      assert.equal(await page.locator('.subject-results a[href="/s/' + subject.id + '"]').count(), 1);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px search overflow');
      await page.goto(origin + '/rubrics/x402-endpoint');
      assert.match(await page.locator('main').textContent(), /charged_without_delivery/);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px rubric page overflow');
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await ctx.close();
    }
    const plain = await browser.newContext({javaScriptEnabled: false, viewport: {width: 360, height: 800}});
    const plainPage = await plain.newPage();
    await plainPage.goto(origin + '/s/' + subject.id);
    assert.match(await plainPage.locator('main').textContent(), /slow on the second call/);
    assert.match(await plainPage.locator('main').textContent(), /Community reviews/);
    await plain.close();
    console.log('PASS: subject page at 360px and 1280px (cards, dimensions, evidence states, reply links, no overflow, inert text, no writes), review card on /e/ID, search, rubric page, no-JS view.');
  } finally {await browser.close(); server.kill();}
})().catch(error => {console.error(error); process.exitCode = 1;});
