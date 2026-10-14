// Local fixtures only. This test publishes labeled disposable reviews to loopback.
// It runs its own owned test binary with REVIEWS=true (the shared gate server
// keeps reviews off). Reviews on every surface (C200): the agent page's reviews
// block and the badge copy UI on the agent page and /s/ID at 360px and 1280px,
// the badge image itself, and the embed in reviews mode on another origin.
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const {pathToFileURL} = require('node:url');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const http = require('node:http');
const {spawn} = require('node:child_process');
const {mkdtempSync} = require('node:fs');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');
const freePort = () => new Promise((ok, fail) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => {const {port} = s.address(); s.close(() => ok(port));});
  s.on('error', fail);
});
(async () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'review-surfaces-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const server = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, DATA_DIR: path.join(dir, 'data'), SERVICES: 'memory', SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port, REVIEWS: 'true'},
    stdio: 'ignore',
  });
  // A second loopback origin hosts the operator's page that embeds the widget.
  const sitePort = await freePort();
  const site = http.createServer((req, res) => {
    res.writeHead(200, {'Content-Type': 'text/html; charset=utf-8'});
    res.end(`<!doctype html><meta charset="utf-8"><title>Operator site</title><main><h1>Our agent</h1><div id="reviews"></div></main>
<script src="${origin}/embed/v1.js" defer data-subject="${decodeURIComponent(new URL(req.url, 'http://x').searchParams.get('id') || '')}" data-target="#reviews" data-limit="3"></script>`);
  }).listen(sitePort, '127.0.0.1');
  process.on('exit', () => {server.kill(); site.close();});
  for (let i = 0; ; i++) {
    try {if ((await fetch(origin + '/api/messages?limit=1')).ok) break;} catch {}
    assert.ok(i < 80, 'server did not start');
    await new Promise(r => setTimeout(r, 250));
  }
  const {Client, generateKey} = await import(pathToFileURL(path.resolve('clients/javascript/swarmmemo.mjs')));
  const carolKey = generateKey();
  const carol = new Client({origin, key: carolKey, allowInsecureLoopback: true});
  const alice = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const bob = new Client({origin, key: generateKey(), allowInsecureLoopback: true});
  const send = (client, command) => client.send(client.prepare(command));
  await send(carol, {operation: 'agent.register', handle: 'carol' + Date.now().toString(36).slice(-5)});
  const fp = carolKey.fingerprint, uri = 'agent:' + fp;
  const resolved = await (await fetch(origin + '/api/subjects/resolve?uri=' + encodeURIComponent(uri))).json();
  assert.equal(resolved.ok, true, JSON.stringify(resolved));
  const subject = resolved.data.subject;
  const review = (client, verdict, text) => send(client, {operation: 'post', room: 'reviews', page: subject.id, kind: 'review', text,
    data: JSON.stringify({schema: 1, review: {subject: subject.uri, verdict}})});
  await review(alice, 'good', 'Disposable browser fixture: carol answered within a minute. <img src=x onerror=alert(1)> ' + 'thorough'.repeat(30));
  await review(bob, 'mixed', 'Disposable browser fixture: right answer, terse.');

  // The badge itself: an SVG image, cacheable, with no script.
  const badge = await fetch(origin + '/agent/' + fp + '/badge.svg');
  assert.equal(badge.status, 200);
  assert.match(badge.headers.get('content-type'), /^image\/svg\+xml/);
  const svg = await badge.text();
  assert.match(svg, /2 reviews/);
  assert.doesNotMatch(svg, /<script|href=/i);

  const markdown = '[![Rated on SwarmMemo](https://swarmmemo.com/agent/' + fp + '/badge.svg)](https://swarmmemo.com/agent/' + fp + '#reviews)';
  const browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {}), args: process.env.PLAYWRIGHT_NO_SANDBOX === 'true' ? ['--no-sandbox'] : []});
  try {
    for (const width of [360, 1280]) {
      const ctx = await browser.newContext({viewport: {width, height: 900}});
      await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], {origin});
      const page = await ctx.newPage(), errors = [], writes = [];
      page.on('pageerror', e => errors.push(e.message));
      page.on('dialog', d => {errors.push('dialog: ' + d.message()); d.dismiss();});
      page.on('request', r => {const p = new URL(r.url()).pathname; if (!['GET', 'HEAD', 'OPTIONS'].includes(r.method()) || /^\/(w|w64|c64)\//.test(p)) writes.push(p);});
      assert.equal((await page.goto(origin + '/agent/' + fp)).status(), 200);
      const block = page.locator('section#reviews');
      await block.waitFor();
      assert.match(await block.textContent(), /2 reviews from 2 accounts/);
      // Each review is shown once on the agent page (C315): in the storefront's
      // list; the block keeps the counts and links the cards on /s/ID.
      assert.equal(await block.locator('.review-card').count(), 0, 'no second copy of the storefront reviews');
      assert.equal(await page.locator('#storefront-reviews li').count(), 2, 'both reviews in the storefront');
      assert.equal(await page.locator('#storefront-reviews img, #storefront-reviews script').count(), 0, 'review text is inert');
      assert.equal(await block.locator('a[href="/s/' + subject.id + '"]', {hasText: 'All reviews'}).count(), 1, 'the way to every card');
      const preview = block.locator('.section-heading img.badge-preview');
      await preview.waitFor();
      assert.ok(await preview.evaluate(img => img.complete && img.naturalWidth > 0), 'the badge renders');
      // The badge copy UI: open it, copy the Markdown.
      await block.locator('summary', {hasText: 'Show the badge'}).click();
      const md = block.locator('code[data-copy-label="Copy Markdown"]');
      assert.equal((await md.textContent()).trim(), markdown);
      const copy = block.locator('.copy-example:has(code[data-copy-label="Copy Markdown"]) button.copy-button');
      await copy.click();
      await page.waitForFunction(() => [...document.querySelectorAll('#reviews .copy-button')].some(b => /Copied/.test(b.textContent)));
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), markdown);
      assert.equal(await block.locator('code[data-copy-label="Copy HTML"]').count(), 1);
      assert.match(await block.locator('code[data-copy-label="Copy the widget"]').textContent(), new RegExp('data-subject="' + subject.id + '"'));
      await block.locator('summary', {hasText: 'Review this agent'}).click();
      assert.match(await block.locator('.review-how').textContent(), new RegExp('write_review.*' + subject.uri));
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px agent page overflow');
      for (const el of await block.locator('.review-card, pre').all()) {
        const box = await el.boundingBox();
        assert.ok(box && box.x >= 0 && box.x + box.width <= width + 1, width + 'px reviews block content outside the viewport');
      }
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-agent-reviews-' + width + '.png'), fullPage: true});

      // The subject page offers the same badge, linking to /s/ID.
      assert.equal((await page.goto(origin + '/s/' + subject.id)).status(), 200);
      const smd = page.locator('code[data-copy-label="Copy Markdown"]');
      assert.match(await smd.textContent(), new RegExp('\\(https://swarmmemo\\.com/s/' + subject.id + '\\)$'));
      assert.ok(await page.locator('img.badge-preview').evaluate(img => img.complete && img.naturalWidth > 0));
      assert.equal(await page.locator('.review-card').count(), 2, 'two review cards on the subject page');
      assert.equal(await page.locator('.review-card img, .review-card script').count(), 0, 'review text is inert');
      assert.equal(await page.locator('a.review-message[href^="/me/messages/new?to="]').count(), 2, 'Message the reviewer on each card');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), width + 'px subject page overflow');

      // The embed in reviews mode, on the operator's own origin.
      await page.goto('http://127.0.0.1:' + sitePort + '/?id=' + subject.id);
      await page.waitForFunction(() => /2 reviews from 2 accounts/.test(document.querySelector('#reviews')?.shadowRoot?.textContent || ''));
      const widget = await page.evaluate(() => {
        const root = document.querySelector('#reviews').shadowRoot;
        return {articles: root.querySelectorAll('article').length, imgs: root.querySelectorAll('article img, article script').length,
          write: root.querySelector('a.write')?.href, text: root.textContent, overflow: document.documentElement.scrollWidth > innerWidth};
      });
      assert.equal(widget.articles, 2);
      assert.equal(widget.imgs, 0, 'widget text is inert');
      assert.equal(widget.write, 'https://swarmmemo.com/s/' + subject.id);
      assert.match(widget.text, /carol answered within a minute\. <img src=x/);
      assert.equal(widget.overflow, false, width + 'px widget overflow');
      await page.screenshot({path: path.join(os.tmpdir(), 'swarmmemo-reviews-widget-' + width + '.png'), fullPage: true});
      assert.deepEqual(writes, []); assert.deepEqual(errors, []);
      await ctx.close();
    }
    const plain = await browser.newContext({javaScriptEnabled: false, viewport: {width: 360, height: 800}});
    const plainPage = await plain.newPage();
    await plainPage.goto(origin + '/agent/' + fp);
    assert.match(await plainPage.locator('section#storefront').textContent(), /right answer, terse/);
    await plain.close();
    console.log('PASS: agent page reviews block and badge copy UI at 360px and 1280px (each review once, in the storefront; inert text, copy to clipboard, no overflow), /s/ID cards with Message the reviewer and badge, badge SVG, embed reviews widget on another origin, no-JS view.');
  } finally {await browser.close(); server.kill(); site.close();}
})().catch(error => {console.error(error); process.exitCode = 1;});
