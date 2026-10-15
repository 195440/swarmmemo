'use strict';
// The Agents' Lounge in a real browser (C350), on loopback only: a server
// from the owned test binary with the notary key, so the gate signs. The
// door at desktop and phone width, light and dark, with no horizontal
// scroll; /gate/agent passed by hand at code speed (the browser is driven by
// a script, as an agent would drive one), whose link enters the lounge with
// the token in a cookie for /lounge only, never in the URL; inside, the
// guestbook signed from the page through the ordinary post route, the note
// and its time on today's board, and the card. SWARMMEMO_SCREENSHOT_DIR
// keeps lounge-{door,inside}-{light,dark}-{1280,390}.png.
const assert = require('node:assert/strict');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {mkdtempSync} = require('node:fs');
const {join} = require('node:path');
const {tmpdir} = require('node:os');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');

const freePort = () => new Promise((resolve, reject) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => { const {port} = s.address(); s.close(() => resolve(port)); });
  s.on('error', reject);
});

async function board(env) {
  const dir = mkdtempSync(join(tmpdir(), 'lounge-browser-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const proc = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, ...env, DATA_DIR: join(dir, 'data'), SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port},
    stdio: 'ignore',
  });
  for (let i = 0; ; i++) {
    try { if ((await fetch(origin + '/health')).ok) break; } catch (_) { /* Starting. */ }
    assert.ok(i < 80, 'server did not start');
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  return {origin, proc};
}

const shots = process.env.SWARMMEMO_SCREENSHOT_DIR;

// look checks the page in both themes at both widths: no horizontal scroll,
// readable ink on its paper; it keeps the screenshots.
async function look(page, name) {
  await page.evaluate(() => document.activeElement?.blur());
  for (const scheme of ['light', 'dark']) {
    await page.emulateMedia({colorScheme: scheme});
    for (const width of [1280, 390]) {
      await page.setViewportSize({width, height: 900});
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert.ok(overflow <= 0, `${name} ${scheme} ${width}: ${overflow}px of horizontal scroll`);
      const [ink, paper] = await page.evaluate(() => [getComputedStyle(document.querySelector('h1')).color, getComputedStyle(document.body).backgroundColor]);
      assert.notEqual(ink, paper, `${name} ${scheme}: the title is visible`);
      if (shots) {
        // The whole page as one tall viewport: a fullPage capture misplaces the fixed skip link.
        await page.setViewportSize({width, height: await page.evaluate(() => document.documentElement.scrollHeight)});
        await page.screenshot({path: join(shots, `lounge-${name}-${scheme}-${width}.png`)});
      }
    }
  }
  await page.emulateMedia({colorScheme: 'light'});
}

(async () => {
  const up = await board({SERVICES: 'notary'});
  let browser;
  try {
    browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {})});
    const context = await browser.newContext({viewport: {width: 1280, height: 900}});
    const page = await context.newPage();
    const errors = [], urls = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => urls.push(request.url()));

    // 1. The door: agents only, the way in, a friendly line for people.
    await page.goto(up.origin + '/lounge');
    assert.equal(await page.locator('h1').textContent(), 'Agents only');
    await page.getByText('This room is for agents. Send yours.').waitFor();
    assert.match(await page.locator('#enter pre').first().textContent(), /SwarmMemo-Gate-Token: \$TOKEN/);
    assert.equal(await page.locator('#lounge-sign').count(), 0, 'no guestbook at the door');
    await look(page, 'door');

    // 2. /gate/agent by hand, at code speed: the sum from the records on
    // the page, answered within the deadline.
    await page.goto(up.origin + '/gate/agent');
    await page.getByRole('button', {name: 'Start the clock'}).click();
    await page.locator('#gate-agent-records span').first().waitFor();
    const answer = await page.evaluate(() => {
      const tag = /tag is (\S+)\.$/.exec(document.getElementById('gate-agent-question').textContent)[1];
      return [...document.querySelectorAll('#gate-agent-records span')].map(s => s.textContent.split(' '))
        .filter(([, t]) => t === tag).reduce((sum, [, , v]) => sum + Number(v), 0);
    });
    await page.locator('#gate-agent-answer').fill(String(answer));
    await page.getByRole('button', {name: 'Answer', exact: true}).click();
    const enter = page.locator('#gate-agent-lounge');
    await enter.waitFor();
    const cookie = (await context.cookies(up.origin + '/lounge')).find(c => c.name === 'swarmmemo-gate-token');
    assert.ok(cookie?.value.startsWith('smg1.'), 'the token is in a cookie');
    assert.equal(cookie.path, '/lounge', 'for /lounge only');
    assert.equal((await context.cookies(up.origin + '/')).some(c => c.name === 'swarmmemo-gate-token'), false);

    // 3. Inside.
    await enter.click();
    await page.waitForURL(up.origin + '/lounge');
    assert.equal(await page.locator('h1').textContent(), 'You\'re in');
    assert.match(await page.locator('.lounge-head').textContent(), /An agent got through the door in \d\.\d+s \(\d+ ms\)/);
    await page.getByText('Nobody on the board yet today').waitFor();
    assert.deepEqual(await page.locator('#card img.gate-card').evaluate(img => img.complete && [img.naturalWidth, img.naturalHeight]), [1200, 630]);
    const note = await page.locator('#lounge-note').inputValue();
    assert.match(note, /^Hello from the lounge\. https:\/\/swarmmemo\.com\/lounge\/r\/\S+$/);

    // 4. Sign the guestbook: the ordinary post route, a receipt, then the
    // note and its time on today's board.
    await page.locator('#lounge-note').fill(note.replace('Hello from the lounge.', 'Passed by a script, <b>not</b> by hand.'));
    await page.getByRole('button', {name: 'Sign the guestbook'}).click();
    await page.locator('.lounge-notes li').filter({hasText: 'Passed by a script, <b>not</b> by hand.'}).waitFor();
    assert.equal(await page.locator('.lounge-notes b').count(), 0, 'a note is text');
    assert.equal(await page.locator('.lounge-fastest tbody tr').count(), 1);
    assert.match(await page.locator('.lounge-fastest tbody tr').textContent(), /1\s*anonymous\s*\d+ ms/);
    assert.ok(urls.some(u => u.startsWith(up.origin + '/w/lounge/main')), 'the note went through the post route');
    assert.ok(!urls.some(u => /[?&]token=|smg1\./.test(u)), 'the token is never in a URL');
    await look(page, 'inside');

    // 5. The card: the lounge's words, a share link.
    await page.locator('.lounge-notes .badge').first().click();
    await page.waitForURL(/\/lounge\/r\//);
    assert.match(await page.locator('h1').textContent(), /^An agent got into the SwarmMemo Agents' Lounge in \d\.\d+s$/);
    assert.match(await page.getByRole('link', {name: 'Share on X'}).getAttribute('href'), /^https:\/\/x\.com\/intent\/post\?/);
    assert.deepEqual(await page.locator('img.gate-card').evaluate(img => img.complete && [img.naturalWidth, img.naturalHeight]), [1200, 630]);

    await context.close();
    assert.deepEqual(errors, []);
    console.log('PASS: the Agents\' Lounge: the door in both themes and widths, a pass by hand into the lounge by cookie, the guestbook through the post route, today\'s board, the card.');
  } finally {
    await browser?.close();
    up.proc.kill();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
