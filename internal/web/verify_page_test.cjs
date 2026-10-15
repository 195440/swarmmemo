// Local fixtures only. Verified by SwarmMemo (C353) in a real browser: this
// suite runs its own owned test binary with the notary on, publishes a
// disposable verification signed by a fresh key, and checks /v/ID at 360px
// and 1280px: the subject at its commit, each check, what was not tested,
// the re-run and signature blocks, the notary stamp, the badge rendering and
// its Markdown copied to the clipboard, inert verifier text, no overflow, no
// writes; the badge and card served cacheable; and the page without script.
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
  const dir = mkdtempSync(path.join(os.tmpdir(), 'verify-page-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const server = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, DATA_DIR: path.join(dir, 'data'), SERVICES: 'notary', SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port},
    stdio: 'ignore',
  });
  process.on('exit', () => server.kill());
  for (let i = 0; ; i++) {
    try {if ((await fetch(origin + '/health')).ok) break;} catch {}
    assert.ok(i < 80, 'server did not start');
    await new Promise(r => setTimeout(r, 250));
  }
  const {Client, generateKey} = await import(pathToFileURL(path.resolve('clients/javascript/swarmmemo.mjs')));
  const key = generateKey();
  const desk = new Client({origin, key, allowInsecureLoopback: true});
  await desk.send(desk.prepare({operation: 'agent.register', handle: 'verifydesk' + Date.now().toString(36).slice(-5)}));
  const commit = 'f00dfeed'.repeat(5);
  const published = await desk.verifyPublish({repo: 'https://github.com/Example/Widget', commit, command: 'npm ci && npm test <img src=x onerror=alert(1)>',
    environment: 'node 22, linux/amd64, network off after install', outputSha256: 'e3'.repeat(32),
    checks: [{property: 'tests', state: 'pass', note: '128 passing'}, {property: 'lint', state: 'fail', note: '3 errors in src/a-really-long-file-name-that-should-wrap-nicely.js'}, {property: 'fuzz', state: 'not_checked'}],
    limits: 'Windows and macOS were not tested.\nNo network tests.'});
  const v = published.data.verification;
  assert.equal(v.result, 'failed');
  assert.ok(v.notary, 'stamped by the notary');

  // The badge and the card: cacheable, with no script.
  const badge = await fetch(origin + '/v/' + v.id + '/badge.svg');
  assert.equal(badge.status, 200);
  assert.match(badge.headers.get('cache-control'), /^public, max-age=3600/);
  assert.ok(badge.headers.get('etag'));
  const svg = await badge.text();
  assert.match(svg, /failed · 1\/2 · f00dfee/);
  assert.doesNotMatch(svg, /<script|href=/i);
  const subjectBadge = await fetch(origin + '/v/badge?repo=' + encodeURIComponent('https://github.com/example/widget') + '&commit=' + commit);
  assert.match(await subjectBadge.text(), /failed · 1\/2/);
  const card = await fetch(origin + '/v/' + v.id + '.png');
  assert.equal(card.headers.get('content-type'), 'image/png');

  const markdown = '[![Verified by SwarmMemo](https://swarmmemo.com/v/' + v.id + '/badge.svg)](https://swarmmemo.com/v/' + v.id + ')';
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    for (const width of [360, 1280]) {
      const ctx = await browser.newContext({viewport: {width, height: 900}});
      await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], {origin});
      const page = await ctx.newPage(), errors = [], writes = [];
      page.on('pageerror', e => errors.push(e.message));
      page.on('dialog', d => {errors.push('dialog: ' + d.message()); d.dismiss();});
      page.on('request', r => {const p = new URL(r.url()).pathname; if (!['GET', 'HEAD', 'OPTIONS'].includes(r.method()) || /^\/(w|w64|c64)\//.test(p)) writes.push(p);});
      const response = await page.goto(origin + '/v/' + v.id);
      assert.equal(response.status(), 200);
      assert.match(response.headers()['cache-control'], /^public, max-age=300/);
      assert.equal((await page.locator('h1').textContent()).trim(), 'example/widget@f00dfee');
      assert.match(await page.locator('.verify-result').textContent(), /Failed: 1 of 2 checks passed/);
      assert.match(await page.locator('.verify-by').textContent(), /Signed by @verifydesk/);
      assert.equal(await page.locator('a[href="https://github.com/example/widget/commit/' + commit + '"]').count(), 1, 'the exact commit is linked');
      const rows = page.locator('.verify-checks tbody tr');
      assert.equal(await rows.count(), 3);
      assert.match(await rows.nth(1).textContent(), /lint\s*fail\s*3 errors/);
      assert.match(await rows.nth(2).textContent(), /not checked/);
      assert.match(await page.locator('.verify-page').textContent(), /Windows and macOS were not tested\.\s*No network tests\./);
      assert.equal(await page.locator('.verify-page img:not(.badge-preview), .verify-page script').count(), 0, 'verifier text is inert');
      assert.match(await page.locator('pre code').nth(1).textContent(), new RegExp('git checkout ' + commit));
      assert.match(await page.locator('.verify-page').textContent(), /Ed25519PublicKey/);
      assert.match(await page.locator('.verify-page').textContent(), new RegExp('verify_log\\.py notary ' + v.record_sha256));
      const preview = page.locator('img.badge-preview');
      await preview.waitFor();
      assert.ok(await preview.evaluate(img => img.complete && img.naturalWidth > 0), 'the badge renders');
      const md = page.locator('code[data-copy-label="Copy Markdown"]').first();
      assert.equal((await md.textContent()).trim(), markdown);
      await page.locator('.copy-example:has(code[data-copy-label="Copy Markdown"]) button.copy-button').first().click();
      await page.waitForFunction(() => [...document.querySelectorAll('.badge-embed .copy-button')].some(b => /Copied/.test(b.textContent)));
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), markdown);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px page overflow');
      for (const el of await page.locator('.verify-page pre, .verify-checks').all()) {
        const box = await el.boundingBox();
        assert.ok(box && box.x >= 0 && box.x + box.width <= width + 1, width + 'px content outside the viewport');
      }
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-verification-' + width + '.png'), fullPage: true});
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await ctx.close();
    }
    const plain = await browser.newContext({javaScriptEnabled: false, viewport: {width: 360, height: 800}});
    const plainPage = await plain.newPage();
    await plainPage.goto(origin + '/v/' + v.id);
    assert.match(await plainPage.locator('.verify-checks').textContent(), /128 passing/);
    await plain.close();
    console.log('PASS: /v/ID at 360px and 1280px (subject at its commit, checks, limits, re-run, signature and notary blocks, inert text, badge copy to clipboard, no overflow, no writes), badge and card cacheable, subject badge, no-JS view.');
  } finally {await browser.close(); server.kill();}
})().catch(e => {console.error(e); process.exit(1);});
